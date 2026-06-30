// Package conncoord is the explicit state machine for the realtime API's
// connection lifecycle (machine "M1" in docs/design/realtime-state-machines.md).
//
// In the legacy code this machine is implicit and fragile. The session handler
// keeps a `vadServerStarted` bool plus a `done` channel that is REASSIGNED to a
// fresh channel every time turn detection is toggled on (session.update) and
// closed both at toggle-off and at teardown (Part 2, failure mode 6). It is
// correct today only because one goroutine owns it; "one variable name meaning
// different channels over time, closed from two sites guarded by a bool" is a
// structural hazard, not an explicit lifecycle. Teardown likewise depends on the
// bool to avoid closing an already-closed channel.
//
// This package makes the lifecycle explicit:
//   - a sealed sum type for State (Live{VADRunning} | Torn) — illegal states
//     such as "running after teardown" are unrepresentable,
//   - a total, pure transition function Next(state, event) -> (state, effects),
//   - a single-writer Coordinator that serializes every transition.
//
// The guarantees the spec checks:
//   - the VAD goroutine's done channel is closed exactly once per start (StopVAD
//     is emitted only while running, so never a double close / close of nil),
//   - teardown runs exactly once (Close from Live; any later Close is a no-op),
//   - nothing is started after teardown (no resurrection / no send-after-close).
//
// Like turncoord (M2), the connection machine is driven by the single session
// goroutine; the Coordinator's lock keeps State() race-free and guards against a
// future second writer. The effects are performed by a sink that owns the actual
// channels/goroutines (see realtime_conncoord.go).
package conncoord

import (
	"fmt"

	"github.com/mudler/LocalAI/core/http/endpoints/openai/coordinator"
)

// State is the sealed sum type of connection states. The only implementations
// are the marker-method structs in this file. Exhaustively: Live | Torn.
type State interface {
	isState()
	String() string
}

// Live: the session is active. VADRunning records whether the turn-detection
// (handleVAD) goroutine is currently running — the single source of truth that
// replaces the legacy vadServerStarted bool, so the per-run done channel is
// closed exactly once.
type Live struct{ VADRunning bool }

// Torn: the session has been torn down. Terminal — no effect is ever produced
// from here again.
type Torn struct{}

func (Live) isState() {}
func (Torn) isState() {}

func (s Live) String() string { return fmt.Sprintf("Live(vad=%t)", s.VADRunning) }
func (Torn) String() string   { return "Torn" }

// Event is the sealed sum type of inputs. Exhaustively: SetVAD | Close.
type Event interface {
	isEvent()
	String() string
}

// SetVAD requests the turn-detection goroutine be running (Active) or not. It is
// raised whenever session.update changes whether turn detection is active. It is
// idempotent: setting the state it is already in is a no-op.
type SetVAD struct{ Active bool }

// Close requests teardown (the transport read loop ended, or the session is
// closing). It is idempotent — only the first Close from Live tears down.
type Close struct{}

func (SetVAD) isEvent() {}
func (Close) isEvent()  {}

func (e SetVAD) String() string { return fmt.Sprintf("SetVAD(%t)", e.Active) }
func (Close) String() string    { return "Close" }

// Effect is a side effect returned by Next as data for the caller to perform.
// Exhaustively: StartVAD | StopVAD | Teardown.
type Effect interface {
	isEffect()
	String() string
}

// StartVAD: create a fresh done channel and spawn the handleVAD goroutine on it.
type StartVAD struct{}

// StopVAD: close the running VAD goroutine's done channel (signal it to exit).
type StopVAD struct{}

// Teardown: the once-only teardown — stop the remaining input goroutines (opus
// decode, sound window), join them, cancel in-flight responses, and remove the
// session from the registry. Emitted exactly once.
type Teardown struct{}

func (StartVAD) isEffect() {}
func (StopVAD) isEffect()  {}
func (Teardown) isEffect() {}

func (StartVAD) String() string { return "StartVAD" }
func (StopVAD) String() string  { return "StopVAD" }
func (Teardown) String() string { return "Teardown" }

// Next is the total, pure transition function. For every (state, event) it
// returns the next state and the ordered effects to perform. It returns a
// non-nil error only for an unknown State/Event implementation. Every in-domain
// pair is defined; there are no forbidden transitions, only no-ops.
//
// The crux: Close moves to Torn, which absorbs every later event with no
// effects. So teardown's channel closes happen exactly once even if Close is
// raised again (e.g. an error path and the normal return both reaching it), and
// no StartVAD can resurrect a torn session.
func Next(s State, e Event) (State, []Effect, error) {
	switch st := s.(type) {
	case Live:
		switch ev := e.(type) {
		case SetVAD:
			switch {
			case ev.Active && !st.VADRunning:
				return Live{VADRunning: true}, []Effect{StartVAD{}}, nil
			case !ev.Active && st.VADRunning:
				return Live{VADRunning: false}, []Effect{StopVAD{}}, nil
			default:
				// Already in the requested state: idempotent no-op.
				return Live{VADRunning: st.VADRunning}, nil, nil
			}
		case Close:
			if st.VADRunning {
				return Torn{}, []Effect{StopVAD{}, Teardown{}}, nil
			}
			return Torn{}, []Effect{Teardown{}}, nil
		}
	case Torn:
		switch e.(type) {
		case SetVAD:
			// No resurrection: a toggle after teardown is ignored.
			return Torn{}, nil, nil
		case Close:
			// Idempotent: teardown already ran.
			return Torn{}, nil, nil
		}
	}
	return s, nil, fmt.Errorf("conncoord: unhandled transition %s <- %s", s, e)
}

// EffectSink performs the effects produced by a transition. See coordinator.Sink:
// Perform runs under the coordinator lock. The Teardown effect does join
// goroutines (which can block) — acceptable here because the connection
// coordinator is single-writer and torn down exactly once at the end of the
// session goroutine, so no other Apply is contending the lock.
type EffectSink = coordinator.Sink[Effect]

// Coordinator serializes the connection-lifecycle transitions.
// See coordinator.Coordinator.
type Coordinator = coordinator.Coordinator[State, Event, Effect]

// New returns a Coordinator in Live{VADRunning:false} that performs effects via
// sink.
func New(sink EffectSink) *Coordinator {
	return coordinator.New[State, Event, Effect](Live{VADRunning: false}, Next, sink)
}
