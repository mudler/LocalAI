package p2prelay_test

import (
	"github.com/libp2p/go-libp2p/core/peer"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/p2prelay"
)

var _ = Describe("NewInProcess", func() {
	It("relays only between admitted peers", func() {
		server, a, b, stranger := newPeer(), newPeer(), newPeer(), newPeer()
		paired := map[string]bool{a.ID().String(): true, b.ID().String(): true}
		admit := func(p peer.ID) bool { return paired[p.String()] }
		lim := p2prelay.DefaultLimits()
		closer, err := p2prelay.NewInProcess(server, admit, lim)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(closer.Close)
		r := &testRelay{h: server}

		Expect(reserve(a, r)).To(Succeed())
		Expect(reserve(stranger, r)).ToNot(Succeed())
		Expect(circuit(b, a, r)).To(Succeed())
		n, _, err := download(b, a, 64<<10)
		Expect(err).ToNot(HaveOccurred())
		Expect(n).To(BeEquivalentTo(64 << 10))
		Expect(circuit(stranger, a, r)).ToNot(Succeed())
	})
})
