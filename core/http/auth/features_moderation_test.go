package auth_test

import (
	. "github.com/mudler/LocalAI/core/http/auth"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Moderation feature registration", func() {
	It("registers both moderation routes as default-on API features", func() {
		Expect(APIFeatures).To(ContainElement(FeatureModeration))

		patterns := []string{}
		for _, route := range RouteFeatureRegistry {
			if route.Feature == FeatureModeration {
				patterns = append(patterns, route.Pattern)
			}
		}
		Expect(patterns).To(ConsistOf("/v1/moderations", "/moderations"))

		metas := APIFeatureMetas()
		Expect(metas).To(ContainElement(FeatureMeta{Key: FeatureModeration, Label: "Moderation", DefaultValue: true}))
	})
})
