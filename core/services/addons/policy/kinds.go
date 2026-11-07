// SPDX-License-Identifier: MIT
package policy

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strings"

	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// A kind says what a resource is. Grants, the epoch, the drain and the
// revocation log work the same for every kind; the kind selects only the
// role each action needs. An add-on resource has the kind "addon:<addonID>".

// addonKindPrefix starts the kind of every add-on resource.
const addonKindPrefix = "addon:"

// addonKind is the kind of the resources of an add-on.
func addonKind(addonID string) string { return addonKindPrefix + addonID }

// addonOf returns the add-on ID of an add-on kind.
func addonOf(kind string) (string, bool) {
	id, ok := strings.CutPrefix(kind, addonKindPrefix)
	return id, ok && id != ""
}

// kindMatchesAddon says whether the stored add-on identity of a record agrees
// with its kind: it is the add-on of an add-on kind and empty for any other.
func kindMatchesAddon(kind, addonID string) bool {
	if id, ok := addonOf(kind); ok {
		return id == addonID
	}
	return !strings.HasPrefix(kind, addonKindPrefix) && addonID == ""
}

// Ref names a resource by its kind and its opaque host-issued ID. It is the
// kind-keyed form of ResourceKey.
type Ref struct{ Kind, ResourceID string }

// refOf is the Ref of an add-on resource.
func refOf(key ResourceKey) Ref { return Ref{Kind: addonKind(key.AddonID), ResourceID: key.ResourceID} }

// keyOf is the ResourceKey of a Ref of an add-on kind.
func (r Ref) keyOf() (ResourceKey, bool) {
	id, ok := addonOf(r.Kind)
	return ResourceKey{AddonID: id, ResourceID: r.ResourceID}, ok
}

// KindSpec is what a kind brings: it is registered once at startup.
type KindSpec struct {
	// Grantable lists the roles a grant of the kind may carry, for example
	// use and manage. The owner is implicit and is never grantable.
	Grantable []Role
	// Roles gives the grantable roles that may run an action. The owner may
	// run every action; an action the table gives no role is the owner's
	// alone. It must answer for every action, ActionUnknown included, with
	// roles from Grantable only. A kind without it is refused.
	Roles func(Action) []Role
	// CreateAuthority says whether the actor may create a resource of the
	// kind. Without it CreateRef denies.
	CreateAuthority func(ctx context.Context, actor contract.Actor) error
	// Backing says whether the backing of a pending resource exists, for
	// Reconcile. It is required: without it a record left pending by a crash
	// could never be settled and would hold quota for good.
	Backing func(ctx context.Context, ref Ref) (bool, error)
	// Eligible says whether the actor may grant a resource of the kind to
	// the user. Without it no user can receive a grant. It is not asked for
	// Everyone, which Everyone below decides. The add-on kinds have none
	// here: the resource service checks the add-on account rule before it
	// asks for a grant.
	Eligible func(ctx context.Context, actor contract.Actor, userID string) (bool, error)
	// Everyone says whether a resource of the kind may be granted to every
	// account at once. A grant to Everyone that is stored while the kind does
	// not allow it is not honored.
	Everyone bool
	// Feature names the host feature (permission) a caller needs to use the
	// resources of the kind, checked with the account on every call. Empty
	// asks only for an active named account with a valid credential. The
	// add-ons feature is never asked for a kind that is not an add-on unless
	// the kind names it here.
	Feature string
}

// KindRegistrar takes the registration of a kind. *Policy implements it.
type KindRegistrar interface {
	RegisterKind(kind string, spec KindSpec) error
}

// KindProvider is a service whose resources are shared through the policy.
// The application hands every provider the registrar when the policy opens,
// before anything is served or reconciled, so the policy and the API never
// name a kind themselves.
type KindProvider interface {
	RegisterKinds(r KindRegistrar) error
}

// RegisterKinds asks each provider to register its kinds and stops at the
// first error. A nil provider is an error, so a wiring mistake fails the
// start instead of leaving a kind unregistered.
func RegisterKinds(r KindRegistrar, providers ...KindProvider) error {
	for _, p := range providers {
		if p == nil {
			return errors.New("addon policy: nil resource kind provider")
		}
		if err := p.RegisterKinds(r); err != nil {
			return err
		}
	}
	return nil
}

// allActions is every Action constant. RegisterKind probes a role table with
// each of them.
var allActions = []Action{ActionRead, ActionOutput, ActionWrite, ActionSchema, ActionShare, ActionDelete, ActionResolve, ActionUnknown}

// kindName is the shape of a kind that is not an add-on: a family and an
// optional subtype, so it can be part of a path.
var kindName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}(:[a-z0-9][a-z0-9-]{0,63})?$`)

// featureName is the shape of a host feature name.
var featureName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// ValidKind says whether a name has the shape of a kind that is not an
// add-on. A request that names a kind of another shape names nothing.
func ValidKind(kind string) bool { return kindName.MatchString(kind) }

// addonRoles is the table of every add-on kind. It is the one Action.required
// gives, written as role sets.
func addonRoles(a Action) []Role {
	switch a.required() {
	case RoleViewer:
		return []Role{RoleViewer, RoleEditor}
	case RoleEditor:
		return []Role{RoleEditor}
	}
	return nil
}

var addonSpec = KindSpec{Grantable: []Role{RoleViewer, RoleEditor}, Roles: addonRoles}

// grantable says whether the kind may grant the role.
func (s KindSpec) grantable(r Role) bool { return r != "" && slices.Contains(s.Grantable, r) }

// allows says whether the role may run the action on a resource of the kind.
// The owner may run everything. Any other role must be grantable for the kind
// and listed for the action, so an empty, unknown or foreign role denies.
func (s KindSpec) allows(r Role, a Action) bool {
	if r == RoleOwner {
		return true
	}
	if !s.grantable(r) || s.Roles == nil {
		return false
	}
	return slices.Contains(s.Roles(a), r)
}

// loses says whether moving a grant from prev to next takes an action away,
// and whether one of those actions is a read.
func (s KindSpec) loses(prev, next Role) (lost, read bool) {
	for _, a := range allActions {
		if s.allows(prev, a) && !s.allows(next, a) {
			lost = true
			read = read || !a.Mutation()
		}
	}
	return lost, read
}

// covers says whether the role a may run every action the role b may.
func (s KindSpec) covers(a, b Role) bool {
	for _, x := range allActions {
		if s.allows(b, x) && !s.allows(a, x) {
			return false
		}
	}
	return true
}

// RegisterKind adds a kind that is not an add-on. The add-on kinds are
// registered by New. A kind registers once, and its name may not look like an
// add-on kind.
func (p *Policy) RegisterKind(kind string, spec KindSpec) error {
	if !kindName.MatchString(kind) || strings.HasPrefix(kind, addonKindPrefix) || spec.Roles == nil || spec.Backing == nil || len(spec.Grantable) == 0 {
		return errors.New("addon policy: invalid resource kind")
	}
	if spec.Feature != "" && !featureName.MatchString(spec.Feature) {
		return errors.New("addon policy: resource kind names an invalid feature")
	}
	for i, r := range spec.Grantable {
		if !grantRole(r) || slices.Contains(spec.Grantable[:i], r) {
			return errors.New("addon policy: resource kind grants an unknown role")
		}
	}
	// A table that answers "" or a role the kind does not grant would make
	// the answer depend on a role nobody can hold, so every action must map
	// to grantable roles only.
	for _, a := range allActions {
		for _, r := range spec.Roles(a) {
			if !spec.grantable(r) {
				return errors.New("addon policy: resource kind has an invalid role table")
			}
		}
	}
	spec.Grantable = slices.Clone(spec.Grantable)
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, dup := p.kinds[kind]; dup {
		return errors.New("addon policy: resource kind is already registered")
	}
	if p.kinds == nil {
		p.kinds = map[string]KindSpec{}
	}
	p.kinds[kind] = spec
	return nil
}

// ShareRules is what a kind allows to be granted.
type ShareRules struct {
	// Grantable lists the roles a grant may carry, in the order the kind
	// declared them.
	Grantable []Role
	// Everyone says whether the kind may be granted to every account.
	Everyone bool
}

// Shareable reports the sharing rules of a registered kind that is not an
// add-on. Add-on resources are shared through their own routes.
func (p *Policy) Shareable(kind string) (ShareRules, bool) {
	if _, addon := addonOf(kind); addon || strings.HasPrefix(kind, addonKindPrefix) {
		return ShareRules{}, false
	}
	s, ok := p.spec(kind)
	if !ok {
		return ShareRules{}, false
	}
	return ShareRules{Grantable: slices.Clone(s.Grantable), Everyone: s.Everyone}, true
}

// AddonRef is the Ref of an add-on resource.
func AddonRef(key ResourceKey) Ref { return refOf(key) }

// spec returns the registration of a kind. A kind nobody registered has none,
// and every call on it is denied like a resource that is not there.
func (p *Policy) spec(kind string) (KindSpec, bool) {
	if _, ok := addonOf(kind); ok {
		return addonSpec, true
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	s, ok := p.kinds[kind]
	return s, ok
}

// heldKind says whether an operation holds the data of the kind still. Only
// add-ons have such operations.
func (p *Policy) heldKind(kind string) bool {
	id, ok := addonOf(kind)
	return ok && p.isHeld(id)
}
