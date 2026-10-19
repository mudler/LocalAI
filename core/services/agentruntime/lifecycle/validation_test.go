// SPDX-License-Identifier: MIT
package lifecycle

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("restored snapshot invariants", func() {
	journal := func(turn string, attempt uint64) ToolAttempt {
		return ToolAttempt{Turn: turn, Request: "mutation", Attempt: attempt, Status: InFlight}
	}
	for _, tc := range []struct {
		name string
		edit func(*State)
	}{
		{"no tool with in-flight journal", func(s *State) { s.Journal = append(s.Journal, journal("a", 1)) }},
		{"in-flight tool without journal", func(s *State) { s.Phase = Running{"a", InFlight} }},
		{"multiple in-flight entries", func(s *State) {
			s.Phase = Running{"a", InFlight}
			first := journal("a", 1)
			second := first
			second.Request = "second"
			s.Journal = append(s.Journal, first, second)
		}},
		{"in-flight entry for different turn", func(s *State) {
			s.Phase = Running{"a", InFlight}
			s.Journal = append(s.Journal, journal("b", 1))
		}},
		{"in-flight entry for previous attempt", func(s *State) {
			s.Phase = Running{"a", InFlight}
			s.Attempt = 2
			s.Journal = append(s.Journal, journal("a", 1))
		}},
		{"settled ID never accepted", func(s *State) { s.Settled = append(s.Settled, "unaccepted") }},
		{"accepted ID with no retained or settled work", func(s *State) { s.Accepted = append(s.Accepted, "orphan") }},
	} {
		It("rejects "+tc.name+" before settlement or FIFO release", func() {
			s := witnessState(Running{"a", NoTool})
			tc.edit(&s)
			before := snapshot(s)
			Expect(validate(s)).NotTo(Succeed())
			for _, kind := range []string{"Complete", "CheckpointOK"} {
				tr, err := Reduce(s, testEvent(s, kind, "", nil))
				Expect(err).To(HaveOccurred(), kind)
				Expect(tr).To(Equal(Transition{}))
				Expect(snapshot(s)).To(Equal(before))
			}
		})
	}
	for _, p := range phaseWitnesses() {
		if p.tag() == "Running" {
			continue
		}
		It("rejects in-flight journal in "+p.tag(), func() {
			s := witnessState(p)
			s.Journal = []ToolAttempt{journal("a", 1)}
			Expect(validate(s)).NotTo(Succeed())
			for _, kind := range []string{"Complete", "CheckpointOK"} {
				_, err := Reduce(s, testEvent(s, kind, "", nil))
				Expect(err).To(HaveOccurred())
			}
		})
	}
	It("accepts exactly one matching in-flight entry with completed history", func() {
		s := witnessState(Running{"a", InFlight})
		s.Journal = append(s.Journal, journal("a", 1))
		Expect(validate(s)).To(Succeed())
		for _, kind := range []string{"Complete", "CheckpointOK"} {
			tr := step(s, kind, "", nil)
			Expect(tr.Rule).To(Equal("R00"))
			Expect(tr.State).To(Equal(s))
			Expect(tr.Effects).To(BeEmpty())
		}
	})
	It("retains uncertainty, not in-flight execution, after cancellation", func() {
		for _, kind := range []string{"Stop", "Restart", "Delete"} {
			s := witnessState(Running{"a", InFlight})
			s.Journal = append(s.Journal, journal("a", 1))
			tr := step(s, kind, "", nil)
			Expect(tr.State.Journal[1].Status).To(Equal(Unknown), kind)
			Expect(tr.State.Journal[0].Status).To(Equal(Completed))
		}
	})
	It("requires partitioned accepted IDs in idle and empty interruption snapshots", func() {
		for _, p := range []Phase{Idle{}, Interrupted{Tool: NoTool}} {
			s := NewState("owner")
			s.Phase = p
			s.Accepted = []string{"orphan"}
			Expect(validate(s)).NotTo(Succeed())
			s.Settled = []string{"orphan"}
			Expect(validate(s)).To(Succeed())
		}
	})
	It("allows deleted tombstones to retain accepted but discarded work", func() {
		s := NewState("owner")
		s.Phase = Deleted{}
		s.Accepted = []string{"discarded", "settled"}
		s.Settled = []string{"settled"}
		Expect(validate(s)).To(Succeed())
		s.Settled = append(s.Settled, "unaccepted")
		Expect(validate(s)).NotTo(Succeed())
	})
})
