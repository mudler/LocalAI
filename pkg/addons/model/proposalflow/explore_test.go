// SPDX-License-Identifier: MIT

package proposalflow

import (
	"fmt"
	"strings"

	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("the invariant search", func() {
	// The search runs the breadth-first search from Unproposed over every
	// event of the Universe. It fails on any safety violation (with the event
	// path to it) and on any reachable state from which no terminal state can
	// be reached (a deadlock).
	ginkgo.It("finds no safety violation and no dead end", func() {
		rep := Explore()
		_, _ = fmt.Fprintf(ginkgo.GinkgoWriter, "states=%d edges=%d terminals=%d deadEnds=%d violations=%d\n", rep.States, rep.Edges, len(rep.Terminals), len(rep.DeadEnds), len(rep.Violations))
		Expect(rep.Violations).To(BeEmpty())
		Expect(rep.DeadEnds).To(BeEmpty(), "no event leads from a dead end to a terminal state")
	})

	// This checks that the search is not vacuous.
	ginkgo.It("reaches every constructor", func() {
		rep := Explore()
		for _, s := range AllStates() {
			Expect(rep.ByName[s.Name()]).NotTo(BeZero(), "the search never reached %s", s.Name())
		}
	})

	// This proves the checker can fail: a reducer that turns a not-sent
	// refusal into an unknown outcome (finding a) must violate S8. The check
	// runs the predicate on a hand-made step, so the real reducer is untouched.
	ginkgo.It("sees an injected bug", func() {
		p := prop(KindWrite, RoleOwner)
		catch := func(rule string, bad []string) {
			Expect(strings.Join(bad, "\n")).To(ContainSubstring(rule), "%s did not catch its violation", rule)
		}
		catch("S8", CheckTransition(ExecPrepared{P: p}, NotSent{NotSentSlots}, OutcomeUnknown{P: p}, nil))
		catch("S4", CheckTransition(Pending{P: prop(KindSchema, RoleOwner)}, Confirm{Actor: "alice", Role: RoleEditor, Human: true, DigestOK: true, Now: 1},
			Admitting{P: prop(KindSchema, RoleOwner), By: "alice"}, nil))
		catch("S5", CheckTransition(Admitting{P: p, By: "alice"}, AdmitGranted{Now: 1, RuntimeGen: 2}, ExecPrepared{P: p},
			[]Effect{Persist{To: "executing"}, Dispatch{}}))
		catch("S1", CheckTransition(Cancelled{P: p}, ApplyResult{ApplyCommitted}, Applied{P: p}, nil))
		catch("S7", CheckTransition(ExecSent{P: p}, NotSent{NotSentSlots}, Pending{P: p}, nil))
	})

	// This is the liveness of finding (c): every blocking unknown situation
	// reaches a terminal state by events the owner or the environment can
	// send, including from UnknownManual with no lookups left.
	ginkgo.It("gives every unknown outcome an exit", func() {
		p := prop(KindWrite, RoleOwner)
		for _, s := range []State{OutcomeUnknown{P: p}, OutcomeUnknown{P: p, Lookups: 1}, UnknownManual{P: p}} {
			cur := s
			for _, e := range []Event{ownerResolve(), RetentionElapsed{}} {
				cur, _, _ = Reduce(cur, e)
			}
			Expect(Terminal(cur)).To(BeTrue(), "from %s the owner resolution and retention end in %s, not a terminal state", Key(s), Key(cur))
		}
	})
})
