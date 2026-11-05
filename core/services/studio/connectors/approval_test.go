package connectors_test

import (
	"encoding/json"

	"github.com/mudler/LocalAI/core/services/studio/connectors"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the approval of a binding", func() {
	It("asks by default, with the default limits", func() {
		var a connectors.Approval
		Expect(a.ModeOrDefault()).To(Equal(connectors.ApprovalAsk))
		Expect(a.Auto()).To(BeFalse())
		Expect(a.Limits()).To(Equal(connectors.Limits{MaxPending: 5, MinGapMinutes: 60, MaxRunsPerDay: 10, MaxLifetimeDays: 30}))
		Expect(a.Validate()).To(Succeed())
	})

	It("knows which modes create without asking", func() {
		Expect(connectors.Approval{Mode: connectors.ApprovalAuto}.Auto()).To(BeTrue())
		Expect(connectors.Approval{Mode: connectors.ApprovalAutoReadonly}.Auto()).To(BeTrue())
		Expect(connectors.Approval{Mode: connectors.ApprovalAsk}.Auto()).To(BeFalse())
	})

	It("keeps the limits that the owner set", func() {
		a := connectors.Approval{Mode: connectors.ApprovalAuto, MaxPending: 2, MinGapMinutes: 120, MaxRunsPerDay: 4, MaxLifetimeDays: 7}
		Expect(a.Limits()).To(Equal(connectors.Limits{MaxPending: 2, MinGapMinutes: 120, MaxRunsPerDay: 4, MaxLifetimeDays: 7}))
	})

	It("refuses an unknown mode and a limit out of bounds", func() {
		Expect(connectors.Approval{Mode: "always"}.Validate()).NotTo(Succeed())
		Expect(connectors.Approval{MaxPending: -1}.Validate()).NotTo(Succeed())
		Expect(connectors.Approval{MaxPending: connectors.MaxMaxPending + 1}.Validate()).NotTo(Succeed())
		Expect(connectors.Approval{MaxLifetimeDays: connectors.MaxMaxLifetimeDays + 1}.Validate()).NotTo(Succeed())
		Expect(connectors.Approval{MaxRunsPerDay: connectors.MaxMaxRunsPerDay + 1}.Validate()).NotTo(Succeed())
		Expect(connectors.Approval{MinGapMinutes: connectors.MaxMinGapMinutes + 1}.Validate()).NotTo(Succeed())
	})

	It("allows no act by default", func() {
		var a connectors.Approval
		Expect(a.Acts).To(BeEmpty())
		Expect(a.AllowsAct("act.files")).To(BeFalse())
		Expect(a.IsZero()).To(BeTrue())
		Expect(connectors.Approval{Acts: []string{"act.send"}}.IsZero()).To(BeFalse())
	})

	It("accepts the closed list of acts and refuses anything else", func() {
		Expect(connectors.Approval{Acts: []string{"act.files", "act.addons", "act.send", "act.spend"}}.Validate()).To(Succeed())
		for _, bad := range []string{"act.root", "files", "", "act.addons:github", "memory.write"} {
			Expect(connectors.Approval{Acts: []string{bad}}.Validate()).NotTo(Succeed(), bad)
		}
	})

	It("cleans the list of acts: no duplicates, the order of the closed list", func() {
		got := connectors.Approval{Acts: []string{"act.spend", "act.files", "act.spend"}}.Cleaned()
		Expect(got.Acts).To(Equal([]string{"act.files", "act.spend"}))
		Expect(connectors.Approval{Acts: []string{}}.Cleaned().Acts).To(BeNil())
		Expect(connectors.Approval{Acts: []string{"act.x", "act.files"}}.Cleaned().Acts).To(Equal([]string{"act.files", "act.x"}), "an unknown name stays so that Validate can refuse it")
	})

	It("round-trips the acts through JSON and leaves them out when empty", func() {
		raw, err := json.Marshal(connectors.Approval{Mode: connectors.ApprovalAuto, Acts: []string{"act.files"}})
		Expect(err).NotTo(HaveOccurred())
		Expect(string(raw)).To(ContainSubstring(`"acts":["act.files"]`))
		var back connectors.Approval
		Expect(json.Unmarshal(raw, &back)).To(Succeed())
		Expect(back.Acts).To(Equal([]string{"act.files"}))
		raw, _ = json.Marshal(connectors.Approval{Mode: connectors.ApprovalAuto})
		Expect(string(raw)).NotTo(ContainSubstring("acts"))
		Expect(json.Unmarshal([]byte(`{"mode":"auto"}`), &back)).To(Succeed(), "a row stored before acts existed")
	})
})
