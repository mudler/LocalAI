package grpc

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Client busy accounting", func() {
	It("remains busy until every parallel operation completes", func() {
		client := &Client{parallel: true}

		client.setBusy(true)
		client.setBusy(true)
		client.setBusy(false)
		Expect(client.IsBusy()).To(BeTrue())

		client.setBusy(false)
		Expect(client.IsBusy()).To(BeFalse())
	})

	It("does not underflow on a redundant completion", func() {
		client := &Client{parallel: true}
		client.setBusy(false)
		client.setBusy(true)
		Expect(client.IsBusy()).To(BeTrue())
	})
})
