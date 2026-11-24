// SPDX-License-Identifier: MIT
package contract

import (
	"encoding/json"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("change proposals", func() {
	It("names every kind and status once", func() {
		Expect(ChangeKinds()).To(ConsistOf(ChangeMemory, ChangeProfile, ChangeSkill, ChangePage, ChangeDashboard, ChangeKnowledge, ChangeVoiceProfile))
		for _, k := range ChangeKinds() {
			Expect(ValidChangeKind(string(k))).To(BeTrue(), "kind %s", k)
		}
		Expect(ValidChangeKind("Memory")).To(BeFalse())
		Expect(ValidChangeKind("")).To(BeFalse())
		Expect(ValidChangeScope("user")).To(BeTrue())
		Expect(ValidChangeScope("agent")).To(BeTrue())
		Expect(ValidChangeScope("channel")).To(BeTrue())
		Expect(ValidChangeScope("team")).To(BeFalse())
	})

	It("tells a settled status from a pending one", func() {
		Expect(ProposalPending.Settled()).To(BeFalse())
		for _, s := range []ProposalStatus{ProposalApplied, ProposalDismissed, ProposalExpired, ProposalReverted, ProposalStale} {
			Expect(s.Settled()).To(BeTrue(), "status %s", s)
		}
	})

	It("omits proposer and project_node for an own proposal and round-trips them for a shared one", func() {
		own := ChangeProposal{V: ChangeProposalVersion, ID: "c1", Kind: ChangeMemory, Scope: ScopeProject, Status: ProposalPending}
		raw, err := json.Marshal(own)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(raw)).NotTo(ContainSubstring("proposer"))
		Expect(string(raw)).NotTo(ContainSubstring("project_node"))
		var back ChangeProposal
		Expect(json.Unmarshal(raw, &back)).To(Succeed())
		Expect(back.Proposer).To(BeEmpty())
		shared := own
		shared.Owner, shared.Proposer, shared.ProjectNode = "owner", "Alex", 42
		raw, err = json.Marshal(shared)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(raw)).To(ContainSubstring(`"proposer":"Alex"`))
		Expect(string(raw)).To(ContainSubstring(`"project_node":42`))
		back = ChangeProposal{}
		Expect(json.Unmarshal(raw, &back)).To(Succeed())
		Expect(back).To(Equal(shared))
		// A record written before the field existed reads with it empty.
		back = ChangeProposal{}
		Expect(json.Unmarshal([]byte(`{"v":1,"id":"old","kind":"memory","scope":"user","target":"x","status":"pending"}`), &back)).To(Succeed())
		Expect(back.Proposer).To(BeEmpty())
		Expect(back.ProjectNode).To(BeZero())
	})

	It("keeps the wire version and omits empty fields", func() {
		p := ChangeProposal{V: ChangeProposalVersion, ID: "c1", Kind: ChangeMemory, Scope: ScopeUser, Target: "index.md",
			Status: ProposalPending, Created: time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)}
		raw, err := json.Marshal(p)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(raw)).To(ContainSubstring(`"v":1`))
		Expect(string(raw)).NotTo(ContainSubstring(`"source"`))
		Expect(string(raw)).NotTo(ContainSubstring(`"rev"`))
	})

	It("has the limits the owner decided", func() {
		Expect(ChangeExpiry).To(Equal(24 * time.Hour))
		Expect(MaxPendingChanges).To(Equal(10))
		Expect(MaxChangesPerHour).To(Equal(5))
		Expect(MaxProfileBytes).To(Equal(4 << 10))
		Expect(MaxChangeBytes).To(Equal(16 << 10))
	})
})
