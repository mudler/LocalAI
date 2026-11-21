package localai

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("validateSchedulingRequest spread_all", func() {
	It("rejects spread_all combined with min_replicas", func() {
		err := validateSchedulingRequest(SetSchedulingRequest{
			ModelName: "m", SpreadAll: true, MinReplicas: 2,
		}, "", 0, 0, 0)
		Expect(err).To(MatchError(ContainSubstring("mutually exclusive")))
	})

	It("accepts spread_all alone", func() {
		err := validateSchedulingRequest(SetSchedulingRequest{
			ModelName: "m", SpreadAll: true,
		}, "", 0, 0, 0)
		Expect(err).ToNot(HaveOccurred())
	})
})

var _ = Describe("validateSchedulingRequest upgrade_strategy", func() {
	strategy := func(s string) *string { return &s }

	It("accepts the two strategies and the default", func() {
		for _, s := range []string{"", "stop_and_start", "canary"} {
			Expect(validateSchedulingRequest(SetSchedulingRequest{ModelName: "m", UpgradeStrategy: strategy(s)}, "", 0, 0, 0)).To(Succeed(), s)
		}
	})

	It("rejects an unknown strategy", func() {
		err := validateSchedulingRequest(SetSchedulingRequest{ModelName: "m", UpgradeStrategy: strategy("rolling")}, "", 0, 0, 0)
		Expect(err).To(MatchError(ContainSubstring("upgrade_strategy")))
	})
})
