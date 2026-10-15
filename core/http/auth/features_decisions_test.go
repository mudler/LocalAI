package auth_test

import (
	. "github.com/mudler/LocalAI/core/http/auth"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Decisions feature registration", func() {
	It("gates the four decision routes behind one default-on API feature", func() {
		Expect(APIFeatures).To(ContainElement(FeatureDecisions))

		patterns := []string{}
		for _, route := range RouteFeatureRegistry {
			if route.Feature == FeatureDecisions {
				Expect(route.Method).To(Equal("POST"))
				patterns = append(patterns, route.Pattern)
			}
		}
		Expect(patterns).To(ConsistOf("/v1/decisions", "/v1/systemone", "/v1/systemone/permute", "/v1/systemone/separate"))

		Expect(APIFeatureMetas()).To(ContainElement(FeatureMeta{Key: FeatureDecisions, Label: "Decisions", DefaultValue: true}))
	})
})
