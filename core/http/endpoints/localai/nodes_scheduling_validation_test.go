package localai

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("validateSchedulingRequest", func() {
	base := func() SetSchedulingRequest {
		return SetSchedulingRequest{ModelName: "m"}
	}

	It("accepts an empty route policy (inherit) with valid thresholds", func() {
		req := base()
		req.RoutePolicy = ""
		req.MinPrefixMatch = 0.4
		req.BalanceAbsThreshold = 3
		req.BalanceRelThreshold = 0
		Expect(validateSchedulingRequest(req)).To(Succeed())
	})

	It("accepts the prefix_cache policy", func() {
		req := base()
		req.RoutePolicy = "prefix_cache"
		Expect(validateSchedulingRequest(req)).To(Succeed())
	})

	It("accepts the round_robin policy", func() {
		req := base()
		req.RoutePolicy = "round_robin"
		Expect(validateSchedulingRequest(req)).To(Succeed())
	})

	It("accepts balance_rel_threshold >= 1", func() {
		req := base()
		req.BalanceRelThreshold = 1.5
		Expect(validateSchedulingRequest(req)).To(Succeed())
	})

	It("rejects a missing model_name", func() {
		req := base()
		req.ModelName = ""
		err := validateSchedulingRequest(req)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("model_name is required"))
	})

	It("rejects an unknown route_policy (no silent default)", func() {
		req := base()
		req.RoutePolicy = "bogus"
		err := validateSchedulingRequest(req)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("route_policy"))
	})

	It("rejects min_prefix_match above 1", func() {
		req := base()
		req.MinPrefixMatch = 2
		err := validateSchedulingRequest(req)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("min_prefix_match"))
	})

	It("rejects a negative min_prefix_match", func() {
		req := base()
		req.MinPrefixMatch = -0.1
		err := validateSchedulingRequest(req)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("min_prefix_match"))
	})

	It("rejects a negative balance_abs_threshold", func() {
		req := base()
		req.BalanceAbsThreshold = -1
		err := validateSchedulingRequest(req)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("balance_abs_threshold"))
	})

	It("rejects balance_rel_threshold between 0 and 1 exclusive", func() {
		req := base()
		req.BalanceRelThreshold = 0.5
		err := validateSchedulingRequest(req)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("balance_rel_threshold"))
	})
})
