package p2p

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("newNodeConfig", func() {
	It("leaves resource limits disabled when LOCALAI_P2P_ENABLE_LIMITS is unset", func() {
		GinkgoT().Setenv("LOCALAI_P2P_ENABLE_LIMITS", "")

		Expect(newNodeConfig("token").Limit.Enable).To(BeFalse())
	})

	It("enables resource limits when LOCALAI_P2P_ENABLE_LIMITS is \"true\"", func() {
		GinkgoT().Setenv("LOCALAI_P2P_ENABLE_LIMITS", "true")

		Expect(newNodeConfig("token").Limit.Enable).To(BeTrue())
	})

	It("leaves resource limits disabled for any other value", func() {
		for _, v := range []string{"false", "1", "yes"} {
			GinkgoT().Setenv("LOCALAI_P2P_ENABLE_LIMITS", v)

			Expect(newNodeConfig("token").Limit.Enable).To(BeFalse(), "value %q", v)
		}
	})

	It("keeps the network token", func() {
		Expect(newNodeConfig("token").NetworkToken).To(Equal("token"))
	})
})
