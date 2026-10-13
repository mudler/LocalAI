// SPDX-License-Identifier: MIT
package policy

import (
	"context"

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
	case gateway.ProposalExecuting, gateway.ProposalOutcomeUnknown:
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
	receipt, err := instance.Client.Receipt(ctx, operationID)
	if err != nil {
		b.markUnknown(r, live, "receipt lookup failed")
		if ctx.Err() != nil {
			return contract.Receipt{}, ctx.Err()
		}
		return contract.Receipt{}, ErrUnavailable
	}
	switch receipt.State {
	case contract.ReceiptCommitted:
		b.settle(operationID, gateway.ProposalApplied, &receipt, "resolved by receipt")
	case contract.ReceiptRejected:
		b.settle(operationID, gateway.ProposalRejected, &receipt, "resolved by receipt")
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
	return nil
}
