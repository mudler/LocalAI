// SPDX-License-Identifier: MIT
package policy

import (
	"context"
	"errors"
	"regexp"
	"sort"
	"time"

	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// The UI-only resource management of the owner is built on these methods. A
// model, a proposal or a typed dispatch has no path to any of them: the
// gateway never names them.

// Mode is the ownership model of the policy's store.
func (p *Policy) Mode() Mode { return p.store.Mode() }

// ResourceView is one active resource as an actor sees it in a listing.
type ResourceView struct {
	Key  ResourceKey
	Role Role
	// OwnerID is the immutable owner user ID. A caller shows a display name,
	// never the identifier, to anyone who is not the owner.
	OwnerID string
	// Shared is the number of grants. It is set only when Role is owner.
	Shared int
}

// Listing lists the active resources of an addon the actor may at least read,
// in ID order, with the actor's own role. Resources of other people that are
// not shared with the actor are never part of the answer.
func (p *Policy) Listing(ctx context.Context, actor contract.Actor, addonID string) ([]ResourceView, error) {
	if err := p.current(ctx, actor); err != nil {
		return nil, err
	}
	p.store.mu.Lock()
	defer p.store.mu.Unlock()
	var out []ResourceView
	for id, r := range p.store.doc.Resources {
		if r.State != stateActive || r.AddonID != addonID {
			continue
		}
		role := p.role(actor, *r)
		if role.rank() < ActionRead.required().rank() {
			continue
		}
		v := ResourceView{Key: ResourceKey{AddonID: addonID, ResourceID: id}, Role: role, OwnerID: r.Owner}
		if role == RoleOwner {
			v.Shared = len(r.Grants)
		}
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key.ResourceID < out[j].Key.ResourceID })
	return out, nil
}

// RoleOf is the actor's current role on an active resource. A resource that
// does not exist, is pending or deleting, or is not the actor's to see is
// ErrForbidden: the same answer for all of them.
func (p *Policy) RoleOf(ctx context.Context, actor contract.Actor, key ResourceKey) (Role, error) {
	if err := p.current(ctx, actor); err != nil {
		return "", err
	}
	r, ok := p.store.get(key.ResourceID)
	if !ok || r.State != stateActive || r.AddonID != key.AddonID || key.AddonID == "" {
		return "", ErrForbidden
	}
	role := p.role(actor, r)
	if role == "" {
		return "", ErrForbidden
	}
	return role, nil
}

// Grants lists the grants of a resource for its owner role. Local-owner mode
// has no sharing and reports ErrForbidden.
func (p *Policy) Grants(ctx context.Context, actor contract.Actor, key ResourceKey) (map[string]Role, error) {
	if p.store.Mode() != ModeNamedUser {
		return nil, ErrForbidden
	}
	if err := p.Authorize(ctx, actor, key, ActionShare); err != nil {
		return nil, err
	}
	r, ok := p.store.get(key.ResourceID)
	if !ok {
		return nil, ErrForbidden
	}
	out := make(map[string]Role, len(r.Grants))
	for u, role := range r.Grants {
		out[u] = role
	}
	return out, nil
}

// Key names the resource with this ID, whatever its state. It decides
// nothing: the caller authorizes the actor on the key it returns, and a
// missing resource and a forbidden one have the same answer there.
func (p *Policy) Key(resourceID string) (ResourceKey, bool) {
	r, ok := p.store.get(resourceID)
	if !ok || resourceID == "" {
		return ResourceKey{}, false
	}
	return ResourceKey{AddonID: r.AddonID, ResourceID: resourceID}, true
}

// OwnerOf returns the immutable owner of an active resource for the actor's
// own listing. It denies like RoleOf.
func (p *Policy) OwnerOf(key ResourceKey) (string, bool) {
	r, ok := p.store.get(key.ResourceID)
	if !ok || r.AddonID != key.AddonID {
		return "", false
	}
	return r.Owner, true
}

var uiResourceID = regexp.MustCompile(`^r[0-9a-f]{31}$`)

// CreateWithID is Create for a caller-derived resource ID, so that repeating
// one create request is one resource. The ID is the host's, never the
// addon's or the model's. ref binds the record to the request: a repeat with
// the same ID and another ref is a denial. A repeat that finds the resource
// active returns it without creating anything; one that finds it pending runs
// the create step again, which the addon answers by its receipt.
func (p *Policy) CreateWithID(ctx context.Context, actor contract.Actor, addonID, id, ref string, create func(context.Context, ResourceKey) error) (ResourceKey, error) {
	if create == nil || !uiResourceID.MatchString(id) || ref == "" || len(ref) > 128 || addonID == "" || len(addonID) > 256 || p.opts.CreateAuthority == nil {
		return ResourceKey{}, ErrForbidden
	}
	if err := p.current(ctx, actor); err != nil {
		return ResourceKey{}, err
	}
	if p.isHeld(addonID) {
		return ResourceKey{}, errHeld
	}
	if err := p.opts.CreateAuthority(ctx, actor, addonID); err != nil {
		if ctx.Err() != nil {
			return ResourceKey{}, ctx.Err()
		}
		return ResourceKey{}, p.classify(err)
	}
	key := ResourceKey{AddonID: addonID, ResourceID: id}
	owner := p.store.Mode().ownerOf(actor)
	release := p.track(id)
	defer release()
	// Repeats of one request run one at a time.
	g := p.gate(id)
	select {
	case g.writer <- struct{}{}:
		defer func() { <-g.writer }()
	case <-ctx.Done():
		return ResourceKey{}, ctx.Err()
	}
	state := ""
	err := p.store.mutate(func(d *document) error {
		if r := d.Resources[id]; r != nil {
			if r.AddonID != addonID || r.Owner != owner || r.CreateRef != ref {
				return ErrForbidden
			}
			state = r.State
			if state != statePend && state != stateActive {
				return ErrForbidden
			}
			return nil
		}
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
		d.Resources[id] = &record{AddonID: addonID, Owner: owner, State: statePend, Grants: map[string]Role{}, CreateRef: ref}
		state = statePend
		return nil
	})
	if err != nil {
		return ResourceKey{}, err
	}
	if state == stateActive {
		return key, nil
	}
	if err = create(ctx, key); err != nil {
		if errors.Is(err, ErrNotCreated) {
			dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			_ = p.Abandon(dctx, actor, key)
			return ResourceKey{}, err
		}
		return key, err
	}
	if err = p.Commit(ctx, actor, key); err != nil {
		return key, err
	}
	return key, nil
}

// Deletion is a resource that is being deleted.
type Deletion struct {
	Key ResourceKey
	// Op is the operation identifier of the delete request.
	Op string
	// By is the owner who asked.
	By string
	// Confirmed says the deletion passed its preconditions and may be
	// resumed after a crash.
	Confirmed bool
}

// Deleting lists the deletions in progress or left by a crash, in ID order.
func (p *Policy) Deleting() []Deletion {
	p.store.mu.Lock()
	defer p.store.mu.Unlock()
	var out []Deletion
	for id, r := range p.store.doc.Resources {
		if r.State == stateDeleting && r.Delete != nil {
			out = append(out, Deletion{Key: ResourceKey{AddonID: r.AddonID, ResourceID: id}, Op: r.Delete.Op, By: r.Delete.By, Confirmed: r.Delete.Confirmed})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key.ResourceID < out[j].Key.ResourceID })
	return out
}

// Track marks a resource as being worked on by this process. Resume leaves a
// tracked deletion alone: its own caller settles it.
func (p *Policy) Track(key ResourceKey) (release func()) { return p.track(key.ResourceID) }

// Busy reports whether this process is working on the resource.
func (p *Policy) Busy(key ResourceKey) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, ok := p.inflight[key.ResourceID]
	return ok
}

// BeginDelete takes a resource out of use for a deletion. Only the owner role
// may. The record becomes deleting at once and durably: it is inaccessible to
// everyone, takes no new admission and no grant change. Admitted work is
// cancelled and waited for, within the drain bound (ErrDrainPending when it
// did not stop; the record stays deleting and the caller aborts or retries).
// Calling it again for a resource that is already deleting resumes it, for
// the same owner only.
func (p *Policy) BeginDelete(ctx context.Context, actor contract.Actor, key ResourceKey, op string) error {
	if op == "" || len(op) > 256 {
		return ErrForbidden
	}
	if err := p.current(ctx, actor); err != nil {
		return err
	}
	g := p.gate(key.ResourceID)
	g.mu.Lock()
	r, ok := p.store.get(key.ResourceID)
	if !ok || r.AddonID != key.AddonID || key.AddonID == "" || (r.State != stateActive && r.State != stateDeleting) {
		g.mu.Unlock()
		return ErrForbidden
	}
	if p.role(actor, r).rank() < ActionDelete.required().rank() {
		g.mu.Unlock()
		return ErrForbidden
	}
	if r.State == stateActive {
		if err := p.store.mutate(func(d *document) error {
			rec := d.Resources[key.ResourceID]
			if rec == nil || rec.State != stateActive {
				return ErrForbidden
			}
			rec.State = stateDeleting
			rec.Delete = &deleteMark{Op: op, By: actor.UserID}
			return nil
		}); err != nil {
			g.mu.Unlock()
			return err
		}
	}
	var victims []*admitted
	for a := range g.active {
		a.cancel()
		victims = append(victims, a)
	}
	g.mu.Unlock()
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
	return nil
}

// ConfirmDelete records, durably, that the deletion passed its preconditions.
// From here a crash resumes the deletion instead of undoing it.
func (p *Policy) ConfirmDelete(key ResourceKey) error {
	return p.store.mutate(func(d *document) error {
		r := d.Resources[key.ResourceID]
		if r == nil || r.State != stateDeleting || r.AddonID != key.AddonID || r.Delete == nil {
			return ErrForbidden
		}
		r.Delete.Confirmed = true
		return nil
	})
}

// AbortDelete returns a deletion to the active state. It is for a deletion
// that was never confirmed, and for one the addon definitively refused, which
// proves that nothing was removed.
func (p *Policy) AbortDelete(key ResourceKey) error {
	return p.store.mutate(func(d *document) error {
		r := d.Resources[key.ResourceID]
		if r == nil || r.State != stateDeleting || r.AddonID != key.AddonID || r.Delete == nil {
			return ErrForbidden
		}
		r.State = stateActive
		r.Delete = nil
		return nil
	})
}

// FinishDelete removes the record of a confirmed deletion, and with it every
// grant. Call it only once the addon confirmed that the backing is gone. A
// tombstone of the deletion stays for its owner, in the same durable write.
func (p *Policy) FinishDelete(key ResourceKey) error {
	return p.store.mutate(func(d *document) error {
		r := d.Resources[key.ResourceID]
		if r == nil {
			return nil
		}
		if r.State != stateDeleting || r.AddonID != key.AddonID || r.Delete == nil || !r.Delete.Confirmed {
			return ErrForbidden
		}
		if d.Deleted == nil {
			d.Deleted = map[string]*tombstone{}
		}
		for len(d.Deleted) >= MaxTombstones {
			oldest := ""
			for id, t := range d.Deleted {
				if oldest == "" || t.At.Before(d.Deleted[oldest].At) || (t.At.Equal(d.Deleted[oldest].At) && id < oldest) {
					oldest = id
				}
			}
			delete(d.Deleted, oldest)
		}
		d.Deleted[key.ResourceID] = &tombstone{AddonID: r.AddonID, Owner: r.Owner, Op: r.Delete.Op, At: time.Now().UTC()}
		delete(d.Resources, key.ResourceID)
		return nil
	})
}

// DeletedBy reports whether the actor, as the owner the resource had, deleted
// the resource under the operation (any operation when op is empty). It is how
// a repeated delete request, or a receipt lookup, learns that the deletion
// finished after its answer was lost. Anyone else, and any other resource,
// gets false: the answer of an unknown resource.
func (p *Policy) DeletedBy(ctx context.Context, actor contract.Actor, resourceID, op string) (bool, error) {
	if err := p.current(ctx, actor); err != nil {
		return false, err
	}
	p.store.mu.Lock()
	defer p.store.mu.Unlock()
	t, ok := p.store.doc.Deleted[resourceID]
	if !ok || t.Owner != p.store.Mode().ownerOf(actor) {
		return false, nil
	}
	return op == "" || t.Op == op, nil
}
