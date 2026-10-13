// SPDX-License-Identifier: MIT
package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
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
	// ProposalOutcomeUnknown may or may not have run. A receipt lookup
	// resolves it; it is never submitted again.
	ProposalOutcomeUnknown ProposalState = "outcome-unknown"
	// ProposalAbandoned was an unresolved outcome that the resource owner
	// chose to stop waiting for. It no longer blocks writes, it is never
	// submitted again, and a later receipt lookup still records what the
	// addon reports.
	ProposalAbandoned ProposalState = "abandoned-unknown"
)

// ProposalResolution records who stopped waiting for an unresolved outcome.
type ProposalResolution struct {
	// ActorID is the resource owner who acknowledged the unknown outcome.
	ActorID string `json:"actorId"`
	// At is when the acknowledgement was recorded.
	At time.Time `json:"at"`
	// Reason is the owner's stated reason.
	Reason string `json:"reason"`
}

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
	// Resolution is present once the resource owner stopped waiting for an
	// unresolved outcome. It stays after a late receipt is recorded.
	Resolution *ProposalResolution `json:"resolution,omitempty"`
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
	// Resolve records that the resource owner stops waiting for an unresolved
	// outcome. It never applies anything. digest must equal the proposal's
	// digest; reason is required.
	Resolve(ctx context.Context, actor contract.Actor, id, digest, reason string) (Proposal, error)
}

// Proposal call operations on the carrier. They travel in ProposalCall, never
// as a typed dispatch action, so no dispatch can confirm a proposal.
const (
	proposalPropose   = "propose"
	proposalConfirm   = "confirm"
	proposalCancel    = "cancel"
	proposalGet       = "get"
	proposalReconcile = "reconcile"
	proposalResolve   = "resolve"
)

// ProposalCall is the body a frontend forwards to the owner.
type ProposalCall struct {
	Op     string `json:"op"`
	ID     string `json:"id,omitempty"`
	Digest string `json:"digest,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// wireAction is the action the carrier classifies a proposal call by. Only
// propose, confirm, cancel and resolve can change state; get and reconcile
// are read-only toward the addon. Anything else is a mutation, as always.
func (c ProposalCall) wireAction(request contract.Request) string {
	if c.Op == proposalPropose {
		return request.Action
	}
	return "proposal-" + c.Op
}

// ProposalUnavailable means a proposal call could not be served and the
// owner's state of that proposal was not changed past a state the client can
// read. The proposal ID is the original operation ID. A proposal call is a
// single-use state machine keyed by that ID, so the client reads the proposal
// (GET) and repeats the same call with the same ID; a repeat never creates a
// second operation. OutcomeUnknown is reserved for a proposal that reached
// the addon on the owner.
type ProposalUnavailable struct {
	ProposalID string `json:"operationId"`
}

func (e *ProposalUnavailable) Error() string {
	return "addon proposal call unavailable; read the proposal and repeat the same call"
}
func (e *ProposalUnavailable) Unwrap() error { return ErrUnavailable }

// stateChanging reports the proposal calls that can move a proposal. Get and
// reconcile only look, so their outages stay plain unavailability.
func (c ProposalCall) stateChanging() bool {
	return c.Op == proposalPropose || c.Op == proposalConfirm || c.Op == proposalCancel || c.Op == proposalResolve
}

// proposalOutcome classifies a failed proposal call by the owner's state, the
// same way for the owner's own frontend and for forwarding frontends. An
// unknown outcome passes through unchanged. Any other outage of a
// state-changing call is ProposalUnavailable: the owner reports
// OutcomeUnknown itself when the proposal reached the addon.
func proposalOutcome(call ProposalCall, id string, e error) error {
	var unknown *OutcomeUnknown
	var unavailable *ProposalUnavailable
	if e == nil || errors.As(e, &unknown) || errors.As(e, &unavailable) || !call.stateChanging() || StatusFor(e) != http.StatusServiceUnavailable {
		return e
	}
	return &ProposalUnavailable{ProposalID: id}
}

func (g *Gateway) proposal(ctx context.Context, a contract.Actor, request contract.Request, call ProposalCall) (Proposal, error) {
	if e := g.revalidate(ctx, a); e != nil {
		return Proposal{}, e
	}
	request.Action = call.wireAction(request)
	if g.Forward != nil {
		p, e := g.Forward.Proposal(ctx, a, request, call)
		return p, proposalOutcome(call, request.OperationID, e)
	}
	p, e := g.proposalLocal(ctx, a, request, call)
	return p, proposalOutcome(call, request.OperationID, e)
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
	case proposalResolve:
		return g.Proposals.Resolve(ctx, a, call.ID, call.Digest, call.Reason)
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

// ResolveProposal records that the resource owner stops waiting for an
// unresolved outcome. The owner accepts it only from a credential that stands
// for a human at a host-owned confirmation surface, and only from the
// resource's owner role. It never submits the operation again.
func (g *Gateway) ResolveProposal(ctx context.Context, a contract.Actor, id, digest, reason string) (Proposal, error) {
	return g.proposal(ctx, a, contract.Request{OperationID: id}, ProposalCall{Op: proposalResolve, ID: id, Digest: digest, Reason: reason})
}

// uncertainProposal is the answer when the reply of the owner was lost or not
// understood. Only a confirmation can have reached the addon, so only it is an
// unknown outcome; the other calls are repeated by the same ID after a read.
func uncertainProposal(call ProposalCall, id string) error {
	switch call.Op {
	case proposalConfirm:
		return &OutcomeUnknown{OperationID: id}
	case proposalPropose, proposalCancel, proposalResolve:
		return &ProposalUnavailable{ProposalID: id}
	}
	return ErrUnavailable
}

// proposalWire is the typed body of an owner's failed proposal call.
type proposalWire struct {
	Type        string `json:"type"`
	OperationID string `json:"operationId"`
}

const (
	wireOutcomeUnknown      = "outcome_unknown"
	wireProposalUnavailable = "proposal_unavailable"
)

// proposalFailure writes the owner's typed failure of a proposal call. An
// unknown outcome and an unavailable call are both 503 and are told apart by
// the body, so a forwarding frontend reports what the owner knows.
func proposalFailure(w http.ResponseWriter, call ProposalCall, id string, e error) {
	e = proposalOutcome(call, id, e)
	var unknown *OutcomeUnknown
	var unavailable *ProposalUnavailable
	body := proposalWire{OperationID: id}
	switch {
	case errors.As(e, &unknown):
		body.Type, body.OperationID = wireOutcomeUnknown, unknown.OperationID
	case errors.As(e, &unavailable):
		body.Type, body.OperationID = wireProposalUnavailable, unavailable.ProposalID
	default:
		typedError(w, e)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusServiceUnavailable)
	_ = json.NewEncoder(w).Encode(body)
}

// proposalReplyError reads the typed failure of an owner's proposal call.
func proposalReplyError(r io.Reader, call ProposalCall, id string) error {
	var b proposalWire
	if decode(io.LimitReader(r, 4096), &b) == nil && b.OperationID == id {
		switch b.Type {
		case wireOutcomeUnknown:
			return &OutcomeUnknown{OperationID: id}
		case wireProposalUnavailable:
			return &ProposalUnavailable{ProposalID: id}
		}
	}
	return uncertainProposal(call, id)
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
		return Proposal{}, uncertainProposal(call, r.OperationID)
	}
	return p, nil
}
