// SPDX-License-Identifier: MIT

package proposalflow

import (
	"strings"
	"testing"
)

// TestInvariants runs the breadth-first search from Unproposed over every
// event of the Universe. It fails on any safety violation (with the event
// path to it) and on any reachable state from which no terminal state can be
// reached (a deadlock).
func TestInvariants(t *testing.T) {
	rep := Explore()
	t.Logf("states=%d edges=%d terminals=%d deadEnds=%d violations=%d", rep.States, rep.Edges, len(rep.Terminals), len(rep.DeadEnds), len(rep.Violations))
	for _, v := range rep.Violations {
		t.Error(v)
	}
	for _, d := range rep.DeadEnds {
		t.Errorf("dead end, no event leads to a terminal state: %s", d)
	}
}

// TestSearchReachesEveryConstructor checks that the search is not vacuous.
func TestSearchReachesEveryConstructor(t *testing.T) {
	rep := Explore()
	for _, s := range AllStates() {
		if rep.ByName[s.Name()] == 0 {
			t.Errorf("the search never reached %s", s.Name())
		}
	}
}

// TestSearchSeesAnInjectedBug proves the checker can fail: a reducer that
// turns a not-sent refusal into an unknown outcome (finding a) must violate
// S8. The check runs the predicate on a hand-made step, so the real reducer is
// untouched.
func TestSearchSeesAnInjectedBug(t *testing.T) {
	p := prop(KindWrite, RoleOwner)
	bad := CheckTransition(ExecPrepared{P: p}, NotSent{NotSentSlots}, OutcomeUnknown{P: p}, nil)
	if len(bad) == 0 || !strings.Contains(strings.Join(bad, "\n"), "S8") {
		t.Fatalf("S8 did not catch a not-sent refusal turned unknown: %v", bad)
	}
	bad = CheckTransition(Pending{P: prop(KindSchema, RoleOwner)}, Confirm{Actor: "alice", Role: RoleEditor, Human: true, DigestOK: true, Now: 1},
		Admitting{P: prop(KindSchema, RoleOwner), By: "alice"}, nil)
	if len(bad) == 0 || !strings.Contains(strings.Join(bad, "\n"), "S4") {
		t.Fatalf("S4 did not catch an editor admitting a schema action: %v", bad)
	}
	bad = CheckTransition(Admitting{P: p, By: "alice"}, AdmitGranted{Now: 1, RuntimeGen: 2}, ExecPrepared{P: p},
		[]Effect{Persist{To: "executing"}, Dispatch{}})
	if len(bad) == 0 || !strings.Contains(strings.Join(bad, "\n"), "S5") {
		t.Fatalf("S5 did not catch an execute from a stale generation: %v", bad)
	}
	bad = CheckTransition(Cancelled{P: p}, ApplyResult{ApplyCommitted}, Applied{P: p}, nil)
	if len(bad) == 0 || !strings.Contains(strings.Join(bad, "\n"), "S1") {
		t.Fatalf("S1 did not catch Cancelled becoming Applied: %v", bad)
	}
	bad = CheckTransition(ExecSent{P: p}, NotSent{NotSentSlots}, Pending{P: p}, nil)
	if len(bad) == 0 || !strings.Contains(strings.Join(bad, "\n"), "S7") {
		t.Fatalf("S7 did not catch a sent proposal returning to Pending: %v", bad)
	}
}

// TestUnknownAlwaysHasAnExit is the liveness of finding (c): every blocking
// unknown situation reaches a terminal state by events the owner or the
// environment can send, including from UnknownManual with no lookups left.
func TestUnknownAlwaysHasAnExit(t *testing.T) {
	p := prop(KindWrite, RoleOwner)
	for _, s := range []State{OutcomeUnknown{P: p}, OutcomeUnknown{P: p, Lookups: 1}, UnknownManual{P: p}} {
		cur := s
		for _, e := range []Event{ownerResolve(), RetentionElapsed{}} {
			cur, _, _ = Reduce(cur, e)
		}
		if !Terminal(cur) {
			t.Errorf("from %s the owner resolution and retention end in %s, not a terminal state", Key(s), Key(cur))
		}
	}
}
