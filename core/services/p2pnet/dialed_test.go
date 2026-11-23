package p2pnet_test

import (
	"sync"

	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/p2pnet"
)

var _ = Describe("Dialed addresses", func() {
	type seen struct {
		p peer.ID
		a ma.Multiaddr
	}
	watch := func(h *p2pnet.Host) func() []seen {
		var mu sync.Mutex
		var got []seen
		h.WatchDialedAddrs(func(p peer.ID, as []ma.Multiaddr) {
			mu.Lock()
			defer mu.Unlock()
			for _, a := range as {
				got = append(got, seen{p, a})
			}
		})
		return func() []seen {
			mu.Lock()
			defer mu.Unlock()
			return append([]seen(nil), got...)
		}
	}

	It("reports the address of a dial that worked, and not the address of a peer that dialed in", func() {
		server := newHost(p2pnet.Config{})
		client := newHost(p2pnet.Config{})
		pair(server, client)
		atClient, atServer := watch(client), watch(server)

		Expect(client.Libp2p().Connect(dialCtx(), addrInfo(server))).To(Succeed())

		Eventually(atClient).Should(HaveLen(1))
		got := atClient()[0]
		Expect(got.p).To(Equal(server.ID()))
		// The address is the one the client dialed, without /p2p/<id>.
		Expect(server.Libp2p().Addrs()).To(ContainElement(got.a))
		_, err := got.a.ValueForProtocol(ma.P_P2P)
		Expect(err).To(HaveOccurred())

		// The server saw an inbound connection: its remote address is a port
		// the client picked, and the client's announced addresses are on
		// loopback, which is never reported.
		Consistently(atServer, "300ms").Should(BeEmpty())
	})
})
