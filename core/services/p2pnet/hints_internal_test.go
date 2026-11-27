package p2pnet

import (
	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Hints that a dial uses", func() {
	r1, r2 := peer.ID("relay-1"), peer.ID("relay-2")
	hint := func(id peer.ID, addrs ...string) peer.AddrInfo {
		ai := peer.AddrInfo{ID: id}
		for _, a := range addrs {
			ai.Addrs = append(ai.Addrs, ma.StringCast(a))
		}
		return ai
	}

	It("keeps only public IP addresses, one entry for each relay", func() {
		got := usableHints([]peer.AddrInfo{
			hint(r1, "/ip4/10.0.0.1/tcp/4001", "/ip4/1.2.3.4/tcp/4001", "/dns4/relay.example.com/tcp/4001"),
			hint(r2, "/ip4/127.0.0.1/tcp/4001", "/ip6zone/eth0/ip6/fe80::1/tcp/4001", "/ip4/192.168.1.1/udp/4001/quic-v1"),
			hint(r1, "/ip4/1.2.3.4/tcp/4001", "/ip6/2606:4700::1/udp/4001/quic-v1"),
		}, false)
		Expect(got).To(Equal([]peer.AddrInfo{
			hint(r1, "/ip4/1.2.3.4/tcp/4001", "/ip6/2606:4700::1/udp/4001/quic-v1"),
		}))
	})

	It("keeps private addresses when the host allows them", func() {
		got := usableHints([]peer.AddrInfo{hint(r1, "/ip4/127.0.0.1/tcp/4001", "/dns4/localhost/tcp/4001")}, true)
		Expect(got).To(Equal([]peer.AddrInfo{hint(r1, "/ip4/127.0.0.1/tcp/4001")}))
	})
})
