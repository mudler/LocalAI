// SPDX-License-Identifier: MIT
// Package executionbridge models host interception decisions only. Facts and
// effects are not capabilities: no persistence, broker, or native body is wired.
package executionbridge

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"strings"
	"unicode/utf8"
)

type redacted struct{}

func (redacted) Format(f fmt.State, _ rune)   { _, _ = io.WriteString(f, "[executionbridge:redacted]") }
func (redacted) MarshalJSON() ([]byte, error) { return []byte(`"[executionbridge:redacted]"`), nil }

// Closure leaves prevent fmt's private-container/invalid-verb fallback from
// traversing identities. Accessors return values; no mutable buffer is exposed.
// This is a diagnostic boundary, not protection against unsafe reflection.
type Call struct {
	redacted
	open func() callFields
}
type callFields struct {
	Principal, Conversation, Turn          string
	Attempt, Epoch                         uint64
	Invocation, Protocol, Helper, Tool     string
	Binding, Provenance, Projection        [32]byte
	Revision, AuthRevision, ConfigRevision uint64
	Codec, TTL                             uint64
	Mode                                   uint8
	Tainted                                bool
}

func ownCall(f callFields) Call { return Call{open: func() callFields { return f }} }
func (c Call) fields() callFields {
	if c.open == nil {
		return callFields{}
	}
	return c.open()
}
func (c Call) equal(d Call) bool { return c.fields() == d.fields() }
func identifier(s string) bool {
	return s != "" && len(s) <= 1024 && utf8.ValidString(s) && !strings.ContainsRune(s, 0)
}
func number(n uint64) bool { return n > 0 && n <= math.MaxInt64 }
func (c Call) valid() bool {
	f := c.fields()
	return identifier(f.Principal) && identifier(f.Conversation) && identifier(f.Turn) && number(f.Attempt) && number(f.Epoch) && identifier(f.Invocation) && identifier(f.Protocol) && identifier(f.Helper) && identifier(f.Tool) && number(f.Revision) && number(f.AuthRevision) && number(f.ConfigRevision) && number(f.Codec) && number(f.TTL) && f.Mode > 0 && f.Mode <= 2 && f.Binding != [32]byte{} && f.Provenance != [32]byte{} && f.Projection != [32]byte{}
}

var errInvalid = errors.New("execution bridge model: invalid or unavailable")

// RequestID is correlation only. The model's domain is bounded identifiers
// (1..1024 UTF-8 bytes, no NUL) and positive signed-64-bit attempts/epochs.
// Immutable admission metadata deliberately does not enter this origin tuple.
func RequestID(c Call) (string, error) {
	f := c.fields()
	if !identifier(f.Principal) || !identifier(f.Conversation) || !identifier(f.Turn) || !identifier(f.Invocation) || !number(f.Attempt) || !number(f.Epoch) {
		return "", errInvalid
	}
	b, err := json.Marshal([]any{"host-execution-request/v1", f.Principal, f.Conversation, f.Turn, f.Attempt, f.Epoch, f.Invocation})
	if err != nil {
		return "", errInvalid
	}
	sum := sha256.Sum256(b)
	return "he1_" + hex.EncodeToString(sum[:]), nil
}
func sameOrigin(a, b Call) bool {
	x, y := a.fields(), b.fields()
	return x.Principal == y.Principal && x.Conversation == y.Conversation && x.Turn == y.Turn && x.Attempt == y.Attempt && x.Epoch == y.Epoch && x.Invocation == y.Invocation
}
func sameTurn(a, b Call) bool {
	x, y := a.fields(), b.fields()
	return x.Principal == y.Principal && x.Conversation == y.Conversation && x.Turn == y.Turn
}

type LeaseState uint8

const (
	Missing LeaseState = iota
	Live
	Expired
	Released
	Stolen
)

type JournalState uint8

const (
	NoOther JournalState = iota
	OtherPrepared
	OtherCompleted
	OtherDenied
	OtherDispatched
	OtherUnknown
	LegacyDispatched
	LegacyUnknown
)

// Facts is a transient trusted input snapshot, not durable authorization. The
// constructor freezes it inside Event; never persist or log raw input builders.
type Facts struct {
	Lease                                                         LeaseState
	Other                                                         JournalState
	RevisionCurrent, TurnCurrent, Registered, Authorized, Durable bool
	NeedsApproval, Normal, Projected, ReleaseAuthorized           bool
	OriginAuthorized, ExactOrigin, OriginCompleted                bool
	HelperScope, Origin                                           Call
	RegisteredHelper                                              string
}

func authority(c Call, f Facts) bool {
	return c.valid() && f.Lease == Live && f.RevisionCurrent && f.TurnCurrent && f.Registered && f.Authorized && sameTurn(c, f.HelperScope) && c.fields().Attempt == f.HelperScope.fields().Attempt && c.fields().Epoch == f.HelperScope.fields().Epoch && c.fields().Helper == f.RegisteredHelper
}
func unblocked(f Facts) bool { return f.Other >= NoOther && f.Other <= OtherDenied }
func replayOK(c Call, f Facts) bool {
	return authority(c, f) && f.Origin.valid() && sameTurn(c, f.Origin) && f.ExactOrigin && f.OriginCompleted && f.OriginAuthorized && f.ReleaseAuthorized
}

type EventKind uint8

const (
	Candidate EventKind = iota
	Admit
	Begin
	Approve
	Claim
	Dispatch
	Finish
	Replay
	Cancel
	Expire
	Reject
	Fault
	Unknown
	Close
)

type Event struct {
	redacted
	open func() eventFields
}
type eventFields struct {
	Kind  EventKind
	Call  Call
	Facts Facts
}

func NewEvent(k EventKind, c Call, f Facts) Event {
	c = ownCall(c.fields())
	f.HelperScope = ownCall(f.HelperScope.fields())
	f.Origin = ownCall(f.Origin.fields())
	x := eventFields{k, c, f}
	return Event{open: func() eventFields { return x }}
}
func (e Event) fields() eventFields {
	if e.open == nil {
		return eventFields{Kind: 255}
	}
	return e.open()
}

type State interface {
	sealed()
	payload() Call
	phase() string
}
type Unmapped struct{}

func (Unmapped) sealed()       {}
func (Unmapped) payload() Call { return Call{} }
func (Unmapped) phase() string { return "Unmapped" }

type Admitted struct{ Call }

func (Admitted) sealed()         {}
func (s Admitted) payload() Call { return s.Call }
func (Admitted) phase() string   { return "Admitted" }

type WaitingApproval struct{ Call }

func (WaitingApproval) sealed()         {}
func (s WaitingApproval) payload() Call { return s.Call }
func (WaitingApproval) phase() string   { return "WaitingApproval" }

type Ready struct{ Call }

func (Ready) sealed()         {}
func (s Ready) payload() Call { return s.Call }
func (Ready) phase() string   { return "Ready" }

type Claimed struct{ Call }

func (Claimed) sealed()         {}
func (s Claimed) payload() Call { return s.Call }
func (Claimed) phase() string   { return "Claimed" }

type Executing struct{ Call }

func (Executing) sealed()         {}
func (s Executing) payload() Call { return s.Call }
func (Executing) phase() string   { return "Executing" }

type Completed struct{ Call }

func (Completed) sealed()         {}
func (s Completed) payload() Call { return s.Call }
func (Completed) phase() string   { return "Completed" }

type Denied struct{ Call }

func (Denied) sealed()         {}
func (s Denied) payload() Call { return s.Call }
func (Denied) phase() string   { return "Denied" }

type Held struct{ Call }

func (Held) sealed()         {}
func (s Held) payload() Call { return s.Call }
func (Held) phase() string   { return "Held" }

type Closed struct{ Call }

func (Closed) sealed()         {}
func (s Closed) payload() Call { return s.Call }
func (Closed) phase() string   { return "Closed" }

type Effect uint8

const (
	PersistAdmission Effect = iota + 1
	BeginBroker
	AwaitApproval
	ClaimCall
	Execute
	Publish
	ReleaseReplay
)

type Transition struct {
	redacted
	State   State
	Effects []Effect
	Rule    string
}

func result(s State, rule string, effects ...Effect) Transition {
	return Transition{State: s, Rule: rule, Effects: slices.Clone(effects)}
}

// Reduce covers every phase. Unsupported event/phase pairs explicitly stutter
// (HB00); terminal interceptions absorb late callbacks. HB IDs are stable.
func Reduce(s State, event Event) Transition {
	if Invariant(s) != nil {
		return result(Unmapped{}, "HB22")
	}
	e := event.fields()
	c, f, k := e.Call, e.Facts, e.Kind
	if _, ok := s.(Closed); ok {
		return result(s, "HB19")
	}
	if _, ok := s.(Held); ok {
		return result(s, "HB20")
	}
	if _, ok := s.(Denied); ok {
		return result(s, "HB20")
	}
	if _, ok := s.(Completed); ok {
		return result(s, "HB21")
	}
	if s.phase() != "Unmapped" && !sameOrigin(s.payload(), c) {
		return result(s, "HB02")
	}
	if s.phase() != "Unmapped" && !s.payload().equal(c) {
		return result(Held{s.payload()}, "HB05")
	}
	if k == Close || k == Expire {
		return result(Closed{c}, "HB18")
	}
	if k == Cancel {
		if s.phase() == "Executing" {
			return result(Held{c}, "HB17")
		}
		return result(Closed{c}, "HB18")
	}
	if k == Fault || k == Unknown {
		return result(Held{c}, "HB14")
	}
	if k == Reject {
		return result(Denied{c}, "HB06")
	}
	switch s.(type) {
	case Unmapped:
		switch k {
		case Candidate:
			return result(s, "HB01")
		case Admit:
			if !authority(c, f) || !f.Durable {
				return result(Denied{c}, "HB06")
			}
			return result(Admitted{c}, "HB03", PersistAdmission)
		case Replay:
			if !replayOK(c, f) {
				return result(Held{c}, "HB16")
			}
			return result(Completed{c}, "HB15", ReleaseReplay)
		}
	case Admitted:
		if k == Admit {
			return result(s, "HB04")
		}
		if k == Begin {
			if !authority(c, f) {
				return result(Denied{c}, "HB06")
			}
			if f.NeedsApproval {
				return result(WaitingApproval{c}, "HB08", BeginBroker, AwaitApproval)
			}
			return result(Ready{c}, "HB07", BeginBroker)
		}
	case WaitingApproval:
		if k == Approve {
			if !authority(c, f) {
				return result(Denied{c}, "HB06")
			}
			return result(Ready{c}, "HB09")
		}
	case Ready:
		if k == Claim {
			if !authority(c, f) || !unblocked(f) {
				return result(Held{c}, "HB11")
			}
			return result(Claimed{c}, "HB10", ClaimCall)
		}
	case Claimed:
		if k == Dispatch {
			if !authority(c, f) || !unblocked(f) {
				return result(Held{c}, "HB11")
			}
			return result(Executing{c}, "HB12", Execute)
		}
	case Executing:
		if k == Finish {
			if !authority(c, f) || !f.Normal || !f.Projected || !f.Durable || !f.ReleaseAuthorized {
				return result(Held{c}, "HB14")
			}
			return result(Completed{c}, "HB13", Publish)
		}
	case Completed, Denied, Held, Closed: // Absorbed above.
	}
	return result(s, "HB00")
}

// Invariant validates the sealed operational vocabulary and immutable metadata.
// Claimed is a model stage, never proof of a real claim or native capability.
func Invariant(s State) error {
	switch x := s.(type) {
	case Unmapped:
		return nil
	case Admitted:
		if x.Call.valid() {
			return nil
		}
	case WaitingApproval:
		if x.Call.valid() {
			return nil
		}
	case Ready:
		if x.Call.valid() {
			return nil
		}
	case Claimed:
		if x.Call.valid() {
			return nil
		}
	case Executing:
		if x.Call.valid() {
			return nil
		}
	case Completed:
		if x.Call.valid() {
			return nil
		}
	case Denied, Held, Closed:
		// Rejection can carry a malformed input; it cannot emit authority effects.
		return nil
	}
	return errInvalid
}

// CheckTransition is a separate rule-certificate checker. It never calls
// Reduce. In addition to validating edges it checks exact effects, preservation
// of immutable admission, terminal absorption, and safety-critical host facts.
func CheckTransition(before State, event Event, r Transition) error {
	if Invariant(r.State) != nil {
		return errInvalid
	}
	if Invariant(before) != nil {
		if r.Rule == "HB22" && r.State.phase() == "Unmapped" && len(r.Effects) == 0 {
			return nil
		}
		return errInvalid
	}
	e := event.fields()
	c, f, k := e.Call, e.Facts, e.Kind
	a, b := before.phase(), r.State.phase()
	unchanged := a == b && before.payload().equal(r.State.payload())
	effects := func(want ...Effect) bool { return slices.Equal(r.Effects, want) }
	ok := false
	switch r.Rule {
	case "HB19":
		ok = a == "Closed" && unchanged && effects()
	case "HB20":
		ok = (a == "Held" || a == "Denied") && unchanged && effects()
	case "HB21":
		ok = a == "Completed" && unchanged && effects()
	case "HB02":
		ok = a != "Unmapped" && !sameOrigin(before.payload(), c) && unchanged && effects()
	case "HB05":
		ok = a != "Unmapped" && sameOrigin(before.payload(), c) && !before.payload().equal(c) && b == "Held" && r.State.payload().equal(before.payload()) && effects()
	case "HB01":
		ok = a == "Unmapped" && k == Candidate && unchanged && effects()
	case "HB03":
		ok = a == "Unmapped" && k == Admit && b == "Admitted" && authority(c, f) && f.Durable && effects(PersistAdmission)
	case "HB04":
		ok = a == "Admitted" && k == Admit && unchanged && effects()
	case "HB06":
		denied := k == Reject || (a == "Unmapped" && k == Admit && (!authority(c, f) || !f.Durable)) || ((a == "Admitted" && k == Begin || a == "WaitingApproval" && k == Approve) && !authority(c, f))
		ok = denied && b == "Denied" && effects()
	case "HB07":
		ok = a == "Admitted" && k == Begin && b == "Ready" && authority(c, f) && !f.NeedsApproval && effects(BeginBroker)
	case "HB08":
		ok = a == "Admitted" && k == Begin && b == "WaitingApproval" && authority(c, f) && f.NeedsApproval && effects(BeginBroker, AwaitApproval)
	case "HB09":
		ok = a == "WaitingApproval" && k == Approve && b == "Ready" && authority(c, f) && effects()
	case "HB10":
		ok = a == "Ready" && k == Claim && b == "Claimed" && authority(c, f) && unblocked(f) && effects(ClaimCall)
	case "HB11":
		ok = (a == "Ready" && k == Claim || a == "Claimed" && k == Dispatch) && b == "Held" && (!authority(c, f) || !unblocked(f)) && effects()
	case "HB12":
		ok = a == "Claimed" && k == Dispatch && b == "Executing" && authority(c, f) && unblocked(f) && effects(Execute)
	case "HB13":
		ok = a == "Executing" && k == Finish && b == "Completed" && authority(c, f) && f.Normal && f.Projected && f.Durable && f.ReleaseAuthorized && effects(Publish)
	case "HB14":
		ok = b == "Held" && (k == Fault || k == Unknown || a == "Executing" && k == Finish && (!authority(c, f) || !f.Normal || !f.Projected || !f.Durable || !f.ReleaseAuthorized)) && effects()
	case "HB15":
		ok = a == "Unmapped" && k == Replay && b == "Completed" && replayOK(c, f) && effects(ReleaseReplay)
	case "HB16":
		ok = a == "Unmapped" && k == Replay && b == "Held" && !replayOK(c, f) && effects()
	case "HB17":
		ok = a == "Executing" && k == Cancel && b == "Held" && effects()
	case "HB18":
		ok = b == "Closed" && (k == Close || k == Expire || k == Cancel && a != "Executing") && effects()
	case "HB00":
		// Only unhandled pairs may stutter, not a forged refusal of a live edge.
		handled := k == Cancel || k == Expire || k == Close || k == Fault || k == Unknown || k == Reject
		switch a {
		case "Unmapped":
			handled = handled || k == Candidate || k == Admit || k == Replay
		case "Admitted":
			handled = handled || k == Admit || k == Begin
		case "WaitingApproval":
			handled = handled || k == Approve
		case "Ready":
			handled = handled || k == Claim
		case "Claimed":
			handled = handled || k == Dispatch
		case "Executing":
			handled = handled || k == Finish
		}
		ok = !handled && unchanged && effects()
	}
	if !ok {
		return errInvalid
	}
	// Absorbing phases cannot be forged into an otherwise locally valid edge.
	if a == "Closed" && r.Rule != "HB19" || a == "Completed" && r.Rule != "HB21" || (a == "Held" || a == "Denied") && r.Rule != "HB20" {
		return errInvalid
	}
	if a != "Unmapped" && a != "Closed" && a != "Held" && a != "Denied" && a != "Completed" {
		if !sameOrigin(before.payload(), c) {
			if r.Rule != "HB02" {
				return errInvalid
			}
		} else if !before.payload().equal(c) {
			if r.Rule != "HB05" {
				return errInvalid
			}
		}
	}
	if !unchanged && r.Rule != "HB05" && !r.State.payload().equal(c) {
		return errInvalid
	}
	return nil
}
