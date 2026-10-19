// SPDX-License-Identifier: MIT
package lifecycle

import (
	"fmt"
	"slices"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// These are exactly the documentation model's 11 tags and 28 kinds, not
// exhaustive payload enumeration. Running uses None, Parked uses Checkpoint,
// and Interrupted uses Unknown. The traces exercise their other variants.
func phaseWitnesses() []Phase {
	return []Phase{Idle{}, Running{"a", NoTool}, WaitingApproval{"a", "request"},
		WaitingAnswer{"a", "request"}, Compacting{"a"}, Parked{"a", Checkpoint},
		Stopping{"a"}, Interrupted{"a", "Restart", Unknown}, Restoring{"a"},
		WaitingReconciliation{"a"}, Deleted{}}
}

func witnessState(p Phase) State {
	s := NewState("owner")
	s.Phase = p
	s.Attempt = 1
	s.Accepted = []string{"a", "b", "settled"}
	s.Settled = []string{"settled"}
	if turn(p) != "" {
		s.Queue = []string{"b"}
	}
	s.Journal = []ToolAttempt{{"settled", "old", 1, Completed, "old-result"}}
	if p.tag() == "Interrupted" || p.tag() == "Restoring" || p.tag() == "WaitingReconciliation" {
		s.Journal = append(s.Journal, ToolAttempt{"a", "request", 1, Unknown, ""})
	}
	return s
}

// Check independently of validate: accepted IDs partition into retained work
// and settled work; only a durable checkpoint may release FIFO work. Tool
// dispatch is unique by logical turn/request, including across new attempts.
func assertSafety(before State, tr Transition) {
	s := tr.State
	Expect(len(s.Queue)).To(BeNumerically("<=", 2))
	for _, ids := range [][]string{s.Queue, s.Accepted, s.Settled} {
		seen := map[string]bool{}
		for _, id := range ids {
			Expect(id).NotTo(BeEmpty())
			Expect(seen[id]).To(BeFalse(), "duplicate %q", id)
			seen[id] = true
		}
	}
	for _, id := range s.Settled {
		Expect(s.Accepted).To(ContainElement(id))
	}
	for _, id := range s.Queue {
		Expect(s.Accepted).To(ContainElement(id))
		Expect(s.Settled).NotTo(ContainElement(id))
		Expect(id).NotTo(Equal(turn(s.Phase)))
	}
	if id := turn(s.Phase); id != "" {
		Expect(s.Accepted).To(ContainElement(id))
		Expect(s.Settled).NotTo(ContainElement(id))
	}
	if s.Lease == Lost {
		Expect(active(s.Phase)).To(BeFalse())
	}
	if s.Phase.tag() == "Deleted" {
		Expect(s.Queue).To(BeEmpty())
	}
	if before.Phase.tag() == "Deleted" {
		Expect(s).To(Equal(before))
		Expect(tr.Effects).To(BeEmpty())
	}
	if tr.Rule != "R25" && tr.Rule != "R11" {
		Expect(len(s.Queue)).To(BeNumerically(">=", len(before.Queue)))
	}
	if tr.Rule == "R25" {
		Expect(s.Checkpoint).To(Equal(before.Checkpoint + 1))
		Expect(s.Settled).To(ContainElement(turn(before.Phase)))
		if len(before.Queue) > 0 {
			Expect(turn(s.Phase)).To(Equal(before.Queue[0]))
			Expect(s.Queue).To(Equal(before.Queue[1:]))
		}
	}
	for _, ef := range tr.Effects {
		switch ef := ef.(type) {
		case AuthorizedTool:
			Expect(s.Access).To(Equal(Granted))
			Expect(s.Lease).To(Equal(Owned))
			Expect(ef.Turn).To(Equal(turn(before.Phase)))
			Expect(ef.Attempt).To(Equal(s.Attempt))
			Expect(ef.Epoch).To(Equal(s.Epoch))
			for _, j := range before.Journal {
				Expect(j.Turn == ef.Turn && j.Request == ef.Request).To(BeFalse(), "redispatched journal entry")
			}
		case Run:
			Expect(tr.Rule).To(BeElementOf("R17", "R25"))
			Expect(before.Settled).NotTo(ContainElement(ef.Turn))
			if tr.Rule == "R25" {
				Expect(ef.Turn).To(Equal(before.Queue[0]))
			}
		case ContinueRestoredTurn:
			Expect(ef.Turn).To(Equal(turn(before.Phase)))
			for _, j := range s.Journal {
				Expect(j.Turn == ef.Turn && j.Status == Unknown).To(BeFalse())
			}
		}
	}
	if tr.Rule == "R20" && turn(before.Phase) != "" {
		Expect(turn(s.Phase)).To(Equal(turn(before.Phase)))
		Expect(s.Attempt).To(Equal(before.Attempt + 1))
		Expect(s.Queue).To(Equal(before.Queue))
	}
	if tr.Rule == "R03" {
		Expect(s.Phase).To(Equal(before.Phase))
		Expect(s.Journal).To(Equal(before.Journal))
		Expect(s.Queue).To(Equal(before.Queue))
	}
}

func poisonState(s *State) {
	for _, ids := range [][]string{s.Queue, s.Accepted, s.Settled} {
		if len(ids) > 0 {
			ids[0] = "poison"
		}
	}
	if len(s.Journal) > 0 {
		s.Journal[0].Result = "poison"
	}
}

var _ = Describe("phase/event witnesses", func() {
	for _, p := range phaseWitnesses() {
		for _, kind := range eventKinds {
			It(p.tag()+" / "+kind, func() {
				s := witnessState(p)
				Expect(validate(s)).To(Succeed())
				before := snapshot(s)
				e := testEvent(s, kind, "request", map[string]any{"result": "verified"})
				tr, err := Reduce(s, e)
				Expect(err).NotTo(HaveOccurred())
				Expect(tr.Rule).To(MatchRegexp(`^R(0[0-9]|[1-4][0-9]|50)$`))
				Expect(tr.Guard).NotTo(BeEmpty())
				assertSafety(s, tr)
				again, err := Reduce(s, e)
				Expect(err).NotTo(HaveOccurred())
				Expect(again).To(Equal(tr), "determinism")
				Expect(snapshot(s)).To(Equal(before), "input mutation")
				poisonState(&tr.State)
				Expect(snapshot(s)).To(Equal(before), "output aliases input")
				poisonState(&s)
				Expect(again.State.Accepted).NotTo(ContainElement("poison"), "sibling aliases input")
				Expect(again.State.Settled).NotTo(ContainElement("poison"))
				Expect(again.State.Journal[0].Result).To(Equal("old-result"))
			})
		}
	}
	It("records the exact documentation witness cardinality", func() {
		Expect(phaseWitnesses()).To(HaveLen(11))
		Expect(eventKinds).To(HaveLen(28))
		for n, p := range phaseWitnesses() {
			Expect(p.tag()).To(Equal(phaseTags[n]))
		}
		fmt.Fprintln(GinkgoWriter, "phase/event witnesses: 11 phases x 28 events = 308 executed specs")
	})
})

var _ = Describe("bounded reachable safety", func() {
	It("checks all fresh-identity successors through depth five for two input IDs", func() {
		// Exact snapshots, no quotient or revision normalization. Four policy
		// roots, two input IDs, one tool request/question and one verified result.
		// Depth five is a finite safety sample, not an unbounded model proof.
		const depth = 5
		frontier := []State{}
		seen := map[string]bool{}
		for _, policy := range []Policy{Wait, DenyContinue, StopPolicy, AllowAll} {
			s := NewState("owner")
			s.Policy = policy
			frontier = append(frontier, s)
			seen[snapshot(s)] = true
		}
		edges := 0
		for d := 0; d < depth; d++ {
			next := []State{}
			for _, s := range frontier {
				for _, kind := range eventKinds {
					ids := []string{"request"}
					if kind == "Submit" {
						ids = []string{"a", "b"}
					}
					for _, id := range ids {
						tr := step(s, kind, id, map[string]any{"result": "verified"})
						assertSafety(s, tr)
						edges++
						key := snapshot(tr.State)
						if !seen[key] {
							seen[key] = true
							next = append(next, tr.State)
						}
					}
				}
			}
			fmt.Fprintf(GinkgoWriter, "reachable depth %d: %d new states\n", d+1, len(next))
			frontier = next
		}
		Expect(edges).To(BeNumerically(">", 0))
		fmt.Fprintf(GinkgoWriter, "bounded safety: depth=%d IDs=2 policies=4 states=%d edges=%d\n", depth, len(seen), edges)
	})
})

var _ = Describe("payload isolation and invalid inputs", func() {
	It("isolates restored result payloads from input, next state and sibling effects", func() {
		s := witnessState(Restoring{"a"})
		s.Journal[1].Status, s.Journal[1].Result = Completed, "recorded"
		before := snapshot(s)
		e := testEvent(s, "RestoreOK", "", nil)
		tr, err := Reduce(s, e)
		Expect(err).NotTo(HaveOccurred())
		again, err := Reduce(s, e)
		Expect(err).NotTo(HaveOccurred())
		results := tr.Effects[0].(ReuseRecordedResults).Results
		Expect(results).To(HaveLen(1))
		results[0].Result = "poison"
		Expect(snapshot(s)).To(Equal(before))
		Expect(tr.State.Journal[1].Result).To(Equal("recorded"))
		Expect(again.Effects[0].(ReuseRecordedResults).Results[0].Result).To(Equal("recorded"))
		poisonState(&tr.State)
		Expect(again.State.Journal[0].Result).To(Equal("old-result"))
	})
	It("copies verified reconciliation data rather than retaining its pointer", func() {
		s := witnessState(WaitingReconciliation{"a"})
		result := "verified"
		e := ReconcileOK{testEvent(s, "RestoreOK", "", nil).identity(), "request", &result}
		tr, err := Reduce(s, e)
		Expect(err).NotTo(HaveOccurred())
		Expect(tr.Rule).To(Equal("R46"))
		result = "poison"
		Expect(tr.State.Journal[1].Result).To(Equal("verified"))
		Expect(s.Journal[1].Status).To(Equal(Unknown))
	})
	It("does not share checkpoint field payloads across calls", func() {
		s := witnessState(Running{"a", NoTool})
		first := step(s, "Complete", "", nil)
		second := step(s, "Complete", "", nil)
		fields := slices.Clone(second.Effects[0].(CommitCheckpoint).Fields)
		first.Effects[0].(CommitCheckpoint).Fields[0] = "poison"
		Expect(second.Effects[0].(CommitCheckpoint).Fields).To(Equal(fields))
	})
	It("rejects nil events and invalid states without mutating retained slices", func() {
		s := witnessState(Running{"a", NoTool})
		before := snapshot(s)
		_, err := Reduce(s, nil)
		Expect(err).To(HaveOccurred())
		Expect(snapshot(s)).To(Equal(before))
		s.Queue = append(s.Queue, "b")
		before = snapshot(s)
		_, err = Reduce(s, testEvent(s, "Stop", "", nil))
		Expect(err).To(HaveOccurred())
		Expect(snapshot(s)).To(Equal(before))
	})
})
