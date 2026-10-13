package messaging_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/messaging"
)

// A frontend that starts before its NATS server must come up and connect when
// the server arrives. Startup relies on this: New returns a client for a URL
// nothing listens on, and refuses only a URL it cannot parse.
var _ = Describe("Client connect", func() {
	It("returns a client that keeps retrying when the server is not reachable", func() {
		c, err := messaging.New("nats://127.0.0.1:1")
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(c.Close)
		Expect(c.IsConnected()).To(BeFalse())
	})

	It("refuses a URL it cannot parse", func() {
		_, err := messaging.New("nats://[::1")
		Expect(err).To(HaveOccurred())
	})
})
