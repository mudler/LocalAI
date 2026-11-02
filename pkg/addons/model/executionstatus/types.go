// SPDX-License-Identifier: MIT

// Package executionstatus models one enclosing request's observational status.
// Bind is authoritative host admission; effects are data, never operations.
package executionstatus

// Binding contains opaque identity tokens, never credentials. All fields take
// part in equality, including incarnation within the same package generation.
type Binding struct {
	Actor, Credential, Conversation, Mode, Addon, Generation, Incarnation, ExecutionTool, StatusTool string
}
type ObservationID string
type Cause string
type Eligibility string

const Eligible Eligibility = "eligible"
const (
	Timeout   Cause = "timeout"
	Malformed Cause = "malformed"
)

type Readiness string
type Isolation string

const (
	Available        Readiness = "available"
	Unavailable      Readiness = "unavailable"
	UnknownReadiness Readiness = "unknown"
	Isolated         Isolation = "isolated"
	Host             Isolation = "host"
	UnknownIsolation Isolation = "unknown"
)

type Value struct {
	Readiness Readiness
	Isolation Isolation
}

func Unknown() Value { return Value{UnknownReadiness, UnknownIsolation} }
func normalize(v Value) Value {
	if v.Readiness != Available {
		v.Isolation = UnknownIsolation
	}
	return v
}

// Payload is a local validation boundary, not a wire decoder. A future strict
// contract decoder supplies ValidPayload only after validating its full schema.
// Zero Payload represents malformed input; no contract dependency is needed.
type Payload struct {
	value Value
	valid bool
}

func ValidPayload(v Value) Payload {
	valid := (v.Readiness == Available || v.Readiness == Unavailable || v.Readiness == UnknownReadiness) && (v.Isolation == Isolated || v.Isolation == Host || v.Isolation == UnknownIsolation)
	return Payload{v, valid}
}
func InvalidPayload() Payload { return Payload{} }

type State interface{ isState() }
type Unbound struct{}
type Unobserved struct {
	B Binding
	Q ObservationID
}
type Pending struct {
	B        Binding
	Q        ObservationID
	Deadline int64
}
type Observed struct {
	B     Binding
	Q     ObservationID
	Value Value
}
type SettledUnknown struct {
	B     Binding
	Q     ObservationID
	Cause Cause
}
type Withdrawn struct{ Cause Cause }
type Closed struct{}

func (Unbound) isState()        {}
func (Unobserved) isState()     {}
func (Pending) isState()        {}
func (Observed) isState()       {}
func (SettledUnknown) isState() {}
func (Withdrawn) isState()      {}
func (Closed) isState()         {}

type Event interface{ isEvent() }
type Bind struct {
	B           Binding
	Q           ObservationID
	Eligibility Eligibility
}
type Probe struct {
	B             Binding
	Q             ObservationID
	Now, Deadline int64
}
type Reply struct {
	B       Binding
	Q       ObservationID
	Payload Payload
	Now     int64
}
type Failure struct {
	B     Binding
	Q     ObservationID
	Cause Cause
	Now   int64
}
type Tick struct{ Now int64 }
type Invalidate struct{ Cause Cause }
type Close struct{}

func (Bind) isEvent()       {}
func (Probe) isEvent()      {}
func (Reply) isEvent()      {}
func (Failure) isEvent()    {}
func (Tick) isEvent()       {}
func (Invalidate) isEvent() {}
func (Close) isEvent()      {}

type Effect interface{ isEffect() }
type Read struct {
	B        Binding
	Q        ObservationID
	Deadline int64
}
type Cancel struct{ Q ObservationID }
type Publish struct {
	Value Value
	Cause Cause
}

func (Read) isEffect()    {}
func (Cancel) isEffect()  {}
func (Publish) isEffect() {}

type Rule string

const (
	R1  Rule = "R1"
	R2  Rule = "R2"
	R3  Rule = "R3"
	R4  Rule = "R4"
	R5  Rule = "R5"
	R6  Rule = "R6"
	R7  Rule = "R7"
	R9  Rule = "R9"
	R10 Rule = "R10"
	R11 Rule = "R11"
	I   Rule = "I"
)

type Transition struct {
	State   State
	Effects []Effect
	Rule    Rule
	Guard   string
}

func Rules() []Rule { return []Rule{R1, R2, R3, R4, R5, R6, R7, R9, R10, R11, I} }

func identity(s State) (Binding, ObservationID, bool) {
	switch s := s.(type) {
	case Unobserved:
		return s.B, s.Q, true
	case Pending:
		return s.B, s.Q, true
	case Observed:
		return s.B, s.Q, true
	case SettledUnknown:
		return s.B, s.Q, true
	case Unbound, Withdrawn, Closed:
		return Binding{}, "", false
	}
	panic("unhandled state constructor")
}
func Project(s State) Value {
	switch s := s.(type) {
	case Observed:
		return normalize(s.Value)
	case Unbound, Unobserved, Pending, SettledUnknown, Withdrawn, Closed:
		return Unknown()
	}
	panic("unhandled state constructor")
}
