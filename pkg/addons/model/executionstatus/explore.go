// SPDX-License-Identifier: MIT
package executionstatus

import (
	"fmt"
	"reflect"
)

type Exploration struct {
	States, Transitions, LivenessChecks int
	Cells                               map[string]bool
}
type history struct {
	state           State
	reads, inflight int
	now             int64
	path            []Event
}

// Explore performs bounded BFS over reachable histories, not just states.
// The representative domain has two identities, two Qs, both admission outcomes,
// payload validity, and times before/equal/after deadline. Rebinding resets the
// lifetime counter only on R1. Deduplication includes counters and monotonic time;
// it never merges a retry violation into an already-visited state.
func Explore(depth int) (Exploration, error) {
	result := Exploration{Cells: map[string]bool{}}
	if depth < 1 {
		return result, fmt.Errorf("depth must be positive")
	}
	events := explorationEvents()
	queue := []history{{state: Unbound{}}}
	seen := map[string]bool{}
	for len(queue) > 0 {
		h := queue[0]
		queue = queue[1:]
		key := fmt.Sprintf("%#v/%d/%d/%d", h.state, h.reads, h.inflight, h.now)
		if seen[key] {
			continue
		}
		seen[key] = true
		result.States++
		if err := live(h.state); err != nil {
			return result, fmt.Errorf("%w; path=%#v", err, h.path)
		}
		result.LivenessChecks++
		// Check frontier transitions too: depth bounds expansion, not invariants.
		for _, e := range events {
			now, hasTime := eventTime(e)
			if hasTime && now < h.now {
				continue
			}
			tr := Reduce(h.state, e)
			result.Transitions++
			result.Cells[fmt.Sprintf("%T/%T", h.state, e)] = true
			next := history{tr.State, h.reads, h.inflight, h.now, append(append([]Event{}, h.path...), e)}
			if hasTime {
				next.now = now
			}
			if tr.Rule == R1 {
				next.reads = 0
			}
			for _, fx := range tr.Effects {
				switch fx := fx.(type) {
				case Read:
					next.reads++
					next.inflight++
					b, q, _ := identity(tr.State)
					if fx.B != b || fx.Q != q || tr.Rule != R2 {
						return result, counterexample("read identity", next)
					}
				case Cancel:
					_, q, _ := identity(h.state)
					if fx.Q != q || next.inflight != 1 {
						return result, counterexample("cancel without exact pending read", next)
					}
					next.inflight--
				case Publish:
					if fx.Value != Project(tr.State) {
						return result, counterexample("publication differs from projection", next)
					}
				}
			}
			if tr.Rule == R3 {
				next.inflight--
			} // reply completed the outstanding read.
			if next.reads > 1 || next.inflight > 1 || next.inflight < 0 {
				return result, counterexample("one read per lifetime/in flight", next)
			}
			_, pending := tr.State.(Pending)
			if pending != (next.inflight == 1) {
				return result, counterexample("pending/read correspondence", next)
			}
			if _, closed := h.state.(Closed); closed && (!reflect.DeepEqual(h.state, tr.State) || len(tr.Effects) != 0 || tr.Rule != I) {
				return result, counterexample("Closed absorbing", next)
			}
			if _, withdrawn := h.state.(Withdrawn); withdrawn {
				if _, _, bound := identity(tr.State); bound && tr.Rule != R1 {
					return result, counterexample("rebind needs admission", next)
				}
			}
			if v := Project(tr.State); v.Readiness != Available && v.Isolation != UnknownIsolation {
				return result, counterexample("nonavailable isolation", next)
			}
			if o, ok := tr.State.(Observed); ok && !reflect.DeepEqual(h.state, tr.State) {
				reply, exact := e.(Reply)
				p, wasPending := h.state.(Pending)
				if !exact || !wasPending || reply.B != o.B || reply.Q != o.Q || reply.Now >= p.Deadline || !reply.Payload.valid || tr.Rule != R3 {
					return result, counterexample("positive without exact current reply", next)
				}
			}
			// Binding data is immutable except for an explicit authoritative R1.
			oldB, oldQ, oldBound := identity(h.state)
			newB, newQ, newBound := identity(tr.State)
			if oldBound && newBound && (oldB != newB || oldQ != newQ) {
				return result, counterexample("binding mutation", next)
			}
			if len(h.path) < depth {
				queue = append(queue, next)
			}
		}
	}
	return result, nil
}
func counterexample(message string, h history) error {
	return fmt.Errorf("%s: state=%#v reads=%d inflight=%d path=%#v", message, h.state, h.reads, h.inflight, h.path)
}

// All non-Pending states are waiting-for-input or terminal. For Pending, BFS
// must reach a settled state, and each timeout and cancellation exit is checked.
func live(s State) error {
	p, ok := s.(Pending)
	if !ok {
		return nil
	}
	exits := []Event{Tick{p.Deadline}, Invalidate{"disabled"}, Close{}}
	queue := []State{s}
	seen := map[State]bool{}
	for len(queue) > 0 {
		s := queue[0]
		queue = queue[1:]
		if seen[s] {
			continue
		}
		seen[s] = true
		if _, pending := s.(Pending); !pending {
			return nil
		}
		for _, e := range exits {
			t := Reduce(s, e)
			if _, pending := t.State.(Pending); pending {
				return fmt.Errorf("pending exit failed: %T", e)
			}
			if len(t.Effects) != 2 {
				return fmt.Errorf("pending exit lacks cancel/publication: %T", e)
			}
			if _, ok := t.Effects[0].(Cancel); !ok {
				return fmt.Errorf("pending exit does not cancel first: %T", e)
			}
			queue = append(queue, t.State)
		}
	}
	return fmt.Errorf("no settled/waiting state reachable")
}
func eventTime(e Event) (int64, bool) {
	switch e := e.(type) {
	case Probe:
		return e.Now, true
	case Reply:
		return e.Now, true
	case Failure:
		return e.Now, true
	case Tick:
		return e.Now, true
	case Bind, Invalidate, Close:
		return 0, false
	}
	panic("unhandled event constructor")
}
func explorationEvents() []Event {
	a := sampleBinding()
	b := a
	b.Incarnation = "incarnation-b"
	out := []Event{Invalidate{"disabled"}, Close{}}
	for _, b := range []Binding{a, b} {
		for _, q := range []ObservationID{"q", "q2"} {
			for _, eligibility := range []Eligibility{Eligible, "denied", "no-contract"} {
				out = append(out, Bind{b, q, eligibility})
			}
			for _, now := range []int64{0, 1, 2, 3} {
				out = append(out, Probe{b, q, now, 2}, Failure{b, q, "failed", now})
				for _, payload := range []Payload{InvalidPayload(), ValidPayload(Value{Available, Isolated}), ValidPayload(Value{Available, Host}), ValidPayload(Value{Unavailable, Isolated}), ValidPayload(Value{Available, UnknownIsolation})} {
					out = append(out, Reply{b, q, payload, now})
				}
			}
		}
	}
	for _, now := range []int64{0, 1, 2, 3} {
		out = append(out, Tick{now})
	}
	return out
}
