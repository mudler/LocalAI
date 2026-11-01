// SPDX-License-Identifier: MIT

// Package music models request-local music workflows without executing effects.
package music

// All workflow data is value data: accepted ABC bytes are never normalized.
type operation uint8

const (
	transcription operation = iota + 1
	generation
	cover
)

type facts struct {
	Authorized, Capable, IdentityMatches, ContextFits bool
	Model                                             string
}

func (f facts) permits() bool {
	return f.Authorized && f.Capable && f.IdentityMatches && f.ContextFits && f.Model != ""
}

type request struct {
	ID, Principal                             string
	Operation                                 operation
	Transcription, Generation                 facts
	Feature, Quota, InputsValid, StorageReady bool
	ABC                                       string
	Deadline, Now                             int64
}
type stage uint8

const (
	transcribe stage = iota + 1
	generate
)

type phase interface{ isPhase() }
type admitting struct{}

func (admitting) isPhase() {}

type running struct{}

func (running) isPhase() {}

type settling struct{ Outcome outcome }

func (settling) isPhase() {}

type cleaningInput struct{}

func (cleaningInput) isPhase() {}

type rechecking struct{}

func (rechecking) isPhase() {}

type fetching struct{}

func (fetching) isPhase() {}

type preparing struct{}

func (preparing) isPhase() {}

type committing struct{}

func (committing) isPhase() {}

type outcome struct {
	ABC    string
	Failed bool
}

// The ledger distinguishes admission outstanding from an acquired lane. Neither
// results nor cancellation imply that a backend has stopped.
type ownership uint8

const (
	unowned ownership = iota
	admissionPending
	laneOwned
	stopPending
	admissionStopPending
)

type session struct {
	Request        request
	ABC, ABCSource string
	Stage          stage
	Phase          phase
	Ownership      ownership
	// Runs retains issued-run authority across the two stages. Artifact IDs
	// are runner-assigned bounded handles, scoped to their issuing stage.
	Runs    uint8
	Cleaned [3]uint16
}
type State interface{ isState() }
type Validated struct{ Session session }

func (Validated) isState() {}

type Transcribing struct{ Session session }

func (Transcribing) isState() {}

type Generating struct{ Session session }

func (Generating) isState() {}

type Succeeded struct {
	Session session
	Output  string
}

func (Succeeded) isState() {}

type Failed struct {
	Session session
	Code    string
}

func (Failed) isState() {}

type Cancelled struct {
	Session session
	Reason  string
}

func (Cancelled) isState() {}

// Correlation identifies the request and the stage's fixed, bounded operation ID.
type correlation struct {
	Request string
	Stage   stage
	ID      string
}
type Event interface{ isEvent() }
type Start struct{}

func (Start) isEvent() {}

type Admitted struct{ correlation }

func (Admitted) isEvent() {}

type AdmissionDenied struct{ correlation }

func (AdmissionDenied) isEvent() {}

type TaskCompleted struct {
	correlation
	ABC      string
	Valid    bool
	Artifact uint8
}

func (TaskCompleted) isEvent() {}

type TaskFailed struct{ correlation }

func (TaskFailed) isEvent() {}

type StageSettled struct{ correlation }

func (StageSettled) isEvent() {}

type InputCleaned struct{ correlation }

func (InputCleaned) isEvent() {}

type GenerationChecked struct {
	correlation
	Facts facts
}

func (GenerationChecked) isEvent() {}

type OutputFetched struct {
	correlation
	Valid bool
}

func (OutputFetched) isEvent() {}

type OutputPrepared struct {
	correlation
	Valid bool
}

func (OutputPrepared) isEvent() {}

type OutputPublished struct {
	correlation
	Output string
}

func (OutputPublished) isEvent() {}

type EffectFailed struct{ correlation }

func (EffectFailed) isEvent() {}

type Cancel struct{}

func (Cancel) isEvent() {}

type DeadlineExpired struct{ Now int64 }

func (DeadlineExpired) isEvent() {}

type CleanupAcknowledged struct{ correlation }

func (CleanupAcknowledged) isEvent() {}

type Effect interface{ isEffect() }
type command struct {
	Request string
	Stage   stage
	ID      string
}
type Admit struct {
	command
	Model string
}

func (Admit) isEffect() {}

type Run struct {
	command
	ABC string
}

func (Run) isEffect() {}

type Stop struct{ command }

func (Stop) isEffect() {}

type CleanInput struct{ command }

func (CleanInput) isEffect() {}

type CheckGeneration struct{ command }

func (CheckGeneration) isEffect() {}

type FetchValidate struct{ command }

func (FetchValidate) isEffect() {}

type PreparePublication struct{ command }

func (PreparePublication) isEffect() {}

// CommitPublication must use the same serialized authority as Reduce. A shell
// must not commit independently and then deliver an unordered notification.
type CommitPublication struct{ command }

func (CommitPublication) isEffect() {}

// Revoke installs a persistent request-scoped tombstone before acknowledging.
// Outstanding fetch/prepare operations must discard late resources under that
// authority; it is not a one-shot deletion of currently visible files.
type Revoke struct{ command }

func (Revoke) isEffect() {}

type CleanArtifact struct {
	command
	Artifact uint8
}

func (CleanArtifact) isEffect() {}

type Respond struct{ command }

func (Respond) isEffect() {}

type Transition struct {
	State       State
	Effects     []Effect
	Rule, Guard string
}
