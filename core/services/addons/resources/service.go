// SPDX-License-Identifier: MIT

// Package resources is the owner's UI-only resource management: it lists the
// resources a person may use, creates, renames and deletes them on the
// person's explicit action, manages whole-resource grants, and keeps which
// resource each conversation selected.
//
// Nothing here is a tool. A model, a proposal, a typed dispatch and an agent
// have no way to reach any of it: the gateway names these calls only for the
// browser routes, the declared-tool rule reads capabilities.tools while the
// delete tool is listed in capabilities.adminTools, and sharing and deletion
// have no tool definition. Every call goes through the same host policy as
// every other operation: authentication is revalidated, the role is checked
// at the owner, and the work is admitted per resource, so a revocation or a
// deletion waits for it and cancels it.
package resources

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/addons/policy"
	addonruntime "github.com/mudler/LocalAI/core/services/addons/runtime"
	"github.com/mudler/LocalAI/core/services/addons/toolargs"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// Bounds of the management calls.
const (
	// MaxListed bounds one listing.
	MaxListed = 100
	// MaxNameRunes bounds a display name the host shows.
	MaxNameRunes = 80
	// MaxUserResults bounds the user picker.
	MaxUserResults = 20
	// MinQueryRunes and MaxQueryRunes bound the user picker query.
	MinQueryRunes = 2
	MaxQueryRunes = 64
	// MaxGrants bounds the grants of one resource that are listed.
	MaxGrants    = 256
	maxNameInput = 256
)

// Capabilities reports the capabilities of the installed generation of an
// add-on. The manager implements it.
type Capabilities interface {
	DeclaredCapabilities(ctx context.Context, addonID string, generation uint64) (contract.Capabilities, error)
}

// UserDirectory answers the identity questions of sharing from the account
// store. It never returns more than an immutable ID and a display name.
type UserDirectory interface {
	// Lookup returns the display names of the IDs it knows. Unknown IDs are
	// absent from the map.
	Lookup(ctx context.Context, ids []string) (map[string]string, error)
	// Eligible says whether the ID is an active named account that may use
	// add-ons, so a grant to it is meaningful.
	Eligible(ctx context.Context, id string) (bool, error)
	// Search finds eligible accounts by a prefix of the display name or of
	// the address, at most limit of them.
	Search(ctx context.Context, query string, limit int) ([]gateway.UserRef, error)
}

// Options are the owner services the management needs. Missing ones make the
// calls that need them unavailable.
type Options struct {
	Policy       *policy.Policy
	Broker       *policy.Broker
	Runtime      gateway.CurrentRuntime
	Capabilities Capabilities
	// Tools reports the declared model tools. The create step of
	// the policy refuses a tool it does not list.
	Tools gateway.ToolSource
	Users UserDirectory
	// Selections holds the conversation selections. Without it the selection
	// calls are unavailable.
	Selections *SelectionStore
	// BackingConfirmed asks the add-on whether the backing of a resource
	// exists. A deletion that finds it gone is finished without the delete
	// tool, so a (false, nil) must be the explicit answer of the add-on's
	// declared existence tool. An add-on that cannot give one answers
	// policy.ErrBackingUnanswered (policy.BackingConfirmed does), and the
	// deletion stays confirmed and is retried.
	BackingConfirmed func(context.Context, policy.ResourceKey) (bool, error)
	Limits           contract.Limits
	Now              func() time.Time
	// NameTimeout bounds the display-name read of one resource.
	NameTimeout time.Duration
}

// Service implements gateway.ResourceManager.
type Service struct {
	o Options
	// binds remembers the request each UI apply operation was dispatched
	// for, newest last and bounded. The add-on's own ledger stays the
	// authority across restarts: it refuses the reuse with a 409.
	bindMu sync.Mutex
	binds  map[string]binding
	order  []string
}

// binding is the request an operation ID was first dispatched for.
type binding struct{ tool, hash string }

// maxBindings bounds the remembered requests.
const maxBindings = 4096

// requestHash is the fingerprint of one UI change: the resource, the tool
// and the canonical arguments (decoded, keys sorted, numbers kept as
// written). The expected revision is not part of it: a repeat of a change
// reads a newer revision and is still the same change.
func requestHash(resourceID, tool string, args json.RawMessage) string {
	var v any
	d := json.NewDecoder(strings.NewReader(string(args)))
	d.UseNumber()
	if d.Decode(&v) != nil {
		v = string(args)
	}
	canon, _ := json.Marshal(v)
	sum := sha256.Sum256([]byte(resourceID + "\x00" + tool + "\x00" + string(canon)))
	return hex.EncodeToString(sum[:])
}

// bound reports the request recorded for a wire operation.
func (s *Service) bound(wire string) (binding, bool) {
	s.bindMu.Lock()
	defer s.bindMu.Unlock()
	b, ok := s.binds[wire]
	return b, ok
}

// unbind forgets a binding the add-on did not keep.
func (s *Service) unbind(wire string, b binding) {
	s.bindMu.Lock()
	defer s.bindMu.Unlock()
	if have, ok := s.binds[wire]; ok && have == b {
		delete(s.binds, wire)
		for i, id := range s.order {
			if id == wire {
				s.order = append(s.order[:i], s.order[i+1:]...)
				break
			}
		}
	}
}

// bind records the request of a wire operation. It reports false when the
// operation is already bound to another request.
func (s *Service) bind(wire string, b binding) bool {
	s.bindMu.Lock()
	defer s.bindMu.Unlock()
	if have, ok := s.binds[wire]; ok {
		return have.hash == b.hash
	}
	if s.binds == nil {
		s.binds = map[string]binding{}
	}
	if len(s.order) >= maxBindings {
		delete(s.binds, s.order[0])
		s.order = s.order[1:]
	}
	s.binds[wire] = b
	s.order = append(s.order, wire)
	return true
}

var _ gateway.ResourceManager = (*Service)(nil)

// New builds the service.
func New(o Options) *Service {
	if o.Limits == (contract.Limits{}) {
		o.Limits = contract.DefaultLimits()
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.NameTimeout <= 0 {
		o.NameTimeout = 3 * time.Second
	}
	return &Service{o: o}
}

var (
	sha256Hex  = regexp.MustCompile(`^[0-9a-f]{64}$`)
	identifier = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)
	// resumeActor is the identity of the owner's own settlement of a
	// deletion after a crash. It carries no authority over any resource: the
	// request is the one typed delete of a resource that is already
	// confirmed as deleting.
	resumeActor = contract.Actor{UserID: "owner-reconcile", CredentialID: "owner:reconcile", Mode: "local-owner"}
)

func (s *Service) ready() error {
	if s.o.Policy == nil || s.o.Runtime == nil || s.o.Capabilities == nil {
		return gateway.ErrUnavailable
	}
	return nil
}

// unavailable wraps a failure of the add-on as the shared unavailable answer.
func unavailable(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, gateway.ErrUnavailable) {
		return err
	}
	if errors.Is(err, contract.ErrRefused) {
		return gateway.Refused(err)
	}
	return fmt.Errorf("%w: %w", gateway.ErrUnavailable, err)
}

// classify keeps a policy denial a denial and everything else unavailable.
func classify(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, policy.ErrQuota):
		return gateway.ErrQuotaReached
	case errors.Is(err, gateway.ErrUnavailable):
		return err
	case errors.Is(err, gateway.ErrForbidden):
		// A typed denial keeps its type. Every other denial is the one plain
		// denial, whichever layer made it, so that an unknown resource and a
		// resource that is not the actor's cannot be told apart.
		var blocked *gateway.OutcomeBlocked
		for _, typed := range []error{gateway.ErrSharingDisabled, gateway.ErrStale, gateway.ErrOperationConflict, gateway.ErrInvalidRequest, gateway.ErrQuotaReached, gateway.ErrNotSelected} {
			if errors.Is(err, typed) {
				return err
			}
		}
		if errors.As(err, &blocked) {
			return err
		}
		return gateway.ErrForbidden
	}
	return unavailable(err)
}

// session resolves the add-on's current instance and capabilities. A caller
// that names a generation gets ErrStale when it is not the current one.
func (s *Service) session(ctx context.Context, addonID string, generation uint64) (addonruntime.Instance, contract.Capabilities, error) {
	if err := s.ready(); err != nil {
		return addonruntime.Instance{}, contract.Capabilities{}, err
	}
	inst, err := s.o.Runtime.Runtime(ctx, addonID)
	if err != nil || inst.Client == nil || inst.Generation == 0 {
		if ctx.Err() != nil {
			return inst, contract.Capabilities{}, ctx.Err()
		}
		return inst, contract.Capabilities{}, unavailable(err)
	}
	if generation != 0 && generation != inst.Generation {
		return inst, contract.Capabilities{}, gateway.ErrStale
	}
	caps, err := s.o.Capabilities.DeclaredCapabilities(ctx, addonID, inst.Generation)
	if err != nil {
		if gateway.StatusFor(err) == 403 {
			return inst, caps, gateway.ErrForbidden
		}
		return inst, caps, unavailable(err)
	}
	return inst, caps, nil
}

func randomID(prefix string) string {
	var raw [12]byte
	_, _ = rand.Read(raw[:])
	return prefix + hex.EncodeToString(raw[:])
}

// uiOperation derives the operation identifier the add-on sees from the
// person's own operation ID, scoped to the person and the kind. Two people
// that chose the same ID never share a receipt, and repeating one request is
// one operation.
func uiOperation(actor contract.Actor, kind, operationID string) string {
	sum := sha256.Sum256([]byte(kind + "\x00" + actor.Mode + "\x00" + actor.UserID + "\x00" + operationID))
	return "u" + hex.EncodeToString(sum[:])[:31]
}

// deleteOperation is the operation identifier of the one deletion of a
// resource, so a retry and a resume after a crash are the same operation.
func deleteOperation(resourceID string) string { return "del-" + resourceID }

// cleanName bounds and cleans a display name the add-on gave.
func cleanName(n string) string {
	if !utf8.ValidString(n) {
		return ""
	}
	var b strings.Builder
	for _, r := range n {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == '<' || r == '>' {
			continue
		}
		b.WriteRune(r)
	}
	out := strings.TrimSpace(b.String())
	if utf8.RuneCountInString(out) > MaxNameRunes {
		out = string([]rune(out)[:MaxNameRunes])
	}
	return out
}

type inspected struct {
	Name     string
	Revision contract.Revision
}

// inspect reads the display name and revision of a resource through the
// add-on's inspect tool, admitted for the actor like any read.
func (s *Service) inspect(ctx context.Context, actor contract.Actor, key policy.ResourceKey, inst addonruntime.Instance, tool string) (inspected, error) {
	var out inspected
	args, _ := json.Marshal(map[string]string{gateway.ToolKey: tool})
	err := s.o.Policy.Admit(ctx, actor, key, policy.ActionRead, func(rctx context.Context) error {
		cctx, cancel := context.WithTimeout(rctx, s.o.NameTimeout)
		defer cancel()
		env := contract.Envelope{
			Request:   contract.Request{AddonID: key.AddonID, ResourceID: key.ResourceID, OperationID: randomID("n"), Generation: inst.Generation, Action: "read", Arguments: args},
			Authority: contract.Authority{Actor: actor, Scope: "read", ExpiresAt: s.o.Now().Add(s.o.Limits.ReadTimeout)},
		}
		res, e := inst.Client.Read(cctx, env)
		if e != nil {
			if rctx.Err() != nil {
				return rctx.Err()
			}
			return unavailable(e)
		}
		var v struct {
			Name     string            `json:"name"`
			Revision contract.Revision `json:"revision"`
		}
		if json.Unmarshal(res.Payload, &v) != nil {
			return unavailable(errors.New("inspect result is not an object"))
		}
		out = inspected{Name: cleanName(v.Name), Revision: v.Revision}
		return nil
	})
	return out, err
}

// listWidth is how many name reads one listing runs at once: half of the
// add-on's concurrency budget, and at least one. The runtime refuses a call over
// the budget, so a listing that took all of it would make every other read of
// the person's page fail while it ran.
func (s *Service) listWidth() int {
	n := int(s.o.Limits.Concurrency / 2)
	if n < 1 {
		return 1
	}
	return n
}

func operations(c contract.Capabilities) []string {
	ops := []string{}
	if r := c.Resources; r != nil {
		if r.Create != "" {
			ops = append(ops, "create")
		}
		if r.Rename != "" {
			ops = append(ops, "rename")
		}
		if r.Delete != "" {
			ops = append(ops, "delete")
		}
	}
	return ops
}

// List lists the resources of an addon the actor may use, with the actor's
// own role. It shows nothing of anyone else's resources.
func (s *Service) List(ctx context.Context, actor contract.Actor, addonID string) (gateway.ResourceList, error) {
	if !identifier.MatchString(addonID) {
		return gateway.ResourceList{}, gateway.ErrInvalidRequest
	}
	inst, caps, err := s.session(ctx, addonID, 0)
	if err != nil {
		return gateway.ResourceList{}, err
	}
	views, err := s.o.Policy.Listing(ctx, actor, addonID)
	if err != nil {
		return gateway.ResourceList{}, classify(err)
	}
	out := gateway.ResourceList{AddonID: addonID, Generation: inst.Generation, Sharing: s.o.Policy.Mode() == policy.ModeNamedUser, Operations: operations(caps), Resources: []gateway.ResourceInfo{}}
	if caps.Resources != nil {
		out.Noun = caps.Resources.Noun
	}
	if len(views) > MaxListed {
		views, out.Truncated = views[:MaxListed], true
	}
	infos := make([]gateway.ResourceInfo, len(views))
	// The names are a convenience: the reads share one budget, and a resource
	// whose name did not arrive in it is listed without one.
	names, cancel := context.WithTimeout(ctx, 3*s.o.NameTimeout)
	defer cancel()
	var wg sync.WaitGroup
	sem := make(chan struct{}, s.listWidth())
	for i, v := range views {
		infos[i] = gateway.ResourceInfo{ID: v.Key.ResourceID, AddonID: addonID, Role: string(v.Role)}
		if v.Role == policy.RoleOwner {
			n := v.Shared
			infos[i].SharedWith = &n
		}
		if caps.Resources == nil || caps.Resources.Inspect == "" {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			r, e := s.inspect(names, actor, v.Key, inst, caps.Resources.Inspect)
			if e != nil {
				// The name is a convenience: the resource is still listed.
				return
			}
			infos[i].Name = r.Name
			if v.Role == policy.RoleOwner {
				rev := r.Revision
				infos[i].Revision = &rev
			}
		}()
	}
	wg.Wait()
	if s.o.Users != nil {
		var ids []string
		seen := map[string]bool{}
		for _, v := range views {
			if v.Role != policy.RoleOwner && !seen[v.OwnerID] {
				seen[v.OwnerID] = true
				ids = append(ids, v.OwnerID)
			}
		}
		if len(ids) > 0 {
			if names, e := s.o.Users.Lookup(ctx, ids); e == nil {
				for i, v := range views {
					if v.Role != policy.RoleOwner {
						infos[i].Owner = cleanName(names[v.OwnerID])
					}
				}
			}
		}
	}
	out.Resources = infos
	return out, nil
}

// Create creates a resource through the add-on's create tool, as the person's
// own UI action. It reserves the resource for the person, runs the typed
// create through the same preview and apply steps every mutation uses, and
// activates it. Repeating the operation ID returns the same resource.
func (s *Service) Create(ctx context.Context, actor contract.Actor, r gateway.CreateResourceRequest) (gateway.ResourceInfo, error) {
	if !identifier.MatchString(r.AddonID) || !identifier.MatchString(r.OperationID) || r.Generation == 0 {
		return gateway.ResourceInfo{}, gateway.ErrInvalidRequest
	}
	inst, caps, err := s.session(ctx, r.AddonID, r.Generation)
	if err != nil {
		return gateway.ResourceInfo{}, err
	}
	if caps.Resources == nil || caps.Resources.Create == "" {
		return gateway.ResourceInfo{}, gateway.ErrInvalidRequest
	}
	def, ok := definition(caps, caps.Resources.Create, contract.ToolKindCreate)
	if !ok {
		return gateway.ResourceInfo{}, gateway.ErrInvalidRequest
	}
	resource, body, err := toolargs.BindDefinition(def, r.Arguments)
	if err != nil || resource != "" {
		return gateway.ResourceInfo{}, gateway.ErrInvalidRequest
	}
	if s.o.Tools == nil {
		return gateway.ResourceInfo{}, gateway.ErrUnavailable
	}
	sum := sha256.Sum256(append([]byte(r.AddonID+"\x00"), body...))
	ref := hex.EncodeToString(sum[:])
	idSum := sha256.Sum256([]byte("create\x00" + actor.Mode + "\x00" + actor.UserID + "\x00" + r.OperationID))
	id := "r" + hex.EncodeToString(idSum[:])[:31]
	step := policy.RuntimeCreate(s.o.Runtime, s.o.Tools, s.o.Limits, actor, body)
	key, err := s.o.Policy.CreateWithID(ctx, actor, r.AddonID, id, ref, step)
	if err != nil {
		switch {
		case errors.Is(err, policy.ErrNotCreated):
			// The add-on refused the request and made nothing.
			return gateway.ResourceInfo{}, gateway.ErrInvalidRequest
		case errors.Is(err, policy.ErrQuota):
			return gateway.ResourceInfo{}, gateway.ErrQuotaReached
		}
		return gateway.ResourceInfo{}, classify(err)
	}
	info := gateway.ResourceInfo{ID: key.ResourceID, AddonID: key.AddonID, Role: string(policy.RoleOwner)}
	n := 0
	info.SharedWith = &n
	if caps.Resources.Inspect != "" {
		if v, e := s.inspect(ctx, actor, key, inst, caps.Resources.Inspect); e == nil {
			info.Name = v.Name
			rev := v.Revision
			info.Revision = &rev
		}
	}
	return info, nil
}

// definition finds the definition of a declared tool of one kind.
func definition(c contract.Capabilities, tool, kind string) (contract.ToolDefinition, bool) {
	declared := false
	for _, t := range c.Tools {
		declared = declared || t == tool
	}
	if !declared {
		return contract.ToolDefinition{}, false
	}
	for _, d := range c.ToolDefinitions {
		if d.Name == tool && d.Kind == kind {
			return d, true
		}
	}
	return contract.ToolDefinition{}, false
}

func validOperation(id string) bool { return identifier.MatchString(id) }

// owned resolves the resource and checks that the actor holds the owner role,
// with the same denial for a resource that does not exist.
func (s *Service) owned(ctx context.Context, actor contract.Actor, resourceID string) (policy.ResourceKey, error) {
	if s.o.Policy == nil {
		return policy.ResourceKey{}, gateway.ErrUnavailable
	}
	// An unknown resource gets a key without an add-on, which the policy
	// denies like a resource that is not the actor's.
	key, _ := s.o.Policy.Key(resourceID)
	key.ResourceID = resourceID
	if err := s.o.Policy.Authorize(ctx, actor, key, policy.ActionShare); err != nil {
		return policy.ResourceKey{}, classify(err)
	}
	return key, nil
}

func (s *Service) sharing() error {
	if s.o.Policy == nil {
		return gateway.ErrUnavailable
	}
	if s.o.Policy.Mode() != policy.ModeNamedUser {
		return gateway.ErrSharingDisabled
	}
	return nil
}

// Grants lists the grants of a resource for its owner role.
func (s *Service) Grants(ctx context.Context, actor contract.Actor, resourceID string) (gateway.GrantList, error) {
	if err := s.sharing(); err != nil {
		return gateway.GrantList{}, err
	}
	key, err := s.owned(ctx, actor, resourceID)
	if err != nil {
		return gateway.GrantList{}, err
	}
	return s.grantList(ctx, actor, key)
}

func (s *Service) grantList(ctx context.Context, actor contract.Actor, key policy.ResourceKey) (gateway.GrantList, error) {
	m, err := s.o.Policy.Grants(ctx, actor, key)
	if err != nil {
		return gateway.GrantList{}, classify(err)
	}
	ids := make([]string, 0, len(m))
	for u := range m {
		ids = append(ids, u)
	}
	sort.Strings(ids)
	if len(ids) > MaxGrants {
		ids = ids[:MaxGrants]
	}
	var names map[string]string
	if s.o.Users != nil && len(ids) > 0 {
		names, _ = s.o.Users.Lookup(ctx, ids)
	}
	out := gateway.GrantList{ResourceID: key.ResourceID, Grants: []gateway.Grant{}}
	for _, u := range ids {
		out.Grants = append(out.Grants, gateway.Grant{UserID: u, DisplayName: cleanName(names[u]), Role: string(m[u])})
	}
	return out, nil
}

// SetGrant gives a named user a role on the whole resource.
func (s *Service) SetGrant(ctx context.Context, actor contract.Actor, resourceID, userID, role string) (gateway.GrantList, error) {
	if err := s.sharing(); err != nil {
		return gateway.GrantList{}, err
	}
	key, err := s.owned(ctx, actor, resourceID)
	if err != nil {
		return gateway.GrantList{}, err
	}
	if role != string(policy.RoleViewer) && role != string(policy.RoleEditor) {
		return gateway.GrantList{}, gateway.ErrInvalidRequest
	}
	if userID == "" || len(userID) > 256 || s.o.Users == nil {
		return gateway.GrantList{}, gateway.ErrForbidden
	}
	ok, err := s.o.Users.Eligible(ctx, userID)
	if err != nil {
		return gateway.GrantList{}, classify(err)
	}
	if !ok {
		return gateway.GrantList{}, gateway.ErrForbidden
	}
	if err = s.o.Policy.SetGrant(ctx, actor, key, userID, policy.Role(role)); err != nil {
		return gateway.GrantList{}, classify(err)
	}
	return s.grantList(ctx, actor, key)
}

// RevokeGrant removes a user's grant. It returns only once the user's
// admitted work stopped and their pending proposals were invalidated.
func (s *Service) RevokeGrant(ctx context.Context, actor contract.Actor, resourceID, userID string) (gateway.GrantList, error) {
	if err := s.sharing(); err != nil {
		return gateway.GrantList{}, err
	}
	key, err := s.owned(ctx, actor, resourceID)
	if err != nil {
		return gateway.GrantList{}, err
	}
	if userID == "" || len(userID) > 256 {
		return gateway.GrantList{}, gateway.ErrInvalidRequest
	}
	if err = s.o.Policy.Revoke(ctx, actor, key, userID); err != nil {
		return gateway.GrantList{}, classify(err)
	}
	// Selections of the revoked user are left alone on purpose: the reference
	// is no authority, so it is denied at its next use like an unknown one.
	return s.grantList(ctx, actor, key)
}

// SearchUsers is the user picker behind a resource's share dialog. It is
// reachable only by the resource's owner role, matches only a bounded prefix
// and returns nothing but an immutable ID and a display name.
func (s *Service) SearchUsers(ctx context.Context, actor contract.Actor, resourceID, query string, limit int) (gateway.UserList, error) {
	if err := s.sharing(); err != nil {
		return gateway.UserList{}, err
	}
	key, err := s.owned(ctx, actor, resourceID)
	if err != nil {
		return gateway.UserList{}, err
	}
	query = strings.TrimSpace(query)
	if n := utf8.RuneCountInString(query); n < MinQueryRunes || n > MaxQueryRunes || strings.ContainsFunc(query, unicode.IsControl) {
		return gateway.UserList{}, gateway.ErrInvalidRequest
	}
	if limit <= 0 || limit > MaxUserResults {
		limit = MaxUserResults
	}
	if s.o.Users == nil {
		return gateway.UserList{}, gateway.ErrUnavailable
	}
	owner, _ := s.o.Policy.OwnerOf(key)
	found, err := s.o.Users.Search(ctx, query, limit+2)
	if err != nil {
		return gateway.UserList{}, classify(err)
	}
	out := gateway.UserList{Users: []gateway.UserRef{}}
	for _, u := range found {
		if u.ID == actor.UserID || u.ID == owner || u.ID == "" || len(out.Users) >= limit {
			continue
		}
		out.Users = append(out.Users, gateway.UserRef{ID: u.ID, DisplayName: cleanName(u.DisplayName)})
	}
	return out, nil
}

// Rename renames a resource through the add-on's rename tool, as the owner's
// own UI action. It needs no proposal: the owner's explicit request is the
// intent. It still runs under the resource's admission, behind unresolved
// outcomes, against the revision the owner saw, and leaves the add-on's
// receipt for the operation.
func (s *Service) Rename(ctx context.Context, actor contract.Actor, resourceID string, r gateway.RenameResourceRequest) (gateway.ResourceInfo, error) {
	if !validOperation(r.OperationID) || r.Generation == 0 || r.Revision.Schema == 0 {
		return gateway.ResourceInfo{}, gateway.ErrInvalidRequest
	}
	name := strings.TrimSpace(r.Name)
	if name == "" || name != r.Name || len(name) > maxNameInput {
		return gateway.ResourceInfo{}, gateway.ErrInvalidRequest
	}
	key, err := s.owned(ctx, actor, resourceID)
	if err != nil {
		return gateway.ResourceInfo{}, err
	}
	inst, caps, err := s.session(ctx, key.AddonID, r.Generation)
	if err != nil {
		return gateway.ResourceInfo{}, err
	}
	if caps.Resources == nil || caps.Resources.Rename == "" {
		return gateway.ResourceInfo{}, gateway.ErrInvalidRequest
	}
	def, ok := definition(caps, caps.Resources.Rename, contract.ToolKindSchema)
	if !ok {
		return gateway.ResourceInfo{}, gateway.ErrInvalidRequest
	}
	asked, _ := json.Marshal(map[string]string{contract.ResourceArgument: key.ResourceID, "name": name})
	res, body, err := toolargs.BindDefinition(def, asked)
	if err != nil || res != key.ResourceID {
		return gateway.ResourceInfo{}, gateway.ErrInvalidRequest
	}
	wire := uiOperation(actor, "rename", r.OperationID)
	err = s.o.Policy.Admit(ctx, actor, key, policy.ActionSchema, func(rctx context.Context) error {
		if s.o.Broker != nil {
			if e := s.o.Broker.BlockedError(actor, key); e != nil {
				return e
			}
		}
		return s.typedChange(rctx, actor, key, inst, string(policy.ActionSchema), wire, body, r.Revision, r.OperationID)
	})
	if err != nil {
		return gateway.ResourceInfo{}, classify(err)
	}
	info := gateway.ResourceInfo{ID: key.ResourceID, AddonID: key.AddonID, Role: string(policy.RoleOwner)}
	if caps.Resources.Inspect != "" {
		if v, e := s.inspect(ctx, actor, key, inst, caps.Resources.Inspect); e == nil {
			info.Name = v.Name
			rev := v.Revision
			info.Revision = &rev
		}
	}
	return info, nil
}

// typedChange previews and applies one typed change against the revision the
// person saw. A moved revision is ErrStale and nothing is sent to apply. An
// apply whose outcome is not known is OutcomeUnknown with the person's
// operation ID, never a retry under a new one.
func (s *Service) typedChange(ctx context.Context, actor contract.Actor, key policy.ResourceKey, inst addonruntime.Instance, action, wire string, body json.RawMessage, seen contract.Revision, original string) error {
	cur, err := s.o.Runtime.Runtime(ctx, key.AddonID)
	if err != nil || cur.Client == nil {
		return unavailable(err)
	}
	if cur.Generation != inst.Generation {
		return gateway.ErrStale
	}
	env := func(scope string, d time.Duration) contract.Envelope {
		return contract.Envelope{
			Request:   contract.Request{AddonID: key.AddonID, ResourceID: key.ResourceID, OperationID: wire, Generation: cur.Generation, Action: action, Arguments: body},
			Authority: contract.Authority{Actor: actor, Scope: scope, ExpiresAt: s.o.Now().Add(d)},
		}
	}
	preview, err := cur.Client.Preview(ctx, env("preview", s.o.Limits.PreviewTimeout))
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(err, contract.ErrRefused) {
			// The add-on's own refusal: deterministic, nothing applied.
			return gateway.Refused(err)
		}
		return s.previewFailure(ctx, actor, key, cur, err)
	}
	if seen.Schema != 0 && preview.Revision != seen {
		return gateway.ErrStale
	}
	receipt, err := cur.Client.Apply(ctx, env("apply", s.o.Limits.ApplyTimeout), preview.Revision)
	if err != nil {
		if errors.Is(err, addonruntime.ErrConflict) {
			return gateway.ErrOperationConflict
		}
		if errors.Is(err, contract.ErrRefused) {
			return gateway.Refused(err)
		}
		return &gateway.OutcomeUnknown{OperationID: original}
	}
	switch receipt.State {
	case contract.ReceiptCommitted:
		return nil
	case contract.ReceiptRejected:
		return gateway.ErrStale
	}
	return &gateway.OutcomeUnknown{OperationID: original}
}

// authorized resolves the resource and checks that the actor holds the role
// the action needs, with the same denial for a resource that does not exist.
func (s *Service) authorized(ctx context.Context, actor contract.Actor, resourceID string, action policy.Action) (policy.ResourceKey, error) {
	if s.o.Policy == nil {
		return policy.ResourceKey{}, gateway.ErrUnavailable
	}
	key, _ := s.o.Policy.Key(resourceID)
	key.ResourceID = resourceID
	if err := s.o.Policy.Authorize(ctx, actor, key, action); err != nil {
		return policy.ResourceKey{}, classify(err)
	}
	return key, nil
}

// Apply changes the data or the schema of a resource through a write or
// schema tool the add-on declares, as the person's own UI action. It needs no
// proposal: the request in the person's own tab is the intent. It runs under
// the same rules as every change: the role of the action (editors write, only
// the owner changes the schema), the declared definition of the tool, the
// resource's admission, the revision the person read, and the add-on's
// receipt, which makes a repeat of the same operation one change.
func (s *Service) Apply(ctx context.Context, actor contract.Actor, resourceID string, r gateway.ApplyResourceRequest) (gateway.ApplyResult, error) {
	if !validOperation(r.OperationID) || r.Generation == 0 || r.Revision.Schema == 0 || !identifier.MatchString(r.Tool) {
		return gateway.ApplyResult{}, gateway.ErrInvalidRequest
	}
	// The lowest role of any change is the editor's. The role of this tool
	// is checked again below, once its kind is known.
	key, err := s.authorized(ctx, actor, resourceID, policy.ActionWrite)
	if err != nil {
		return gateway.ApplyResult{}, err
	}
	inst, caps, err := s.session(ctx, key.AddonID, r.Generation)
	if err != nil {
		return gateway.ApplyResult{}, err
	}
	kind := ""
	for _, d := range caps.ToolDefinitions {
		if d.Name == r.Tool {
			kind = d.Kind
		}
	}
	action := policy.ActionWrite
	switch kind {
	case contract.ToolKindWrite:
	case contract.ToolKindSchema:
		action = policy.ActionSchema
	default:
		// Reads, creations, admin tools and tools nobody declared are not
		// changes a person applies here.
		return gateway.ApplyResult{}, gateway.ErrInvalidRequest
	}
	if rs := caps.Resources; rs != nil && (r.Tool == rs.Rename || r.Tool == rs.Create || r.Tool == rs.Delete) {
		return gateway.ApplyResult{}, gateway.ErrInvalidRequest
	}
	def, ok := definition(caps, r.Tool, kind)
	if !ok {
		return gateway.ApplyResult{}, gateway.ErrInvalidRequest
	}
	if _, err = s.authorized(ctx, actor, resourceID, action); err != nil {
		return gateway.ApplyResult{}, err
	}
	// The arguments are the tool's own and name the resource through the
	// path only: a body that names another one is not a valid request.
	var given map[string]json.RawMessage
	if len(r.Arguments) == 0 || json.Unmarshal(r.Arguments, &given) != nil || given == nil {
		return gateway.ApplyResult{}, gateway.ErrInvalidRequest
	}
	if _, named := given[contract.ResourceArgument]; named {
		return gateway.ApplyResult{}, gateway.ErrInvalidRequest
	}
	if _, named := given[gateway.ToolKey]; named {
		return gateway.ApplyResult{}, gateway.ErrInvalidRequest
	}
	asked := make(map[string]json.RawMessage, len(given)+1)
	for k, v := range given {
		asked[k] = v
	}
	asked[contract.ResourceArgument], _ = json.Marshal(key.ResourceID)
	raw, _ := json.Marshal(asked)
	res, body, err := toolargs.BindDefinition(def, raw)
	if err != nil || res != key.ResourceID {
		return gateway.ApplyResult{}, gateway.ErrInvalidRequest
	}
	wire := uiOperation(actor, "apply", r.OperationID)
	want := binding{tool: r.Tool, hash: requestHash(key.ResourceID, r.Tool, raw)}
	if have, ok := s.bound(wire); ok && have.hash != want.hash {
		// The ID belongs to another request: refuse it before any call to
		// the add-on, so it is never an unknown outcome.
		return gateway.ApplyResult{}, gateway.ErrOperationConflict
	}
	var out gateway.ApplyResult
	err = s.o.Policy.Admit(ctx, actor, key, action, func(rctx context.Context) error {
		if s.o.Broker != nil {
			if e := s.o.Broker.BlockedError(actor, key); e != nil {
				return e
			}
		}
		var e error
		out, e = s.typedApply(rctx, actor, key, inst, string(action), wire, body, r.Revision, r.OperationID, want)
		return e
	})
	if err != nil {
		return gateway.ApplyResult{}, classify(err)
	}
	out.ResourceID, out.OperationID, out.RequestHash = key.ResourceID, r.OperationID, want.hash
	return out, nil
}

// typedApply is typedChange that also returns what the receipt reported.
func (s *Service) typedApply(ctx context.Context, actor contract.Actor, key policy.ResourceKey, inst addonruntime.Instance, action, wire string, body json.RawMessage, seen contract.Revision, original string, want binding) (gateway.ApplyResult, error) {
	cur, err := s.o.Runtime.Runtime(ctx, key.AddonID)
	if err != nil || cur.Client == nil {
		return gateway.ApplyResult{}, unavailable(err)
	}
	if cur.Generation != inst.Generation {
		return gateway.ApplyResult{}, gateway.ErrStale
	}
	env := func(scope string, d time.Duration) contract.Envelope {
		return contract.Envelope{
			Request:   contract.Request{AddonID: key.AddonID, ResourceID: key.ResourceID, OperationID: wire, Generation: cur.Generation, Action: action, Arguments: body},
			Authority: contract.Authority{Actor: actor, Scope: scope, ExpiresAt: s.o.Now().Add(d)},
		}
	}
	preview, err := cur.Client.Preview(ctx, env("preview", s.o.Limits.PreviewTimeout))
	if err != nil {
		if ctx.Err() != nil {
			return gateway.ApplyResult{}, ctx.Err()
		}
		if errors.Is(err, contract.ErrRefused) {
			return gateway.ApplyResult{}, gateway.Refused(err)
		}
		return gateway.ApplyResult{}, s.previewFailure(ctx, actor, key, cur, err)
	}
	if preview.Revision != seen {
		// A repeat of a change that already committed previews as its own
		// receipt's revision. It goes on to apply, where the add-on replays
		// the receipt for the same request and refuses another one.
		prior, e := cur.Client.Receipt(ctx, wire)
		if e != nil || prior.State != contract.ReceiptCommitted || prior.Revision != preview.Revision {
			return gateway.ApplyResult{}, gateway.ErrStale
		}
	}
	if !s.bind(wire, want) {
		return gateway.ApplyResult{}, gateway.ErrOperationConflict
	}
	receipt, err := cur.Client.Apply(ctx, env("apply", s.o.Limits.ApplyTimeout), preview.Revision)
	if err != nil {
		if errors.Is(err, addonruntime.ErrConflict) {
			// The add-on's ledger holds the ID for another request and
			// changed nothing.
			return gateway.ApplyResult{}, gateway.ErrOperationConflict
		}
		if errors.Is(err, contract.ErrRefused) {
			// The add-on refused the apply before it began, so nothing was
			// applied and nothing was recorded for the ID: it is free again.
			s.unbind(wire, want)
			return gateway.ApplyResult{}, gateway.Refused(err)
		}
		return gateway.ApplyResult{}, &gateway.OutcomeUnknown{OperationID: original}
	}
	switch receipt.State {
	case contract.ReceiptCommitted:
		return gateway.ApplyResult{Revision: receipt.Revision, Result: receipt.Result.Payload}, nil
	case contract.ReceiptRejected:
		// Nothing was recorded for the ID, so it is free again.
		s.unbind(wire, want)
		// A rejection names why in the add-on's own words: a moved revision
		// is the stale view, anything else is a request it cannot carry out.
		var why struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(receipt.Result.Payload, &why) == nil && why.Error != "" && why.Error != "stale" {
			return gateway.ApplyResult{}, gateway.ErrInvalidRequest
		}
		return gateway.ApplyResult{}, gateway.ErrStale
	}
	return gateway.ApplyResult{}, &gateway.OutcomeUnknown{OperationID: original}
}

// previewFailure tells a refusal of the request from an outage: the runtime
// client reports any non-success the same way, so an add-on that still
// answers a read made the refusal.
func (s *Service) previewFailure(ctx context.Context, actor contract.Actor, key policy.ResourceKey, inst addonruntime.Instance, cause error) error {
	args, _ := json.Marshal(map[string]string{gateway.ToolKey: "resource-exists"})
	env := contract.Envelope{
		Request:   contract.Request{AddonID: key.AddonID, ResourceID: key.ResourceID, OperationID: randomID("p"), Generation: inst.Generation, Action: "read", Arguments: args},
		Authority: contract.Authority{Actor: actor, Scope: "read", ExpiresAt: s.o.Now().Add(s.o.Limits.ReadTimeout)},
	}
	if _, e := inst.Client.Read(ctx, env); e == nil {
		return gateway.ErrInvalidRequest
	}
	return unavailable(cause)
}

// Delete deletes a resource and its data, as the owner's explicit UI action.
//
// The order is the whole contract and every step is durable:
//
//  1. The resource becomes deleting. It is inaccessible to everyone and takes
//     no new admission, and admitted work is cancelled and waited for.
//  2. With no work left, the revision the owner saw and the confirmation the
//     owner typed are checked. A failure here returns the resource to use.
//  3. The deletion is marked confirmed, pending proposals are invalidated and
//     the add-on is asked to delete under one operation identifier.
//  4. Only after the add-on confirmed it, the record and every grant go.
//
// A crash leaves a deleting record: an unconfirmed one is returned to use, a
// confirmed one is finished by Resume. The add-on is never uninstalled.
func (s *Service) Delete(ctx context.Context, actor contract.Actor, resourceID string, r gateway.DeleteResourceRequest) (gateway.DeleteResult, error) {
	if !validOperation(r.OperationID) || r.Generation == 0 || r.Revision.Schema == 0 || r.Confirm == "" || len(r.Confirm) > maxNameInput {
		return gateway.DeleteResult{}, gateway.ErrInvalidRequest
	}
	key, ok := s.keyOf(resourceID)
	if !ok {
		// A deletion that finished after its answer was lost is the same
		// success for the owner who asked, under the same operation.
		if s.o.Policy != nil {
			done, err := s.o.Policy.DeletedBy(ctx, actor, resourceID, r.OperationID)
			if err != nil {
				return gateway.DeleteResult{}, classify(err)
			}
			if done {
				return gateway.DeleteResult{ResourceID: resourceID, Deleted: true}, nil
			}
		}
		// A resource that is not there is denied like one that is not yours.
		if _, err := s.owned(ctx, actor, resourceID); err != nil {
			return gateway.DeleteResult{}, err
		}
		return gateway.DeleteResult{}, gateway.ErrForbidden
	}
	if s.deletingByOwner(key, actor) {
		// A repeat of a deletion that is already confirmed finishes it.
		release := s.o.Policy.Track(key)
		defer release()
		if err := s.o.Policy.BeginDelete(ctx, actor, key, r.OperationID); err != nil {
			return gateway.DeleteResult{}, classify(err)
		}
		return s.finishDelete(ctx, actor, key, r.OperationID)
	}
	if _, err := s.owned(ctx, actor, resourceID); err != nil {
		return gateway.DeleteResult{}, err
	}
	// Only the owner role gets here, so only it marks the resource as busy.
	release := s.o.Policy.Track(key)
	defer release()
	inst, caps, err := s.session(ctx, key.AddonID, r.Generation)
	if err != nil {
		return gateway.DeleteResult{}, err
	}
	if caps.Resources == nil || caps.Resources.Delete == "" || caps.Resources.Inspect == "" || !contains(caps.AdminTools, caps.Resources.Delete) {
		return gateway.DeleteResult{}, gateway.ErrInvalidRequest
	}
	seen, err := s.inspect(ctx, actor, key, inst, caps.Resources.Inspect)
	if err != nil {
		return gateway.DeleteResult{}, classify(err)
	}
	if seen.Name == "" || r.Confirm != seen.Name {
		return gateway.DeleteResult{}, gateway.ErrInvalidRequest
	}
	if s.o.Broker != nil {
		if e := s.o.Broker.BlockedError(actor, key); e != nil {
			return gateway.DeleteResult{}, classify(e)
		}
	}
	// Past this point a failure before the confirmation returns the
	// resource to use, even when the request was cancelled.
	confirmed := false
	abort := func() {
		if !confirmed {
			_ = s.o.Policy.AbortDelete(key)
		}
	}
	if err = s.o.Policy.BeginDelete(ctx, actor, key, r.OperationID); err != nil {
		abort()
		return gateway.DeleteResult{}, classify(err)
	}
	defer abort()
	// No admitted work is left and nothing new is admitted: this is the
	// revision the deletion removes.
	cur, err := s.revisionOf(ctx, actor, key, inst, caps.Resources.Inspect)
	if err != nil {
		return gateway.DeleteResult{}, classify(err)
	}
	if cur != r.Revision {
		return gateway.DeleteResult{}, gateway.ErrStale
	}
	if err = s.o.Policy.ConfirmDelete(key); err != nil {
		return gateway.DeleteResult{}, classify(err)
	}
	confirmed = true
	return s.finishDelete(ctx, actor, key, r.OperationID)
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func (s *Service) keyOf(resourceID string) (policy.ResourceKey, bool) {
	if s.o.Policy == nil {
		return policy.ResourceKey{}, false
	}
	return s.o.Policy.Key(resourceID)
}

// deletingByOwner reports a confirmed deletion that this actor, as the owner
// role, asked for.
func (s *Service) deletingByOwner(key policy.ResourceKey, actor contract.Actor) bool {
	return s.deleting(key, actor, true)
}

// deletingByActor reports a deletion in progress, confirmed or not, of a
// resource this actor owns.
func (s *Service) deletingByActor(key policy.ResourceKey, actor contract.Actor) bool {
	return s.deleting(key, actor, false)
}

func (s *Service) deleting(key policy.ResourceKey, actor contract.Actor, confirmedOnly bool) bool {
	for _, d := range s.o.Policy.Deleting() {
		if d.Key == key && (d.Confirmed || !confirmedOnly) {
			owner, ok := s.o.Policy.OwnerOf(key)
			return ok && (s.o.Policy.Mode() == policy.ModeLocalOwner || owner == actor.UserID)
		}
	}
	return false
}

// revisionOf reads the revision of a resource that is already out of use, as
// the owner's own request, directly from the add-on.
func (s *Service) revisionOf(ctx context.Context, actor contract.Actor, key policy.ResourceKey, inst addonruntime.Instance, tool string) (contract.Revision, error) {
	args, _ := json.Marshal(map[string]string{gateway.ToolKey: tool})
	cctx, cancel := context.WithTimeout(ctx, s.o.NameTimeout)
	defer cancel()
	env := contract.Envelope{
		Request:   contract.Request{AddonID: key.AddonID, ResourceID: key.ResourceID, OperationID: randomID("n"), Generation: inst.Generation, Action: "read", Arguments: args},
		Authority: contract.Authority{Actor: actor, Scope: "read", ExpiresAt: s.o.Now().Add(s.o.Limits.ReadTimeout)},
	}
	res, err := inst.Client.Read(cctx, env)
	if err != nil {
		return contract.Revision{}, unavailable(err)
	}
	var v struct {
		Revision contract.Revision `json:"revision"`
	}
	if json.Unmarshal(res.Payload, &v) != nil || v.Revision.Schema == 0 {
		return contract.Revision{}, unavailable(errors.New("inspect result has no revision"))
	}
	return v.Revision, nil
}

// finishDelete runs the confirmed deletion to its end.
func (s *Service) finishDelete(ctx context.Context, actor contract.Actor, key policy.ResourceKey, original string) (gateway.DeleteResult, error) {
	if s.o.Broker != nil {
		if err := s.o.Broker.InvalidateResource(ctx, key); err != nil {
			return gateway.DeleteResult{}, &gateway.OutcomeUnknown{OperationID: original}
		}
	}
	if err := s.settleDeletion(ctx, actor, key, original); err != nil {
		return gateway.DeleteResult{}, err
	}
	return gateway.DeleteResult{ResourceID: key.ResourceID, Deleted: true}, nil
}

// settleDeletion asks the add-on to delete the backing of a confirmed
// deletion under its one operation identifier, and removes the record and its
// grants only after the add-on confirmed. A backing that is already gone is
// the same outcome.
func (s *Service) settleDeletion(ctx context.Context, actor contract.Actor, key policy.ResourceKey, original string) error {
	unknown := func() error { return &gateway.OutcomeUnknown{OperationID: original} }
	inst, caps, err := s.session(ctx, key.AddonID, 0)
	if err != nil {
		return unknown()
	}
	if s.o.BackingConfirmed != nil {
		found, e := s.o.BackingConfirmed(ctx, key)
		if e != nil {
			return unknown()
		}
		if !found {
			return s.complete(key, unknown)
		}
	}
	if caps.Resources == nil || caps.Resources.Delete == "" || !contains(caps.AdminTools, caps.Resources.Delete) {
		return unknown()
	}
	body, _ := json.Marshal(map[string]string{gateway.ToolKey: caps.Resources.Delete})
	wire := deleteOperation(key.ResourceID)
	env := func(scope string, d time.Duration) contract.Envelope {
		return contract.Envelope{
			Request:   contract.Request{AddonID: key.AddonID, ResourceID: key.ResourceID, OperationID: wire, Generation: inst.Generation, Action: string(policy.ActionDelete), Arguments: body},
			Authority: contract.Authority{Actor: actor, Scope: scope, ExpiresAt: s.o.Now().Add(d)},
		}
	}
	preview, err := inst.Client.Preview(ctx, env("preview", s.o.Limits.PreviewTimeout))
	if err != nil {
		// A preview deletes nothing; the deletion stays confirmed and is
		// resumed.
		return unknown()
	}
	receipt, err := inst.Client.Apply(ctx, env("apply", s.o.Limits.ApplyTimeout), preview.Revision)
	if err != nil {
		return unknown()
	}
	switch receipt.State {
	case contract.ReceiptCommitted:
		return s.complete(key, unknown)
	case contract.ReceiptRejected:
		// The add-on refused, so nothing was removed: the resource is back.
		_ = s.o.Policy.AbortDelete(key)
		return gateway.ErrStale
	}
	return unknown()
}

func (s *Service) complete(key policy.ResourceKey, unknown func() error) error {
	// Selections that name the resource are left alone: the reference is
	// denied like an unknown one, and the bounds and the expiry of the store
	// remove it.
	if err := s.o.Policy.FinishDelete(key); err != nil {
		return unknown()
	}
	return nil
}

// Resume settles the deletions a crash or an outage left: an unconfirmed one
// is returned to use, a confirmed one is finished. It is fail-soft and is
// repeated by the owner's reconcile loop while any deletion is left.
func (s *Service) Resume(ctx context.Context) {
	if s.o.Policy == nil {
		return
	}
	for _, d := range s.o.Policy.Deleting() {
		if ctx.Err() != nil {
			return
		}
		if s.o.Policy.Busy(d.Key) {
			continue
		}
		if !d.Confirmed {
			_ = s.o.Policy.AbortDelete(d.Key)
			continue
		}
		release := s.o.Policy.Track(d.Key)
		if s.o.Broker != nil {
			_ = s.o.Broker.InvalidateResource(ctx, d.Key)
		}
		_ = s.settleDeletion(ctx, resumeActor, d.Key, d.Op)
		release()
	}
}

// Operation looks the receipt of a UI mutation up. It is read-only toward the
// add-on and available to the owner role only. kind is rename or delete.
func (s *Service) Operation(ctx context.Context, actor contract.Actor, resourceID, kind, operationID, hash string) (gateway.OperationStatus, error) {
	if kind != "rename" && kind != "delete" && kind != "apply" || !validOperation(operationID) {
		return gateway.OperationStatus{}, gateway.ErrInvalidRequest
	}
	if hash != "" && (kind != "apply" || !sha256Hex.MatchString(hash)) {
		return gateway.OperationStatus{}, gateway.ErrInvalidRequest
	}
	if kind == "delete" && s.o.Policy != nil {
		if k, known := s.o.Policy.Key(resourceID); !known {
			// The record of a finished deletion is gone; its owner can still
			// learn that it committed.
			done, err := s.o.Policy.DeletedBy(ctx, actor, resourceID, operationID)
			if err != nil {
				return gateway.OperationStatus{}, classify(err)
			}
			if done {
				return gateway.OperationStatus{OperationID: operationID, State: string(contract.ReceiptCommitted)}, nil
			}
		} else if s.deletingByActor(k, actor) {
			return gateway.OperationStatus{OperationID: operationID, State: string(contract.ReceiptPending)}, nil
		}
	}
	var key policy.ResourceKey
	var err error
	if kind == "apply" {
		// Editors apply changes, so they look their own receipts up. The
		// wire operation is derived from the actor: nobody reads another
		// person's receipt.
		key, err = s.authorized(ctx, actor, resourceID, policy.ActionWrite)
	} else {
		key, err = s.owned(ctx, actor, resourceID)
	}
	if err != nil {
		return gateway.OperationStatus{}, err
	}
	wire := uiOperation(actor, kind, operationID)
	if kind == "delete" {
		wire = deleteOperation(key.ResourceID)
	}
	inst, _, err := s.session(ctx, key.AddonID, 0)
	if err != nil {
		return gateway.OperationStatus{}, err
	}
	have, known := s.bound(wire)
	if kind == "apply" && hash != "" && (!known || have.hash != hash) {
		if known {
			// The ID was used for another request: its receipt is not an
			// answer about this one.
			return gateway.OperationStatus{}, gateway.ErrOperationConflict
		}
		// The host does not hold the request this ID was used for, so it
		// cannot vouch that the receipt is the caller's. Repeating the
		// change is safe: the add-on replays the same request and refuses
		// another one.
		return gateway.OperationStatus{OperationID: operationID, State: string(contract.ReceiptUnknown)}, nil
	}
	rcpt, err := inst.Client.Receipt(ctx, wire)
	if err != nil {
		return gateway.OperationStatus{}, unavailable(err)
	}
	st := gateway.OperationStatus{OperationID: operationID, State: string(rcpt.State)}
	if kind == "apply" && known {
		st.Tool, st.RequestHash = have.tool, have.hash
	}
	return st, nil
}

// conversation is the validated key of a selection.
func conversation(id string) bool { return toolargs.ValidConversationID(id) }

func (s *Service) selections() (*SelectionStore, error) {
	if s.o.Selections == nil || s.o.Policy == nil {
		return nil, gateway.ErrUnavailable
	}
	return s.o.Selections, nil
}

// authorizeSelected re-authorizes a stored reference at the owner: the actor
// must hold at least the viewer role now. A reference that fails reports the
// same denial as an unknown resource. It is kept: it is no authority, and the
// person may be granted the resource again.
func (s *Service) authorizeSelected(ctx context.Context, actor contract.Actor, addonID, resourceID string) (policy.ResourceKey, policy.Role, error) {
	key, ok := s.o.Policy.Key(resourceID)
	if ok && key.AddonID != addonID {
		ok = false
	}
	if !ok {
		key = policy.ResourceKey{AddonID: addonID, ResourceID: resourceID}
	}
	role, err := s.o.Policy.RoleOf(ctx, actor, key)
	if err != nil {
		return key, "", classify(err)
	}
	return key, role, nil
}

// Selection reads the selection of a conversation. A missing, revoked or
// deleted reference is the same answer: nothing is selected.
func (s *Service) Selection(ctx context.Context, actor contract.Actor, conv, addonID string) (gateway.Selection, error) {
	st, err := s.selections()
	if err != nil {
		return gateway.Selection{}, err
	}
	if !conversation(conv) || !identifier.MatchString(addonID) {
		return gateway.Selection{}, gateway.ErrInvalidRequest
	}
	id, ok, e := st.Get(actor.UserID, conv, addonID)
	if e != nil {
		return gateway.Selection{}, unavailable(e)
	}
	if !ok {
		return gateway.Selection{}, gateway.ErrNotSelected
	}
	key, role, err := s.authorizeSelected(ctx, actor, addonID, id)
	if err != nil {
		if errors.Is(err, gateway.ErrForbidden) {
			return gateway.Selection{}, gateway.ErrNotSelected
		}
		return gateway.Selection{}, err
	}
	return s.selectionView(ctx, actor, conv, key, role), nil
}

func (s *Service) selectionView(ctx context.Context, actor contract.Actor, conv string, key policy.ResourceKey, role policy.Role) gateway.Selection {
	out := gateway.Selection{AddonID: key.AddonID, ConversationID: conv, ResourceID: key.ResourceID, Role: string(role)}
	if inst, caps, err := s.session(ctx, key.AddonID, 0); err == nil && caps.Resources != nil && caps.Resources.Inspect != "" {
		if v, e := s.inspect(ctx, actor, key, inst, caps.Resources.Inspect); e == nil {
			out.Name = v.Name
		}
	}
	return out
}

// Select stores the selection of a conversation. The caller must hold at
// least the viewer role on the resource now, and the resource must belong to
// the add-on of the path.
func (s *Service) Select(ctx context.Context, actor contract.Actor, conv, addonID, resourceID string) (gateway.Selection, error) {
	st, err := s.selections()
	if err != nil {
		return gateway.Selection{}, err
	}
	if !conversation(conv) || !identifier.MatchString(addonID) || !identifier.MatchString(resourceID) {
		return gateway.Selection{}, gateway.ErrInvalidRequest
	}
	key, ok := s.o.Policy.Key(resourceID)
	if !ok || key.AddonID != addonID {
		// The same denial as a resource the person may not read.
		if _, err = s.o.Policy.RoleOf(ctx, actor, policy.ResourceKey{AddonID: addonID, ResourceID: resourceID}); err != nil {
			return gateway.Selection{}, classify(err)
		}
		return gateway.Selection{}, gateway.ErrForbidden
	}
	role, err := s.o.Policy.RoleOf(ctx, actor, key)
	if err != nil {
		return gateway.Selection{}, classify(err)
	}
	if e := st.Put(actor.UserID, conv, addonID, resourceID); e != nil {
		return gateway.Selection{}, unavailable(e)
	}
	return s.selectionView(ctx, actor, conv, key, role), nil
}

// ClearSelection removes the selection of a conversation.
func (s *Service) ClearSelection(_ context.Context, actor contract.Actor, conv, addonID string) error {
	st, err := s.selections()
	if err != nil {
		return err
	}
	if !conversation(conv) || !identifier.MatchString(addonID) {
		return gateway.ErrInvalidRequest
	}
	if e := st.Delete(actor.UserID, conv, addonID); e != nil {
		return unavailable(e)
	}
	return nil
}

// ResolveSelection is the tool path: the resource ID of the conversation's
// selection after the owner authorized it for the actor, or ErrNotSelected,
// or the denial of an unknown resource.
func (s *Service) ResolveSelection(ctx context.Context, actor contract.Actor, conv, addonID string) (string, error) {
	st, err := s.selections()
	if err != nil {
		return "", err
	}
	if !conversation(conv) || !identifier.MatchString(addonID) {
		return "", gateway.ErrInvalidRequest
	}
	id, ok, e := st.Get(actor.UserID, conv, addonID)
	if e != nil {
		return "", unavailable(e)
	}
	if !ok {
		return "", gateway.ErrNotSelected
	}
	if _, _, err = s.authorizeSelected(ctx, actor, addonID, id); err != nil {
		return "", err
	}
	return id, nil
}
