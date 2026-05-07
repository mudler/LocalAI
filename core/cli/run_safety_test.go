package cli

import (
	"errors"
	"net"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// withInterfaceAddrs swaps interfaceAddrsFn for the duration of one spec.
// Ginkgo's DeferCleanup restores the original after the spec finishes, so
// concurrent specs can each pretend to be running on a different host.
func withInterfaceAddrs(cidrs ...string) {
	original := interfaceAddrsFn
	interfaceAddrsFn = func() ([]net.Addr, error) {
		out := make([]net.Addr, 0, len(cidrs))
		for _, c := range cidrs {
			ip, ipnet, err := net.ParseCIDR(c)
			Expect(err).ToNot(HaveOccurred())
			out = append(out, &net.IPNet{IP: ip, Mask: ipnet.Mask})
		}
		return out, nil
	}
	DeferCleanup(func() { interfaceAddrsFn = original })
}

func withInterfaceAddrsErr(err error) {
	original := interfaceAddrsFn
	interfaceAddrsFn = func() ([]net.Addr, error) { return nil, err }
	DeferCleanup(func() { interfaceAddrsFn = original })
}

var _ = Describe("requireAuthOrTrustedBind", func() {
	BeforeEach(func() {
		// Default to "host has only loopback" — the literal-IP cases below
		// don't touch interfaceAddrsFn but the wildcard cases do, and a
		// loopback-only host is the safest default for those.
		withInterfaceAddrs("127.0.0.1/8", "::1/128")
	})

	It("permits any bind when auth is configured", func() {
		Expect(requireAuthOrTrustedBind("0.0.0.0:8080", true, false)).To(Succeed())
		Expect(requireAuthOrTrustedBind("203.0.113.5:8080", true, false)).To(Succeed())
	})

	It("permits any bind when --allow-insecure-public-bind is set", func() {
		Expect(requireAuthOrTrustedBind("0.0.0.0:8080", false, true)).To(Succeed())
		Expect(requireAuthOrTrustedBind("203.0.113.5:8080", false, true)).To(Succeed())
	})

	Context("literal IP binds", func() {
		It("permits loopback", func() {
			Expect(requireAuthOrTrustedBind("127.0.0.1:8080", false, false)).To(Succeed())
			Expect(requireAuthOrTrustedBind("[::1]:8080", false, false)).To(Succeed())
			Expect(requireAuthOrTrustedBind("127.5.4.3:8080", false, false)).To(Succeed())
		})

		DescribeTable("permits private LAN ranges",
			func(addr string) {
				Expect(requireAuthOrTrustedBind(addr, false, false)).To(Succeed())
			},
			Entry("RFC 1918 — 10/8", "10.0.0.5:8080"),
			Entry("RFC 1918 — 172.16/12", "172.16.5.5:8080"),
			Entry("RFC 1918 — 192.168/16", "192.168.1.5:8080"),
			Entry("IPv6 ULA — fc00::/7", "[fc00::1]:8080"),
			Entry("IPv6 ULA — fd00::/8", "[fd12:3456:789a::1]:8080"),
		)

		It("permits link-local addresses", func() {
			Expect(requireAuthOrTrustedBind("169.254.10.10:8080", false, false)).To(Succeed())
			Expect(requireAuthOrTrustedBind("[fe80::1]:8080", false, false)).To(Succeed())
		})

		It("permits CGNAT (Tailscale default)", func() {
			Expect(requireAuthOrTrustedBind("100.64.0.5:8080", false, false)).To(Succeed())
			Expect(requireAuthOrTrustedBind("100.127.255.1:8080", false, false)).To(Succeed())
		})

		It("rejects boundary addresses just outside CGNAT", func() {
			Expect(requireAuthOrTrustedBind("100.63.255.255:8080", false, false)).To(HaveOccurred())
			Expect(requireAuthOrTrustedBind("100.128.0.0:8080", false, false)).To(HaveOccurred())
		})

		It("rejects public IPv4", func() {
			Expect(requireAuthOrTrustedBind("8.8.8.8:8080", false, false)).To(HaveOccurred())
			Expect(requireAuthOrTrustedBind("203.0.113.5:8080", false, false)).To(HaveOccurred())
		})

		It("rejects public IPv6", func() {
			Expect(requireAuthOrTrustedBind("[2001:db8::1]:8080", false, false)).To(HaveOccurred())
		})
	})

	Context("wildcard bind (`:port`, 0.0.0.0, ::)", func() {
		It("permits when every interface is private/loopback", func() {
			withInterfaceAddrs("127.0.0.1/8", "::1/128", "10.0.0.5/24", "fc00::1/64")
			Expect(requireAuthOrTrustedBind(":8080", false, false)).To(Succeed())
			Expect(requireAuthOrTrustedBind("0.0.0.0:8080", false, false)).To(Succeed())
			Expect(requireAuthOrTrustedBind("[::]:8080", false, false)).To(Succeed())
		})

		It("permits when interfaces are loopback + Tailscale CGNAT", func() {
			withInterfaceAddrs("127.0.0.1/8", "::1/128", "100.65.10.20/32")
			Expect(requireAuthOrTrustedBind("0.0.0.0:8080", false, false)).To(Succeed())
		})

		It("rejects when ANY interface has a public IP", func() {
			withInterfaceAddrs("127.0.0.1/8", "::1/128", "10.0.0.5/24", "203.0.113.42/24")
			Expect(requireAuthOrTrustedBind(":8080", false, false)).To(HaveOccurred())
			Expect(requireAuthOrTrustedBind("0.0.0.0:8080", false, false)).To(HaveOccurred())
			Expect(requireAuthOrTrustedBind("[::]:8080", false, false)).To(HaveOccurred())
		})

		It("fails closed when interface enumeration errors", func() {
			withInterfaceAddrsErr(errors.New("enumeration disabled"))
			Expect(requireAuthOrTrustedBind("0.0.0.0:8080", false, false)).To(HaveOccurred())
		})

		It("fails closed when the host has no addresses at all", func() {
			withInterfaceAddrs()
			Expect(requireAuthOrTrustedBind("0.0.0.0:8080", false, false)).To(HaveOccurred())
		})
	})

	Context("hostname binds", func() {
		It("permits 'localhost' (resolves to loopback)", func() {
			Expect(requireAuthOrTrustedBind("localhost:8080", false, false)).To(Succeed())
		})
	})

	Context("malformed input", func() {
		It("rejects an address with no port", func() {
			Expect(requireAuthOrTrustedBind("8080", false, false)).To(HaveOccurred())
		})

		It("rejects an empty address", func() {
			Expect(requireAuthOrTrustedBind("", false, false)).To(HaveOccurred())
		})
	})

	Context("error message", func() {
		It("guides the operator with all four escape hatches", func() {
			err := requireAuthOrTrustedBind("203.0.113.5:8080", false, false)
			Expect(err).To(HaveOccurred())
			msg := err.Error()
			Expect(msg).To(ContainSubstring("--auth"))
			Expect(msg).To(ContainSubstring("--api-keys"))
			Expect(msg).To(ContainSubstring("--allow-insecure-public-bind"))
			Expect(msg).To(ContainSubstring("LAN"))
			Expect(msg).To(ContainSubstring("VPN"))
		})
	})
})
