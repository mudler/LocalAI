// SPDX-License-Identifier: MIT

package proposalflow

import (
	"fmt"
	"sort"
	"strings"
)

// Terminal says the proposal reached an end that no event leaves. Abandoned is
// not terminal: a late receipt still settles it.
func Terminal(s State) bool {
	switch s.(type) {
	case Refused, Applied, Rejected, NotApplied, Cancelled, Expired, Stale, Dropped:
		return true
	case Unproposed, Pending, Admitting, ExecPrepared, ExecSent, OutcomeUnknown, UnknownManual, Abandoned:
		return false
	}
	return false
}

// Blocking says the situation blocks conflicting writes on the resource.
func Blocking(s State) bool {
	switch s.(type) {
	case ExecPrepared, ExecSent, OutcomeUnknown, UnknownManual:
		return true
	case Unproposed, Refused, Pending, Admitting, Abandoned, Applied, Rejected, NotApplied, Cancelled, Expired, Stale, Dropped:
		return false
	}
	return false
}

// PropOf returns the proposal data of a situation, if it carries any.
func PropOf(s State) (Prop, bool) {
	switch st := s.(type) {
	case Pending:
		return st.P, true
	case Admitting:
		return st.P, true
	case ExecPrepared:
		return st.P, true
	case ExecSent:
		return st.P, true
	case OutcomeUnknown:
		return st.P, true
	case UnknownManual:
		return st.P, true
	case Abandoned:
		return st.P, true
	case Applied:
		return st.P, true
	case Rejected:
		return st.P, true
	case NotApplied:
		return st.P, true
	case Cancelled:
		return st.P, true
	case Expired:
		return st.P, true
	case Stale:
		return st.P, true
	case Dropped:
		return st.P, true
	case Unproposed, Refused:
		return Prop{}, false
	}
	return Prop{}, false
}

// Key is a stable identity of a state value for the search.
func Key(s State) string { return fmt.Sprintf("%s%+v", s.Name(), s) }

// EffectNames renders effects in their short stable form.
func EffectNames(fx []Effect) []string {
	out := make([]string, len(fx))
	for i, f := range fx {
		out[i] = f.String()
	}
	return out
}

// ---------------------------------------------------------------- invariants

// CheckState checks the safety predicates that hold in every reachable state.
func CheckState(s State) []string {
	var bad []string
	if p, ok := PropOf(s); ok {
		// A schema action is only ever held by an owner's proposal. The action
		// is derived from Kind, so no state can disagree with its tool.
		if p.Kind == KindSchema && p.ProposerRole != RoleOwner {
			bad = append(bad, "schema proposal held for a non-owner: "+Key(s))
		}
		if p.Action() == ActionNone {
			bad = append(bad, "proposal without a mutating action: "+Key(s))
		}
	}
	if u, ok := s.(OutcomeUnknown); ok && (u.Lookups < 0 || u.Lookups >= MaxLookups) {
		bad = append(bad, fmt.Sprintf("lookup counter out of bound: %+v", u))
	}
	if _, ok := s.(Abandoned); ok && Blocking(s) {
		bad = append(bad, "an abandoned outcome must not block writes")
	}
	return bad
}

func postSend(s State) bool {
	switch s.(type) {
	case ExecSent, OutcomeUnknown, UnknownManual, Abandoned:
		return true
	case Unproposed, Refused, Pending, Admitting, ExecPrepared, Applied, Rejected, NotApplied, Cancelled, Expired, Stale, Dropped:
		return false
	}
	return false
}

func executing(s State) bool {
	switch s.(type) {
	case ExecPrepared, ExecSent:
		return true
	case Unproposed, Refused, Pending, Admitting, OutcomeUnknown, UnknownManual, Abandoned, Applied, Rejected, NotApplied, Cancelled, Expired, Stale, Dropped:
		return false
	}
	return false
}

func isUnknown(s State) bool {
	switch s.(type) {
	case OutcomeUnknown, UnknownManual:
		return true
	case Unproposed, Refused, Pending, Admitting, ExecPrepared, ExecSent, Abandoned, Applied, Rejected, NotApplied, Cancelled, Expired, Stale, Dropped:
		return false
	}
	return false
}

// CheckTransition checks the safety predicates of one step.
func CheckTransition(from State, e Event, to State, fx []Effect) []string {
	var bad []string
	add := func(format string, a ...any) {
		bad = append(bad, fmt.Sprintf(format, a...)+" ["+Key(from)+" --"+describe(e)+"--> "+Key(to)+"]")
	}
	// S1: a closed proposal never changes, so it is never Applied and
	// Cancelled. Only Abandoned is open, and it only ends.
	if Terminal(from) && Key(from) != Key(to) {
		add("S1 a terminal state changed")
	}
	// S2: Applied only on evidence of a commit. A dispatch is never success.
	if _, was := from.(Applied); !was {
		if _, now := to.(Applied); now {
			okEv := false
			switch ev := e.(type) {
			case ApplyResult:
				okEv = ev.Outcome == ApplyCommitted
			case ReceiptResult:
				okEv = ev.Answer == ReceiptCommitted
			}
			if !okEv {
				add("S2 Applied without a committed answer")
			}
		}
	}
	// S3: a proposal enters Admitting only by a confirmation of its own
	// proposer, from a human surface, with the stored digest, before expiry
	// and with a live role that covers the derived action.
	_, wasAdmitting := from.(Admitting)
	if _, now := to.(Admitting); now && !wasAdmitting {
		if _, was := from.(Pending); !was {
			add("S3 Admitting entered from outside Pending")
		}
		c, ok := e.(Confirm)
		p, _ := PropOf(to)
		if !ok || c.Actor != p.Proposer || !c.Human || !c.DigestOK || c.Now >= p.ExpiresAt || c.Role.rank() < p.Action().Required().rank() {
			add("S3 Admitting without a valid confirmation")
		}
		// S4: a schema action is never admitted for a non-owner.
		if p.Action() == ActionSchema && (!ok || c.Role != RoleOwner) {
			add("S4 schema action admitted for a non-owner")
		}
	}
	// S5: execution starts only from an admission for the proposal's own
	// generation, before expiry. A stale generation never executes.
	if _, now := to.(ExecPrepared); now {
		if _, was := from.(Admitting); !was {
			if _, again := from.(ExecPrepared); !again {
				add("S5 execution entered from outside Admitting")
			}
		} else {
			g, ok := e.(AdmitGranted)
			p, _ := PropOf(to)
			if !ok || g.RuntimeGen != p.Gen || g.Now >= p.ExpiresAt {
				add("S5 execution from a stale generation or expired proposal")
			}
		}
	}
	// S6: at most one dispatch, only on the step that persists executing, and
	// persist comes first.
	nd := 0
	for i, f := range fx {
		if _, ok := f.(Dispatch); ok {
			nd++
			if i == 0 || fx[i-1].String() != "persist:executing" {
				add("S6 dispatch not preceded by persist:executing")
			}
			if _, was := from.(Admitting); !was {
				add("S6 dispatch outside the admission step")
			}
		}
	}
	if nd > 1 {
		add("S6 more than one dispatch in a step")
	}
	// S7: once the request may have been sent it never goes back to a state
	// that can send again. This is what makes a second apply impossible.
	if postSend(from) {
		switch to.(type) {
		case Unproposed, Pending, Admitting, ExecPrepared:
			add("S7 a sent proposal returned to a state that can dispatch")
		case Refused, ExecSent, OutcomeUnknown, UnknownManual, Abandoned, Applied, Rejected, NotApplied, Cancelled, Expired, Stale, Dropped:
		}
	}
	if _, was := from.(ExecPrepared); was {
		if _, back := to.(Pending); back {
			n, ok := e.(NotSent)
			if !ok || (n.Cause != NotSentSlots && n.Cause != NotSentInvalid && n.Cause != NotSentWithdrawn) {
				add("S7 prepared returned to Pending without a retryable not-sent answer")
			}
		}
	}
	// S8: an unknown outcome comes only from a send that may have happened
	// (finding a), or from a restart that cannot tell. An admission refusal or
	// a not-sent answer never makes one.
	if isUnknown(to) && !isUnknown(from) {
		okSrc := false
		if executing(from) {
			switch ev := e.(type) {
			case ApplyResult:
				okSrc = ev.Outcome == ApplyUnsettled || ev.Outcome == ApplyTransportError || ev.Outcome == ApplyTimeout
			case Restart:
				okSrc = true
			}
		}
		if !okSrc {
			add("S8 unknown outcome without a send that may have reached the add-on")
		}
	}
	// S9: blocking states only exist after the dispatch step.
	if Blocking(to) && !Blocking(from) {
		if _, adm := from.(Admitting); !adm {
			add("S9 a blocking situation entered outside admission or recovery")
		}
	}
	// S10: Propose creates a state only with a role that covers the action
	// derived from the tool kind, whatever action the caller claimed.
	if _, was := from.(Unproposed); was {
		if _, now := to.(Pending); now {
			pr, _ := e.(Propose)
			if pr.Role.rank() < ActionOf(pr.Tool).Required().rank() || ActionOf(pr.Tool) == ActionNone {
				add("S10 proposal stored with a role below the derived action")
			}
		}
	}
	return bad
}

func describe(e Event) string { return fmt.Sprintf("%s%+v", e.Name(), e) }

// Describe renders an event for people.
func Describe(e Event) string { return describe(e) }

// ---------------------------------------------------------------- search

// Universe is every external event that can arrive in any state, with a small
// finite set of parameters. Each parameter varies one at a time around a
// base, plus the full role and tool grid for Propose.
func Universe() []Event {
	const exp = 10
	var u []Event
	base := Propose{ID: "op", Actor: "alice", Role: RoleOwner, Tool: KindWrite, Claimed: ActionWrite, Gen: 1, RuntimeGen: 1, ExpiresAt: exp, Now: 0}
	for _, r := range []Role{RoleViewer, RoleEditor, RoleOwner} {
		for _, k := range []ToolKind{KindUndeclared, KindRead, KindWrite, KindSchema} {
			p := base
			p.Role, p.Tool = r, k
			u = append(u, p)
		}
	}
	for _, mod := range []func(*Propose){
		func(p *Propose) { p.RuntimeGen = 2 },
		func(p *Propose) { p.Held = true },
		func(p *Propose) { p.Blocked = true },
		func(p *Propose) { p.ExpiresAt = 0 },
		func(p *Propose) { p.Claimed = ActionSchema },
	} {
		p := base
		mod(&p)
		u = append(u, p)
	}
	cb := Confirm{Actor: "alice", Role: RoleOwner, Human: true, DigestOK: true, Now: 0}
	u = append(u, cb)
	for _, mod := range []func(*Confirm){
		func(c *Confirm) { c.Role = RoleEditor },
		func(c *Confirm) { c.Role = RoleViewer },
		func(c *Confirm) { c.Role = RoleNone },
		func(c *Confirm) { c.Human = false },
		func(c *Confirm) { c.DigestOK = false },
		func(c *Confirm) { c.Actor = "bob" },
		func(c *Confirm) { c.Now = exp },
	} {
		c := cb
		mod(&c)
		u = append(u, c)
	}
	u = append(u, Cancel{Actor: "alice"}, Cancel{Actor: "bob"}, Tick{Now: 0}, Tick{Now: exp})
	u = append(u, AdmitGranted{Now: 0, RuntimeGen: 1}, AdmitGranted{Now: 0, RuntimeGen: 2}, AdmitGranted{Now: exp, RuntimeGen: 1})
	for _, c := range []AdmitCause{CauseHeld, CauseForbidden, CauseUnavailable, CauseBlocked, CauseCaller} {
		u = append(u, AdmitRefused{Cause: c})
	}
	u = append(u, SendStarted{})
	for _, c := range []NotSentCause{NotSentSlots, NotSentInvalid, NotSentWithdrawn, NotSentHold, NotSentAuthority, NotSentSuperseded} {
		u = append(u, NotSent{Cause: c})
	}
	for _, o := range []ApplyOutcome{ApplyCommitted, ApplyRejected, ApplyRefused, ApplyConflict, ApplyUnsettled, ApplyTransportError, ApplyTimeout} {
		u = append(u, ApplyResult{Outcome: o})
	}
	u = append(u, LookupDue{})
	for _, a := range []ReceiptAnswer{ReceiptCommitted, ReceiptRejected, ReceiptNotCommitted, ReceiptUnsettled} {
		u = append(u, ReceiptResult{Answer: a})
	}
	ob := OwnerResolve{Role: RoleOwner, Human: true, Reason: true}
	u = append(u, ob)
	for _, mod := range []func(*OwnerResolve){
		func(o *OwnerResolve) { o.Role = RoleEditor },
		func(o *OwnerResolve) { o.Human = false },
		func(o *OwnerResolve) { o.Reason = false },
		func(o *OwnerResolve) { o.Held = true },
	} {
		o := ob
		mod(&o)
		u = append(u, o)
	}
	u = append(u, HoldAcquired{}, HoldReleased{}, GenerationChanged{New: 2}, AuthorityLowered{}, Restart{}, RetentionElapsed{})
	return u
}

// Edge is one explored step.
type Edge struct {
	From, To string
	Event    string
	Rule     string
}

// Report is the result of the search.
type Report struct {
	States     int
	Edges      int
	Terminals  []string
	DeadEnds   []string
	Violations []string
	RuleHits   map[string]int
	// ByName counts reached situations per constructor name.
	ByName map[string]int
	// Path gives, per state key, the shortest event path from the start.
	Path map[string][]string
}

// Explore searches every state reachable from Unproposed by the Universe, checks
// the safety predicates on every state and step, and checks liveness: from
// every reachable state a terminal state must be reachable. Counters are
// bounded by MaxLookups and the finite Universe.
func Explore() Report {
	type node struct {
		s    State
		path []string
	}
	init := Unproposed{}
	seen := map[string]node{Key(init): {s: init}}
	queue := []node{{s: init}}
	succ := map[string]map[string]bool{}
	rep := Report{RuleHits: map[string]int{}, ByName: map[string]int{}, Path: map[string][]string{}}
	uni := Universe()
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		k := Key(n.s)
		for _, v := range CheckState(n.s) {
			rep.Violations = append(rep.Violations, v+" path="+strings.Join(n.path, " ; "))
		}
		succ[k] = map[string]bool{}
		for _, e := range uni {
			next, fx, rule := Reduce(n.s, e)
			rep.Edges++
			rep.RuleHits[rule]++
			if _, ok := RuleByID(rule); !ok {
				rep.Violations = append(rep.Violations, "unknown rule id "+rule)
			}
			for _, v := range CheckTransition(n.s, e, next, fx) {
				rep.Violations = append(rep.Violations, v+" path="+strings.Join(n.path, " ; "))
			}
			nk := Key(next)
			succ[k][nk] = true
			if _, ok := seen[nk]; !ok {
				nn := node{s: next, path: append(append([]string{}, n.path...), describe(e))}
				seen[nk] = nn
				queue = append(queue, nn)
			}
		}
	}
	// Liveness: reverse reachability from the terminal states.
	good := map[string]bool{}
	for k, n := range seen {
		if Terminal(n.s) {
			good[k] = true
		}
	}
	for changed := true; changed; {
		changed = false
		for k, outs := range succ {
			if good[k] {
				continue
			}
			for o := range outs {
				if good[o] {
					good[k] = true
					changed = true
					break
				}
			}
		}
	}
	for k, n := range seen {
		rep.ByName[n.s.Name()]++
		rep.Path[k] = n.path
		if Terminal(n.s) {
			rep.Terminals = append(rep.Terminals, k)
		}
		if !good[k] {
			rep.DeadEnds = append(rep.DeadEnds, k+" path="+strings.Join(n.path, " ; "))
		}
	}
	rep.States = len(seen)
	sort.Strings(rep.Terminals)
	sort.Strings(rep.DeadEnds)
	sort.Strings(rep.Violations)
	return rep
}
