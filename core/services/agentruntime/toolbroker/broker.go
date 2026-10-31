// SPDX-License-Identifier: MIT
package toolbroker

import (
	"context"
	"encoding/hex"
	"errors"
	"reflect"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/agentruntime/nibadapter"
	"github.com/mudler/LocalAI/core/services/agentruntime/ownership"
	"github.com/mudler/LocalAI/core/services/agentruntime/store"
	"github.com/mudler/nib/chat"
)

var ErrRecorded = errors.New("toolbroker: recorded result available")
var ErrUnavailable = errors.New("toolbroker: authority unavailable")

// Authority is authenticated current host evidence, not request-supplied grants.
type Authority struct {
	redacted
	Scope                Scope
	Revisions            Revisions
	Lease                ownership.Lease
	Policy               PolicyMode
	ExternalTaintedWrite bool
	Selection            Selection
	HelperID             string
}
type ConversationReader interface {
	Get(context.Context, string, string) (store.Snapshot, error)
}
type Journal interface {
	Check(context.Context, ownership.Lease, time.Time) error
	Prepare(context.Context, ownership.Lease, time.Time, ownership.ToolCall) (ownership.ToolRecord, error)
	Dispatch(context.Context, ownership.Lease, time.Time, ownership.ToolCall) (ownership.ToolRecord, error)
	Complete(context.Context, ownership.Lease, time.Time, ownership.ToolCall, []byte) (ownership.ToolRecord, error)
	Records(context.Context, ownership.Lease, time.Time, string) ([]ownership.ToolRecord, error)
	Recover(context.Context, ownership.Lease, time.Time, string) ([]ownership.ToolRecord, error)
}
type Dependencies struct {
	Clock     func() time.Time
	Current   func(context.Context, Binding) (Authority, error)
	Reader    ConversationReader
	Journal   Journal
	Approvals func(context.Context, ApprovalDecision) (Approval, error)
	Audit     func(context.Context, AuditEntry) error
	Helpers   func(context.Context, Scope, string) (HelperGrant, error)
	Sanitize  func(context.Context, Binding, ToolOutput) (Output, error)
}
type AuditEntry struct {
	redacted
	Binding Binding
	RuleID  string
	Actor   Actor
	ActorID string
}
type BeginRequest struct {
	redacted
	Config ModelConfig
}
type CallHandle struct {
	redacted
	broker *Broker
	call   *brokerCall
}
type Claim struct {
	redacted
	broker *Broker
	call   *brokerCall
	token  *claimToken
}
type claimToken struct{ consumed bool }
type ApprovalDecision struct {
	redacted
	Handle   CallHandle
	Approval Approval
	Allow    bool
}
type CancelReason uint8

const CancelUser CancelReason = 1

type View struct {
	redacted
	Phase  string
	Result Output
}
type Outcome struct {
	redacted
	Status string
	Result Output
}
type ToolOutput = Output
type Execution struct {
	redacted
	binding Binding
}

func (e Execution) Binding() Binding    { return e.binding }
func (e Execution) Arguments() []byte   { return e.binding.Arguments() }
func (e Execution) ProviderKey() string { return e.binding.ProviderKey() }

type Executor func(context.Context, Execution) (ToolOutput, error)
type brokerCall struct {
	state    State
	lease    ownership.Lease
	token    *claimToken
	approval Approval
	cancel   context.CancelFunc
}

// Broker keeps approvals and single-consumption claims only in memory. Restart
// invalidates both; the database journal, not this mutex, arbitrates dispatch
// across independent brokers. Approvals are bounded to 4096 live call slots.
//
// Unknown is local: this journal has no per-call MarkUnknown. A dispatched row
// remains durably Dispatched in the same epoch until a later epoch Recover.
// Lease checks cannot atomically fence external effects. Trusted executors must
// enforce current authority, epoch and provider idempotency at their boundary.
// Unfenceable powerful native tools are not supported by this isolated broker.
type Broker struct {
	mu      sync.Mutex
	dep     Dependencies
	catalog Catalog
	calls   map[*brokerCall]struct{}
}

func nilDependency(v any) bool {
	if v == nil {
		return true
	}
	r := reflect.ValueOf(v)
	return r.Kind() == reflect.Pointer && r.IsNil()
}
func NewBroker(d Dependencies, c Catalog) (*Broker, error) {
	if d.Clock == nil || d.Current == nil || nilDependency(d.Reader) || nilDependency(d.Journal) || d.Approvals == nil || d.Audit == nil || d.Helpers == nil || d.Sanitize == nil || c.specs == nil {
		return nil, ErrInvalid
	}
	specs := make([]ToolSpec, 0, len(c.specs))
	for _, s := range c.specs {
		specs = append(specs, s)
	}
	cloned, e := NewCatalog(specs)
	if e != nil {
		return nil, ErrInvalid
	}
	return &Broker{dep: d, catalog: cloned, calls: make(map[*brokerCall]struct{})}, nil
}
func (b *Broker) input(c *brokerCall) Input {
	return Input{Binding: c.state.Binding(), Now: b.dep.Clock().UnixNano()}
}
func (b *Broker) valid(h CallHandle) bool {
	_, ok := b.calls[h.call]
	return h.broker == b && h.call != nil && ok
}
func view(c *brokerCall) View { return View{Phase: PhaseName(c.state.phase), Result: c.state.result} }
func (b *Broker) View(h CallHandle) (View, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.valid(h) {
		return View{}, ErrInvalid
	}
	return view(h.call), nil
}
func (b *Broker) Begin(ctx context.Context, r BeginRequest) (CallHandle, View, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !r.Config.Binding.valid() || r.Config.Policy > AllowAll || r.Config.ApprovalTTL <= 0 || len(b.calls) >= 4096 {
		return CallHandle{}, View{}, ErrInvalid
	}
	c := &brokerCall{state: NewState(r.Config)}
	b.calls[c] = struct{}{}
	h := CallHandle{broker: b, call: c}
	if e := b.apply(ctx, c, Requested{b.input(c)}); e != nil {
		delete(b.calls, c)
		return CallHandle{}, View{}, e
	}
	return h, view(c), nil
}

// admission deliberately uses the same effective selection at every boundary.
// Helper slices returned by a host registry are copied before intersection.
func (b *Broker) admission(ctx context.Context, c *brokerCall) (Admission, ownership.Lease) {
	binding := c.state.Binding()
	s := binding.scope
	a, e := b.dep.Current(ctx, binding)
	if e != nil {
		return Admission{}, ownership.Lease{}
	}
	registered := false
	if _, ok := b.catalog.specs[binding.tool]; ok {
		registered = true
	}
	evidence := Admission{Registered: registered}
	if a.Scope != s || a.Revisions != binding.revisions || a.Policy != c.state.config.Policy || a.ExternalTaintedWrite != c.state.config.ExternalTaintedWrite || a.Selection.Scope != s || a.Lease.Principal != s.Principal || a.Lease.Conversation != s.Conversation || a.Lease.Epoch != s.Epoch {
		return evidence, ownership.Lease{}
	}
	snap, e := b.dep.Reader.Get(ctx, s.Principal, s.Conversation)
	if e != nil || snap.Deleted || snap.Running == nil || snap.Running.InputID != s.Turn {
		return evidence, a.Lease
	}
	if e = b.dep.Journal.Check(ctx, a.Lease, b.dep.Clock()); e != nil {
		return evidence, a.Lease
	}
	evidence.Current = true
	// A caller-provided Selection.Helper is never registry evidence.
	selection := cloneSelection(a.Selection)
	selection.Helper = nil
	evidence.Helper = true
	if a.HelperID != "" {
		grant, err := b.dep.Helpers(ctx, s, a.HelperID)
		if err != nil || grant.Scope != s {
			evidence.Helper = false
		} else {
			grant.IDs = append([]ToolID(nil), grant.IDs...)
			selection.Helper = &grant
		}
	} else if a.Selection.Helper != nil {
		evidence.Helper = false
	}
	evidence.Selected = contains(b.catalog.Effective(selection), binding.tool)
	evidence.Resource = contains(selection.Resources, binding.tool)
	return evidence, a.Lease
}
func cloneSelection(s Selection) Selection {
	s.Selected = append([]ToolID(nil), s.Selected...)
	s.Allow = append([]ToolID(nil), s.Allow...)
	s.Exclude = append([]ToolID(nil), s.Exclude...)
	s.ACL = append([]ToolID(nil), s.ACL...)
	s.Resources = append([]ToolID(nil), s.Resources...)
	s.Artifacts = append([]ToolID(nil), s.Artifacts...)
	if s.Helper != nil {
		h := *s.Helper
		h.IDs = append([]ToolID(nil), h.IDs...)
		s.Helper = &h
	}
	return s
}
func admittedEvidence(a Admission) bool {
	return a.Current && a.Registered && a.Selected && a.Resource && a.Helper
}
func journalCall(binding Binding) ownership.ToolCall {
	d := binding.Digest()
	return ownership.ToolCall{TurnID: binding.scope.Turn, RequestID: binding.request, Attempt: binding.scope.Attempt, ArgumentsHash: hex.EncodeToString(d[:])}
}
func journalStatus(s ownership.ToolStatus) RecordStatus {
	switch s {
	case ownership.Prepared:
		return RecordPrepared
	case ownership.Dispatched:
		return RecordDispatched
	case ownership.Completed:
		return RecordCompleted
	case ownership.Unknown:
		return RecordUnknown
	}
	return RecordConflict
}

// apply is the sole transition gate. No effect is interpreted until BOTH model
// checks succeed; recursive observations go through this same gate.
// checkedEffects is a private, single-use capability produced only by the
// reducer and both transition checks. A phase snapshot alone grants no effects.
type checkedEffects struct {
	broker        *Broker
	call          *brokerCall
	state         State
	runs          []RunExecutor
	consumed      bool
	approvalUntil int64
}

// The reducer carries a deadline through claim/admission phases, but Executing
// intentionally has no approval lifetime. Keep it in the checked run capability
// so the final (potentially blocking) trusted admission cannot outlive approval.
func approvalDeadline(s State) int64 {
	switch p := s.phase.(type) {
	case Authorized:
		return p.until
	case Claiming:
		return p.until
	case Claimed:
		return p.until
	case Checking:
		return p.until
	}
	return 0
}

func (b *Broker) takeRun(ctx context.Context, c *brokerCall, effects *checkedEffects) (Binding, bool) {
	if effects == nil || effects.broker != b || effects.call != c || effects.consumed {
		return Binding{}, false
	}
	effects.consumed = true
	if len(effects.runs) != 1 || effects.runs[0].Binding != c.state.Binding() || !reflect.DeepEqual(effects.state, c.state) {
		return Binding{}, false
	}
	evidence, _ := b.admission(ctx, c)
	if !admittedEvidence(evidence) {
		_ = b.invalidate(ctx, c)
		return Binding{}, false
	}
	if effects.approvalUntil > 0 && b.dep.Clock().UnixNano() >= effects.approvalUntil {
		_ = b.apply(ctx, c, Expired{b.input(c)})
		return Binding{}, false
	}
	return effects.runs[0].Binding, true
}

func (b *Broker) apply(ctx context.Context, c *brokerCall, e Event) error {
	_, err := b.applyChecked(ctx, c, e)
	return err
}

func (b *Broker) applyChecked(ctx context.Context, c *brokerCall, e Event) (*checkedEffects, error) {
	checked := &checkedEffects{broker: b, call: c, approvalUntil: approvalDeadline(c.state)}
	observe := func(ctx context.Context, c *brokerCall, e Event) error {
		next, err := b.applyChecked(ctx, c, e)
		if err == nil && next != nil {
			checked.runs = append(checked.runs, next.runs...)
		}
		return err
	}
	before := c.state
	t, err := Reduce(before, e)
	if err != nil {
		return nil, ErrInvalid
	}
	if CheckInvariants(t.State) != nil || CheckTransition(before, e, t) != nil {
		return nil, ErrInvariant
	}
	c.state = t.State
	for _, effect := range t.Effects {
		switch fx := effect.(type) {
		case CheckAdmission, RecheckExecutionAdmission:
			evidence, lease := b.admission(ctx, c)
			c.lease = lease
			if err = observe(ctx, c, AdmissionPassed{Input: b.input(c), Evidence: evidence}); err != nil {
				return nil, err
			}
		case RecordDecision:
			actor, id := c.approval.Actor, c.approval.ActorID
			if actor == 0 {
				actor = HostActor
				id = "host-policy"
			}
			if b.dep.Audit(ctx, AuditEntry{Binding: c.state.Binding(), RuleID: t.RuleID, Actor: actor, ActorID: id}) != nil {
				return nil, b.invalidate(ctx, c)
			}
		case PrepareJournal:
			evidence, lease := b.admission(ctx, c)
			if !admittedEvidence(evidence) {
				return nil, b.invalidate(ctx, c)
			}
			c.lease = lease
			now := b.dep.Clock()
			if until := approvalDeadline(c.state); until > 0 && now.UnixNano() >= until {
				err = observe(ctx, c, Expired{Input{Binding: c.state.Binding(), Now: now.UnixNano()}})
				checked.state = c.state
				return checked, err
			}
			record, err := b.dep.Journal.Prepare(ctx, lease, now, journalCall(fx.Binding))
			if err != nil {
				err = observe(ctx, c, PreparedObserved{Input: b.input(c), Status: RecordConflict})
				checked.state = c.state
				return checked, err
			}
			output := Output{}
			if record.Status == ownership.Completed {
				output, err = b.dep.Sanitize(ctx, fx.Binding, NewOutput(record.Result))
				if err != nil {
					err = observe(ctx, c, PreparedObserved{Input: b.input(c), Status: RecordUnknown})
					checked.state = c.state
					return checked, err
				}
			}
			if err = observe(ctx, c, PreparedObserved{Input: b.input(c), Status: journalStatus(record.Status), Output: output}); err != nil {
				return nil, err
			}
		case DispatchJournal:
			evidence, lease := b.admission(ctx, c)
			if !admittedEvidence(evidence) {
				return nil, b.invalidate(ctx, c)
			}
			c.lease = lease
			now := b.dep.Clock()
			if until := approvalDeadline(c.state); until > 0 && now.UnixNano() >= until {
				err = observe(ctx, c, Expired{Input{Binding: c.state.Binding(), Now: now.UnixNano()}})
				checked.state = c.state
				return checked, err
			}
			_, err := b.dep.Journal.Dispatch(ctx, lease, now, journalCall(fx.Binding))
			if err != nil {
				err = observe(ctx, c, DispatchRejected{b.input(c)})
				checked.state = c.state
				return checked, err
			}
			if err = observe(ctx, c, DispatchGranted{Input: b.input(c), Fresh: true}); err != nil {
				return nil, err
			}
			if _, ok := c.state.phase.(Claimed); ok {
				c.token = &claimToken{}
			}
		case CompleteJournal:
			// Recheck projection authority after the body too: a revoked result must
			// never become an ordinary journal row.
			evidence, lease := b.admission(ctx, c)
			if !admittedEvidence(evidence) {
				err = observe(ctx, c, JournalFailed{b.input(c)})
				checked.state = c.state
				return checked, err
			}
			_, err := b.dep.Journal.Complete(ctx, lease, b.dep.Clock(), journalCall(fx.Binding), fx.Output.Bytes())
			if err != nil {
				err = observe(ctx, c, JournalFailed{b.input(c)})
				checked.state = c.state
				return checked, err
			}
			if err = observe(ctx, c, JournalCompleted{b.input(c)}); err != nil {
				return nil, err
			}
		case RunExecutor:
			checked.runs = append(checked.runs, fx)
		case CancelExecutor:
			if c.cancel != nil {
				c.cancel()
			}
		case NotifyInterrupted, HoldForReconciliation:
			if c.cancel != nil {
				c.cancel()
			}
		case RequestApproval, ReturnRecordedResult, ReturnDenied:
		default:
			return nil, ErrInvariant
		}
	}
	checked.state = c.state
	return checked, nil
}
func (b *Broker) invalidate(ctx context.Context, c *brokerCall) error {
	return b.apply(ctx, c, AccessRevoked{b.input(c)})
}
func (b *Broker) Decide(ctx context.Context, d ApprovalDecision) (View, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.valid(d.Handle) {
		return View{}, ErrInvalid
	}
	c := d.Handle.call
	evidence, _ := b.admission(ctx, c)
	if !admittedEvidence(evidence) {
		err := b.invalidate(ctx, c)
		return view(c), err
	}
	// The trusted recorder authenticates actor identity, binding and decision.
	a, e := b.dep.Approvals(ctx, d)
	if e != nil {
		return view(c), ErrUnavailable
	}
	var event Event = ApprovalDenied{b.input(c)}
	if d.Allow {
		event = ApprovalGranted{Input: b.input(c), Approval: a}
	}
	previous := c.approval
	c.approval = a
	if e = b.apply(ctx, c, event); e != nil {
		c.approval = previous
		return view(c), e
	}
	if _, ok := c.state.phase.(WaitingApproval); ok {
		c.approval = previous
	}
	return view(c), nil
}
func (b *Broker) Cancel(ctx context.Context, h CallHandle, reason CancelReason) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.valid(h) || reason != CancelUser {
		return ErrInvalid
	}
	return b.apply(ctx, h.call, Cancelled{b.input(h.call)})
}
func (b *Broker) Expire(ctx context.Context, h CallHandle) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.valid(h) {
		return ErrInvalid
	}
	return b.apply(ctx, h.call, Expired{b.input(h.call)})
}
func (b *Broker) ClaimCall(ctx context.Context, h CallHandle) (Claim, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.valid(h) {
		return Claim{}, ErrInvalid
	}
	c := h.call
	if err := b.apply(ctx, c, ClaimRequested{b.input(c)}); err != nil {
		return Claim{}, err
	}
	if _, ok := c.state.phase.(Completed); ok {
		return Claim{}, ErrRecorded
	}
	if _, ok := c.state.phase.(Claimed); !ok || c.token == nil {
		return Claim{}, ErrUnavailable
	}
	return Claim{broker: b, call: c, token: c.token}, nil
}
func outcome(c *brokerCall) Outcome {
	return Outcome{Status: PhaseName(c.state.phase), Result: c.state.result}
}
func runSafely(ctx context.Context, executor Executor, e Execution) (out ToolOutput, err error) {
	defer func() {
		if recover() != nil {
			out = Output{}
			err = ErrUnavailable
		}
	}()
	return executor(ctx, e)
}
func (b *Broker) Invoke(ctx context.Context, claim Claim, executor Executor) (Outcome, error) {
	b.mu.Lock()
	if claim.broker != b || claim.call == nil || claim.token == nil || executor == nil {
		b.mu.Unlock()
		return Outcome{}, ErrInvalid
	}
	c := claim.call
	if _, ok := b.calls[c]; !ok || c.token != claim.token || claim.token.consumed {
		b.mu.Unlock()
		return Outcome{}, ErrInvalid
	}
	claim.token.consumed = true
	effects, err := b.applyChecked(ctx, c, InvokeRequested{b.input(c)})
	if err != nil {
		b.mu.Unlock()
		return Outcome{}, err
	}
	binding, ok := b.takeRun(ctx, c, effects)
	if !ok {
		out := outcome(c)
		b.mu.Unlock()
		return out, nil
	}
	runctx, cancel := context.WithCancel(ctx)
	c.cancel = cancel
	if err := b.apply(ctx, c, ExecutionStarted{b.input(c)}); err != nil {
		cancel()
		b.mu.Unlock()
		return Outcome{}, err
	}
	b.mu.Unlock()
	raw, err := runSafely(runctx, executor, Execution{binding: binding})
	b.mu.Lock()
	defer b.mu.Unlock()
	defer cancel()
	c.cancel = nil
	if err != nil || runctx.Err() != nil {
		_ = b.apply(ctx, c, ExecutionUncertain{b.input(c)})
		return outcome(c), nil
	}
	if _, ok := c.state.phase.(Executing); !ok {
		return outcome(c), nil
	}
	evidence, _ := b.admission(ctx, c)
	if !admittedEvidence(evidence) {
		_ = b.apply(ctx, c, ExecutionUncertain{b.input(c)})
		return outcome(c), nil
	}
	projected, err := b.dep.Sanitize(ctx, binding, raw)
	if err != nil {
		_ = b.apply(ctx, c, ExecutionUncertain{b.input(c)})
		return outcome(c), nil
	}
	if err = b.apply(ctx, c, ExecutionCompleted{Input: b.input(c), Output: projected}); err != nil {
		return Outcome{}, err
	}
	return outcome(c), nil
}

// AttemptBinding freezes the host identity. The pinned native callback has no
// unique host request ID, so Policy is strictly a veto, never dispatch authority.
// Even registered wrappers must separately call Begin/ClaimCall/Invoke with an
// independent host request ID; this callback cannot infer one from name/args.
type AttemptBinding struct {
	redacted
	Scope Scope
}

func (b *Broker) Policy(attempt AttemptBinding) nibadapter.Policy {
	scope := attempt.Scope
	return func(id nibadapter.Identity, _ chat.ToolCallRequest) error {
		if id.Principal != scope.Principal || id.Conversation != scope.Conversation || id.Turn != scope.Turn || id.Attempt != scope.Attempt || id.Epoch != scope.Epoch {
			return ErrUnavailable
		}
		return ErrUnavailable
	}
}
