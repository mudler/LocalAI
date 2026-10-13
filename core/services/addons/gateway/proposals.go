// SPDX-License-Identifier: MIT
package gateway

import (
	"context"
	"encoding/json"
	"io"
	"time"

	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// ProposalState is the lifecycle state of a stored proposal. Every transition
// is persisted by the owner before it is reported.
type ProposalState string

const (
	// ProposalPending waits for a human decision.
	ProposalPending ProposalState = "pending"
	// ProposalCancelled was withdrawn by its actor or invalidated by a
	// revocation. It never executes.
	ProposalCancelled ProposalState = "cancelled"
	// ProposalExpired reached its expiry before it was confirmed.
	ProposalExpired ProposalState = "expired"
	// ProposalExecuting was confirmed and handed to the addon runtime.
	ProposalExecuting ProposalState = "executing"
	// ProposalApplied is committed; the receipt is attached.
	ProposalApplied ProposalState = "applied"
	// ProposalRejected was refused by the runtime or found stale.
	ProposalRejected ProposalState = "rejected"
	// ProposalOutcomeUnknown may or may not have run. Only a receipt lookup
	// resolves it; it is never submitted again.
	ProposalOutcomeUnknown ProposalState = "outcome-unknown"
)

// ProposalEffects is what the confirming human is shown: the exact number of
// effects and a bounded sample of them.
type ProposalEffects struct {
	// Count is the exact number of effects the operation would have.
	Count uint64 `json:"count"`
	// Sample is a bounded excerpt of the effects, or the whole preview summary
	// when the addon did not separate one.
	Sample json.RawMessage `json:"sample,omitempty" swaggertype:"object"`
}

// Proposal is the public view of a stored proposal. It never carries the
// credential that proposed it.
type Proposal struct {
	// ID is the proposal identifier. It is the operation identifier of the
	// stored operation, so a lost reply is always resolved by the same ID.
	ID          string `json:"id"`
	OperationID string `json:"operationId"`
	AddonID     string `json:"addonId"`
	ResourceID  string `json:"resourceId"`
	Generation  uint64 `json:"generation"`
	Action      string `json:"action"`
	// Arguments are the canonical stored arguments. They are the only
	// arguments that can ever be submitted for this proposal.
	Arguments json.RawMessage `json:"arguments" swaggertype:"object"`
	// Revision is the schema and data revision the preview saw. The runtime
	// refuses the apply when it moved.
	Revision contract.Revision `json:"revision"`
	// Digest binds a confirmation to exactly what was shown.
	Digest    string          `json:"digest"`
	Effects   ProposalEffects `json:"effects"`
	State     ProposalState   `json:"state"`
	ExpiresAt time.Time       `json:"expiresAt"`
	CreatedAt time.Time       `json:"createdAt"`
	// Receipt is present once the runtime answered.
	Receipt *contract.Receipt `json:"receipt,omitempty"`
	// Reason says why a proposal is cancelled, expired or rejected.
	Reason string `json:"reason,omitempty"`
	// BlockedBy is the operation whose unresolved outcome blocks this
	// proposal. Resolve that operation first.
	BlockedBy string `json:"blockedBy,omitempty"`
}

// ProposalBroker is the owner-held proposal state machine. Only the designated
// owner implements it; other frontends reach it through the carrier.
type ProposalBroker interface {
	Propose(ctx context.Context, actor contract.Actor, request contract.Request) (Proposal, error)
	// Confirm applies a pending proposal. digest must equal the proposal's
	// digest; it is what the human saw.
	Confirm(ctx context.Context, actor contract.Actor, id, digest string) (Proposal, error)
	Cancel(ctx context.Context, actor contract.Actor, id string) (Proposal, error)
	Get(ctx context.Context, actor contract.Actor, id string) (Proposal, error)
	// Reconcile looks the operation's receipt up. It never applies anything.
	Reconcile(ctx context.Context, actor contract.Actor, id string) (Proposal, error)
}

// Proposal call operations on the carrier. They travel in ProposalCall, never
// as a typed dispatch action, so no dispatch can confirm a proposal.
const (
	proposalPropose   = "propose"
	proposalConfirm   = "confirm"
	proposalCancel    = "cancel"
	proposalGet       = "get"
	proposalReconcile = "reconcile"
)

// ProposalCall is the body a frontend forwards to the owner.
type ProposalCall struct {
	Op     string `json:"op"`
	ID     string `json:"id,omitempty"`
	Digest string `json:"digest,omitempty"`
}

// wireAction is the action the carrier classifies a proposal call by. Only
// propose, confirm and cancel can change state; get and reconcile are
// read-only toward the addon. Anything else is a mutation, as always.
func (c ProposalCall) wireAction(request contract.Request) string {
	if c.Op == proposalPropose {
		return request.Action
	}
	return "proposal-" + c.Op
}

func (g *Gateway) proposal(ctx context.Context, a contract.Actor, request contract.Request, call ProposalCall) (Proposal, error) {
	if e := g.revalidate(ctx, a); e != nil {
		return Proposal{}, e
	}
	request.Action = call.wireAction(request)
	if g.Forward != nil {
		return g.Forward.Proposal(ctx, a, request, call)
	}
	p, e := g.proposalLocal(ctx, a, request, call)
	return p, mutationOutcome(request, e)
}

// proposalLocal serves a proposal call on the owner itself.
func (g *Gateway) proposalLocal(ctx context.Context, a contract.Actor, request contract.Request, call ProposalCall) (Proposal, error) {
	if g.Proposals == nil {
		return Proposal{}, ErrUnavailable
	}
	if call.Op != proposalPropose && request.OperationID != call.ID {
		return Proposal{}, ErrForbidden
	}
	switch call.Op {
	case proposalPropose:
		return g.Proposals.Propose(ctx, a, request)
	case proposalConfirm:
		return g.Proposals.Confirm(ctx, a, call.ID, call.Digest)
	case proposalCancel:
		return g.Proposals.Cancel(ctx, a, call.ID)
	case proposalGet:
		return g.Proposals.Get(ctx, a, call.ID)
	case proposalReconcile:
		return g.Proposals.Reconcile(ctx, a, call.ID)
	}
	return Proposal{}, ErrForbidden
}

// Propose stores a proposal for a data or schema mutation. It never executes.
func (g *Gateway) Propose(ctx context.Context, a contract.Actor, request contract.Request) (Proposal, error) {
	return g.proposal(ctx, a, request, ProposalCall{Op: proposalPropose, ID: request.OperationID})
}

// ConfirmProposal applies a pending proposal. The owner accepts it only from a
// credential that stands for a human at a host-owned confirmation surface.
func (g *Gateway) ConfirmProposal(ctx context.Context, a contract.Actor, id, digest string) (Proposal, error) {
	return g.proposal(ctx, a, contract.Request{OperationID: id}, ProposalCall{Op: proposalConfirm, ID: id, Digest: digest})
}

// CancelProposal withdraws a pending proposal.
func (g *Gateway) CancelProposal(ctx context.Context, a contract.Actor, id string) (Proposal, error) {
	return g.proposal(ctx, a, contract.Request{OperationID: id}, ProposalCall{Op: proposalCancel, ID: id})
}

// GetProposal reads a stored proposal of the actor.
func (g *Gateway) GetProposal(ctx context.Context, a contract.Actor, id string) (Proposal, error) {
	return g.proposal(ctx, a, contract.Request{OperationID: id}, ProposalCall{Op: proposalGet, ID: id})
}

// ReconcileProposal resolves an uncertain outcome by receipt lookup. It is
// read-only toward the addon and never submits the operation again.
func (g *Gateway) ReconcileProposal(ctx context.Context, a contract.Actor, id string) (Proposal, error) {
	return g.proposal(ctx, a, contract.Request{OperationID: id}, ProposalCall{Op: proposalReconcile, ID: id})
}

// Proposal forwards a proposal call to the owner.
func (c *Carrier) Proposal(ctx context.Context, a contract.Actor, r contract.Request, call ProposalCall) (Proposal, error) {
	resp, e := c.send(ctx, a, wireRequest{Request: r, Proposal: &call})
	if e != nil {
		return Proposal{}, e
	}
	defer resp.Body.Close()
	var p Proposal
	if e = decode(io.LimitReader(resp.Body, 2<<20), &p); e != nil {
		if mutation(r.Action) {
			return Proposal{}, &OutcomeUnknown{OperationID: r.OperationID}
		}
		return Proposal{}, ErrUnavailable
	}
	return p, nil
}
