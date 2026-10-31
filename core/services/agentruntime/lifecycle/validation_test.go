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

var _ = Describe("unresolved journal snapshot coherence", func() {
	unknown := func(id string, attempt uint64) ToolAttempt {
		return ToolAttempt{Turn: id, Request: "mutation", Attempt: attempt, Status: Unknown}
	}
	reject := func(s State) {
		before := snapshot(s)
		Expect(validate(s)).NotTo(Succeed())
		for _, kind := range []string{"Complete", "CheckpointOK"} {
			tr, err := Reduce(s, testEvent(s, kind, "", nil))
			Expect(err).To(HaveOccurred(), kind)
			Expect(tr).To(Equal(Transition{}))
			Expect(snapshot(s)).To(Equal(before))
		}
	}
	It("rejects the exported checkpoint-ready repro before R25 releases FIFO", func() {
		s := NewState("owner")
		s.Attempt = 1
		s.Accepted = []string{"a", "b"}
		s.Queue = []string{"b"}
		s.Phase = Parked{Turn: "a", Reason: Checkpoint}
		s.Journal = []ToolAttempt{unknown("a", 1)}
		reject(s)
	})
	for _, p := range []Phase{Idle{}, Running{"a", NoTool}, WaitingApproval{"a", "approval"},
		WaitingAnswer{"a", "question"}, Compacting{"a"}, Parked{"a", Checkpoint}, Parked{"a", CompactionFailed}} {
		It("rejects unknown outcomes outside recovery in "+p.tag(), func() {
			s := witnessState(p)
			id := turn(p)
			if id == "" {
				id = "settled"
			}
			s.Journal = append(s.Journal, unknown(id, 1))
			reject(s)
		})
	}
	for _, p := range []Phase{Interrupted{"a", "Restart", Unknown}, Interrupted{"a", "RestoreFailed", NoTool},
		Stopping{"a"}, Restoring{"a"}, WaitingReconciliation{"a"}, Parked{"a", StoppedReason}} {
		It("holds only the same unsettled turn's unknown outcomes in "+p.tag(), func() {
			s := witnessState(p)
			s.Attempt = 2
			// Restore increments the worker attempt, not the dispatched call's identity.
			s.Journal = []ToolAttempt{unknown("a", 1)}
			Expect(validate(s)).To(Succeed())
			for _, kind := range []string{"Complete", "CheckpointOK"} {
				tr := step(s, kind, "", nil)
				Expect(tr.Rule).To(Equal("R00"))
				Expect(tr.State).To(Equal(s))
				Expect(tr.Effects).To(BeEmpty())
			}
			for _, id := range []string{"b", "settled", "unaccepted"} {
				s.Journal = []ToolAttempt{unknown(id, 1)}
				reject(s)
			}
			for _, attempt := range []uint64{0, 3} {
				s.Journal = []ToolAttempt{unknown("a", attempt)}
				reject(s)
			}
		})
	}
	It("preserves normal stopped uncertainty through restart, restore and reconciliation", func() {
		s := witnessState(Running{"a", InFlight})
		s.Journal = append(s.Journal, ToolAttempt{Turn: "a", Request: "mutation", Attempt: 1, Status: InFlight})
		s = step(s, "Stop", "", nil).State
		s = step(s, "Stopped", "", nil).State
		Expect(s.Phase).To(Equal(Parked{"a", StoppedReason}))
		Expect(s.Journal[1].Status).To(Equal(Unknown))
		s = step(s, "Restart", "", nil).State
		s = step(s, "Resume", "", nil).State
		Expect(s.Attempt).To(Equal(uint64(2)))
		Expect(s.Journal[1].Attempt).To(Equal(uint64(1)))
		s = step(s, "RestoreOK", "", nil).State
		Expect(s.Phase).To(Equal(WaitingReconciliation{"a"}))
		result := "verified"
		s = step(s, "ReconcileOK", "", map[string]any{"request": "mutation", "result": result}).State
		Expect(s.Phase).To(Equal(Running{"a", NoTool}))
		Expect(s.Queue).To(Equal([]string{"b"}))
		s = step(s, "Complete", "", nil).State
		s = step(s, "CheckpointOK", "", nil).State
		Expect(s.Settled).To(ContainElement("a"))
		Expect(s.Phase).To(Equal(Running{"b", NoTool}))
	})
	It("retains historical unknown outcomes in absorbing deletion tombstones", func() {
		s := witnessState(Deleted{})
		s.Attempt = 2
		s.Journal = append(s.Journal, unknown("a", 1), unknown("settled", 1))
		Expect(validate(s)).To(Succeed())
		for _, kind := range []string{"Complete", "CheckpointOK", "Restart"} {
			tr := step(s, kind, "", nil)
			Expect(tr.Rule).To(Equal("R02"))
			Expect(tr.State).To(Equal(s))
			Expect(tr.Effects).To(BeEmpty())
		}
	})
})
