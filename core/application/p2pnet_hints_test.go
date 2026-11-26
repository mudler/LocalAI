package application

import (
	"crypto/rand"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/p2pnet"
)

func hintPeer() peer.ID {
	_, pub, err := crypto.GenerateEd25519Key(rand.Reader)
	Expect(err).NotTo(HaveOccurred())
	id, err := peer.IDFromPublicKey(pub)
	Expect(err).NotTo(HaveOccurred())
	return id
}

var _ = Describe("relay hints of an invite", func() {
	var self, pub1, pub2, pub3, gated peer.ID
	BeforeEach(func() {
		self, pub1, pub2, pub3, gated = hintPeer(), hintPeer(), hintPeer(), hintPeer(), hintPeer()
	})
	// circuit is the address the host lists for a reservation on relay.
	circuit := func(transport string, relay peer.ID) p2pnet.Multiaddr {
		a, err := p2pnet.ParseMultiaddr(transport + "/p2p/" + relay.String() + "/p2p-circuit/p2p/" + self.String())
		Expect(err).NotTo(HaveOccurred())
		return a
	}
	plain := func(s string) p2pnet.Multiaddr {
		a, err := p2pnet.ParseMultiaddr(s + "/p2p/" + self.String())
		Expect(err).NotTo(HaveOccurred())
		return a
	}
	hintStrings := func(hs []p2pnet.AddrInfo) []string {
		var out []string
		for _, h := range hs {
			Expect(h.Addrs).To(HaveLen(1))
			out = append(out, h.Addrs[0].String()+"/p2p/"+h.ID.String())
		}
		return out
	}

	It("keeps circuit addresses only, one per relay, and leaves out the relays of the settings", func() {
		addrs := []p2pnet.Multiaddr{
			plain("/ip4/203.0.113.9/tcp/4001"),
			circuit("/ip4/198.51.100.7/tcp/4001", gated),
			circuit("/ip6/2001:db8::1/tcp/4001", pub1),
			circuit("/ip4/192.0.2.1/udp/4001/quic-v1", pub1),
			circuit("/ip4/192.0.2.1/tcp/4001", pub1),
			circuit("/ip4/192.0.2.2/udp/4001/quic-v1", pub2),
			circuit("/ip6/2001:db8::2/tcp/4001", pub2),
		}
		got := pickRelayHints(addrs, map[string]bool{gated.String(): true}, 0)
		Expect(hintStrings(got)).To(Equal([]string{
			"/ip4/192.0.2.1/tcp/4001/p2p/" + pub1.String(),
			"/ip4/192.0.2.2/udp/4001/quic-v1/p2p/" + pub2.String(),
		}), "IPv4 TCP, then IPv4 QUIC, then IPv6")
	})

	It("prefers a public address to a private one, and keeps a private one when it is all there is", func() {
		addrs := []p2pnet.Multiaddr{
			circuit("/ip4/10.0.0.3/tcp/4001", pub1),
			circuit("/dns6/relay.example.org/udp/4001/quic-v1", pub1),
			circuit("/ip4/10.0.0.4/tcp/4001", pub2),
		}
		Expect(hintStrings(pickRelayHints(addrs, nil, 0))).To(Equal([]string{
			"/dns6/relay.example.org/udp/4001/quic-v1/p2p/" + pub1.String(),
			"/ip4/10.0.0.4/tcp/4001/p2p/" + pub2.String(),
		}))
	})

	It("keeps at most the limit, in the order the relays first appear", func() {
		addrs := []p2pnet.Multiaddr{
			circuit("/ip4/192.0.2.3/tcp/4001", pub3),
			circuit("/ip4/192.0.2.1/tcp/4001", pub1),
			circuit("/ip4/192.0.2.2/tcp/4001", pub2),
		}
		got := pickRelayHints(addrs, nil, maxRelayHints)
		Expect(got).To(HaveLen(2))
		Expect([]peer.ID{got[0].ID, got[1].ID}).To(Equal([]peer.ID{pub3, pub1}))
		Expect(pickRelayHints(addrs, nil, 0)).To(HaveLen(3))
	})

	It("finds nothing in an address list with no circuit address", func() {
		Expect(pickRelayHints([]p2pnet.Multiaddr{plain("/ip4/203.0.113.9/tcp/4001")}, nil, 0)).To(BeEmpty())
		Expect(pickRelayHints(nil, nil, 2)).To(BeEmpty())
	})

	It("gives none with the public DHT off", func() {
		rt := &p2pRuntime{cfg: config.P2PSettings{DisablePublicDHT: true}}
		Expect(rt.relayHints()).To(BeEmpty())
	})
})
