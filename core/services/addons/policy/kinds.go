// SPDX-License-Identifier: MIT
package policy

import (
	"context"
	"errors"
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
	// Roles gives the least role that may run an action. It must classify
	// every action, ActionUnknown included. A kind without it is refused.
	Roles func(Action) Role
	// CreateAuthority says whether the actor may create a resource of the
	// kind. Without it CreateRef denies.
	CreateAuthority func(ctx context.Context, actor contract.Actor) error
	// Backing says whether the backing of a pending resource exists, for
	// Reconcile. It is required: without it a record left pending by a crash
	// could never be settled and would hold quota for good.
	Backing func(ctx context.Context, ref Ref) (bool, error)
}

// allActions is every Action constant. RegisterKind probes a role table with
// each of them.
var allActions = []Action{ActionRead, ActionOutput, ActionWrite, ActionSchema, ActionShare, ActionDelete, ActionResolve, ActionUnknown}

// validRole says whether a table may return the role: only the three real ones.
func validRole(r Role) bool { return r.rank() > 0 }

// addonSpec is the table of every add-on kind: the one Action.required has.
var addonSpec = KindSpec{Roles: Action.required}

// RegisterKind adds a kind that is not an add-on. The add-on kinds are
// registered by New. A kind registers once, and its name may not look like an
// add-on kind.
func (p *Policy) RegisterKind(kind string, spec KindSpec) error {
	if kind == "" || len(kind) > 256 || strings.HasPrefix(kind, addonKindPrefix) || spec.Roles == nil || spec.Backing == nil {
		return errors.New("addon policy: invalid resource kind")
	}
	// A table that answers "" or an unknown role for an action would let a
	// caller with no grant through, so every action must map to a real role.
	for _, a := range allActions {
		if !validRole(spec.Roles(a)) {
			return errors.New("addon policy: resource kind has an incomplete role table")
		}
	}
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
