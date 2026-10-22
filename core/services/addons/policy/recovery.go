// SPDX-License-Identifier: MIT
package policy

import (
	"context"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// Reconcile resolves an operation whose outcome is not known by looking up its
// receipt. It never submits the operation again, so it is safe to repeat. A
// committed or rejected receipt settles the proposal; anything else leaves it
// unresolved and blocking, and the lookup can be repeated.
func (b *Broker) Reconcile(ctx context.Context, operationID string) (contract.Receipt, error) {
	if _, err := b.attached(); err != nil {
		return contract.Receipt{}, err
	}
	// A lookup in flight is part of the broker: Close cancels and waits for it.
	ctx, leave, err := b.enter(ctx)
	if err != nil {
		return contract.Receipt{}, err
	}
	defer leave()
	r, ok := b.store.get(operationID)
	if !ok || operationID == "" {
		return contract.Receipt{}, ErrForbidden
	}
	switch r.State {
	case gateway.ProposalApplied, gateway.ProposalRejected:
		if r.Receipt != nil {
			return *r.Receipt, nil
		}
		return contract.Receipt{}, ErrProposalClosed
	case gateway.ProposalExecuting, gateway.ProposalOutcomeUnknown, gateway.ProposalAbandoned:
		// An abandoned outcome is still looked up: a late receipt that says
		// the operation ran must be recorded, not hidden.
	default:
		return contract.Receipt{}, ErrProposalClosed
	}
	b.mu.Lock()
	_, live := b.live[operationID]
	b.mu.Unlock()
	instance, err := b.runtime(ctx, r.AddonID)
	if err != nil {
		b.markUnknown(r, live, "runtime unavailable for the receipt lookup")
		return contract.Receipt{}, err
	}
	// A receipt from another generation's runtime proves nothing about this
	// operation. The lookup must reach the generation that received it.
	if instance.Generation != r.Generation {
		b.markUnknown(r, live, "addon generation changed")
		return contract.Receipt{}, ErrUnavailable
	}
	receipt, err := instance.Client.Receipt(ctx, r.wireOperation())
	if err != nil {
		b.markUnknown(r, live, "receipt lookup failed")
		if ctx.Err() != nil {
			return contract.Receipt{}, ctx.Err()
		}
		return contract.Receipt{}, ErrUnavailable
	}
	reason := "resolved by receipt"
	if r.State == gateway.ProposalAbandoned {
		reason = "late receipt after the outcome was abandoned"
	}
	switch receipt.State {
	case contract.ReceiptCommitted:
		b.settle(operationID, gateway.ProposalApplied, &receipt, reason)
	case contract.ReceiptRejected:
		b.settle(operationID, gateway.ProposalRejected, &receipt, reason)
	default:
		b.markUnknown(r, live, "receipt is not final")
	}
	return receipt, nil
}

// markUnknown turns a durable executing state into outcome-unknown when no
// call of this process is still running it.
func (b *Broker) markUnknown(r proposalRecord, live bool, reason string) {
	if live || r.State != gateway.ProposalExecuting {
		return
	}
	_, _ = b.store.transition(r.ID, []ProposalState{gateway.ProposalExecuting}, func(x *proposalRecord) {
		x.State = gateway.ProposalOutcomeUnknown
		x.Reason = reason
	})
}

// reconcileFor is Reconcile for the actor's own proposal, returning its view.
func (b *Broker) reconcileFor(ctx context.Context, actor contract.Actor, id string) (Proposal, error) {
	p, err := b.attached()
	if err != nil {
		return Proposal{}, err
	}
	if err = p.current(ctx, actor); err != nil {
		return Proposal{}, err
	}
	if _, err = b.own(actor, id); err != nil {
		return Proposal{}, err
	}
	if _, err = b.Reconcile(ctx, id); err != nil {
		// A proposal that is not unresolved has nothing to look up; the stored
		// state is the answer.
		if r, e := b.own(actor, id); e == nil && !r.unresolved() {
			return b.view(r), nil
		}
		return Proposal{}, err
	}
	r, err := b.own(actor, id)
	if err != nil {
		return Proposal{}, err
	}
	return b.view(r), nil
}

// Recover settles what a crash left behind. Every proposal still executing was
// confirmed by a human and may have reached the addon, so each is resolved by
// receipt lookup and otherwise recorded as outcome-unknown. It never applies,
// and a pending proposal is left to expire by itself. Call it after Attach and
// before the owner serves requests.
func (b *Broker) Recover(ctx context.Context) error {
	if _, err := b.attached(); err != nil {
		return err
	}
	// Expiry that ran out while the owner was down is recorded first.
	_ = b.Sweep()
	for _, r := range b.store.all() {
		if r.State != gateway.ProposalExecuting {
			continue
		}
		if _, err := b.Reconcile(ctx, r.ID); err != nil {
			// Reconcile already recorded the proposal as outcome-unknown.
			continue
		}
	}
	for _, r := range b.store.all() {
		if r.State == gateway.ProposalExecuting {
			b.markUnknown(r, false, "recovered after restart")
		}
	}
	// A creation whose outcome was stored but whose reservation was not
	// settled before the crash is settled from its stored state.
	b.settleCreates()
	return nil
}

// maxResolveReason bounds the reason the resource owner states.
const maxResolveReason = 512

// lookupGrace is how long past the apply deadline the automatic receipt
// lookups go on. After that only an explicit lookup or resolution remains.
const lookupGrace = 5 * time.Minute

// abandonedLookupWindow bounds the automatic lookups for a late receipt of an
// abandoned outcome, counted from the acknowledgement.
const abandonedLookupWindow = 24 * time.Hour

// abandonedLookupCap is the longest wait between those lookups.
const abandonedLookupCap = 5 * time.Minute

// lookupWindow gives the time the automatic lookups of r stop, and the
// longest wait between two of them.
func (b *Broker) lookupWindow(r proposalRecord) (until time.Time, longest time.Duration) {
	if r.State == gateway.ProposalAbandoned {
		at := r.UpdatedAt
		if r.Resolution != nil {
			at = r.Resolution.At
		}
		return at.Add(abandonedLookupWindow), abandonedLookupCap
	}
	since := r.UnknownSince
	if since.IsZero() {
		since = r.UpdatedAt
	}
	return since.Add(b.opts.Limits.ApplyTimeout + lookupGrace), b.opts.Limits.BackoffMax
}

// RetryUnresolved looks the receipt up once for every unresolved or abandoned
// proposal that is due, and returns how many lookups it made. A lookup is
// read-only toward the addon: nothing is ever submitted again. Lookups for one
// proposal back off exponentially from the backoff limits and stop at the end
// of a finite window, so a missing addon cannot be asked forever.
func (b *Broker) RetryUnresolved(ctx context.Context) int {
	if _, err := b.attached(); err != nil {
		return 0
	}
	now := b.opts.Now()
	n := 0
	for _, r := range b.store.all() {
		if !r.unresolved() && r.State != gateway.ProposalAbandoned {
			continue
		}
		b.mu.Lock()
		_, live := b.live[r.ID]
		b.mu.Unlock()
		until, longest := b.lookupWindow(r)
		if live || !now.Before(until) || now.Before(r.NextLookup) || ctx.Err() != nil {
			continue
		}
		n++
		_, _ = b.Reconcile(ctx, r.ID)
		// Count the attempt and schedule the next one. A proposal that
		// settled meanwhile is left alone.
		wait := b.opts.Limits.BackoffInitial << min(r.Lookups+1, 16)
		if wait <= 0 || wait > longest {
			wait = longest
		}
		_, _ = b.store.transition(r.ID, []ProposalState{gateway.ProposalExecuting, gateway.ProposalOutcomeUnknown, gateway.ProposalAbandoned}, func(x *proposalRecord) {
			x.Lookups++
			x.NextLookup = now.Add(wait)
		})
	}
	return n
}

// Run keeps expiry recorded and repeats due receipt lookups until ctx ends or
// the broker is closed. It is the owner's background recovery.
func (b *Broker) Run(ctx context.Context) {
	ctx, leave, err := b.enter(ctx)
	if err != nil {
		return
	}
	defer leave()
	tick := b.tickC
	if tick == nil {
		t := time.NewTicker(b.opts.LookupInterval)
		defer t.Stop()
		tick = t.C
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-b.done:
			return
		case <-tick:
			_ = b.Sweep()
			b.RetryUnresolved(ctx)
		}
	}
}

// Resolve records that the resource owner stops waiting for an unresolved
// outcome. It is the explicit recovery path for an operation whose receipt
// cannot be found: the proposal becomes abandoned-unknown with the owner, the
// time and the stated reason, and no longer blocks writes. It never applies
// the operation. The operation stays resolvable by receipt: a later lookup
// that says it ran is recorded as applied.
//
// Only the resource's owner role may resolve, whoever proposed it, and only
// from a credential that stands for a human at a confirmation surface.
// Installation administrators have no access through this path. The owner is
// asserting that the outcome is not worth waiting for; the revision check of
// later writes protects data from a stale apply.
func (b *Broker) Resolve(ctx context.Context, actor contract.Actor, id, digest, reason string) (Proposal, error) {
	p, err := b.attached()
	if err != nil {
		return Proposal{}, err
	}
	if err = p.current(ctx, actor); err != nil {
		return Proposal{}, err
	}
	if !b.confirmCredential(p, actor) {
		return Proposal{}, ErrForbidden
	}
	reason = strings.TrimSpace(reason)
	if reason == "" || len(reason) > maxResolveReason || strings.IndexFunc(reason, unicode.IsControl) >= 0 {
		return Proposal{}, ErrForbidden
	}
	r, ok := b.store.get(id)
	if !ok || id == "" {
		return Proposal{}, ErrForbidden
	}
	if r.Action == createAction {
		return b.resolveCreate(ctx, p, actor, r, digest, reason)
	}
	key := ResourceKey{AddonID: r.AddonID, ResourceID: r.ResourceID}
	var out Proposal
	err = p.Admit(ctx, actor, key, ActionResolve, func(rctx context.Context) error {
		cur, ok := b.store.get(id)
		if !ok {
			return ErrForbidden
		}
		// The owner names what was shown. An empty digest is not a wildcard.
		if digestOf(&cur) != cur.Digest || digest == "" || digest != cur.Digest {
			return ErrProposalChanged
		}
		switch cur.State {
		case gateway.ProposalApplied, gateway.ProposalRejected, gateway.ProposalAbandoned:
		case gateway.ProposalExecuting, gateway.ProposalOutcomeUnknown:
			// One last lookup first: a final receipt settles it and there
			// is nothing to abandon.
			_, _ = b.Reconcile(rctx, id)
			cur, _ = b.store.get(id)
			if !cur.unresolved() {
				break
			}
			b.mu.Lock()
			_, live := b.live[id]
			b.mu.Unlock()
			if live {
				return ErrProposalClosed
			}
			at := b.opts.Now()
			moved, e := b.store.transition(id, []ProposalState{gateway.ProposalExecuting, gateway.ProposalOutcomeUnknown}, func(x *proposalRecord) {
				x.State = gateway.ProposalAbandoned
				x.Reason = "abandoned by the resource owner"
				x.Resolution = &gateway.ProposalResolution{ActorID: actor.UserID, At: at, Reason: reason}
			})
			if e != nil {
				return e
			}
			cur = moved
		default:
			return ErrProposalClosed
		}
		out = b.view(cur)
		return nil
	})
	return out, err
}

// Unresolved lists the proposals on a resource whose outcome is unresolved or
// abandoned, whoever proposed them. It is the owner's way to learn the ID and
// digest that Resolve needs: a proposal is otherwise readable by its proposer
// only. The caller must hold the resource's owner role and a credential that
// stands for a human at a confirmation surface, as for Resolve; installation
// administrators and keys are refused. It is read-only and shows no proposed
// arguments, only the bounded effect sample the proposer saw.
func (b *Broker) Unresolved(ctx context.Context, actor contract.Actor, resourceID string) ([]gateway.BlockingProposal, error) {
	p, err := b.attached()
	if err != nil {
		return nil, err
	}
	if err = p.current(ctx, actor); err != nil {
		return nil, err
	}
	if !b.confirmCredential(p, actor) {
		return nil, ErrForbidden
	}
	rec, ok := p.store.get(resourceID)
	if !ok || resourceID == "" {
		return nil, ErrForbidden
	}
	if err = p.check(actor, ResourceKey{AddonID: rec.AddonID, ResourceID: resourceID}, ActionResolve); err != nil {
		return nil, err
	}
	now := b.opts.Now()
	out := []gateway.BlockingProposal{}
	for _, r := range b.store.all() {
		if r.ResourceID != resourceID || r.AddonID != rec.AddonID || (!r.unresolved() && r.State != gateway.ProposalAbandoned) {
			continue
		}
		since := r.UnknownSince
		if since.IsZero() {
			since = r.UpdatedAt
		}
		age := int64(0)
		if !since.IsZero() && now.After(since) {
			age = int64(now.Sub(since) / time.Second)
		}
		out = append(out, gateway.BlockingProposal{ID: r.ID, AddonID: r.AddonID, ResourceID: r.ResourceID, State: r.State,
			ProposerID: r.Actor.UserID, Action: r.Action, Effects: r.Effects, ExpiresAt: r.ExpiresAt, CreatedAt: r.CreatedAt,
			Digest: r.Digest, AgeSeconds: age, Reason: r.Reason, Resolution: r.Resolution})
	}
	// Oldest first, so the owner sees what has waited longest.
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

// resolveCreate is Resolve for a create proposal. The resource is only a
// reservation of the proposer, so the proposer is the one who may stop
// waiting; there is no resource owner role yet. The reservation is dropped
// only when the addon confirms that its backing does not exist. When the addon
// says it exists, or cannot answer, the reservation stays pending: a late
// receipt or the startup reconciliation settles it, and it is never made
// reachable or dropped on a guess.
func (b *Broker) resolveCreate(ctx context.Context, p *Policy, actor contract.Actor, r proposalRecord, digest, reason string) (Proposal, error) {
	if r.Actor.UserID != actor.UserID || r.Actor.Mode != actor.Mode {
		return Proposal{}, ErrForbidden
	}
	key := ResourceKey{AddonID: r.AddonID, ResourceID: r.ResourceID}
	var out Proposal
	// The slot serializes the resolution with a confirmation of the same
	// reservation. Creation authority is not needed to stop waiting.
	g := p.gate(r.ResourceID)
	select {
	case g.writer <- struct{}{}:
		defer func() { <-g.writer }()
	case <-ctx.Done():
		return Proposal{}, ctx.Err()
	}
	cur, ok := b.store.get(r.ID)
	if !ok {
		return Proposal{}, ErrForbidden
	}
	if digestOf(&cur) != cur.Digest || digest == "" || digest != cur.Digest {
		return Proposal{}, ErrProposalChanged
	}
	switch cur.State {
	case gateway.ProposalApplied, gateway.ProposalRejected, gateway.ProposalAbandoned:
	case gateway.ProposalExecuting, gateway.ProposalOutcomeUnknown:
		_, _ = b.Reconcile(ctx, r.ID)
		cur, _ = b.store.get(r.ID)
		if !cur.unresolved() {
			break
		}
		b.mu.Lock()
		_, live := b.live[r.ID]
		b.mu.Unlock()
		if live {
			return Proposal{}, ErrProposalClosed
		}
		var found bool
		var askErr error = ErrUnavailable
		if b.opts.BackingExists != nil {
			found, askErr = b.opts.BackingExists(ctx, key)
		}
		at := b.opts.Now()
		moved, e := b.store.transition(r.ID, []ProposalState{gateway.ProposalExecuting, gateway.ProposalOutcomeUnknown}, func(x *proposalRecord) {
			x.State = gateway.ProposalAbandoned
			x.Reason = "abandoned by the proposer"
			x.Resolution = &gateway.ProposalResolution{ActorID: actor.UserID, At: at, Reason: reason}
		})
		if e != nil {
			return Proposal{}, e
		}
		cur = moved
		if askErr == nil && !found {
			_ = p.dropReservation(key)
		}
	default:
		return Proposal{}, ErrProposalClosed
	}
	b.settleCreates()
	out = b.view(cur)
	return out, nil
}
