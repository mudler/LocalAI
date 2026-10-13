// SPDX-License-Identifier: MIT
package policy

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// ProposalInvalidator is the seam the proposal broker implements. Policy calls
// it when a user loses write authority on a resource, so a stored proposal made
// under the old authority can no longer be confirmed. It returns after the
// proposals are invalid or fails; a failure leaves the revocation unacknowledged.
type ProposalInvalidator interface {
	InvalidateProposals(ctx context.Context, resource ResourceKey, userID string) error
}

// DefaultDrainTimeout bounds how long a revocation waits for admitted work.
const DefaultDrainTimeout = 10 * time.Second

// Options are the host-supplied dependencies. Missing ones deny.
type Options struct {
	// Revalidate checks the current account and credential. It is the host's
	// authentication check and runs on every admission, not only at login.
	Revalidate func(context.Context, contract.Actor) error
	// CreateAuthority says whether the actor may create a resource for the addon.
	// Without it no resource can be created.
	CreateAuthority func(ctx context.Context, actor contract.Actor, addonID string) error
	Proposals       ProposalInvalidator
	DrainTimeout    time.Duration
	// MaxPendingPerUser, MaxResourcesPerUser and MaxResources bound the store.
	// Zero selects the finite defaults; a limit can never be disabled.
	MaxPendingPerUser   int
	MaxResourcesPerUser int
	MaxResources        int
}

// Default limits for registrations, per owner and in total.
const (
	DefaultMaxPendingPerUser   = 8
	DefaultMaxResourcesPerUser = 256
	DefaultMaxResources        = 4096
)

// Policy decides and serializes access. It keeps no per-user state besides
// the admissions currently running; callers pass the actor on every call.
type Policy struct {
	store *Store
	opts  Options
	mu    sync.Mutex
	gates map[string]*gate
}

// New builds a policy on an open store.
func New(store *Store, opts Options) *Policy {
	if opts.DrainTimeout <= 0 {
		opts.DrainTimeout = DefaultDrainTimeout
	}
	if opts.MaxPendingPerUser <= 0 {
		opts.MaxPendingPerUser = DefaultMaxPendingPerUser
	}
	if opts.MaxResourcesPerUser <= 0 {
		opts.MaxResourcesPerUser = DefaultMaxResourcesPerUser
	}
	if opts.MaxResources <= 0 {
		opts.MaxResources = DefaultMaxResources
	}
	return &Policy{store: store, opts: opts, gates: map[string]*gate{}}
}

type admitted struct {
	user     string
	mutation bool
	// epoch is the resource's authority epoch when the work was admitted.
	epoch  uint64
	cancel context.CancelFunc
	done   chan struct{}
}

// gate serializes everything that can change or depend on one resource's
// authority: grant changes, revocation and the start of each admission. Mutating
// work additionally holds the writer slot for its whole run.
type gate struct {
	mu     sync.Mutex
	active map[*admitted]struct{}
	writer chan struct{}
}

func (p *Policy) gate(id string) *gate {
	p.mu.Lock()
	defer p.mu.Unlock()
	g, ok := p.gates[id]
	if !ok {
		g = &gate{active: map[*admitted]struct{}{}, writer: make(chan struct{}, 1)}
		p.gates[id] = g
	}
	return g
}

// role is the actor's role on a stored record, or "" for none.
func (p *Policy) role(a contract.Actor, r record) Role {
	if p.store.Mode() == ModeLocalOwner {
		return RoleOwner
	}
	if a.UserID == r.Owner {
		return RoleOwner
	}
	return r.Grants[a.UserID]
}

// check is the pure store decision, with no identity or account step.
func (p *Policy) check(a contract.Actor, key ResourceKey, action Action) error {
	r, ok := p.store.get(key.ResourceID)
	if !ok || r.State != stateActive || r.AddonID != key.AddonID || key.AddonID == "" {
		return ErrForbidden
	}
	if p.role(a, r).rank() < action.required().rank() {
		return ErrForbidden
	}
	return nil
}

func (p *Policy) current(ctx context.Context, a contract.Actor) error {
	if p.opts.Revalidate == nil {
		return ErrForbidden
	}
	if err := p.store.Mode().identity(a); err != nil {
		return err
	}
	if err := p.opts.Revalidate(ctx, a); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return p.classify(err)
	}
	return nil
}

// classify maps a host check error to the policy's own answers. Only a
// definite denial is ErrForbidden; an unclassified error is ErrUnavailable.
func (p *Policy) classify(err error) error {
	if errors.Is(gateway.ClassifyCheck(err), gateway.ErrForbidden) {
		return ErrForbidden
	}
	return ErrUnavailable
}

// Authorize answers whether the actor may run the action now. It does not
// serialize with other work; use Admit around the work itself.
func (p *Policy) Authorize(ctx context.Context, actor contract.Actor, resource ResourceKey, action Action) error {
	if err := p.current(ctx, actor); err != nil {
		return err
	}
	return p.check(actor, resource, action)
}

// Admit runs run while the actor is authorized. The decision and the
// registration of the work are atomic with respect to grant changes on the same
// resource; a revocation cancels the context given to run and waits for it.
// Writes are admitted one at a time per resource.
func (p *Policy) Admit(ctx context.Context, actor contract.Actor, resource ResourceKey, action Action, run func(context.Context) error) error {
	if run == nil {
		return ErrForbidden
	}
	if err := p.Authorize(ctx, actor, resource, action); err != nil {
		return err
	}
	g := p.gate(resource.ResourceID)
	runCtx, cancel := context.WithCancel(ctx)
	adm := &admitted{user: actor.UserID, mutation: action.Mutation(), cancel: cancel, done: make(chan struct{})}
	g.mu.Lock()
	if err := p.check(actor, resource, action); err != nil {
		g.mu.Unlock()
		cancel()
		return err
	}
	// The epoch is read under the gate, the same lock that serializes grant
	// changes, so it names exactly the lowerings this work was admitted after.
	rec, _ := p.store.get(resource.ResourceID)
	adm.epoch = rec.Epoch
	g.active[adm] = struct{}{}
	g.mu.Unlock()
	defer func() {
		g.mu.Lock()
		delete(g.active, adm)
		g.mu.Unlock()
		cancel()
		close(adm.done)
	}()
	if adm.mutation {
		select {
		case g.writer <- struct{}{}:
			defer func() { <-g.writer }()
		case <-runCtx.Done():
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return ErrForbidden
		}
		// Authority can change while this admission waited for the writer slot.
		g.mu.Lock()
		err := p.check(actor, resource, action)
		g.mu.Unlock()
		if err != nil {
			return err
		}
	}
	if err := p.current(runCtx, actor); err != nil {
		return err
	}
	// The host check may ignore cancellation. A revocation cancels admitted work
	// while it holds the gate, so under the gate a live context means the
	// revocation, if any, comes after this point and will wait for run.
	g.mu.Lock()
	err := p.check(actor, resource, action)
	if err == nil && runCtx.Err() != nil {
		err = ErrForbidden
		if ctx.Err() != nil {
			err = ctx.Err()
		}
	}
	g.mu.Unlock()
	if err != nil {
		return err
	}
	return run(runCtx)
}

// SetGrant gives userID the role on the whole resource. Only the owner may
// share, never in local-owner mode, and the owner role cannot be granted.
// Lowering a role invalidates the user's pending proposals and stops their
// admitted mutations before it returns.
func (p *Policy) SetGrant(ctx context.Context, actor contract.Actor, resource ResourceKey, userID string, role Role) error {
	if role != RoleViewer && role != RoleEditor {
		return ErrForbidden
	}
	return p.change(ctx, actor, resource, userID, role, false)
}

// Revoke removes userID's grant. It returns only after the user's pending
// proposals are invalidated and their admitted work has stopped, or reports
// ErrDrainPending when that did not happen in the bound.
func (p *Policy) Revoke(ctx context.Context, actor contract.Actor, resource ResourceKey, userID string) error {
	return p.change(ctx, actor, resource, userID, "", true)
}

func (p *Policy) change(ctx context.Context, actor contract.Actor, resource ResourceKey, userID string, role Role, remove bool) error {
	if p.store.Mode() != ModeNamedUser || !validUserID(userID) {
		return ErrForbidden
	}
	if err := p.Authorize(ctx, actor, resource, ActionShare); err != nil {
		return err
	}
	g := p.gate(resource.ResourceID)
	g.mu.Lock()
	if err := p.check(actor, resource, ActionShare); err != nil {
		g.mu.Unlock()
		return err
	}
	r, _ := p.store.get(resource.ResourceID)
	if userID == r.Owner {
		g.mu.Unlock()
		return ErrForbidden
	}
	prev := r.Grants[userID]
	owed, isOwed := r.Drain[userID]
	// Only a change that takes authority away starts a new epoch. A retry of
	// that change, or a regrant while its drain is still owed, keeps the mark
	// as it is: the cutoff must stay at the lowering that created it, or the
	// retry would stop work admitted under the later grant.
	lowering := (prev != "" && (remove || role.rank() < prev.rank())) || (remove && !isOwed)
	// A removal after a downgrade keeps the older read cutoff, which the copy
	// of the owed mark carries; a newer lowering only ever raises a cutoff.
	mark := owed
	if lowering {
		mark.Writes = r.Epoch + 1
		if remove {
			mark.Reads = r.Epoch + 1
		}
	}
	// A previous call may have made the grant durable and then failed to finish
	// the drain, so the stored grant alone cannot say whether work above the new
	// role is still running. The retry waits whenever a drain is owed.
	lowered := lowering || isOwed
	mutErr := p.store.mutate(func(d *document) error {
		rec := d.Resources[resource.ResourceID]
		if remove {
			delete(rec.Grants, userID)
		} else {
			rec.Grants[userID] = role
		}
		if lowering {
			rec.Epoch++
			if rec.Drain == nil {
				rec.Drain = map[string]drainMark{}
			}
			rec.Drain[userID] = mark
		}
		return nil
	})
	if mutErr != nil {
		// A store that failed after the rename already applied the change in
		// memory. Then the stricter authority is in force and its work is
		// drained, but the call is still not acknowledged.
		cur, _ := p.store.get(resource.ResourceID)
		applied := cur.Grants[userID] == role
		if remove {
			applied = cur.Grants[userID] == ""
		}
		if !applied {
			g.mu.Unlock()
			return mutErr
		}
	}
	var victims []*admitted
	if lowered {
		for a := range g.active {
			if a.user != userID {
				continue
			}
			cutoff := mark.Reads
			if a.mutation {
				cutoff = mark.Writes
			}
			if a.epoch < cutoff {
				a.cancel()
				victims = append(victims, a)
			}
		}
	}
	g.mu.Unlock()
	if !lowered {
		return mutErr
	}
	if p.opts.Proposals != nil {
		if err := p.opts.Proposals.InvalidateProposals(ctx, resource, userID); err != nil {
			return ErrDrainPending
		}
	}
	timer := time.NewTimer(p.opts.DrainTimeout)
	defer timer.Stop()
	for _, a := range victims {
		select {
		case <-a.done:
		case <-timer.C:
			return ErrDrainPending
		case <-ctx.Done():
			return ErrDrainPending
		}
	}
	if mutErr != nil {
		return mutErr
	}
	return p.store.mutate(func(d *document) error {
		// A lowering that ran meanwhile replaced the mark with a later cutoff
		// and owes its own drain; only the mark this call drained is cleared.
		if rec := d.Resources[resource.ResourceID]; rec != nil && rec.Drain[userID] == mark {
			delete(rec.Drain, userID)
		}
		return nil
	})
}

// Register records a new resource as pending, owned by the creating actor. A
// pending resource is inaccessible to everyone, including its owner, until
// Commit or Reconcile activates it. Call it before the addon creates anything,
// so a crash leaves a record that Reconcile can resolve and never an
// unrecorded database.
func (p *Policy) Register(ctx context.Context, actor contract.Actor, addonID string) (ResourceKey, error) {
	if addonID == "" || len(addonID) > 256 || p.opts.CreateAuthority == nil {
		return ResourceKey{}, ErrForbidden
	}
	if err := p.current(ctx, actor); err != nil {
		return ResourceKey{}, err
	}
	if err := p.opts.CreateAuthority(ctx, actor, addonID); err != nil {
		if ctx.Err() != nil {
			return ResourceKey{}, ctx.Err()
		}
		return ResourceKey{}, p.classify(err)
	}
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return ResourceKey{}, ErrUnavailable
	}
	// The envelope contract requires a lowercase letter first, so a bare hex
	// value would be refused by the runtime whenever it starts with a digit.
	// The first character is fixed; the other 124 bits stay random.
	id := "r" + hex.EncodeToString(raw)[1:]
	owner := p.store.Mode().ownerOf(actor)
	err := p.store.mutate(func(d *document) error {
		var pending, owned int
		for _, r := range d.Resources {
			if r.Owner == owner {
				owned++
				if r.State == statePend {
					pending++
				}
			}
		}
		if pending >= p.opts.MaxPendingPerUser || owned >= p.opts.MaxResourcesPerUser || len(d.Resources) >= p.opts.MaxResources {
			return ErrQuota
		}
		d.Resources[id] = &record{AddonID: addonID, Owner: owner, State: statePend, Grants: map[string]Role{}}
		return nil
	})
	return ResourceKey{AddonID: addonID, ResourceID: id}, err
}

func (p *Policy) pendingOwner(ctx context.Context, actor contract.Actor, key ResourceKey) error {
	if err := p.current(ctx, actor); err != nil {
		return err
	}
	r, ok := p.store.get(key.ResourceID)
	if !ok || r.State != statePend || r.AddonID != key.AddonID || r.Owner != p.store.Mode().ownerOf(actor) {
		return ErrForbidden
	}
	return nil
}

// Commit makes a registered resource usable once its backing exists.
func (p *Policy) Commit(ctx context.Context, actor contract.Actor, key ResourceKey) error {
	if err := p.pendingOwner(ctx, actor, key); err != nil {
		return err
	}
	return p.store.mutate(func(d *document) error {
		if r := d.Resources[key.ResourceID]; r != nil && r.State == statePend {
			r.State = stateActive
		}
		return nil
	})
}

// Abandon drops a registration whose backing was not created.
func (p *Policy) Abandon(ctx context.Context, actor contract.Actor, key ResourceKey) error {
	if err := p.pendingOwner(ctx, actor, key); err != nil {
		return err
	}
	return p.store.mutate(func(d *document) error {
		if r := d.Resources[key.ResourceID]; r != nil && r.State == statePend {
			delete(d.Resources, key.ResourceID)
		}
		return nil
	})
}

// Reconcile resolves registrations left pending by a crash. exists reports
// whether the addon created the backing resource: it is then activated for the
// owner recorded before creation, otherwise the record is dropped. A resource
// with no record is never reachable, so an orphan database stays private.
func (p *Policy) Reconcile(ctx context.Context, exists func(context.Context, ResourceKey) (bool, error)) error {
	for _, id := range p.store.pending() {
		r, ok := p.store.get(id)
		if !ok {
			continue
		}
		found, err := exists(ctx, ResourceKey{AddonID: r.AddonID, ResourceID: id})
		if err != nil {
			return ErrUnavailable
		}
		err = p.store.mutate(func(d *document) error {
			rec := d.Resources[id]
			if rec == nil || rec.State != statePend {
				return nil
			}
			if found {
				rec.State = stateActive
			} else {
				delete(d.Resources, id)
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// Resolve maps the opaque resource ID of a request to the host record. The
// returned key carries the recorded addon identity, never the requested one,
// and the caller must be able to read the resource.
func (p *Policy) Resolve(ctx context.Context, actor contract.Actor, request contract.Request) (ResourceKey, error) {
	r, ok := p.store.get(request.ResourceID)
	if !ok || request.ResourceID == "" || r.AddonID != request.AddonID {
		return ResourceKey{}, ErrForbidden
	}
	key := ResourceKey{AddonID: r.AddonID, ResourceID: request.ResourceID}
	if err := p.Authorize(ctx, actor, key, ActionRead); err != nil {
		return ResourceKey{}, err
	}
	return key, nil
}

// Gateway adapts the policy to the gateway's Admission and ResourceResolver.
// Output describes an already-ingested private artifact through the Outputs
// source, which the artifact store implements; without one Output denies.
func (p *Policy) Gateway() *GatewayProvider { return &GatewayProvider{p: p} }

// OutputSource describes private outputs from host-owned records.
type OutputSource interface {
	Output(ctx context.Context, actor contract.Actor, request contract.Request, handle string) (contract.OutputDescription, error)
}

// GatewayProvider implements gateway.Admission and gateway.ResourceResolver.
type GatewayProvider struct {
	p       *Policy
	Outputs OutputSource
}

func (g *GatewayProvider) Admit(ctx context.Context, actor contract.Actor, key ResourceKey, action string, run func(context.Context) error) error {
	return g.p.Admit(ctx, actor, key, ParseAction(action), run)
}

func (g *GatewayProvider) Resolve(ctx context.Context, actor contract.Actor, request contract.Request) (ResourceKey, error) {
	return g.p.Resolve(ctx, actor, request)
}

func (g *GatewayProvider) Output(ctx context.Context, actor contract.Actor, request contract.Request, handle string) (contract.OutputDescription, error) {
	if g.Outputs == nil {
		return contract.OutputDescription{}, ErrForbidden
	}
	return g.Outputs.Output(ctx, actor, request, handle)
}
