// Package ttscoord is the explicit state machine for the realtime API's
// TTS-pipeline lifecycle (machine "M5" in docs/design/realtime-state-machines.md).
//
// The realtime TTS pipeline (realtime_tts_pipeline.go) decouples synthesis from
// LLM token generation: the token callback enqueues clauses, a single worker
// goroutine synthesizes them in order, and wait() closes the queue and joins the
// worker. In the legacy code the lifecycle is an implicit `closed bool` (guarded
// by the pipeline mutex) plus a `done` channel closed once by the worker. Two
// gaps: enqueue does NOT check `closed`, so a clause offered after wait() is
// silently appended to a worker that may have already exited (dropped); and the
// open/closed lifecycle is inferred from a bool rather than stored.
//
// This package makes the lifecycle explicit:
//   - a sealed sum type for State (Open | Closing | Closed) — monotonic; illegal
//     reversals are unrepresentable,
//   - a total, pure transition function Next(state, event) -> (state, effects),
//   - a single-writer Coordinator that serializes every transition.
//
// It is a genuine two-writer machine: the producer goroutine raises Close (from
// wait()), and the worker goroutine raises WorkerExited when it has drained the
// queue and seen the close — so serializing the transition matters. The poison
// `failed` latch stays a lock-free atomic.Bool in the pipeline (it is read per
// clause on the worker's hot path and is orthogonal to open/closed); this machine
// owns only the open->closing->closed lifecycle.
//
// Guarantees the spec checks:
//   - Close wakes the worker to exit exactly once (idempotent wait(); invariant
//     #10),
//   - the lifecycle is monotonic and Closed is terminal — so a clause is never
//     accepted after close (enqueue is gated on Open) and the worker is joined
//     exactly once (no leak; invariant #8).
package ttscoord

import (
	"fmt"

	"github.com/mudler/LocalAI/core/http/endpoints/openai/coordinator"
)

// State is the sealed sum type of TTS-pipeline lifecycle states. Exhaustively:
// Open | Closing | Closed.
type State interface {
	isState()
	String() string
}

// Open: the worker is running and accepting clauses.
type Open struct{}

// Closing: wait() has been called; the worker is draining the remaining queue and
// will exit. No new clause is accepted.
type Closing struct{}

// Closed: the worker has exited (its done channel is closed). Terminal.
type Closed struct{}

func (Open) isState()    {}
func (Closing) isState() {}
func (Closed) isState()  {}

func (Open) String() string    { return "Open" }
func (Closing) String() string { return "Closing" }
func (Closed) String() string  { return "Closed" }

// Event is the sealed sum type of inputs. Exhaustively: Close | WorkerExited.
type Event interface {
	isEvent()
	String() string
}

// Close is raised by the producer goroutine (wait()): close the queue and ask
// the worker to finish. Idempotent.
type Close struct{}

// WorkerExited is raised by the worker goroutine when it has drained the queue
// and observed the close, just before it closes its done channel.
type WorkerExited struct{}

func (Close) isEvent()        {}
func (WorkerExited) isEvent() {}

func (Close) String() string        { return "Close" }
func (WorkerExited) String() string { return "WorkerExited" }

// Effect is a side effect returned by Next as data. Exhaustively: Wake.
type Effect interface {
	isEffect()
	String() string
}

// Wake: signal the worker (via the buffered wake channel) so it re-checks the
// lifecycle and exits. Emitted once, on the Open->Closing transition.
type Wake struct{}

func (Wake) isEffect() {}

func (Wake) String() string { return "Wake" }

// Next is the total, pure transition function. For every (state, event) it
// returns the next state and the ordered effects. It returns a non-nil error
// only for an unknown State/Event implementation. Every in-domain pair is
// defined; there are no forbidden transitions, only no-ops.
//
// The lifecycle is monotonic Open -> Closing -> Closed. Close wakes the worker
// only on the first Open->Closing transition (idempotent wait()); a later Close
// is absorbed. WorkerExited only advances Closing -> Closed.
func Next(s State, e Event) (State, []Effect, error) {
	switch s.(type) {
	case Open:
		switch e.(type) {
		case Close:
			return Closing{}, []Effect{Wake{}}, nil
		case WorkerExited:
			// Worker exited while still Open (e.g. never any clause and an early
			// close race) -- treat as fully closed; defensive, keeps Next total.
			return Closed{}, nil, nil
		}
	case Closing:
		switch e.(type) {
		case Close:
			// Idempotent wait(): already closing, no second wake.
			return Closing{}, nil, nil
		case WorkerExited:
			return Closed{}, nil, nil
		}
	case Closed:
		switch e.(type) {
		case Close:
			return Closed{}, nil, nil
		case WorkerExited:
			return Closed{}, nil, nil
		}
	}
	return s, nil, fmt.Errorf("ttscoord: unhandled transition %s <- %s", s, e)
}

// EffectSink performs the effects produced by a transition. See coordinator.Sink:
// Wake does a non-blocking send on a buffered channel, so Perform does not block
// under the lock.
type EffectSink = coordinator.Sink[Effect]

// Coordinator serializes the TTS-pipeline transitions. The producer (Close) and
// worker (WorkerExited) goroutines both call Apply, so the lock serializes the
// two writers. See coordinator.Coordinator.
type Coordinator = coordinator.Coordinator[State, Event, Effect]

// New returns an Open Coordinator that performs effects via sink.
func New(sink EffectSink) *Coordinator {
	return coordinator.New[State, Event, Effect](Open{}, Next, sink)
}
