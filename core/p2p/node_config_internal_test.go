package p2p

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("newNodeConfig", func() {
	It("leaves resource limits disabled by default and when explicitly disabled", func() {
		Expect(newNodeConfig("token").Limit.Enable).To(BeFalse())

		Expect(newNodeConfig("token", NodeConfig{EnableLimits: false}).Limit.Enable).To(BeFalse())
	})

	It("enables resource limits when explicitly requested", func() {
		Expect(newNodeConfig("token", NodeConfig{EnableLimits: true}).Limit.Enable).To(BeTrue())
	})

	It("keeps the network token", func() {
		Expect(newNodeConfig("token").NetworkToken).To(Equal("token"))
	})
})
