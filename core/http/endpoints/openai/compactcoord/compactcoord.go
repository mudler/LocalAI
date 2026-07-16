// Package compactcoord is the explicit state machine for the realtime API's
// conversation-compaction concern (machine "M4" in
// docs/design/realtime-state-machines.md).
//
// In the legacy code this machine is an implicit single-flight guard: a
// per-conversation `compacting atomic.Bool` that maybeCompact CAS-flips to start
// a background summarize+evict and a deferred Store(false) clears. The intent —
// at most one compaction running per conversation at a time, so two goroutines
// never summarize and evict the same overflow concurrently (Part 4, invariant
// #9) — is correct but implicit in a bare atomic.
//
// This package makes it explicit:
//   - a sealed sum type for State (Idle | Running) — "two compactions running" is
//     unrepresentable,
//   - a total, pure transition function Next(state, event) -> (state, effects),
//   - a single-writer Coordinator that serializes every transition.
//
// Unlike respcoord (M3), a Trigger while Running is NOT a supersede: compaction
// is idempotent work on the same overflow, so a concurrent trigger is simply
// dropped (matching the legacy CAS-fails-so-skip), not queued or restarted.
package compactcoord

import (
	"fmt"

	"github.com/mudler/LocalAI/core/http/endpoints/openai/coordinator"
)

// State is the sealed sum type of compaction states. Exhaustively:
// Idle | Running | Terminated.
type State interface {
	isState()
	String() string
}

// Idle: no compaction is running.
type Idle struct{}

// Running: exactly one compaction is in flight.
type Running struct{}

// Terminated: the conversation/session is torn down. Absorbing — no compaction
// can start from here, so the M1 (connection) parent's teardown can cancel +
// join the in-flight compaction and guarantee none outlives the session (see
// formal-verification/session_lifecycle.fizz). This closes the legacy gap where
// the fire-and-forget compaction goroutine could outlive the session.
type Terminated struct{}

func (Idle) isState()       {}
func (Running) isState()    {}
func (Terminated) isState() {}

func (Idle) String() string       { return "Idle" }
func (Running) String() string    { return "Running" }
func (Terminated) String() string { return "Terminated" }

// Event is the sealed sum type of inputs. Exhaustively:
// Trigger | Finished | Shutdown.
type Event interface {
	isEvent()
	String() string
}

// Trigger requests a compaction (the live buffer grew past the trigger). It
// starts one only when Idle; while Running it is a no-op (single-flight).
type Trigger struct{}

// Finished reports that the running compaction goroutine finished (success, error, or
// timeout — it always reports Finished so the flag can never stick).
type Finished struct{}

// Shutdown terminates the coordinator at teardown: the in-flight compaction is
// cancelled + joined by the sink, and no compaction can start afterwards.
type Shutdown struct{}

func (Trigger) isEvent()  {}
func (Finished) isEvent() {}
func (Shutdown) isEvent() {}

func (Trigger) String() string  { return "Trigger" }
func (Finished) String() string { return "Finished" }
func (Shutdown) String() string { return "Shutdown" }

// Effect is a side effect returned by Next as data. Exhaustively: StartCompaction.
type Effect interface {
	isEffect()
	String() string
}

// StartCompaction: spawn the background summarize+evict goroutine.
type StartCompaction struct{}

func (StartCompaction) isEffect() {}

func (StartCompaction) String() string { return "StartCompaction" }

// Next is the total, pure transition function. For every (state, event) it
// returns the next state and the ordered effects. It returns a non-nil error
// only for an unknown State/Event implementation. Every in-domain pair is
// defined; there are no forbidden transitions, only no-ops.
//
// Single-flight crux: StartCompaction is emitted only on Idle+Trigger, and a
// Trigger while Running is a no-op — so at most one compaction ever runs.
func Next(s State, e Event) (State, []Effect, error) {
	switch s.(type) {
	case Idle:
		switch e.(type) {
		case Trigger:
			return Running{}, []Effect{StartCompaction{}}, nil
		case Finished:
			// No compaction to finish: stale/idempotent no-op.
			return Idle{}, nil, nil
		case Shutdown:
			return Terminated{}, nil, nil
		}
	case Running:
		switch e.(type) {
		case Trigger:
			// Already compacting: drop (single-flight).
			return Running{}, nil, nil
		case Finished:
			return Idle{}, nil, nil
		case Shutdown:
			// Teardown while compacting: the sink cancels + joins the goroutine,
			// so its later Finished is absorbed here in Terminated.
			return Terminated{}, nil, nil
		}
	case Terminated:
		// Absorbing: a Trigger after teardown is rejected (no StartCompaction), so
		// no compaction outlives the session.
		switch e.(type) {
		case Trigger, Finished, Shutdown:
			return Terminated{}, nil, nil
		}
	}
	return s, nil, fmt.Errorf("compactcoord: unhandled transition %s <- %s", s, e)
}

// EffectSink performs the effects produced by a transition. See coordinator.Sink:
// StartCompaction spawns a goroutine, so Perform does not block under the lock.
type EffectSink = coordinator.Sink[Effect]

// Coordinator serializes the compaction transitions. See coordinator.Coordinator.
type Coordinator = coordinator.Coordinator[State, Event, Effect]

// New returns an idle Coordinator that performs effects via sink.
func New(sink EffectSink) *Coordinator {
	return coordinator.New[State, Event, Effect](Idle{}, Next, sink)
}
