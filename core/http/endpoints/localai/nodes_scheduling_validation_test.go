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
		Expect(validateSchedulingRequest(base(), "", 3, 0, 0.4)).To(Succeed())
	})

	It("accepts the prefix_cache policy", func() {
		Expect(validateSchedulingRequest(base(), "prefix_cache", 0, 0, 0)).To(Succeed())
	})

	It("accepts the round_robin policy", func() {
		Expect(validateSchedulingRequest(base(), "round_robin", 0, 0, 0)).To(Succeed())
	})

	It("accepts balance_rel_threshold >= 1", func() {
		Expect(validateSchedulingRequest(base(), "", 0, 1.5, 0)).To(Succeed())
	})

	It("rejects a missing model_name", func() {
		req := base()
		req.ModelName = ""
		err := validateSchedulingRequest(req, "", 0, 0, 0)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("model_name is required"))
	})

	It("rejects an unknown route_policy (no silent default)", func() {
		err := validateSchedulingRequest(base(), "bogus", 0, 0, 0)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("route_policy"))
	})

	It("rejects min_prefix_match above 1", func() {
		err := validateSchedulingRequest(base(), "", 0, 0, 2)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("min_prefix_match"))
	})

	It("rejects a negative min_prefix_match", func() {
		err := validateSchedulingRequest(base(), "", 0, 0, -0.1)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("min_prefix_match"))
	})

	It("rejects a negative balance_abs_threshold", func() {
		err := validateSchedulingRequest(base(), "", -1, 0, 0)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("balance_abs_threshold"))
	})

	It("rejects balance_rel_threshold between 0 and 1 exclusive", func() {
		err := validateSchedulingRequest(base(), "", 0, 0.5, 0)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("balance_rel_threshold"))
	})
})
