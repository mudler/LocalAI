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
