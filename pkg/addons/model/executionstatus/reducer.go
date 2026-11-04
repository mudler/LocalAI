// SPDX-License-Identifier: MIT
package executionstatus

// Reduce consumes serialized inputs. All time is caller-provided monotonic
// time in a caller-chosen unit. Cancellation effects precede publication.
func Reduce(s State, event Event) Transition {
	ignored := Transition{s, nil, I, "ignored: pair or identity guard"}
	if _, ok := s.(Closed); ok {
		return ignored
	}
	b, q, bound := identity(s)
	emit := func(next State, r Rule, g string, effects ...Effect) Transition {
		return Transition{next, effects, r, g}
	}
	withdraw := func(next State, r Rule, g string, c Cause) Transition {
		effects := []Effect{}
		if p, ok := s.(Pending); ok {
			effects = append(effects, Cancel{p.Q})
		}
		effects = append(effects, Publish{Unknown(), c})
		return emit(next, r, g, effects...)
	}
	settle := func(p Pending, r Rule, c Cause) Transition {
		return emit(SettledUnknown{p.B, p.Q, c}, r, "exact pending completion; deadline takes precedence", Cancel{p.Q}, Publish{Unknown(), c})
	}
	switch e := event.(type) {
	case Bind:
		if bound {
			if b != e.B {
				return withdraw(Withdrawn{"binding-changed"}, R11, "bound identity changed", "binding-changed")
			}
			return ignored // Q never resets an admitted observation.
		}
		switch s.(type) {
		case Unbound, Withdrawn:
			if e.Eligibility == Eligible {
				return emit(Unobserved{e.B, e.Q}, R1, "authoritative eligible bind", Publish{Unknown(), ""})
			}
			c := Cause(e.Eligibility)
			return emit(SettledUnknown{e.B, e.Q, c}, R1, "authoritative ineligible bind", Publish{Unknown(), c})
		}
	case Probe:
		if !bound || b != e.B || q != e.Q {
			return ignored
		}
		switch s.(type) {
		case Unobserved:
			if e.Deadline <= e.Now {
				return emit(SettledUnknown{b, q, Timeout}, R9, "probe already expired", Publish{Unknown(), Timeout})
			}
			return emit(Pending{b, q, e.Deadline}, R2, "exact never-probed and future deadline", Read{b, q, e.Deadline}, Publish{Unknown(), ""})
		case Pending, Observed, SettledUnknown:
			return emit(s, R7, "exact probe already attempted or settled")
		}
	case Reply:
		if p, ok := s.(Pending); ok && p.B == e.B && p.Q == e.Q {
			if e.Now >= p.Deadline {
				return settle(p, R5, Timeout)
			}
			if !e.Payload.valid {
				return settle(p, R4, Malformed)
			}
			v := normalize(e.Payload.value)
			return emit(Observed{b, q, v}, R3, "exact valid reply before deadline", Publish{v, ""})
		}
	case Failure:
		if p, ok := s.(Pending); ok && p.B == e.B && p.Q == e.Q {
			if e.Now >= p.Deadline {
				return settle(p, R5, Timeout)
			}
			return settle(p, R4, e.Cause)
		}
	case Tick:
		if p, ok := s.(Pending); ok && e.Now >= p.Deadline {
			return settle(p, R5, Timeout)
		}
	case Invalidate:
		return withdraw(Withdrawn{e.Cause}, R6, "lifecycle invalidation", e.Cause)
	case Close:
		return withdraw(Closed{}, R10, "enclosing request closed", "")
	}
	return ignored
}
