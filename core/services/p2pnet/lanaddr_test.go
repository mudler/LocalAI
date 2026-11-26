package p2pnet_test

import (
	ma "github.com/multiformats/go-multiaddr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/p2pnet"
)

var _ = DescribeTable("IsLANAddr",
	func(addr string, want bool) {
		a, err := ma.NewMultiaddr(addr)
		Expect(err).NotTo(HaveOccurred())
		Expect(p2pnet.IsLANAddr(a)).To(Equal(want))
	},
	Entry("10/8", "/ip4/10.1.2.3/tcp/1", true),
	Entry("172.16/12", "/ip4/172.20.0.2/tcp/1", true),
	Entry("192.168/16", "/ip4/192.168.1.9/udp/1/quic-v1", true),
	Entry("unique local IPv6", "/ip6/fd12:3456::1/tcp/1", true),
	Entry("a public IPv4 address", "/ip4/203.0.113.9/tcp/1", false),
	Entry("a public IPv6 address", "/ip6/2001:db8::1/tcp/1", false),
	Entry("loopback", "/ip4/127.0.0.1/tcp/1", false),
	Entry("IPv6 loopback", "/ip6/::1/tcp/1", false),
	Entry("link-local IPv4", "/ip4/169.254.3.4/tcp/1", false),
	Entry("unspecified", "/ip4/0.0.0.0/tcp/1", false),
	Entry("a DNS name", "/dns4/example.com/tcp/1", false),
)
