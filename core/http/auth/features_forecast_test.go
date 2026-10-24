package auth_test

import (
	. "github.com/mudler/LocalAI/core/http/auth"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Forecast feature registration", func() {
	It("gates /v1/forecast behind a default-on API feature", func() {
		Expect(APIFeatures).To(ContainElement(FeatureForecast))

		patterns := []string{}
		for _, route := range RouteFeatureRegistry {
			if route.Feature == FeatureForecast {
				Expect(route.Method).To(Equal("POST"))
				patterns = append(patterns, route.Pattern)
			}
		}
		Expect(patterns).To(ConsistOf("/v1/forecast"))

		Expect(APIFeatureMetas()).To(ContainElement(FeatureMeta{Key: FeatureForecast, Label: "Forecast", DefaultValue: true}))
	})
})
