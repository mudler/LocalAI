// Package coordinator is the shared single-writer state-machine runtime for the
// realtime API's explicit coordinators (machines M1–M5 in
// docs/design/realtime-state-machines.md).
//
// Each machine package (respcoord, turncoord, conncoord, compactcoord, ttscoord)
// defines its OWN sealed sum types for State/Event/Effect and a total, pure
// transition function Next(state, event) -> (state, []effect, error). The
// plumbing around that — a single-writer Coordinator that serializes every
// transition behind one lock and performs the returned effects in order — is
// identical across all five, so it lives here once instead of being copied.
//
// A machine package wires itself up with three lines:
//
//	type EffectSink = coordinator.Sink[Effect]
//	type Coordinator = coordinator.Coordinator[State, Event, Effect]
//	func New(sink EffectSink) *Coordinator { return coordinator.New[State, Event, Effect](Idle{}, Next, sink) }
//
// The aliases keep each package's public API (Coordinator, New, EffectSink,
// Apply, State) unchanged. The single-writer serialization — the load-bearing
// concurrency guarantee the FizzBee specs check — is therefore implemented and
// reasoned about in exactly one place.
package coordinator

import "sync"

// TransitionFunc is a machine's total, pure transition: given the current state
// and an event it returns the next state, the ordered effects to perform, and a
// non-nil error ONLY for an unhandled (programmer-error) state/event pair. It
// must not perform I/O or block; side effects are returned as data (F) for the
// Coordinator to hand to the Sink.
type TransitionFunc[S, E, F any] func(state S, event E) (S, []F, error)

// Sink performs the effects a transition produces. Implementations MUST be
// non-blocking: Perform is called while the Coordinator holds its lock, so it
// must not block (it should spawn a goroutine, call a cancel func, or do a
// non-blocking channel send) and MUST NOT call back into the same Coordinator's
// Apply.
type Sink[F any] interface {
	Perform(F)
}

// Coordinator is the single-writer wrapper around a pure transition function.
// Every Apply is serialized by mu, so multiple goroutines can drive the machine
// without racing, and a transition's effects are performed in order under the
// lock (before any subsequent Apply can observe the new state).
type Coordinator[S, E, F any] struct {
	mu    sync.Mutex
	state S
	next  TransitionFunc[S, E, F]
	sink  Sink[F]
}

// New returns a Coordinator in the given initial state that transitions via next
// and performs effects via sink.
func New[S, E, F any](initial S, next TransitionFunc[S, E, F], sink Sink[F]) *Coordinator[S, E, F] {
	return &Coordinator[S, E, F]{state: initial, next: next, sink: sink}
}

// Apply runs one transition under the lock and performs its effects in order. If
// the transition function returns an error (an unhandled state/event), the state
// is left unchanged and the error is returned to the caller — never silently
// swallowed.
func (c *Coordinator[S, E, F]) Apply(e E) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	ns, effects, err := c.next(c.state, e)
	if err != nil {
		return err
	}
	c.state = ns
	for _, eff := range effects {
		c.sink.Perform(eff)
	}
	return nil
}

// State returns the current state (a value; safe to call concurrently).
func (c *Coordinator[S, E, F]) State() S {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}
