package auth_test

import (
	"strings"

	. "github.com/mudler/LocalAI/core/http/auth"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Plugins feature registration", func() {
	It("is a general feature that is on by default", func() {
		Expect(GeneralFeatures).To(ContainElement(FeaturePlugins))
		Expect(APIFeatures).NotTo(ContainElement(FeaturePlugins))
		Expect(AllFeatures).To(ContainElement(FeaturePlugins))
		Expect(GeneralFeatureMetas()).To(ContainElement(FeatureMeta{Key: FeaturePlugins, Label: "Plugins", DefaultValue: true}))
		// The other general features stay off by default.
		for _, m := range GeneralFeatureMetas() {
			if m.Key != FeaturePlugins {
				Expect(m.DefaultValue).To(BeFalse(), m.Key)
			}
		}
	})

	It("gates every /api/plugins route with the plugins feature, and the chat switch with the chats feature", func() {
		count := 0
		for _, r := range RouteFeatureRegistry {
			if strings.HasPrefix(r.Pattern, "/api/plugins") {
				count++
				Expect(r.Feature).To(Equal(FeaturePlugins), r.Method+" "+r.Pattern)
			}
			if r.Pattern == "/api/chats/:id/plugins" {
				Expect(r.Feature).To(Equal(FeatureChats))
			}
		}
		Expect(count).To(Equal(19))
	})
})
