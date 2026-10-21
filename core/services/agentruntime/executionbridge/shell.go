// SPDX-License-Identifier: MIT
package executionbridge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/agentruntime/nibadapter"
	"github.com/mudler/LocalAI/core/services/agentruntime/ownership"
	"github.com/mudler/LocalAI/core/services/agentruntime/toolbroker"
	chat "github.com/mudler/nib/chat"
)

func absent(v any) bool {
	if v == nil {
		return true
	}
	r := reflect.ValueOf(v)
	switch r.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Func, reflect.Map, reflect.Slice:
		return r.IsNil()
	}
	return false
}
func New(scope Scope, dep Dependencies) (*Bridge, error) {
	id := scope.Identity
	if !identifier(id.Principal) || !identifier(id.Conversation) || !identifier(id.Turn) || !number(id.Attempt) || !number(id.Epoch) || !number(scope.Revision) || scope.Lease.Principal != id.Principal || scope.Lease.Conversation != id.Conversation || scope.Lease.Epoch != id.Epoch || dep.Clock == nil || absent(dep.Reader) || absent(dep.Manager) || dep.Resolve == nil || dep.Helpers == nil || dep.Approvals == nil || dep.Audit == nil || dep.ProjectText == nil || dep.Release == nil || dep.AuthorizeOrigin == nil || dep.Waiting == nil {
		return nil, ErrHold
	}
	b := &Bridge{scope: scope, dep: dep, active: map[*controlSlot]struct{}{}}
	broker, err := toolbroker.NewBroker(toolbroker.Dependencies{Clock: dep.Clock, Reader: dep.Reader, Journal: gatedJournal{dep.Manager, scope}, Current: b.current, Helpers: dep.Helpers, Approvals: dep.Approvals, Audit: dep.Audit, Sanitize: b.sanitize}, dep.Catalog)
	if err != nil {
		return nil, ErrHold
	}
	b.broker = broker
	return b, nil
}
func (b *Bridge) live() bool { b.mu.Lock(); defer b.mu.Unlock(); return !b.closed }
func (b *Bridge) scopeBinding() toolbroker.Scope {
	id := b.scope.Identity
	return toolbroker.Scope{Principal: id.Principal, Conversation: id.Conversation, Turn: id.Turn, Attempt: id.Attempt, Epoch: id.Epoch}
}
func candidate(inv, protocol, agent, name string, args []byte) (HostCandidate, error) {
	if !identifier(inv) || !identifier(protocol) || !identifier(name) || (agent != "" && !identifier(agent)) || !json.Valid(args) {
		return HostCandidate{}, ErrHold
	}
	f := candidateFields{strings.Clone(inv), strings.Clone(protocol), strings.Clone(agent), strings.Clone(name), string(args)}
	return HostCandidate{open: func() candidateFields { return f }}, nil
}
func (b *Bridge) resolve(ctx context.Context, c HostCandidate) (Registration, error) {
	if !b.live() || ctx.Err() != nil {
		return Registration{}, ErrHold
	}
	r, err := b.dep.Resolve(ctx, b.scope, c)
	if err != nil || r.Authority.Scope != b.scopeBinding() || r.Authority.Lease != b.scope.Lease || r.Authority.Selection.Scope != b.scopeBinding() || !identifier(r.Helper) || !identifier(r.Registration) || !identifier(r.Agent) || r.Tool == "" || !codecDigest(r.Provenance) || !codecDigest(r.Projection) || r.ApprovalTTL <= 0 || r.ApprovalTTL > ownership.MaxTTL || !number(r.AuthRevision) || !number(r.ConfigRevision) || r.Authority.Policy > toolbroker.AllowAll {
		return Registration{}, ErrHold
	}
	// Resolve is trusted, but availability still uses the same catalog and
	// parent/helper intersection at policy and final execution boundaries.
	selection := r.Authority.Selection
	selection.Helper = nil
	granted := r.Authority.HelperID == ""
	if r.Authority.HelperID == "" && r.Authority.Selection.Helper != nil {
		return Registration{}, ErrHold
	}
	if r.Authority.HelperID != "" {
		g, e := b.dep.Helpers(ctx, b.scopeBinding(), r.Authority.HelperID)
		if e != nil || g.Scope != b.scopeBinding() {
			return Registration{}, ErrHold
		}
		g.IDs = append([]toolbroker.ToolID(nil), g.IDs...)
		selection.Helper = &g
		granted = true
	}
	selected := false
	for _, id := range b.dep.Catalog.Effective(selection) {
		if id == r.Tool {
			selected = true
		}
	}
	if !granted || !selected {
		return Registration{}, ErrHold
	}
	snap, err := b.dep.Reader.Get(ctx, b.scope.Identity.Principal, b.scope.Identity.Conversation)
	if err != nil || snap.Deleted || snap.Revision != b.scope.Revision || snap.Running == nil || snap.Running.InputID != b.scope.Identity.Turn || b.dep.Manager.Check(ctx, b.scope.Lease, b.dep.Clock()) != nil {
		return Registration{}, ErrHold
	}
	if ctx.Err() != nil || !b.live() {
		return Registration{}, ErrHold
	}
	return r, nil
}
func (b *Bridge) Policy() nibadapter.Policy {
	return func(id nibadapter.Identity, req chat.ToolCallRequest) error {
		if id != b.scope.Identity {
			return ErrHold
		}
		c, err := candidate(req.InvocationID, req.ID, req.AgentID, req.Name, []byte(req.Arguments))
		if err != nil {
			return ErrHold
		}
		_, err = b.resolve(context.Background(), c)
		if err != nil {
			return ErrHold
		}
		return nil
	}
}
func (b *Bridge) current(ctx context.Context, binding toolbroker.Binding) (toolbroker.Authority, error) {
	op, err := operationFrom(ctx)
	if err != nil || op.candidate.binding != binding {
		return toolbroker.Authority{}, ErrHold
	}
	r, err := b.resolve(ctx, op.candidate.candidate)
	if err != nil || !sameRegistration(r, op.candidate.registration) {
		return toolbroker.Authority{}, ErrHold
	}
	return r.Authority, nil
}
func sameRegistration(a, b Registration) bool {
	return a.Tool == b.Tool && a.Helper == b.Helper && a.Registration == b.Registration && a.Agent == b.Agent && a.Provenance == b.Provenance && a.Projection == b.Projection && a.ApprovalTTL == b.ApprovalTTL && a.AuthRevision == b.AuthRevision && a.ConfigRevision == b.ConfigRevision && a.Authority.Scope == b.Authority.Scope && a.Authority.Lease == b.Authority.Lease && a.Authority.Revisions == b.Authority.Revisions && a.Authority.Policy == b.Authority.Policy && a.Authority.HelperID == b.Authority.HelperID && a.Authority.ExternalTaintedWrite == b.Authority.ExternalTaintedWrite
}
func (b *Bridge) prepare(ctx context.Context, c HostCandidate) (executionCandidate, error) {
	r, err := b.resolve(ctx, c)
	if err != nil {
		return executionCandidate{}, ErrHold
	}
	id := b.scope.Identity
	f := callFields{Principal: id.Principal, Conversation: id.Conversation, Turn: id.Turn, Attempt: id.Attempt, Epoch: id.Epoch, Invocation: c.InvocationID(), Protocol: c.ProtocolID(), Helper: r.Helper, Tool: string(r.Tool), Revision: b.scope.Revision, AuthRevision: r.AuthRevision, ConfigRevision: r.ConfigRevision, Codec: 1, TTL: uint64(r.ApprovalTTL), Mode: uint8(r.Authority.Policy), Tainted: r.Authority.ExternalTaintedWrite}
	req, err := RequestID(ownCall(f))
	if err != nil {
		return executionCandidate{}, ErrHold
	}
	binding, err := toolbroker.NewBinding(b.scopeBinding(), req, r.Tool, c.Arguments(), r.Authority.Revisions, req)
	if err != nil {
		return executionCandidate{}, ErrHold
	}
	f.Binding = binding.Digest()
	p, _ := hex.DecodeString(r.Provenance)
	copy(f.Provenance[:], p)
	p, _ = hex.DecodeString(r.Projection)
	copy(f.Projection[:], p)
	return executionCandidate{candidate: c, registration: r, binding: binding, call: ownCall(f)}, nil
}

// shellEvidence is private, ephemeral transition bookkeeping, not a bearer
// capability or a persisted claim. Facts certify completed host guards/effects;
// current authority is rebuilt for every reducer guard, never cached here.
type shellEvidence struct {
	normal, projected, released, completed, originAuthorized, exact bool
	origin                                                          Call
}

func (b *Bridge) facts(ctx context.Context, c executionCandidate, e shellEvidence) Facts {
	f := Facts{HelperScope: c.call, RegisteredHelper: c.registration.Helper,
		NeedsApproval: c.registration.Authority.Policy == toolbroker.RequireApproval || c.registration.Authority.ExternalTaintedWrite,
		Durable:       c.admission.Digest() != "" && c.admission.RequestID() == c.binding.RequestID(), Normal: e.normal, Projected: e.projected,
		ReleaseAuthorized: e.released, OriginCompleted: e.completed,
		OriginAuthorized: e.originAuthorized, ExactOrigin: e.exact, Origin: e.origin}
	if b.publicationCurrent(ctx, c) == nil {
		f.Lease = Live
		f.RevisionCurrent = true
		f.TurnCurrent = true
		f.Registered = true
		f.Authorized = true
	}
	return f
}

// Dependencies are finite callbacks, not an atomic external ACL transaction.
// Recheck actual current dependencies after Release/origin callbacks. No bridge
// mutex is held across them; cancellation/closure are sampled again at the end.
func (b *Bridge) publicationCurrent(ctx context.Context, c executionCandidate) error {
	if ctx == nil || ctx.Err() != nil || !b.live() {
		return ErrHold
	}
	r, err := b.resolve(ctx, c.candidate)
	if err != nil || !sameRegistration(r, c.registration) || ctx.Err() != nil || !b.live() {
		return ErrHold
	}
	return nil
}
func advance(s *State, k EventKind, c executionCandidate, f Facts, want Effect) error {
	e := NewEvent(k, c.call, f)
	t := Reduce(*s, e)
	if Invariant(t.State) != nil || CheckTransition(*s, e, t) != nil {
		return ErrHold
	}
	if want != 0 {
		found := false
		for _, fx := range t.Effects {
			if fx == want {
				found = true
			}
		}
		if !found {
			return ErrHold
		}
	}
	*s = t.State
	return nil
}
func (b *Bridge) admit(ctx context.Context, c *executionCandidate) error {
	r := c.registration
	v := r.Authority.Revisions
	a := sha256.Sum256(c.candidate.Arguments())
	d := c.binding.Digest()
	m, err := ownership.NewExecutionMetadata(b.scope.Identity.Turn, c.candidate.InvocationID(), c.candidate.ProtocolID(), r.Helper, r.Registration, r.Agent, string(r.Tool), b.scope.Identity.Attempt, b.scope.Revision, [7]string{v.Catalog, v.Config, v.Authorization, v.Resource, v.Proposal, v.ParentGrant, v.Taint}, uint8(r.Authority.Policy), r.ApprovalTTL, r.Authority.ExternalTaintedWrite, 1, hex.EncodeToString(a[:]), hex.EncodeToString(d[:]), r.Provenance)
	if err != nil {
		return ErrHold
	}
	c.admission, err = b.dep.Manager.AdmitExecution(ctx, b.scope.Lease, b.dep.Clock(), m)
	if err != nil || c.admission.RequestID() != c.binding.RequestID() {
		return ErrHold
	}
	return nil
}
func (b *Bridge) Execution() chat.ToolExecutionBridge {
	return func(ctx context.Context, native chat.ToolExecutionCall, body chat.ToolExecutionBody) (chat.ToolExecutionResult, error) {
		hold := func() (chat.ToolExecutionResult, error) { return chat.ToolExecutionResult{}, ErrHold }
		if body == nil || ctx == nil {
			return hold()
		}
		args, err := json.Marshal(native.Arguments())
		if err != nil {
			return hold()
		}
		cand, err := candidate(native.InvocationID(), native.ToolCallID(), native.AgentID(), native.Name(), args)
		if err != nil {
			return hold()
		}
		c, err := b.prepare(ctx, cand)
		if err != nil {
			return hold()
		}
		var state State = Unmapped{}
		// HB03 is certified only after fenced persistence succeeds. Its effect
		// names the operation just witnessed, not a second admission request.
		evidence := shellEvidence{}
		if b.admit(ctx, &c) != nil || advance(&state, Admit, c, b.facts(ctx, c, evidence), PersistAdmission) != nil {
			return hold()
		}
		replayctx := withOperation(ctx, c, true)
		if advance(&state, Begin, c, b.facts(ctx, c, evidence), BeginBroker) != nil {
			return hold()
		}
		h, view, err := b.broker.Begin(replayctx, toolbroker.BeginRequest{Config: toolbroker.ModelConfig{Binding: c.binding, Policy: c.registration.Authority.Policy, ApprovalTTL: int64(c.registration.ApprovalTTL), ExternalTaintedWrite: c.registration.Authority.ExternalTaintedWrite}})
		if err != nil {
			return hold()
		}
		slot := &controlSlot{bridge: b, handle: h, binding: c.binding, candidate: c, wake: make(chan struct{}, 1)}
		b.mu.Lock()
		if b.closed {
			b.mu.Unlock()
			_ = b.broker.Cancel(replayctx, h, toolbroker.CancelUser)
			return hold()
		}
		b.active[slot] = struct{}{}
		b.mu.Unlock()
		defer b.closeSlot(slot)
		defer func() { _ = b.broker.Cancel(replayctx, h, toolbroker.CancelUser) }()
		if view.Phase == "WaitingApproval" {
			if b.dep.Waiting(replayctx, Control{open: func() *controlSlot { return slot }}) != nil {
				return hold()
			}
			ticker := time.NewTicker(10 * time.Millisecond)
			defer ticker.Stop()
			for view.Phase == "WaitingApproval" {
				select {
				case <-ctx.Done():
					return hold()
				case <-slot.wake:
				case <-ticker.C:
				}
				if !b.live() {
					return hold()
				}
				_ = b.broker.Expire(replayctx, h)
				view, err = b.broker.View(h)
				if err != nil {
					return hold()
				}
			}
			if advance(&state, Approve, c, b.facts(ctx, c, evidence), 0) != nil {
				return hold()
			}
		}
		if view.Phase != "Authorized" {
			return hold()
		}
		if advance(&state, Claim, c, b.facts(ctx, c, evidence), ClaimCall) != nil {
			return hold()
		}
		claim, err := b.broker.ClaimCall(replayctx, h)
		if errors.Is(err, toolbroker.ErrRecorded) {
			view, e := b.broker.View(h)
			if e != nil || view.Phase != "Completed" {
				return hold()
			}
			p, e := b.replay(replayctx, c, view.Result.Bytes())
			if e != nil {
				return hold()
			}
			evidence = shellEvidence{projected: true, released: true, completed: true, originAuthorized: true, exact: true, origin: c.call}
			if advance(&state, Replay, c, b.facts(ctx, c, evidence), ReleaseReplay) != nil {
				return hold()
			}
			return chat.ToolExecutionResult{Text: p.Text()}, nil
		}
		if err != nil || advance(&state, Dispatch, c, b.facts(ctx, c, evidence), Execute) != nil {
			return hold()
		}
		out, err := b.broker.Invoke(withOperation(ctx, c, false), claim, func(runctx context.Context, e toolbroker.Execution) (toolbroker.ToolOutput, error) {
			if e.Binding() != c.binding {
				return toolbroker.ToolOutput{}, ErrHold
			}
			result, err := body(runctx)
			if err != nil {
				return toolbroker.ToolOutput{}, ErrHold
			}
			evidence.normal = true
			text, err := normalizeNativeText(result)
			if err != nil {
				return toolbroker.ToolOutput{}, ErrHold
			}
			return toolbroker.NewOutput([]byte(text)), nil
		})
		if err != nil || out.Status != "Completed" || ctx.Err() != nil || !b.live() {
			return hold()
		}
		if _, e := b.current(withOperation(ctx, c, false), c.binding); e != nil {
			return hold()
		}
		p, err := b.replay(ctx, c, out.Result.Bytes())
		evidence.projected = err == nil
		evidence.released = err == nil
		evidence.completed = out.Status == "Completed"
		if err != nil || !evidence.completed || advance(&state, Finish, c, b.facts(ctx, c, evidence), Publish) != nil {
			return hold()
		}
		return chat.ToolExecutionResult{Text: p.Text()}, nil
	}
}

// Origin is an explicit host provenance reference, never inferred from a native
// protocol/name/arguments match. It owns the exact original codec expectations.
type Origin struct {
	redacted
	open func() (ownership.ExecutionAdmission, Call, string, string)
}

// NewOrigin accepts trusted stored host provenance. The reference is not a grant;
// current target, origin relation and release authorization are checked on read.
func NewOrigin(ref ownership.ExecutionAdmission, call Call, provenance, projection string) (Origin, error) {
	id, err := RequestID(call)
	d := call.fields().Binding
	if err != nil || !call.valid() || id != ref.RequestID() || ref.Call().ArgumentsHash != hex.EncodeToString(d[:]) || !codecDigest(ref.Digest()) || !codecDigest(provenance) || !codecDigest(projection) {
		return Origin{}, ErrHold
	}
	return Origin{open: func() (ownership.ExecutionAdmission, Call, string, string) {
		return ref, call, strings.Clone(provenance), strings.Clone(projection)
	}}, nil
}

// Recover never calls Begin, claims or invokes a body. The database fences the
// current target separately from immutable original admission provenance.
func (b *Bridge) Recover(ctx context.Context, target HostCandidate, origin Origin) (string, error) {
	if ctx == nil || origin.open == nil {
		return "", ErrHold
	}
	ref, originCall, provenance, projection := origin.open()
	c, err := b.prepare(ctx, target)
	if err != nil || b.dep.AuthorizeOrigin(ctx, target, ref) != nil {
		return "", ErrHold
	}
	raw, err := b.dep.Manager.ReadCompletedExecution(ctx, b.scope.Lease, b.dep.Clock(), b.scope.Identity.Turn, b.scope.Revision, ref)
	if err != nil {
		return "", ErrHold
	}
	p, err := decodeTextEnvelope(raw.Bytes(), ref.Digest(), provenance, projection)
	if err != nil || b.dep.Release(ctx, target, ref) != nil {
		return "", ErrHold
	}
	// Repeat the origin relation check after Release, then refresh target guards.
	if b.dep.AuthorizeOrigin(ctx, target, ref) != nil || b.publicationCurrent(ctx, c) != nil {
		return "", ErrHold
	}
	f := b.facts(ctx, c, shellEvidence{projected: true, released: true, completed: true, originAuthorized: true, exact: true, origin: originCall})
	var state State = Unmapped{}
	if advance(&state, Replay, c, f, ReleaseReplay) != nil {
		return "", ErrHold
	}
	return p.Text(), nil
}
