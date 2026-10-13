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

// BlockingProposal is what the owner role of a resource sees of an unresolved
// proposal on it, whoever proposed it. It names the operation and what is
// needed to resolve it. It carries neither the proposed arguments nor the
// proposer's credential; the effect sample is the bounded one the proposer's
// own view carries.
type BlockingProposal struct {
	// ID is the proposal identifier, which is its operation identifier.
	ID         string `json:"id"`
	AddonID    string `json:"addonId"`
	ResourceID string `json:"resourceId"`
	// State is executing, outcome-unknown or abandoned-unknown.
	State ProposalState `json:"state"`
	// ProposerID is the user who proposed the operation.
	ProposerID string          `json:"proposerId"`
	Action     string          `json:"action"`
	Effects    ProposalEffects `json:"effects"`
	ExpiresAt  time.Time       `json:"expiresAt"`
	CreatedAt  time.Time       `json:"createdAt"`
	// Digest is what the owner names to resolve the proposal.
	Digest string `json:"digest"`
	// AgeSeconds is how long the outcome has been unresolved, counted from
	// the hand-over to the addon.
	AgeSeconds int64 `json:"ageSeconds"`
	// Reason says why the outcome is unresolved.
	Reason string `json:"reason,omitempty"`
	// Resolution is present once the owner stopped waiting.
	Resolution *ProposalResolution `json:"resolution,omitempty"`
}

// ProposalList is the answer to the owner's list of unresolved proposals.
type ProposalList struct {
	Proposals []BlockingProposal `json:"proposals"`
}

// OutcomeBlocked is the denial of a write on a resource that has an
// unresolved outcome. It names the blocking operation, and the owner builds
// it only for a caller that holds the owner role of the resource: others get
// the plain denial and never learn another user's operation identifier.
type OutcomeBlocked struct {
	// BlockedBy is the identifier of the unresolved operation.
	BlockedBy string
	// Cause is the plain denial, matched by errors.Is.
	Cause error
}

func (e *OutcomeBlocked) Error() string {
	if e.Cause != nil {
		return e.Cause.Error()
	}
	return "addon operation outcome unresolved"
}

// Unwrap lets errors.Is match the denial and its status classification.
func (e *OutcomeBlocked) Unwrap() error {
	if e.Cause != nil {
		return e.Cause
	}
	return ErrForbidden
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
	// Unresolved lists the proposals on a resource whose outcome is unresolved,
	// whoever proposed them. Only the resource's owner role may call it.
	Unresolved(ctx context.Context, actor contract.Actor, resourceID string) ([]BlockingProposal, error)
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
	// proposalList lists the unresolved proposals of a resource for its owner
	// role. It is read-only. The call's ID is the resource identifier.
	proposalList = "list"
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

// ProposalUnavailable means a proposal call could not be served by the owner
// and that this call sent nothing to the addon. The proposal is in the state
// the owner recorded, which a read shows: pending, or already settled by an
// earlier call. The proposal ID is the original operation ID. A proposal call
// is a single-use state machine keyed by that ID, so the client reads the
// proposal (GET) and repeats the same call with the same ID; a repeat never
// creates a second operation. OutcomeUnknown is reserved for a proposal that
// reached the addon on the owner.
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

// UnresolvedProposals lists the proposals on a resource whose outcome is
// unresolved, whoever proposed them. The owner answers only the resource's
// owner role, from a credential that stands for a human at a confirmation
// surface. It is read-only and shows no proposed arguments.
func (g *Gateway) UnresolvedProposals(ctx context.Context, a contract.Actor, resourceID string) (ProposalList, error) {
	if e := g.revalidate(ctx, a); e != nil {
		return ProposalList{}, e
	}
	request := contract.Request{OperationID: resourceID, ResourceID: resourceID}
	call := ProposalCall{Op: proposalList, ID: resourceID}
	request.Action = call.wireAction(request)
	if g.Forward != nil {
		return g.Forward.proposalList(ctx, a, request, call)
	}
	return g.listLocal(ctx, a, request, call)
}

// listLocal serves the list call on the owner itself.
func (g *Gateway) listLocal(ctx context.Context, a contract.Actor, request contract.Request, call ProposalCall) (ProposalList, error) {
	if g.Proposals == nil {
		return ProposalList{}, ErrUnavailable
	}
	if call.Op != proposalList || call.ID == "" || request.OperationID != call.ID || request.ResourceID != call.ID {
		return ProposalList{}, ErrForbidden
	}
	list, e := g.Proposals.Unresolved(ctx, a, call.ID)
	if e != nil {
		return ProposalList{}, e
	}
	if list == nil {
		list = []BlockingProposal{}
	}
	return ProposalList{Proposals: list}, nil
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
	BlockedBy   string `json:"blockedBy,omitempty"`
}

const (
	wireOutcomeUnknown      = "outcome_unknown"
	wireProposalUnavailable = "proposal_unavailable"
	// wireOutcomeBlocked is a 403 that names the blocking operation. The owner
	// sends it only for the resource's owner role.
	wireOutcomeBlocked = "outcome_blocked"
)

// proposalFailure writes the owner's typed failure of a proposal call. An
// unknown outcome and an unavailable call are both 503 and are told apart by
// the body, so a forwarding frontend reports what the owner knows.
func proposalFailure(w http.ResponseWriter, call ProposalCall, id string, e error) {
	e = proposalOutcome(call, id, e)
	var unknown *OutcomeUnknown
	var unavailable *ProposalUnavailable
	var blocked *OutcomeBlocked
	body := proposalWire{OperationID: id}
	if errors.As(e, &blocked) && blocked.BlockedBy != "" && StatusFor(e) == http.StatusForbidden {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(proposalWire{Type: wireOutcomeBlocked, OperationID: id, BlockedBy: blocked.BlockedBy})
		return
	}
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

// proposalDenied reads a 403 of an owner's proposal call. A denial is a
// denial: it stays one when its body is not understood, and only a typed body
// adds the blocking operation.
func proposalDenied(r io.Reader, id string) error {
	var b proposalWire
	if decode(io.LimitReader(r, 4096), &b) == nil && b.OperationID == id && b.Type == wireOutcomeBlocked && b.BlockedBy != "" && len(b.BlockedBy) <= 256 {
		return &OutcomeBlocked{BlockedBy: b.BlockedBy, Cause: ErrForbidden}
	}
	return ErrForbidden
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

// proposalList forwards the list call to the owner.
func (c *Carrier) proposalList(ctx context.Context, a contract.Actor, r contract.Request, call ProposalCall) (ProposalList, error) {
	resp, e := c.send(ctx, a, wireRequest{Request: r, Proposal: &call})
	if e != nil {
		return ProposalList{}, e
	}
	defer resp.Body.Close()
	var l ProposalList
	if e = decode(io.LimitReader(resp.Body, 2<<20), &l); e != nil {
		return ProposalList{}, ErrUnavailable
	}
	return l, nil
}
