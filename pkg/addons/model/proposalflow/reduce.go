// SPDX-License-Identifier: MIT

// Package proposalflow is a pure model of the add-on proposal execution flow:
// Propose, Confirm, Cancel, execute, outcome-unknown handling and receipt
// lookup, plus the add-on hold and generation changes. It is the spec of that
// flow. It has no clock, does no I/O and starts no goroutine: time and I/O
// enter as events, and the reducer returns effects as data.
//
// The model describes ONE proposal. The environment (admission decisions, the
// runtime client, the hold, the generation) speaks to the proposal through
// events. A thin shell that owns the real clock, store and runtime executes
// the effects and feeds the answers back as events.
//
// This file holds the state, event and effect types and the reducer. The
// traces (traces.go) and the invariant search (explore.go) import it.
package proposalflow

import "fmt"

// MaxLookups bounds the automatic receipt lookups of one unknown outcome. The
// implementation bounds them by a time window; the model uses a count so the
// state space stays finite. After the bound only an explicit lookup or the
// owner's resolution remains.
const MaxLookups = 2

// Role is the role an actor holds on the resource, as the host sees it now.
type Role int

// Roles, lowest first. The order is the rank.
const (
	RoleNone Role = iota
	RoleViewer
	RoleEditor
	RoleOwner
)

func (r Role) String() string {
	switch r {
	case RoleNone:
		return "none"
	case RoleViewer:
		return "viewer"
	case RoleEditor:
		return "editor"
	case RoleOwner:
		return "owner"
	}
	return fmt.Sprintf("role(%d)", int(r))
}

// ToolKind is the kind the add-on declared for a tool. The host derives the
// action of a proposal from it and from nothing the caller sends.
type ToolKind int

// Tool kinds. KindUndeclared is a tool the installed add-on does not declare.
const (
	KindUndeclared ToolKind = iota
	KindRead
	KindWrite
	KindSchema
)

func (k ToolKind) String() string {
	switch k {
	case KindUndeclared:
		return "undeclared"
	case KindRead:
		return "read"
	case KindWrite:
		return "write"
	case KindSchema:
		return "schema"
	}
	return fmt.Sprintf("kind(%d)", int(k))
}

// Action is the host authority a proposal runs under.
type Action int

// Actions of a proposal. ActionNone is what a read or undeclared tool maps to:
// it is never a proposal.
const (
	ActionNone Action = iota
	ActionWrite
	ActionSchema
)

func (a Action) String() string {
	switch a {
	case ActionNone:
		return "none"
	case ActionWrite:
		return "write"
	case ActionSchema:
		return "schema"
	}
	return fmt.Sprintf("action(%d)", int(a))
}

// ActionOf derives the action from the tool kind. It is the only place an
// action comes from.
func ActionOf(k ToolKind) Action {
	switch k {
	case KindWrite:
		return ActionWrite
	case KindSchema:
		return ActionSchema
	case KindUndeclared, KindRead:
		return ActionNone
	}
	return ActionNone
}

// Required is the least role that may run the action. ActionNone needs a role
// nobody holds below owner, so it never passes for a lower one.
func (a Action) Required() Role {
	switch a {
	case ActionWrite:
		return RoleEditor
	case ActionSchema:
		return RoleOwner
	case ActionNone:
		return RoleOwner
	}
	return RoleOwner
}

// Prop is what every stored situation of a proposal carries. There is no
// Action field: it is derived from Kind, so a proposal cannot hold an action
// that disagrees with its tool.
type Prop struct {
	ID           string
	Proposer     string
	ProposerRole Role
	Kind         ToolKind
	// Gen is the add-on generation the preview was made against.
	Gen int
	// ExpiresAt is a number on the clock the shell supplies in events.
	ExpiresAt int64
}

// Action is the action derived from the tool kind.
func (p Prop) Action() Action { return ActionOf(p.Kind) }

// ---------------------------------------------------------------- states

// State is a tagged union: one constructor per situation. The marker method
// limits the set to this package; a test checks that AllStates lists them all.
type State interface {
	isState()
	// Name is the constructor name.
	Name() string
	// Durable is the stored lifecycle state the situation maps to, or "" when
	// nothing is stored.
	Durable() string
}

// Unproposed is the start: nothing is stored.
type Unproposed struct{}

// Refused is a proposal the host refused at Propose. Nothing is stored.
type Refused struct{ Why RefuseWhy }

// Pending is a stored proposal that waits for a human decision.
type Pending struct{ P Prop }

// Admitting is a confirmed proposal that waits for the admission decision of
// the resource (role recheck, hold, writer slot, blocking outcome, runtime
// generation). Nothing is sent to the add-on and nothing is persisted yet.
type Admitting struct {
	P  Prop
	By string
}

// ExecPrepared is the durable executing state with NOTHING sent to the add-on
// yet. Leaving it by a definite not-sent answer is safe and retryable.
type ExecPrepared struct{ P Prop }

// ExecSent is the executing state after the request may have reached the
// add-on. From here the outcome is only ever known by evidence.
type ExecSent struct{ P Prop }

// OutcomeUnknown is a sent apply with no evidence, with automatic receipt
// lookups remaining. It blocks conflicting writes on the resource.
type OutcomeUnknown struct {
	P       Prop
	Lookups int
}

// UnknownManual is an unresolved outcome whose automatic lookups ran out. It
// still blocks writes. An explicit lookup or the owner's resolution leaves it.
type UnknownManual struct{ P Prop }

// Abandoned is an unresolved outcome the resource owner stopped waiting for.
// It no longer blocks writes. A late receipt is still recorded.
type Abandoned struct{ P Prop }

// Applied is a proposal with a committed receipt. Terminal.
type Applied struct{ P Prop }

// Rejected is a proposal the add-on definitely did not apply: it refused the
// request, answered a conflict, or recorded a rejected receipt. Terminal.
type Rejected struct {
	P   Prop
	Why RejectWhy
}

// NotApplied is a sent proposal that a receipt lookup proved never committed.
// Terminal.
type NotApplied struct{ P Prop }

// Cancelled is a proposal that never ran. Terminal.
type Cancelled struct {
	P   Prop
	Why CancelWhy
}

// Expired is a proposal that reached its expiry unconfirmed. Terminal.
type Expired struct{ P Prop }

// Stale is a proposal whose add-on generation moved, so what the human saw is
// not what would run. Terminal.
type Stale struct{ P Prop }

// Dropped is an abandoned proposal removed after retention. Terminal.
type Dropped struct{ P Prop }

func (Unproposed) isState()     {}
func (Refused) isState()        {}
func (Pending) isState()        {}
func (Admitting) isState()      {}
func (ExecPrepared) isState()   {}
func (ExecSent) isState()       {}
func (OutcomeUnknown) isState() {}
func (UnknownManual) isState()  {}
func (Abandoned) isState()      {}
func (Applied) isState()        {}
func (Rejected) isState()       {}
func (NotApplied) isState()     {}
func (Cancelled) isState()      {}
func (Expired) isState()        {}
func (Stale) isState()          {}
func (Dropped) isState()        {}

func (Unproposed) Name() string     { return "Unproposed" }
func (Refused) Name() string        { return "Refused" }
func (Pending) Name() string        { return "Pending" }
func (Admitting) Name() string      { return "Admitting" }
func (ExecPrepared) Name() string   { return "ExecPrepared" }
func (ExecSent) Name() string       { return "ExecSent" }
func (OutcomeUnknown) Name() string { return "OutcomeUnknown" }
func (UnknownManual) Name() string  { return "UnknownManual" }
func (Abandoned) Name() string      { return "Abandoned" }
func (Applied) Name() string        { return "Applied" }
func (Rejected) Name() string       { return "Rejected" }
func (NotApplied) Name() string     { return "NotApplied" }
func (Cancelled) Name() string      { return "Cancelled" }
func (Expired) Name() string        { return "Expired" }
func (Stale) Name() string          { return "Stale" }
func (Dropped) Name() string        { return "Dropped" }

// Durable names are the gateway.ProposalState values of the implementation.
// Several constructors share one: the model is finer than the stored state.
func (Unproposed) Durable() string     { return "" }
func (Refused) Durable() string        { return "" }
func (Pending) Durable() string        { return "pending" }
func (Admitting) Durable() string      { return "pending" }
func (ExecPrepared) Durable() string   { return "executing" }
func (ExecSent) Durable() string       { return "executing" }
func (OutcomeUnknown) Durable() string { return "outcome-unknown" }
func (UnknownManual) Durable() string  { return "outcome-unknown" }
func (Abandoned) Durable() string      { return "abandoned-unknown" }
func (Applied) Durable() string        { return "applied" }
func (Rejected) Durable() string       { return "rejected" }
func (NotApplied) Durable() string     { return "rejected" }
func (Cancelled) Durable() string      { return "cancelled" }
func (Expired) Durable() string        { return "expired" }
func (Stale) Durable() string          { return "rejected" }
func (Dropped) Durable() string        { return "" }

// AllStates lists one value of every constructor. A test compares it with the
// types that declare isState, so a new constructor cannot be forgotten.
func AllStates() []State {
	return []State{
		Unproposed{}, Refused{}, Pending{}, Admitting{}, ExecPrepared{}, ExecSent{}, OutcomeUnknown{},
		UnknownManual{}, Abandoned{}, Applied{}, Rejected{}, NotApplied{}, Cancelled{}, Expired{}, Stale{}, Dropped{},
	}
}

// RefuseWhy says why Propose refused.
type RefuseWhy int

// Refusal reasons of Propose.
const (
	RefuseUndeclaredTool RefuseWhy = iota + 1
	RefuseNotAMutation
	RefuseInsufficientRole
	RefuseStaleGeneration
)

func (w RefuseWhy) String() string {
	switch w {
	case RefuseUndeclaredTool:
		return "undeclared-tool"
	case RefuseNotAMutation:
		return "not-a-mutation"
	case RefuseInsufficientRole:
		return "insufficient-role"
	case RefuseStaleGeneration:
		return "stale-generation"
	}
	return fmt.Sprintf("refuse(%d)", int(w))
}

// RejectWhy says why the add-on definitely did not apply.
type RejectWhy int

// Rejection reasons.
const (
	RejectReceipt RejectWhy = iota + 1
	RejectRefused
	RejectConflict
)

func (w RejectWhy) String() string {
	switch w {
	case RejectReceipt:
		return "rejected-receipt"
	case RejectRefused:
		return "addon-refused"
	case RejectConflict:
		return "revision-conflict"
	}
	return fmt.Sprintf("reject(%d)", int(w))
}

// CancelWhy says why a proposal was cancelled.
type CancelWhy int

// Cancellation reasons.
const (
	CancelByProposer CancelWhy = iota + 1
	CancelByHold
	CancelByAuthority
)

func (w CancelWhy) String() string {
	switch w {
	case CancelByProposer:
		return "proposer"
	case CancelByHold:
		return "hold"
	case CancelByAuthority:
		return "authority-lowered"
	}
	return fmt.Sprintf("cancel(%d)", int(w))
}

// ---------------------------------------------------------------- events

// Event is the complete input vocabulary. Time enters as numbers in Now
// fields; the reducer only compares them.
type Event interface {
	isEvent()
	Name() string
}

// Propose asks to store a proposal. Tool is the kind the installed add-on
// declared for the named tool (KindUndeclared when it declared none). Claimed
// is the action the caller wrote in the request: the model carries it only to
// show it is never used.
type Propose struct {
	ID         string
	Actor      string
	Role       Role
	Tool       ToolKind
	Claimed    Action
	Gen        int
	RuntimeGen int
	ExpiresAt  int64
	Now        int64
	// Held is true while a backup, upgrade or restore holds the add-on.
	Held bool
	// Blocked is true while another operation on the resource is unresolved.
	Blocked bool
}

// Confirm is a confirmation from a host confirmation surface. Role is the
// actor's live role. Human says the credential stands for a human. DigestOK
// says the named digest equals the stored one.
type Confirm struct {
	Actor    string
	Role     Role
	Human    bool
	DigestOK bool
	Now      int64
}

// Cancel withdraws the proposal.
type Cancel struct{ Actor string }

// Tick delivers the clock. It is how expiry is noticed without a sweep call.
type Tick struct{ Now int64 }

// AdmitGranted is the admission decision "go": the writer slot is held, the
// role and identity were rechecked, no other outcome blocks, and the add-on
// runtime of RuntimeGen is ready.
type AdmitGranted struct {
	Now        int64
	RuntimeGen int
}

// AdmitRefused is an admission decision "no", before anything was persisted
// or sent.
type AdmitRefused struct{ Cause AdmitCause }

// SendStarted says the runtime client passed its local gates (not withdrawn, a
// free slot, a valid envelope) and the request is now on the wire. It is the
// moment from which the add-on may have received the operation.
type SendStarted struct{}

// NotSent says the runtime client refused the dispatch before the request left
// the host. The add-on cannot have seen it.
type NotSent struct{ Cause NotSentCause }

// ApplyResult is the end of the apply call, after the request was sent.
type ApplyResult struct{ Outcome ApplyOutcome }

// LookupDue is the retry timer of the receipt lookup.
type LookupDue struct{}

// ReceiptResult is the answer of a receipt lookup.
type ReceiptResult struct{ Answer ReceiptAnswer }

// OwnerResolve is the resource owner stopping the wait for an unresolved
// outcome. Reason says a non-empty reason was given. Held is true while the
// add-on is held.
type OwnerResolve struct {
	Role   Role
	Human  bool
	Reason bool
	Held   bool
}

// HoldAcquired is HoldAddon: new admissions stop and admitted work is
// cancelled.
type HoldAcquired struct{}

// HoldReleased lifts the hold.
type HoldReleased struct{}

// GenerationChanged says the add-on moved to another generation.
type GenerationChanged struct{ New int }

// AuthorityLowered says the proposer lost the authority the action needs.
type AuthorityLowered struct{}

// Restart is the owner process starting again after a stop or crash. Volatile
// facts are gone; stored state remains.
type Restart struct{}

// RetentionElapsed says an abandoned outcome passed its retention.
type RetentionElapsed struct{}

func (Propose) isEvent()           {}
func (Confirm) isEvent()           {}
func (Cancel) isEvent()            {}
func (Tick) isEvent()              {}
func (AdmitGranted) isEvent()      {}
func (AdmitRefused) isEvent()      {}
func (SendStarted) isEvent()       {}
func (NotSent) isEvent()           {}
func (ApplyResult) isEvent()       {}
func (LookupDue) isEvent()         {}
func (ReceiptResult) isEvent()     {}
func (OwnerResolve) isEvent()      {}
func (HoldAcquired) isEvent()      {}
func (HoldReleased) isEvent()      {}
func (GenerationChanged) isEvent() {}
func (AuthorityLowered) isEvent()  {}
func (Restart) isEvent()           {}
func (RetentionElapsed) isEvent()  {}

func (Propose) Name() string           { return "Propose" }
func (Confirm) Name() string           { return "Confirm" }
func (Cancel) Name() string            { return "Cancel" }
func (Tick) Name() string              { return "Tick" }
func (AdmitGranted) Name() string      { return "AdmitGranted" }
func (AdmitRefused) Name() string      { return "AdmitRefused" }
func (SendStarted) Name() string       { return "SendStarted" }
func (NotSent) Name() string           { return "NotSent" }
func (ApplyResult) Name() string       { return "ApplyResult" }
func (LookupDue) Name() string         { return "LookupDue" }
func (ReceiptResult) Name() string     { return "ReceiptResult" }
func (OwnerResolve) Name() string      { return "OwnerResolve" }
func (HoldAcquired) Name() string      { return "HoldAcquired" }
func (HoldReleased) Name() string      { return "HoldReleased" }
func (GenerationChanged) Name() string { return "GenerationChanged" }
func (AuthorityLowered) Name() string  { return "AuthorityLowered" }
func (Restart) Name() string           { return "Restart" }
func (RetentionElapsed) Name() string  { return "RetentionElapsed" }

// AllEvents lists one value of every event type, for the coverage tests.
func AllEvents() []Event {
	return []Event{
		Propose{}, Confirm{}, Cancel{}, Tick{}, AdmitGranted{}, AdmitRefused{}, SendStarted{}, NotSent{},
		ApplyResult{}, LookupDue{}, ReceiptResult{}, OwnerResolve{}, HoldAcquired{}, HoldReleased{},
		GenerationChanged{}, AuthorityLowered{}, Restart{}, RetentionElapsed{},
	}
}

// AdmitCause is why an admission said no.
type AdmitCause int

// Admission refusal causes. All of them happen before anything is persisted
// or sent, so none of them can leave an unknown outcome.
const (
	CauseHeld AdmitCause = iota + 1
	CauseForbidden
	CauseUnavailable
	CauseBlocked
	CauseCaller
)

func (c AdmitCause) String() string {
	switch c {
	case CauseHeld:
		return "held"
	case CauseForbidden:
		return "forbidden"
	case CauseUnavailable:
		return "unavailable"
	case CauseBlocked:
		return "blocked"
	case CauseCaller:
		return "caller-gone"
	}
	return fmt.Sprintf("cause(%d)", int(c))
}

// NotSentCause is why the runtime client did not send.
type NotSentCause int

// Causes of a definite not-sent answer. Slots, Invalid and Withdrawn are
// retryable. Hold and Authority are cancellations that arrived before the
// send. Superseded means the generation moved.
const (
	NotSentSlots NotSentCause = iota + 1
	NotSentInvalid
	NotSentWithdrawn
	NotSentHold
	NotSentAuthority
	NotSentSuperseded
)

func (c NotSentCause) String() string {
	switch c {
	case NotSentSlots:
		return "no-free-slot"
	case NotSentInvalid:
		return "invalid-envelope"
	case NotSentWithdrawn:
		return "withdrawn"
	case NotSentHold:
		return "hold"
	case NotSentAuthority:
		return "authority-lowered"
	case NotSentSuperseded:
		return "superseded"
	}
	return fmt.Sprintf("notsent(%d)", int(c))
}

// ApplyOutcome is how the apply call ended once the request was sent.
type ApplyOutcome int

// Apply outcomes. Committed, Rejected, Refused and Conflict are answers of the
// add-on. Unsettled, TransportError and Timeout give no evidence.
const (
	ApplyCommitted ApplyOutcome = iota + 1
	ApplyRejected
	ApplyRefused
	ApplyConflict
	ApplyUnsettled
	ApplyTransportError
	ApplyTimeout
)

func (o ApplyOutcome) String() string {
	switch o {
	case ApplyCommitted:
		return "committed"
	case ApplyRejected:
		return "rejected"
	case ApplyRefused:
		return "refused"
	case ApplyConflict:
		return "conflict"
	case ApplyUnsettled:
		return "unsettled"
	case ApplyTransportError:
		return "transport-error"
	case ApplyTimeout:
		return "timeout"
	}
	return fmt.Sprintf("apply(%d)", int(o))
}

// ReceiptAnswer is the answer of a receipt lookup.
type ReceiptAnswer int

// Receipt answers. NotCommitted is a definite answer: the add-on promises the
// operation never commits. The implementation has no such answer today. A
// missing receipt, a pending receipt and a failed lookup are all Unsettled.
const (
	ReceiptCommitted ReceiptAnswer = iota + 1
	ReceiptRejected
	ReceiptNotCommitted
	ReceiptUnsettled
)

func (a ReceiptAnswer) String() string {
	switch a {
	case ReceiptCommitted:
		return "committed"
	case ReceiptRejected:
		return "rejected"
	case ReceiptNotCommitted:
		return "not-committed"
	case ReceiptUnsettled:
		return "unsettled"
	}
	return fmt.Sprintf("receipt(%d)", int(a))
}

// ---------------------------------------------------------------- effects

// Effect is data the shell executes. The reducer never executes one.
type Effect interface {
	isEffect()
	// String is a short stable form, used by tests and the viewer.
	String() string
}

// Reply answers the caller of the event.
type Reply struct{ Code ReplyCode }

// Persist writes the durable lifecycle state before anything else happens.
type Persist struct{ To string }

// Admit asks for the admission decision under the action.
type Admit struct{ Action Action }

// Dispatch hands the stored operation to the runtime client, once.
type Dispatch struct{}

// CancelDispatch asks the shell to stop a dispatch that has not started. The
// shell answers with NotSent or, if it was too late, SendStarted.
type CancelDispatch struct{ Why NotSentCause }

// CancelSend aborts the context of a request that is on the wire. The call
// then ends with an ApplyResult.
type CancelSend struct{}

// LookupReceipt asks the add-on for the receipt of the operation. Read only.
type LookupReceipt struct{}

// Alarm reports a broken contract between shell and model.
type Alarm struct{ Msg string }

func (Reply) isEffect()          {}
func (Persist) isEffect()        {}
func (Admit) isEffect()          {}
func (Dispatch) isEffect()       {}
func (CancelDispatch) isEffect() {}
func (CancelSend) isEffect()     {}
func (LookupReceipt) isEffect()  {}
func (Alarm) isEffect()          {}

func (e Reply) String() string          { return "reply:" + string(e.Code) }
func (e Persist) String() string        { return "persist:" + e.To }
func (e Admit) String() string          { return "admit:" + e.Action.String() }
func (Dispatch) String() string         { return "dispatch" }
func (e CancelDispatch) String() string { return "cancel-dispatch:" + e.Why.String() }
func (CancelSend) String() string       { return "cancel-send" }
func (LookupReceipt) String() string    { return "lookup-receipt" }
func (e Alarm) String() string          { return "alarm:" + e.Msg }

// ReplyCode is the answer the caller gets.
type ReplyCode string

// Reply codes.
const (
	ReplyCreated     ReplyCode = "created"
	ReplyExisting    ReplyCode = "existing"
	ReplyForbidden   ReplyCode = "forbidden"
	ReplyClosed      ReplyCode = "closed"
	ReplyExpired     ReplyCode = "expired"
	ReplyChanged     ReplyCode = "changed"
	ReplyBusy        ReplyCode = "busy"
	ReplyUnavailable ReplyCode = "unavailable"
	ReplyBlocked     ReplyCode = "blocked"
	ReplyStale       ReplyCode = "stale"
	ReplyApplied     ReplyCode = "applied"
	ReplyRejected    ReplyCode = "rejected"
	ReplyUnknown     ReplyCode = "outcome-unknown"
	ReplyCancelled   ReplyCode = "cancelled"
	ReplyResolved    ReplyCode = "resolved"
	ReplyRefused     ReplyCode = "refused"
)

// ---------------------------------------------------------------- rules

// RuleKind separates real transitions from ignored pairs.
type RuleKind string

// Rule kinds.
const (
	KindTransition RuleKind = "transition"
	KindIgnored    RuleKind = "ignored"
	KindViolation  RuleKind = "violation"
)

// Rule documents one rule id. The reducer returns only ids that are listed.
type Rule struct {
	ID   string
	Kind RuleKind
	Text string
}

// Rules is the rule table. A test checks that the reducer returns only these
// ids and that the traces plus the search fire every one of them.
var Rules = []Rule{
	{"R1", KindTransition, "Propose while the add-on is held: nothing stored, reply unavailable, retryable"},
	{"R2", KindTransition, "Propose with a tool the add-on did not declare: Refused"},
	{"R3", KindTransition, "Propose with a read tool: Refused, a read is never a proposal"},
	{"R4", KindTransition, "Propose where the actor's role is below what the derived action needs: Refused (an editor cannot propose a schema tool)"},
	{"R5", KindTransition, "Propose against a generation that is not the runtime's: Refused"},
	{"R6", KindTransition, "Propose while another outcome on the resource is unresolved: nothing stored, reply blocked"},
	{"R7", KindTransition, "Propose with no usable expiry: nothing stored, reply unavailable"},
	{"R8", KindTransition, "Propose stored: Pending. The action comes from the tool kind"},
	{"R9", KindTransition, "Propose repeated for a stored proposal: the stored proposal is returned, nothing changes"},
	{"R10", KindTransition, "Confirm by another user than the proposer: forbidden, no change"},
	{"R11", KindTransition, "Confirm from a credential that is not a human surface: forbidden, no change"},
	{"R12", KindTransition, "Confirm whose digest is not the stored one: changed, no change"},
	{"R13", KindTransition, "Confirm after expiry: Expired"},
	{"R14", KindTransition, "Confirm whose live role no longer covers the derived action: forbidden, stays Pending"},
	{"R15", KindTransition, "Confirm accepted: Admitting, ask for the admission decision"},
	{"R16", KindTransition, "Confirm while a confirmation is being admitted: busy, no change"},
	{"R17", KindTransition, "Confirm of an executing or unresolved proposal: look the receipt up, never apply again"},
	{"R18", KindTransition, "Confirm of an Applied proposal: the stored answer, nothing runs"},
	{"R19", KindTransition, "Confirm of a Rejected, NotApplied or Stale proposal: the stored answer, nothing runs"},
	{"R20", KindTransition, "Confirm of a Cancelled or Dropped proposal: closed, nothing runs"},
	{"R21", KindTransition, "Confirm of an Expired proposal: expired, nothing runs"},
	{"R22", KindTransition, "Any caller request for a proposal that was never stored: forbidden"},
	{"R23", KindTransition, "Cancel by another user than the proposer: forbidden, no change"},
	{"R24", KindTransition, "Cancel of a Pending proposal: Cancelled"},
	{"R25", KindTransition, "Cancel while a confirmation is in admission: Cancelled, the late admission answer finds it closed"},
	{"R26", KindTransition, "Cancel of a proposal that is not pending: closed, no change"},
	{"R27", KindTransition, "Cancel of a Cancelled proposal: the same answer again"},
	{"R28", KindTransition, "Tick past expiry of a Pending or Admitting proposal: Expired"},
	{"R29", KindTransition, "Admission refused (held, forbidden, unavailable, blocked, caller gone): back to Pending, retryable. Nothing was persisted or sent"},
	{"R30", KindTransition, "Admission granted after expiry: Expired"},
	{"R31", KindTransition, "Admission granted for another generation than the proposal's: Stale"},
	{"R32", KindTransition, "Admission granted: ExecPrepared, persist executing, then dispatch once"},
	{"R33", KindTransition, "SendStarted: ExecSent. From here an error is not proof of anything"},
	{"R34", KindTransition, "NotSent for a retryable cause (no slot, invalid envelope, withdrawn): back to Pending. The add-on never saw it"},
	{"R35", KindTransition, "NotSent because a hold or lowered authority cancelled the dispatch: Cancelled"},
	{"R36", KindTransition, "NotSent because the generation moved: Stale"},
	{"R37", KindTransition, "Apply answered committed: Applied"},
	{"R38", KindTransition, "Apply answered rejected, refused or conflict: Rejected, the add-on definitely did not apply"},
	{"R39", KindTransition, "Apply ended with no evidence (unsettled, transport error, timeout) after a send: OutcomeUnknown"},
	{"R40", KindTransition, "Receipt lookup says committed: Applied"},
	{"R41", KindTransition, "Receipt lookup says rejected: Rejected"},
	{"R42", KindTransition, "Receipt lookup says not-committed: NotApplied"},
	{"R43", KindTransition, "Receipt lookup unsettled with lookups left: stay OutcomeUnknown, count it"},
	{"R44", KindTransition, "Receipt lookup unsettled and the bound reached: UnknownManual"},
	{"R45", KindTransition, "Lookup timer fired for an unresolved or abandoned outcome: look the receipt up"},
	{"R46", KindTransition, "Owner resolution from a non-owner, a non-human credential or without a reason: forbidden"},
	{"R47", KindTransition, "Owner resolution while the add-on is held: unavailable, no change"},
	{"R48", KindTransition, "Owner resolution of an unresolved outcome: Abandoned, one last lookup"},
	{"R49", KindTransition, "Owner resolution of a proposal that is not unresolved: closed, no change"},
	{"R50", KindTransition, "Owner resolution of a settled or abandoned proposal: the stored answer"},
	{"R51", KindTransition, "Hold on a Pending or Admitting proposal: Cancelled"},
	{"R52", KindTransition, "Hold on a prepared dispatch: ask the shell to cancel it, wait for NotSent or SendStarted"},
	{"R53", KindTransition, "Hold on a sent request: abort its context and wait for the apply result"},
	{"R54", KindTransition, "Proposer's authority lowered on a Pending or Admitting proposal: Cancelled"},
	{"R55", KindTransition, "Proposer's authority lowered on a prepared dispatch: ask the shell to cancel it"},
	{"R56", KindTransition, "Proposer's authority lowered on a sent request: abort its context and wait"},
	{"R57", KindTransition, "Generation changed under a Pending or Admitting proposal: Stale"},
	{"R58", KindTransition, "Generation changed under a prepared dispatch: ask the shell to cancel it"},
	{"R59", KindTransition, "Restart while a confirmation is in admission: Pending, admission is volatile"},
	{"R60", KindTransition, "Restart with an executing proposal: OutcomeUnknown, a crash may have followed the send"},
	{"R61", KindTransition, "Retention elapsed for an abandoned outcome: Dropped"},
	{"I1", KindIgnored, "no proposal is stored yet, so the event has nothing to act on"},
	{"I2", KindIgnored, "the proposal is closed (terminal): the event changes nothing"},
	{"I3", KindIgnored, "late or out-of-order reply for an operation that is not executing: no new information, no change"},
	{"I4", KindIgnored, "environment fact with no effect on this situation (hold released, lifted hold, nothing to retry)"},
	{"I5", KindIgnored, "the answer carries no new evidence: the outcome stays as it is"},
	{"I6", KindIgnored, "the live call decides: its own result moves the proposal"},
	{"I7", KindIgnored, "the automatic lookups are over or not due; only an explicit lookup or resolution remains"},
	{"I8", KindIgnored, "the time has not reached expiry"},
	{"I9", KindIgnored, "the stored state already is what the event would make it"},
	{"I10", KindIgnored, "the generation moved but this situation is decided by the result of its own call or lookup"},
	{"V1", KindViolation, "SendStarted with no prepared dispatch: the shell broke its contract, raise an alarm and change nothing"},
}

// RuleByID finds a rule.
func RuleByID(id string) (Rule, bool) {
	for _, r := range Rules {
		if r.ID == id {
			return r, true
		}
	}
	return Rule{}, false
}

// ---------------------------------------------------------------- reducer

// Reduce is the model. It is pure: the same state and event give the same
// state, effects and rule id. It panics only on a state or event type this
// package does not declare, which the type system already forbids outside it.
func Reduce(s State, e Event) (State, []Effect, string) {
	switch st := s.(type) {
	case Unproposed:
		return reduceUnproposed(st, e)
	case Refused:
		return reduceRefused(st, e)
	case Pending:
		return reducePending(st, e)
	case Admitting:
		return reduceAdmitting(st, e)
	case ExecPrepared:
		return reducePrepared(st, e)
	case ExecSent:
		return reduceSent(st, e)
	case OutcomeUnknown:
		return reduceUnknown(st, e)
	case UnknownManual:
		return reduceManual(st, e)
	case Abandoned:
		return reduceAbandoned(st, e)
	case Applied:
		return reduceClosed(st, st.P, e)
	case Rejected:
		return reduceClosed(st, st.P, e)
	case NotApplied:
		return reduceClosed(st, st.P, e)
	case Cancelled:
		return reduceClosed(st, st.P, e)
	case Expired:
		return reduceClosed(st, st.P, e)
	case Stale:
		return reduceClosed(st, st.P, e)
	case Dropped:
		return reduceClosed(st, st.P, e)
	}
	panic(fmt.Sprintf("proposalflow: unknown state %T", s))
}

// move enters next and persists its durable state first, so a crash after it
// always leaves something the recovery can find.
func move(next State, rule string, fx ...Effect) (State, []Effect, string) {
	out := []Effect{}
	if d := next.Durable(); d != "" {
		out = append(out, Persist{To: d})
	}
	return next, append(out, fx...), rule
}

// stay returns the given state and effects without persisting. It is for
// unchanged states and for moves between constructors that share one durable
// state (Pending and Admitting, ExecPrepared and ExecSent).
func stay(s State, rule string, fx ...Effect) (State, []Effect, string) {
	return s, fx, rule
}

// ignore keeps the state and marks the pair ignored under a reason id.
func ignore(s State, rule string) (State, []Effect, string) { return s, nil, rule }

func reply(c ReplyCode) Effect { return Reply{Code: c} }

func unhandled(s State, e Event) string {
	return fmt.Sprintf("proposalflow: %s has no case for %T", s.Name(), e)
}

// violation is V1: SendStarted outside a prepared dispatch.
func violation(s State) (State, []Effect, string) {
	return stay(s, "V1", Alarm{Msg: "send started with no prepared dispatch in " + s.Name()})
}

// ---- Unproposed

func reduceUnproposed(s Unproposed, e Event) (State, []Effect, string) {
	switch ev := e.(type) {
	case Propose:
		action := ActionOf(ev.Tool)
		switch {
		case ev.Held:
			return stay(s, "R1", reply(ReplyUnavailable))
		case ev.Tool == KindUndeclared:
			return move(Refused{Why: RefuseUndeclaredTool}, "R2", reply(ReplyRefused))
		case ev.Tool == KindRead:
			return move(Refused{Why: RefuseNotAMutation}, "R3", reply(ReplyRefused))
		case ev.Role.rank() < action.Required().rank():
			// The claimed action is never read: only the tool kind decides.
			return move(Refused{Why: RefuseInsufficientRole}, "R4", reply(ReplyForbidden))
		case ev.Gen != ev.RuntimeGen:
			return move(Refused{Why: RefuseStaleGeneration}, "R5", reply(ReplyStale))
		case ev.Blocked:
			return stay(s, "R6", reply(ReplyBlocked))
		case ev.ExpiresAt <= ev.Now:
			return stay(s, "R7", reply(ReplyUnavailable))
		}
		p := Prop{ID: ev.ID, Proposer: ev.Actor, ProposerRole: ev.Role, Kind: ev.Tool, Gen: ev.Gen, ExpiresAt: ev.ExpiresAt}
		return move(Pending{P: p}, "R8", reply(ReplyCreated))
	case Confirm, Cancel, OwnerResolve:
		return stay(s, "R22", reply(ReplyForbidden))
	case Tick, AdmitGranted, AdmitRefused, NotSent, ApplyResult, LookupDue, ReceiptResult, HoldAcquired,
		HoldReleased, GenerationChanged, AuthorityLowered, Restart, RetentionElapsed:
		return ignore(s, "I1")
	case SendStarted:
		return violation(s)
	}
	panic(unhandled(s, e))
}

// rank makes Role comparable without relying on the integer order in callers.
func (r Role) rank() int { return int(r) }

// ---- Refused

func reduceRefused(s Refused, e Event) (State, []Effect, string) {
	switch e.(type) {
	case Propose:
		return stay(s, "R9", reply(ReplyExisting))
	case Confirm, Cancel, OwnerResolve:
		return stay(s, "R22", reply(ReplyForbidden))
	case Tick, AdmitGranted, AdmitRefused, NotSent, ApplyResult, LookupDue, ReceiptResult, HoldAcquired,
		HoldReleased, GenerationChanged, AuthorityLowered, Restart, RetentionElapsed:
		return ignore(s, "I1")
	case SendStarted:
		return violation(s)
	}
	panic(unhandled(s, e))
}

// ---- Pending

func reducePending(s Pending, e Event) (State, []Effect, string) {
	p := s.P
	switch ev := e.(type) {
	case Propose:
		return stay(s, "R9", reply(ReplyExisting))
	case Confirm:
		switch {
		case !ev.Human:
			return stay(s, "R11", reply(ReplyForbidden))
		case ev.Actor != p.Proposer:
			return stay(s, "R10", reply(ReplyForbidden))
		case !ev.DigestOK:
			return stay(s, "R12", reply(ReplyChanged))
		case ev.Now >= p.ExpiresAt:
			return move(Expired{P: p}, "R13", reply(ReplyExpired))
		case ev.Role.rank() < p.Action().Required().rank():
			return stay(s, "R14", reply(ReplyForbidden))
		}
		return stay(Admitting{P: p, By: ev.Actor}, "R15", Admit{Action: p.Action()})
	case Cancel:
		if ev.Actor != p.Proposer {
			return stay(s, "R23", reply(ReplyForbidden))
		}
		return move(Cancelled{P: p, Why: CancelByProposer}, "R24", reply(ReplyCancelled))
	case Tick:
		if ev.Now >= p.ExpiresAt {
			return move(Expired{P: p}, "R28")
		}
		return ignore(s, "I8")
	case OwnerResolve:
		return stay(s, "R49", reply(ReplyClosed))
	case HoldAcquired:
		return move(Cancelled{P: p, Why: CancelByHold}, "R51")
	case AuthorityLowered:
		return move(Cancelled{P: p, Why: CancelByAuthority}, "R54")
	case GenerationChanged:
		return move(Stale{P: p}, "R57")
	case Restart:
		return ignore(s, "I9")
	case AdmitGranted, AdmitRefused, NotSent, ApplyResult, LookupDue, ReceiptResult:
		return ignore(s, "I3")
	case HoldReleased, RetentionElapsed:
		return ignore(s, "I4")
	case SendStarted:
		return violation(s)
	}
	panic(unhandled(s, e))
}

// ---- Admitting

func reduceAdmitting(s Admitting, e Event) (State, []Effect, string) {
	p := s.P
	switch ev := e.(type) {
	case Propose:
		return stay(s, "R9", reply(ReplyExisting))
	case Confirm:
		return stay(s, "R16", reply(ReplyBusy))
	case Cancel:
		if ev.Actor != p.Proposer {
			return stay(s, "R23", reply(ReplyForbidden))
		}
		return move(Cancelled{P: p, Why: CancelByProposer}, "R25", reply(ReplyCancelled))
	case Tick:
		if ev.Now >= p.ExpiresAt {
			return move(Expired{P: p}, "R28")
		}
		return ignore(s, "I8")
	case AdmitRefused:
		return stay(Pending{P: p}, "R29", reply(admitReply(ev.Cause)))
	case AdmitGranted:
		switch {
		case ev.Now >= p.ExpiresAt:
			return move(Expired{P: p}, "R30", reply(ReplyExpired))
		case ev.RuntimeGen != p.Gen:
			return move(Stale{P: p}, "R31", reply(ReplyStale))
		}
		// The executing state is durable before the add-on sees anything.
		return move(ExecPrepared{P: p}, "R32", Dispatch{})
	case OwnerResolve:
		return stay(s, "R49", reply(ReplyClosed))
	case HoldAcquired:
		return move(Cancelled{P: p, Why: CancelByHold}, "R51")
	case AuthorityLowered:
		return move(Cancelled{P: p, Why: CancelByAuthority}, "R54")
	case GenerationChanged:
		return move(Stale{P: p}, "R57")
	case Restart:
		return stay(Pending{P: p}, "R59")
	case NotSent, ApplyResult, LookupDue, ReceiptResult:
		return ignore(s, "I3")
	case HoldReleased, RetentionElapsed:
		return ignore(s, "I4")
	case SendStarted:
		return violation(s)
	}
	panic(unhandled(s, e))
}

func admitReply(c AdmitCause) ReplyCode {
	switch c {
	case CauseForbidden:
		return ReplyForbidden
	case CauseBlocked:
		return ReplyBlocked
	case CauseHeld, CauseUnavailable, CauseCaller:
		return ReplyUnavailable
	}
	return ReplyUnavailable
}

// ---- evidence shared by every situation that was sent

// evidence applies the answers that settle a sent proposal. It reports false
// when the event is not evidence.
func evidence(p Prop, e Event) (State, []Effect, string, bool) {
	switch ev := e.(type) {
	case ApplyResult:
		switch ev.Outcome {
		case ApplyCommitted:
			s, fx, r := move(Applied{P: p}, "R37", reply(ReplyApplied))
			return s, fx, r, true
		case ApplyRejected:
			s, fx, r := move(Rejected{P: p, Why: RejectReceipt}, "R38", reply(ReplyRejected))
			return s, fx, r, true
		case ApplyRefused:
			s, fx, r := move(Rejected{P: p, Why: RejectRefused}, "R38", reply(ReplyRejected))
			return s, fx, r, true
		case ApplyConflict:
			s, fx, r := move(Rejected{P: p, Why: RejectConflict}, "R38", reply(ReplyRejected))
			return s, fx, r, true
		case ApplyUnsettled, ApplyTransportError, ApplyTimeout:
			return nil, nil, "", false
		}
	case ReceiptResult:
		switch ev.Answer {
		case ReceiptCommitted:
			s, fx, r := move(Applied{P: p}, "R40")
			return s, fx, r, true
		case ReceiptRejected:
			s, fx, r := move(Rejected{P: p, Why: RejectReceipt}, "R41")
			return s, fx, r, true
		case ReceiptNotCommitted:
			s, fx, r := move(NotApplied{P: p}, "R42")
			return s, fx, r, true
		case ReceiptUnsettled:
			return nil, nil, "", false
		}
	}
	return nil, nil, "", false
}

// ---- ExecPrepared

func reducePrepared(s ExecPrepared, e Event) (State, []Effect, string) {
	p := s.P
	switch ev := e.(type) {
	case SendStarted:
		return stay(ExecSent{P: p}, "R33")
	case NotSent:
		switch ev.Cause {
		case NotSentSlots, NotSentInvalid, NotSentWithdrawn:
			// Finding (a): a refusal before the send is retryable. It must not
			// become an unknown outcome.
			return move(Pending{P: p}, "R34", reply(ReplyUnavailable))
		case NotSentHold:
			return move(Cancelled{P: p, Why: CancelByHold}, "R35", reply(ReplyCancelled))
		case NotSentAuthority:
			return move(Cancelled{P: p, Why: CancelByAuthority}, "R35", reply(ReplyCancelled))
		case NotSentSuperseded:
			return move(Stale{P: p}, "R36", reply(ReplyStale))
		}
		panic(unhandled(s, e))
	case ApplyResult:
		if st, fx, r, ok := evidence(p, e); ok {
			return st, fx, r
		}
		return move(OutcomeUnknown{P: p}, "R39", reply(ReplyUnknown))
	case ReceiptResult:
		if st, fx, r, ok := evidence(p, e); ok {
			return st, fx, r
		}
		return ignore(s, "I5")
	case Propose:
		return stay(s, "R9", reply(ReplyExisting))
	case Confirm:
		return stay(s, "R17", LookupReceipt{}, reply(ReplyUnknown))
	case Cancel:
		return stay(s, "R26", reply(ReplyClosed))
	case OwnerResolve:
		return stay(s, "R49", reply(ReplyClosed))
	case HoldAcquired:
		return stay(s, "R52", CancelDispatch{Why: NotSentHold})
	case AuthorityLowered:
		return stay(s, "R55", CancelDispatch{Why: NotSentAuthority})
	case GenerationChanged:
		return stay(s, "R58", CancelDispatch{Why: NotSentSuperseded})
	case Restart:
		return move(OutcomeUnknown{P: p}, "R60")
	case Tick:
		return ignore(s, "I8")
	case AdmitGranted, AdmitRefused:
		return ignore(s, "I3")
	case LookupDue:
		return ignore(s, "I6")
	case HoldReleased, RetentionElapsed:
		return ignore(s, "I4")
	}
	panic(unhandled(s, e))
}

// ---- ExecSent

func reduceSent(s ExecSent, e Event) (State, []Effect, string) {
	p := s.P
	switch e.(type) {
	case ApplyResult:
		if st, fx, r, ok := evidence(p, e); ok {
			return st, fx, r
		}
		return move(OutcomeUnknown{P: p}, "R39", reply(ReplyUnknown))
	case ReceiptResult:
		if st, fx, r, ok := evidence(p, e); ok {
			return st, fx, r
		}
		return ignore(s, "I5")
	case Propose:
		return stay(s, "R9", reply(ReplyExisting))
	case Confirm:
		return stay(s, "R17", LookupReceipt{}, reply(ReplyUnknown))
	case Cancel:
		return stay(s, "R26", reply(ReplyClosed))
	case OwnerResolve:
		return stay(s, "R49", reply(ReplyClosed))
	case HoldAcquired:
		return stay(s, "R53", CancelSend{})
	case AuthorityLowered:
		return stay(s, "R56", CancelSend{})
	case Restart:
		return move(OutcomeUnknown{P: p}, "R60")
	case GenerationChanged:
		return ignore(s, "I10")
	case Tick:
		return ignore(s, "I8")
	case AdmitGranted, AdmitRefused, NotSent:
		return ignore(s, "I3")
	case LookupDue:
		return ignore(s, "I6")
	case SendStarted:
		return ignore(s, "I9")
	case HoldReleased, RetentionElapsed:
		return ignore(s, "I4")
	}
	panic(unhandled(s, e))
}

// ---- OutcomeUnknown

func reduceUnknown(s OutcomeUnknown, e Event) (State, []Effect, string) {
	p := s.P
	switch ev := e.(type) {
	case ApplyResult:
		if st, fx, r, ok := evidence(p, e); ok {
			return st, fx, r
		}
		return ignore(s, "I5")
	case ReceiptResult:
		if st, fx, r, ok := evidence(p, e); ok {
			return st, fx, r
		}
		n := s.Lookups + 1
		if n >= MaxLookups {
			return move(UnknownManual{P: p}, "R44")
		}
		return move(OutcomeUnknown{P: p, Lookups: n}, "R43")
	case LookupDue:
		return stay(s, "R45", LookupReceipt{})
	case Confirm:
		return stay(s, "R17", LookupReceipt{}, reply(ReplyUnknown))
	case OwnerResolve:
		return resolve(s, p, ev)
	case Propose:
		return stay(s, "R9", reply(ReplyExisting))
	case Cancel:
		return stay(s, "R26", reply(ReplyClosed))
	case GenerationChanged:
		return ignore(s, "I10")
	case Restart:
		return ignore(s, "I9")
	case Tick:
		return ignore(s, "I8")
	case AdmitGranted, AdmitRefused, NotSent:
		return ignore(s, "I3")
	case HoldAcquired, HoldReleased, AuthorityLowered, RetentionElapsed:
		return ignore(s, "I4")
	case SendStarted:
		return violation(s)
	}
	panic(unhandled(s, e))
}

// resolve is the owner's resolution of an unresolved outcome.
func resolve(s State, p Prop, ev OwnerResolve) (State, []Effect, string) {
	switch {
	case ev.Role != RoleOwner || !ev.Human || !ev.Reason:
		return stay(s, "R46", reply(ReplyForbidden))
	case ev.Held:
		return stay(s, "R47", reply(ReplyUnavailable))
	}
	// One last lookup runs first: a final receipt that arrives afterwards
	// still settles the abandoned outcome.
	return move(Abandoned{P: p}, "R48", LookupReceipt{}, reply(ReplyResolved))
}

// ---- UnknownManual

func reduceManual(s UnknownManual, e Event) (State, []Effect, string) {
	p := s.P
	switch ev := e.(type) {
	case ApplyResult:
		if st, fx, r, ok := evidence(p, e); ok {
			return st, fx, r
		}
		return ignore(s, "I5")
	case ReceiptResult:
		if st, fx, r, ok := evidence(p, e); ok {
			return st, fx, r
		}
		return ignore(s, "I5")
	case LookupDue:
		return ignore(s, "I7")
	case Confirm:
		return stay(s, "R17", LookupReceipt{}, reply(ReplyUnknown))
	case OwnerResolve:
		return resolve(s, p, ev)
	case Propose:
		return stay(s, "R9", reply(ReplyExisting))
	case Cancel:
		return stay(s, "R26", reply(ReplyClosed))
	case GenerationChanged:
		return ignore(s, "I10")
	case Restart:
		return ignore(s, "I9")
	case Tick:
		return ignore(s, "I8")
	case AdmitGranted, AdmitRefused, NotSent:
		return ignore(s, "I3")
	case HoldAcquired, HoldReleased, AuthorityLowered, RetentionElapsed:
		return ignore(s, "I4")
	case SendStarted:
		return violation(s)
	}
	panic(unhandled(s, e))
}

// ---- Abandoned

func reduceAbandoned(s Abandoned, e Event) (State, []Effect, string) {
	p := s.P
	switch e.(type) {
	case ApplyResult:
		if st, fx, r, ok := evidence(p, e); ok {
			return st, fx, r
		}
		return ignore(s, "I5")
	case ReceiptResult:
		if st, fx, r, ok := evidence(p, e); ok {
			return st, fx, r
		}
		return ignore(s, "I5")
	case LookupDue:
		return stay(s, "R45", LookupReceipt{})
	case RetentionElapsed:
		return move(Dropped{P: p}, "R61")
	case Confirm:
		return stay(s, "R17", LookupReceipt{}, reply(ReplyUnknown))
	case OwnerResolve:
		return stay(s, "R50", reply(ReplyResolved))
	case Propose:
		return stay(s, "R9", reply(ReplyExisting))
	case Cancel:
		return stay(s, "R26", reply(ReplyClosed))
	case GenerationChanged:
		return ignore(s, "I10")
	case Restart:
		return ignore(s, "I9")
	case Tick:
		return ignore(s, "I8")
	case AdmitGranted, AdmitRefused, NotSent:
		return ignore(s, "I3")
	case HoldAcquired, HoldReleased, AuthorityLowered:
		return ignore(s, "I4")
	case SendStarted:
		return violation(s)
	}
	panic(unhandled(s, e))
}

// ---- terminal situations

// reduceClosed handles every closed proposal. The stored answer depends on
// which closed state it is; everything else is ignored with a reason.
func reduceClosed(s State, p Prop, e Event) (State, []Effect, string) {
	switch e.(type) {
	case Propose:
		return stay(s, "R9", reply(ReplyExisting))
	case Confirm:
		switch s.(type) {
		case Applied:
			return stay(s, "R18", reply(ReplyApplied))
		case Rejected, NotApplied, Stale:
			return stay(s, "R19", reply(ReplyRejected))
		case Cancelled, Dropped:
			return stay(s, "R20", reply(ReplyClosed))
		case Expired:
			return stay(s, "R21", reply(ReplyExpired))
		}
		panic(unhandled(s, e))
	case Cancel:
		if _, ok := s.(Cancelled); ok {
			return stay(s, "R27", reply(ReplyCancelled))
		}
		return stay(s, "R26", reply(ReplyClosed))
	case OwnerResolve:
		switch s.(type) {
		case Applied, Rejected, NotApplied, Stale:
			return stay(s, "R50", reply(ReplyResolved))
		case Cancelled, Expired, Dropped:
			return stay(s, "R49", reply(ReplyClosed))
		}
		panic(unhandled(s, e))
	case AdmitGranted, AdmitRefused, NotSent, ApplyResult, ReceiptResult:
		return ignore(s, "I3")
	case Tick, LookupDue, RetentionElapsed, Restart:
		return ignore(s, "I2")
	case HoldAcquired, HoldReleased, GenerationChanged, AuthorityLowered:
		return ignore(s, "I2")
	case SendStarted:
		return violation(s)
	}
	panic(unhandled(s, e))
}
