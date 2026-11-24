package p2pnet_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/p2pnet"
)

var _ = Describe("Local network discovery", func() {
	It("does not stop a host that has no address to announce", func() {
		// libp2p's mDNS service fails to start without an IP address. That
		// must not take the host down, or mode connect would never start.
		h := newHost(p2pnet.Config{ListenAddrs: noListen, MDNS: true})
		Expect(h.ID()).ToNot(BeEmpty())
	})
})
