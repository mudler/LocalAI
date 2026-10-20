// SPDX-License-Identifier: MIT
// Package toolbroker models host tool authorization. It does not execute tools,
// persist approvals, or prove external-provider fencing. Host evidence is trusted
// input; only the later shell can authenticate it.
package toolbroker

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
)

var ErrInvalid = errors.New("toolbroker: invalid model input")
var ErrInvariant = errors.New("toolbroker: model invariant violated")

// Embedding redacted also protects future nested default formatting paths.
type redacted struct{}

func (redacted) String() string               { return "toolbroker:[private]" }
func (redacted) GoString() string             { return "toolbroker:[private]" }
func (redacted) MarshalJSON() ([]byte, error) { return []byte(`"toolbroker:[private]"`), nil }

type Scope struct {
	Principal, Conversation, Turn string
	Attempt, Epoch                uint64
}
type ToolID string
type Revisions struct{ Catalog, Config, Authorization, Resource, Proposal, ParentGrant, Taint string }
type Binding struct {
	redacted
	scope       Scope
	request     string
	tool        ToolID
	arguments   string
	revisions   Revisions
	providerKey string
	digest      [32]byte
}

// Exact argument bytes are retained as an immutable string, not canonicalized JSON.
// Nil and empty arguments share the empty-byte binding. Nonempty bytes stay exact.
// Length-delimited JSON tuple encoding prevents concatenation ambiguity.
func NewBinding(s Scope, r string, t ToolID, args []byte, v Revisions, key string) (Binding, error) {
	if s.Principal == "" || s.Conversation == "" || s.Turn == "" || s.Attempt == 0 || s.Epoch == 0 || r == "" || t == "" {
		return Binding{}, ErrInvalid
	}
	b := Binding{scope: s, request: r, tool: t, arguments: string(args), revisions: v, providerKey: key}
	data, _ := json.Marshal([]any{"toolbroker-binding/v1", s, r, t, b.Arguments(), v, key})
	b.digest = sha256.Sum256(data)
	return b, nil
}
func (b Binding) Arguments() []byte   { return []byte(b.arguments) }
func (b Binding) Scope() Scope        { return b.scope }
func (b Binding) RequestID() string   { return b.request }
func (b Binding) ToolID() ToolID      { return b.tool }
func (b Binding) Digest() [32]byte    { return b.digest }
func (b Binding) ProviderKey() string { return b.providerKey }
func (b Binding) valid() bool {
	c, e := NewBinding(b.scope, b.request, b.tool, b.Arguments(), b.revisions, b.providerKey)
	return e == nil && c == b
}

type Output struct {
	redacted
	data string
}

func NewOutput(b []byte) Output          { return Output{data: string(b)} }
func (o Output) Bytes() []byte           { return []byte(o.data) }
func (o Output) equal(other Output) bool { return bytes.Equal(o.Bytes(), other.Bytes()) }

type PolicyMode uint8

const (
	RequireApproval PolicyMode = iota
	DenyContinue
	Stop
	AllowAll
)

type ModelConfig struct {
	redacted
	Binding              Binding
	Policy               PolicyMode
	ApprovalTTL          int64
	ExternalTaintedWrite bool
}
type Actor uint8

const (
	HumanActor Actor = iota + 1
	HostActor
)

type Approval struct {
	redacted
	Binding   Binding
	Actor     Actor
	ActorID   string
	ExpiresAt int64
}
type Admission struct{ Current, Registered, Selected, Resource, Helper bool }
type RecordStatus uint8

const (
	RecordPrepared RecordStatus = iota + 1
	RecordDispatched
	RecordCompleted
	RecordUnknown
	RecordConflict
)

type Input struct {
	redacted
	Binding Binding
	Now     int64
}
type Phase interface{ isPhase() }
type admissionPurpose uint8

const (
	initialAdmission admissionPurpose = iota
	invocationAdmission
)

type claimStage uint8

const (
	awaitingPrepare claimStage = iota
	awaitingDispatch
)

type executionStage uint8

const (
	runningBody executionStage = iota
	awaitingJournal
)

type New struct{}

func (New) isPhase() {}

type Checking struct {
	purpose admissionPurpose
	until   int64
}

func (Checking) isPhase() {}

type WaitingApproval struct{ expires int64 }

func (WaitingApproval) isPhase() {}

type Authorized struct{ until int64 }

func (Authorized) isPhase() {}

type Claiming struct {
	stage claimStage
	until int64
}

func (Claiming) isPhase() {}

type Claimed struct{ until int64 }

func (Claimed) isPhase() {}

type Executing struct{ stage executionStage }

func (Executing) isPhase() {}

type Completed struct{}

func (Completed) isPhase() {}

type Denied struct{}

func (Denied) isPhase() {}

type Interrupted struct{}

func (Interrupted) isPhase() {}

type Unknown struct{}

func (Unknown) isPhase() {}
func PhaseName(p Phase) string {
	switch p.(type) {
	case New:
		return "New"
	case Checking:
		return "Checking"
	case WaitingApproval:
		return "WaitingApproval"
	case Authorized:
		return "Authorized"
	case Claiming:
		return "Claiming"
	case Claimed:
		return "Claimed"
	case Executing:
		return "Executing"
	case Completed:
		return "Completed"
	case Denied:
		return "Denied"
	case Interrupted:
		return "Interrupted"
	case Unknown:
		return "Unknown"
	}
	panic("toolbroker: unregistered phase")
}

type State struct {
	redacted
	config           ModelConfig
	phase            Phase
	dispatches, runs uint8
	result           Output
	lastTime         int64
}

func NewState(c ModelConfig) State { return State{config: c, phase: New{}} }
func (s State) Phase() Phase       { return s.phase }
func (s State) Binding() Binding   { return s.config.Binding }
func (s State) Result() Output     { return s.result }

type Event interface {
	isEvent()
	eventInput() Input
}
type Requested struct{ Input }

func (Requested) isEvent()            {}
func (e Requested) eventInput() Input { return e.Input }

type AdmissionPassed struct {
	Input
	Evidence Admission
}

func (AdmissionPassed) isEvent()            {}
func (e AdmissionPassed) eventInput() Input { return e.Input }

type AdmissionRejected struct{ Input }

func (AdmissionRejected) isEvent()            {}
func (e AdmissionRejected) eventInput() Input { return e.Input }

type ApprovalGranted struct {
	Input
	Approval Approval
}

func (ApprovalGranted) isEvent()            {}
func (e ApprovalGranted) eventInput() Input { return e.Input }

type ApprovalDenied struct{ Input }

func (ApprovalDenied) isEvent()            {}
func (e ApprovalDenied) eventInput() Input { return e.Input }

type Expired struct{ Input }

func (Expired) isEvent()            {}
func (e Expired) eventInput() Input { return e.Input }

type Cancelled struct{ Input }

func (Cancelled) isEvent()            {}
func (e Cancelled) eventInput() Input { return e.Input }

type AccessRevoked struct{ Input }

func (AccessRevoked) isEvent()            {}
func (e AccessRevoked) eventInput() Input { return e.Input }

type LeaseLost struct{ Input }

func (LeaseLost) isEvent()            {}
func (e LeaseLost) eventInput() Input { return e.Input }

type AttemptReplaced struct{ Input }

func (AttemptReplaced) isEvent()            {}
func (e AttemptReplaced) eventInput() Input { return e.Input }

type ClaimRequested struct{ Input }

func (ClaimRequested) isEvent()            {}
func (e ClaimRequested) eventInput() Input { return e.Input }

type PreparedObserved struct {
	Input
	Status RecordStatus
	Output Output
}

func (PreparedObserved) isEvent()            {}
func (e PreparedObserved) eventInput() Input { return e.Input }

type DispatchGranted struct {
	Input
	Fresh bool
}

func (DispatchGranted) isEvent()            {}
func (e DispatchGranted) eventInput() Input { return e.Input }

type DispatchRejected struct{ Input }

func (DispatchRejected) isEvent()            {}
func (e DispatchRejected) eventInput() Input { return e.Input }

type InvokeRequested struct{ Input }

func (InvokeRequested) isEvent()            {}
func (e InvokeRequested) eventInput() Input { return e.Input }

type ExecutionStarted struct{ Input }

func (ExecutionStarted) isEvent()            {}
func (e ExecutionStarted) eventInput() Input { return e.Input }

type ExecutionCompleted struct {
	Input
	Output Output
}

func (ExecutionCompleted) isEvent()            {}
func (e ExecutionCompleted) eventInput() Input { return e.Input }

type ExecutionUncertain struct{ Input }

func (ExecutionUncertain) isEvent()            {}
func (e ExecutionUncertain) eventInput() Input { return e.Input }

type JournalCompleted struct{ Input }

func (JournalCompleted) isEvent()            {}
func (e JournalCompleted) eventInput() Input { return e.Input }

type JournalFailed struct{ Input }

func (JournalFailed) isEvent()            {}
func (e JournalFailed) eventInput() Input { return e.Input }

type RecoveryObserved struct {
	Input
	Status RecordStatus
	Output Output
}

func (RecoveryObserved) isEvent()            {}
func (e RecoveryObserved) eventInput() Input { return e.Input }
func EventName(e Event) string {
	switch e.(type) {
	case Requested:
		return "Requested"
	case AdmissionPassed:
		return "AdmissionPassed"
	case AdmissionRejected:
		return "AdmissionRejected"
	case ApprovalGranted:
		return "ApprovalGranted"
	case ApprovalDenied:
		return "ApprovalDenied"
	case Expired:
		return "Expired"
	case Cancelled:
		return "Cancelled"
	case AccessRevoked:
		return "AccessRevoked"
	case LeaseLost:
		return "LeaseLost"
	case AttemptReplaced:
		return "AttemptReplaced"
	case ClaimRequested:
		return "ClaimRequested"
	case PreparedObserved:
		return "PreparedObserved"
	case DispatchGranted:
		return "DispatchGranted"
	case DispatchRejected:
		return "DispatchRejected"
	case InvokeRequested:
		return "InvokeRequested"
	case ExecutionStarted:
		return "ExecutionStarted"
	case ExecutionCompleted:
		return "ExecutionCompleted"
	case ExecutionUncertain:
		return "ExecutionUncertain"
	case JournalCompleted:
		return "JournalCompleted"
	case JournalFailed:
		return "JournalFailed"
	case RecoveryObserved:
		return "RecoveryObserved"
	}
	panic("toolbroker: unregistered event")
}

type Effect interface{ isEffect() }
type CheckAdmission struct {
	redacted
	Binding Binding
	Output  Output
}

func (CheckAdmission) isEffect() {}

type RequestApproval struct {
	redacted
	Binding Binding
	Output  Output
}

func (RequestApproval) isEffect() {}

type RecordDecision struct {
	redacted
	Binding Binding
	Output  Output
}

func (RecordDecision) isEffect() {}

type PrepareJournal struct {
	redacted
	Binding Binding
	Output  Output
}

func (PrepareJournal) isEffect() {}

type DispatchJournal struct {
	redacted
	Binding Binding
	Output  Output
}

func (DispatchJournal) isEffect() {}

type RecheckExecutionAdmission struct {
	redacted
	Binding Binding
	Output  Output
}

func (RecheckExecutionAdmission) isEffect() {}

type RunExecutor struct {
	redacted
	Binding Binding
	Output  Output
}

func (RunExecutor) isEffect() {}

type CompleteJournal struct {
	redacted
	Binding Binding
	Output  Output
}

func (CompleteJournal) isEffect() {}

type CancelExecutor struct {
	redacted
	Binding Binding
	Output  Output
}

func (CancelExecutor) isEffect() {}

type ReturnRecordedResult struct {
	redacted
	Binding Binding
	Output  Output
}

func (ReturnRecordedResult) isEffect() {}

type ReturnDenied struct {
	redacted
	Binding Binding
	Output  Output
}

func (ReturnDenied) isEffect() {}

type HoldForReconciliation struct {
	redacted
	Binding Binding
	Output  Output
}

func (HoldForReconciliation) isEffect() {}

type NotifyInterrupted struct {
	redacted
	Binding Binding
	Output  Output
}

func (NotifyInterrupted) isEffect() {}

type Transition struct {
	redacted
	State         State
	Effects       []Effect
	RuleID, Guard string
}

func BindingMismatchWitnesses(b Binding) []Binding {
	out := []Binding{}
	add := func(s Scope, r string, t ToolID, a string, v Revisions, k string) {
		c, _ := NewBinding(s, r, t, []byte(a), v, k)
		out = append(out, c)
	}
	for i := 0; i < 5; i++ {
		s := b.scope
		switch i {
		case 0:
			s.Principal += "2"
		case 1:
			s.Conversation += "2"
		case 2:
			s.Turn += "2"
		case 3:
			s.Attempt++
		case 4:
			s.Epoch++
		}
		add(s, b.request, b.tool, b.arguments, b.revisions, b.providerKey)
	}
	add(b.scope, b.request+"2", b.tool, b.arguments, b.revisions, b.providerKey)
	add(b.scope, b.request, b.tool+"2", b.arguments, b.revisions, b.providerKey)
	add(b.scope, b.request, b.tool, b.arguments+" ", b.revisions, b.providerKey)
	for i := 0; i < 7; i++ {
		v := b.revisions
		switch i {
		case 0:
			v.Catalog += "2"
		case 1:
			v.Config += "2"
		case 2:
			v.Authorization += "2"
		case 3:
			v.Resource += "2"
		case 4:
			v.Proposal += "2"
		case 5:
			v.ParentGrant += "2"
		case 6:
			v.Taint += "2"
		}
		add(b.scope, b.request, b.tool, b.arguments, v, b.providerKey)
	}
	add(b.scope, b.request, b.tool, b.arguments, b.revisions, b.providerKey+"2")
	return out
}
func stateKey(s State) string {
	return fmt.Sprintf("%s/%#v/%d/%d/%d/%x", PhaseName(s.phase), s.phase, s.dispatches, s.runs, s.lastTime, sha256.Sum256(s.result.Bytes()))
}
