// Package respcoord is the explicit state machine for the realtime API's
// response-coordination concern (machine "M3" in
// docs/design/realtime-state-machines.md).
//
// In the legacy code this machine is implicit: a response is "active" iff
// Session.activeResponseDone is a non-nil, unclosed channel, and the lifecycle
// is driven from TWO goroutines (the client read-loop and the VAD goroutine)
// that both call startResponse/cancelActiveResponse. responseMu guards only the
// field swap, while the <-done wait happens outside the lock, so two concurrent
// starts can briefly leave two live response goroutines both appending to the
// conversation. See docs/design/realtime-state-machines.md, Part 2 (failure
// mode 2) and the ResponseLifecycle spec under formal-verification/.
//
// This package replaces that with:
//   - a sealed sum type for State (illegal states are unrepresentable),
//   - a total, pure transition function Next(state, event) -> (state, effects),
//   - a single-writer Coordinator that serializes every transition.
//
// The design guarantees the invariants the specs check:
//   - at most one live response at any instant,
//   - exactly one terminal (response.done) per started response,
//   - no response is started after its terminal (no resurrection).
package respcoord

import (
	"fmt"

	"github.com/mudler/LocalAI/core/http/endpoints/openai/coordinator"
)

// ResponseID identifies a single response attempt. The caller mints a fresh,
// monotonically increasing id for every Start; ids are never reused. The
// monotonic id is what lets the machine ignore "stale" Finished events from a
// response that was already superseded or cancelled.
type ResponseID uint64

// Source records which goroutine drove an event. It is carried for
// observability/logging only; it never affects a transition (both sources are
// equal authority). Keeping it in the event type makes the dual-writer reality
// explicit rather than hidden.
type Source int

const (
	// SourceClient is the read-loop: response.create or a manual
	// input_audio_buffer.commit.
	SourceClient Source = iota
	// SourceVAD is the turn-detection goroutine: end-of-speech commit or a
	// barge-in cancel.
	SourceVAD
)

func (s Source) String() string {
	switch s {
	case SourceClient:
		return "client"
	case SourceVAD:
		return "vad"
	default:
		return fmt.Sprintf("Source(%d)", int(s))
	}
}

// Status is the terminal status reported on response.done.
type Status int

const (
	// StatusCompleted is a response that finished on its own.
	StatusCompleted Status = iota
	// StatusCancelled is a response cut short by a barge-in, an explicit
	// response.cancel, or by being superseded by a newer response.
	StatusCancelled
)

func (s Status) String() string {
	switch s {
	case StatusCompleted:
		return "completed"
	case StatusCancelled:
		return "cancelled"
	default:
		return fmt.Sprintf("Status(%d)", int(s))
	}
}

// State is the sealed sum type of coordinator states. The only implementations
// are the unexported-method-bearing structs in this file, so callers outside
// the package cannot fabricate an out-of-band state. Exhaustively:
// Idle | Active | Terminated.
type State interface {
	isState()
	String() string
}

// Idle: no response is in flight.
type Idle struct{}

// Active: exactly one response (ID) is in flight. The struct holds a single id,
// so "two active responses" is not representable.
type Active struct{ ID ResponseID }

// Terminated: the session is torn down. Absorbing — no response can start from
// here, so the M1 (connection) parent's teardown can guarantee no response
// outlives the session (see formal-verification/session_lifecycle.fizz).
type Terminated struct{}

func (Idle) isState()       {}
func (Active) isState()     {}
func (Terminated) isState() {}

func (Idle) String() string       { return "Idle" }
func (a Active) String() string   { return fmt.Sprintf("Active(%d)", a.ID) }
func (Terminated) String() string { return "Terminated" }

// Event is the sealed sum type of inputs. Exhaustively:
// Start | Finished | Cancel | Shutdown.
type Event interface {
	isEvent()
	String() string
}

// Start requests a new response. ID must be a fresh, never-before-used id.
type Start struct {
	ID     ResponseID
	Source Source
}

// Finished reports that the response goroutine for ID reached its own terminal.
// If ID is not the currently-active response it is "stale" (the response was
// already superseded/cancelled) and is ignored.
type Finished struct{ ID ResponseID }

// Cancel requests cancellation of the in-flight response (barge-in or explicit
// response.cancel). It is a no-op when idle.
type Cancel struct{ Source Source }

// Shutdown terminates the coordinator at session teardown: it cancels any
// in-flight response and moves to the absorbing Terminated state, after which no
// response can start. Raised by the connection (M1) parent's teardown.
type Shutdown struct{}

func (Start) isEvent()    {}
func (Finished) isEvent() {}
func (Cancel) isEvent()   {}
func (Shutdown) isEvent() {}

func (e Start) String() string    { return fmt.Sprintf("Start(%d,%s)", e.ID, e.Source) }
func (e Finished) String() string { return fmt.Sprintf("Finished(%d)", e.ID) }
func (e Cancel) String() string   { return fmt.Sprintf("Cancel(%s)", e.Source) }
func (Shutdown) String() string   { return "Shutdown" }

// Effect is a side effect returned by Next as data for the caller to perform.
// Returning effects as data (rather than firing callbacks inside the
// transition) keeps Next pure and exhaustively testable, and lets the
// Coordinator decide how/when to perform them. Exhaustively:
// CancelResponse | StartResponse | EmitTerminal.
type Effect interface {
	isEffect()
	String() string
}

// CancelResponse: cancel the context of the running response ID.
type CancelResponse struct{ ID ResponseID }

// StartResponse: spawn the response goroutine for ID.
type StartResponse struct{ ID ResponseID }

// EmitTerminal: send response.done for ID with Status.
type EmitTerminal struct {
	ID     ResponseID
	Status Status
}

func (CancelResponse) isEffect() {}
func (StartResponse) isEffect()  {}
func (EmitTerminal) isEffect()   {}

func (e CancelResponse) String() string { return fmt.Sprintf("CancelResponse(%d)", e.ID) }
func (e StartResponse) String() string  { return fmt.Sprintf("StartResponse(%d)", e.ID) }
func (e EmitTerminal) String() string {
	return fmt.Sprintf("EmitTerminal(%d,%s)", e.ID, e.Status)
}

// Next is the total, pure transition function. For every (state, event) it
// returns the next state and the ordered effects to perform. It returns a
// non-nil error only for an unknown State/Event implementation (a programmer
// error / future type added without updating this function) — callers must
// surface that, never silently ignore it. Every in-domain (state, event) pair
// is defined; there are no "forbidden" transitions, only no-ops for stale or
// idle inputs.
//
// The supersede rule (Active + Start) is the crux of the fix: starting a new
// response while one is active emits the old response's cancelled terminal and
// cancels it BEFORE the replacement starts, all within one serialized
// transition. The old goroutine's later Finished is therefore stale and
// ignored — so each id gets exactly one terminal and there is never more than
// one live response.
func Next(s State, e Event) (State, []Effect, error) {
	switch st := s.(type) {
	case Idle:
		switch ev := e.(type) {
		case Start:
			return Active{ID: ev.ID}, []Effect{StartResponse{ID: ev.ID}}, nil
		case Cancel:
			// Nothing in flight: idempotent no-op.
			return Idle{}, nil, nil
		case Finished:
			// Stale terminal from an already-superseded/cancelled response.
			return Idle{}, nil, nil
		case Shutdown:
			// Teardown with nothing in flight: go terminal.
			return Terminated{}, nil, nil
		}
	case Active:
		switch ev := e.(type) {
		case Start:
			return Active{ID: ev.ID}, []Effect{
				CancelResponse{ID: st.ID},
				EmitTerminal{ID: st.ID, Status: StatusCancelled},
				StartResponse{ID: ev.ID},
			}, nil
		case Finished:
			if ev.ID == st.ID {
				return Idle{}, []Effect{EmitTerminal{ID: st.ID, Status: StatusCompleted}}, nil
			}
			// Stale finish from a superseded response — already terminal-ed.
			return Active{ID: st.ID}, nil, nil
		case Cancel:
			return Idle{}, []Effect{
				CancelResponse{ID: st.ID},
				EmitTerminal{ID: st.ID, Status: StatusCancelled},
			}, nil
		case Shutdown:
			// Teardown while a response is live: cancel it (with its terminal) and
			// go terminal so nothing can start afterwards.
			return Terminated{}, []Effect{
				CancelResponse{ID: st.ID},
				EmitTerminal{ID: st.ID, Status: StatusCancelled},
			}, nil
		}
	case Terminated:
		// Absorbing: every event is a no-op. A Start after teardown is rejected
		// (no StartResponse), so no response can outlive the session.
		switch e.(type) {
		case Start, Finished, Cancel, Shutdown:
			return Terminated{}, nil, nil
		}
	}
	return s, nil, fmt.Errorf("respcoord: unhandled transition %s <- %s", s, e)
}

// EffectSink performs the effects produced by a transition. See coordinator.Sink
// for the non-blocking contract: Perform runs under the coordinator lock, so it
// must not block and must not re-enter Apply (the spawned response goroutine's
// Finished apply happens only after the sink returns).
type EffectSink = coordinator.Sink[Effect]

// Coordinator serializes every Start/Finished/Cancel/Shutdown transition behind
// one lock, so the two driving goroutines (read-loop and VAD) can call Apply
// concurrently without the legacy dual-writer race. Effects are performed in
// order under the lock — preserving the (cancel old, emit old terminal, start
// new) supersede ordering. See coordinator.Coordinator.
type Coordinator = coordinator.Coordinator[State, Event, Effect]

// New returns an idle Coordinator that performs effects via sink.
func New(sink EffectSink) *Coordinator {
	return coordinator.New[State, Event, Effect](Idle{}, Next, sink)
}
