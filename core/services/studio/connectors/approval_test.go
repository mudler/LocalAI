package connectors_test

import (
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
})
