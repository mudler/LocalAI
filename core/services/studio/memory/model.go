// SPDX-License-Identifier: MIT
// Package memory models capture only. Effects require an atomic fenced shell;
// this pure model does not establish backend atomicity or implement deletion.
package memory

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
)

type State interface{ isState() }
type Event interface{ isEvent() }
type Effect interface{ isEffect() }
type Input interface{ isInput() }
type ContentInput struct{ Text string }
type SummaryInput struct{ Text string }
type FrozenPayload struct{ Text, Hash string }

// PendingPayload freezes produced text without claiming durable acknowledgment.
type PendingPayload struct{ Payload FrozenPayload }

func (PendingPayload) isInput() {}
func (ContentInput) isInput()   {}
func (SummaryInput) isInput()   {}
func (FrozenPayload) isInput()  {}

type Identity struct{ Principal, Destination, Agent, Chat, Source, Kind, Origin string }

func (i Identity) Key() string { b, _ := json.Marshal(i); return digest(string(b)) }
func digest(s string) string   { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

type Job struct {
	Identity   Identity
	Input      Input
	Attempts   int
	Generation int
	Expires    int64
}
type Envelope struct {
	Key, Owner string
	Generation int
	Operation  string
	Now        int64
}
type Work struct {
	Job      Job
	Fence    Envelope
	Deadline int64
}
type Queued struct{ Job Job }
type Authorizing struct{ Work Work }
type Transforming struct{ Work Work }
type PersistingPayload struct {
	Work    Work
	Payload FrozenPayload
}
type Writing struct{ Work Work }
type RetryWaiting struct {
	Job Job
	Due int64
}
type Saved struct {
	Job     Job
	EntryID string
}
type Failed struct {
	Job    Job
	Reason string
}
type Skipped struct {
	Job    Job
	Reason string
}

func (Queued) isState()            {}
func (Authorizing) isState()       {}
func (Transforming) isState()      {}
func (PersistingPayload) isState() {}
func (Writing) isState()           {}
func (RetryWaiting) isState()      {}
func (Saved) isState()             {}
func (Failed) isState()            {}
func (Skipped) isState()           {}

type ClaimGranted struct{ Envelope }

func (ClaimGranted) isEvent() {}

type ClaimDenied struct{ Envelope }

func (ClaimDenied) isEvent() {}

type AuthorizationGranted struct{ Envelope }

func (AuthorizationGranted) isEvent() {}

type AuthorizationDenied struct{ Envelope }

func (AuthorizationDenied) isEvent() {}

type SummaryFailed struct{ Envelope }

func (SummaryFailed) isEvent() {}

type PayloadPersisted struct{ Envelope }

func (PayloadPersisted) isEvent() {}

type PayloadPersistFailed struct{ Envelope }

func (PayloadPersistFailed) isEvent() {}

type WriteFailed struct{ Envelope }

func (WriteFailed) isEvent() {}

type Timeout struct{ Envelope }

func (Timeout) isEvent() {}

type RetryTick struct{ Envelope }

func (RetryTick) isEvent() {}

type Revoked struct{ Envelope }

func (Revoked) isEvent() {}

type DuplicateDelivered struct{ Envelope }

func (DuplicateDelivered) isEvent() {}

type RestartRecovered struct{ Envelope }

func (RestartRecovered) isEvent() {}

type AuthorizedRetry struct{ Envelope }

func (AuthorizedRetry) isEvent() {}

type WriteDeleted struct{ Envelope }

func (WriteDeleted) isEvent() {}

type EntryDeleted struct{ Envelope }

func (EntryDeleted) isEvent() {}

type SummarySucceeded struct {
	Envelope
	Text string
}

func (SummarySucceeded) isEvent() {}

type WriteSucceeded struct {
	Envelope
	EntryID string
}

func (WriteSucceeded) isEvent() {}

type CheckAuthorization struct{ Work Work }

func (CheckAuthorization) isEffect() {}

type Summarize struct{ Work Work }

func (Summarize) isEffect() {}

type ReconcileOrWrite struct{ Work Work }

func (ReconcileOrWrite) isEffect() {}

type PersistPayload struct {
	Work    Work
	Payload FrozenPayload
}

func (PersistPayload) isEffect() {}

type ScheduleRetry struct {
	Key string
	Due int64
}

func (ScheduleRetry) isEffect() {}

type PublishStatus struct{ Key, Status string }

func (PublishStatus) isEffect() {}

type Transition struct {
	State                 State
	Effects               []Effect
	RuleID, IgnoredReason string
}

func Initial(source string, input Input) State {
	return Queued{Job{Identity{"principal", "collection", "agent", "chat", source, "incoming", "direct"}, input, 0, 0, 10000}}
}
func stateName(s State) string { return reflect.TypeOf(s).Name() }
func effectNames(e []Effect) []string {
	out := []string{}
	for _, v := range e {
		out = append(out, reflect.TypeOf(v).Name())
	}
	return out
}
func jobOf(s State) Job {
	switch v := s.(type) {
	case Queued:
		return v.Job
	case Authorizing:
		return v.Work.Job
	case Transforming:
		return v.Work.Job
	case PersistingPayload:
		return v.Work.Job
	case Writing:
		return v.Work.Job
	case RetryWaiting:
		return v.Job
	case Saved:
		return v.Job
	case Failed:
		return v.Job
	case Skipped:
		return v.Job
	}
	panic(fmt.Sprintf("unregistered state %T", s))
}
func workOf(s State) (Work, bool) {
	switch v := s.(type) {
	case Authorizing:
		return v.Work, true
	case Transforming:
		return v.Work, true
	case PersistingPayload:
		return v.Work, true
	case Writing:
		return v.Work, true
	}
	return Work{}, false
}
func envelopeOfState(s State) Envelope {
	if w, ok := workOf(s); ok {
		return w.Fence
	}
	j := jobOf(s)
	return Envelope{j.Identity.Key(), "frontend-a", j.Generation + 1, "authorize", 0}
}
func sampleEnvelope() Envelope {
	return envelopeOfState(Initial("source-a", ContentInput{Text: "hello"}))
}
func terminal(s State) bool {
	switch s.(type) {
	case Saved, Failed, Skipped:
		return true
	}
	return false
}
func nextWork(w Work, phase string, now int64) Work {
	w.Fence.Operation = w.Fence.Operation + "/" + phase
	w.Fence.Now = now
	w.Deadline = now + 60
	return w
}
func eventEnvelope(e Event) Envelope {
	switch v := e.(type) {
	case ClaimGranted:
		return v.Envelope
	case ClaimDenied:
		return v.Envelope
	case AuthorizationGranted:
		return v.Envelope
	case AuthorizationDenied:
		return v.Envelope
	case SummarySucceeded:
		return v.Envelope
	case SummaryFailed:
		return v.Envelope
	case PayloadPersisted:
		return v.Envelope
	case PayloadPersistFailed:
		return v.Envelope
	case WriteSucceeded:
		return v.Envelope
	case WriteFailed:
		return v.Envelope
	case Timeout:
		return v.Envelope
	case RetryTick:
		return v.Envelope
	case Revoked:
		return v.Envelope
	case DuplicateDelivered:
		return v.Envelope
	case RestartRecovered:
		return v.Envelope
	case AuthorizedRetry:
		return v.Envelope
	case WriteDeleted:
		return v.Envelope
	case EntryDeleted:
		return v.Envelope
	}
	panic(fmt.Sprintf("unregistered event %T", e))
}
