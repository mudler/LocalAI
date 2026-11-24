package safehttp_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"

	"github.com/mudler/LocalAI/pkg/web/safehttp"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Guard", func() {
	g := safehttp.NewGuard(safehttp.GuardPolicy{}, newStub())

	DescribeTable("CheckHost refuses",
		func(host string, port int, want error) {
			_, err := g.CheckHost(host, port)
			Expect(err).To(MatchError(want))
		},
		Entry("loopback literal", "127.0.0.1", 443, safehttp.ErrBlockedAddress),
		Entry("private literal", "10.0.0.5", 80, safehttp.ErrBlockedAddress),
		Entry("metadata", "169.254.169.254", 80, safehttp.ErrMetadataAddress),
		Entry("bracketed IPv6 loopback", "[::1]", 443, safehttp.ErrBlockedAddress),
		Entry("IPv4-mapped loopback", "::ffff:127.0.0.1", 443, safehttp.ErrBlockedAddress),
		Entry("decimal integer", "2130706433", 80, safehttp.ErrURL),
		Entry("hex dotted", "0x7f.1", 80, safehttp.ErrURL),
		Entry("short form", "127.1", 80, safehttp.ErrURL),
		Entry("zone", "fe80::1%eth0", 80, safehttp.ErrURL),
		Entry("localhost", "localhost", 443, safehttp.ErrBlockedAddress),
		Entry("localhost with a dot", "localhost.", 443, safehttp.ErrBlockedAddress),
		Entry("sub.localhost", "a.LOCALHOST", 443, safehttp.ErrBlockedAddress),
		Entry("port 22", "example.com", 22, safehttp.ErrPort),
		Entry("port 0", "example.com", 0, safehttp.ErrURL),
		Entry("empty", "", 443, safehttp.ErrURL),
		Entry("a path in the host", "example.com/x", 443, safehttp.ErrURL),
		Entry("userinfo in the host", "user@example.com", 443, safehttp.ErrURL),
		Entry("an international name with no valid ASCII form", "a\u200d\u200db.com", 443, safehttp.ErrURL),
		Entry("full-width digits that spell loopback", "\uff11\uff12\uff17.\uff10.\uff10.\uff11", 443, safehttp.ErrBlockedAddress),
	)

	It("returns an international name in punycode, so a look-alike shows as what it is", func() {
		h, err := g.CheckHost("p\u0430ypal.com", 443) // a Cyrillic a
		Expect(err).NotTo(HaveOccurred())
		Expect(h).To(Equal("xn--pypal-4ve.com"))
		h, err = g.CheckHost("B\u00fccher.example", 443)
		Expect(err).NotTo(HaveOccurred())
		Expect(h).To(Equal("xn--bcher-kva.example"))
		h, err = g.CheckHost("xn--bcher-kva.example", 443)
		Expect(err).NotTo(HaveOccurred())
		Expect(h).To(Equal("xn--bcher-kva.example"))
	})

	It("returns the canonical form of a name and a literal", func() {
		h, err := g.CheckHost("PyPI.org.", 443)
		Expect(err).NotTo(HaveOccurred())
		Expect(h).To(Equal("pypi.org"))
		h, err = g.CheckHost("[2606:4700:4700::1111]", 443)
		Expect(err).NotTo(HaveOccurred())
		Expect(h).To(Equal("2606:4700:4700::1111"))
	})

	It("lets private addresses through only with the switch, never metadata", func() {
		open := g.WithAllowPrivate(true)
		_, err := open.CheckHost("10.0.0.5", 80)
		Expect(err).NotTo(HaveOccurred())
		_, err = open.CheckHost("169.254.169.254", 80)
		Expect(err).To(MatchError(safehttp.ErrMetadataAddress))
		_, err = g.CheckHost("10.0.0.5", 80)
		Expect(err).To(MatchError(safehttp.ErrBlockedAddress), "the copy does not change the original")
	})

	It("refuses the addresses of DenyAddrs, even public ones, and says why", func() {
		self := netip.MustParsePrefix("93.184.215.14/32")
		d := safehttp.NewGuard(safehttp.GuardPolicy{AllowPrivate: true, DenyAddrs: func() []netip.Prefix { return []netip.Prefix{self} }}, newStub())
		_, err := d.CheckHost("93.184.215.14", 443)
		Expect(err).To(MatchError(safehttp.ErrSelfAddress))
		Expect(errors.Is(err, safehttp.ErrBlockedAddress)).To(BeTrue())
		Expect(d.AllowedAddr(netip.MustParseAddr("::ffff:93.184.215.14"))).To(MatchError(safehttp.ErrSelfAddress))
		Expect(d.AllowedAddr(netip.MustParseAddr("93.184.215.15"))).To(Succeed())
	})

	It("the Control hook refuses an address that was swapped after the check", func() {
		Expect(safehttp.Control(g, "10.0.0.1:443")).To(MatchError(safehttp.ErrBlockedAddress))
		Expect(safehttp.Control(g, "1.1.1.1:443")).To(Succeed())
		Expect(safehttp.Control(g, "not an address")).To(MatchError(safehttp.ErrBlockedAddress))
	})

	Describe("DialContext", func() {
		var (
			srv      *httptest.Server
			resolver *stubResolver
			guard    *safehttp.Guard
		)
		BeforeEach(func() {
			srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") }))
			DeferCleanup(srv.Close)
			resolver = newStub()
			guard = safehttp.NewGuard(safehttp.GuardPolicy{Ports: []int{portOf(srv)}, TreatAsPublic: []netip.Addr{loopback}}, resolver)
		})
		addr := func(host string) string { return net.JoinHostPort(host, fmt.Sprint(portOf(srv))) }

		It("dials the checked address of a public name", func() {
			resolver.set("public.test", []string{"127.0.0.1"})
			conn, err := guard.DialContext(context.Background(), "tcp", addr("public.test"))
			Expect(err).NotTo(HaveOccurred())
			_ = conn.Close()
			Expect(resolver.count("public.test")).To(Equal(1))
		})

		It("refuses a private answer, and a name with mixed answers", func() {
			resolver.set("private.test", []string{"10.1.2.3"})
			resolver.set("mixed.test", []string{"127.0.0.1", "192.168.1.1"})
			_, err := guard.DialContext(context.Background(), "tcp", addr("private.test"))
			Expect(err).To(MatchError(safehttp.ErrBlockedAddress))
			_, err = guard.DialContext(context.Background(), "tcp", addr("mixed.test"))
			Expect(err).To(MatchError(safehttp.ErrBlockedAddress))
		})

		It("refuses a rebinding name on the next dial", func() {
			resolver.set("rebind.test", []string{"127.0.0.1"}, []string{"10.9.9.9"})
			conn, err := guard.DialContext(context.Background(), "tcp", addr("rebind.test"))
			Expect(err).NotTo(HaveOccurred())
			_ = conn.Close()
			_, err = guard.DialContext(context.Background(), "tcp", addr("rebind.test"))
			Expect(err).To(MatchError(safehttp.ErrBlockedAddress))
		})

		It("refuses the server's own address", func() {
			deny := safehttp.NewGuard(safehttp.GuardPolicy{Ports: []int{portOf(srv)}, AllowPrivate: true,
				DenyAddrs: safehttp.SelfAddrs(nil)}, resolver)
			resolver.set("self.test", []string{"127.0.0.1"})
			_, err := deny.DialContext(context.Background(), "tcp", addr("self.test"))
			Expect(err).To(MatchError(safehttp.ErrSelfAddress))
		})
	})

	It("SelfAddrs lists loopback, the interfaces and the extra hosts", func() {
		list := safehttp.SelfAddrs(func() []string { return []string{"203.0.113.9"} })()
		has := func(a string) bool {
			for _, p := range list {
				if p.Contains(netip.MustParseAddr(a)) {
					return true
				}
			}
			return false
		}
		Expect(has("127.0.0.1")).To(BeTrue())
		Expect(has("127.5.5.5")).To(BeTrue())
		Expect(has("::1")).To(BeTrue())
		Expect(has("203.0.113.9")).To(BeTrue())
		Expect(has("8.8.8.8")).To(BeFalse())
	})
})

var _ = Describe("Client with DenyAddrs", func() {
	It("refuses a fetch of the server's own address even when private addresses are allowed", func() {
		self := netip.MustParsePrefix("93.184.215.14/32")
		c := safehttp.New(safehttp.Policy{AllowPrivate: true, DenyAddrs: func() []netip.Prefix { return []netip.Prefix{self} }}, newStub())
		_, err := c.CheckURL("http://93.184.215.14/")
		Expect(err).To(MatchError(safehttp.ErrBlockedAddress))
	})
})

var _ = Describe("the deny list of another process", func() {
	It("carries the server's addresses as CIDR strings and refuses them in the guard", func() {
		self := []netip.Prefix{netip.MustParsePrefix("8.8.4.4/32"), netip.MustParsePrefix("2001:db8::7/128")}
		wire := safehttp.FormatDenyList(self)
		Expect(wire).To(Equal([]string{"8.8.4.4/32", "2001:db8::7/128"}))
		back := safehttp.ParseDenyList(append(wire, "not a prefix", "::ffff:9.9.9.9/128"))
		Expect(back).To(ConsistOf(netip.MustParsePrefix("8.8.4.4/32"), netip.MustParsePrefix("2001:db8::7/128"), netip.MustParsePrefix("9.9.9.9/32")))

		g := safehttp.NewGuard(safehttp.GuardPolicy{DenyAddrs: safehttp.JoinDeny(nil, func() []netip.Prefix { return back })}, newStub())
		Expect(g.AllowedAddr(netip.MustParseAddr("8.8.4.4"))).To(MatchError(safehttp.ErrSelfAddress))
		Expect(g.AllowedAddr(netip.MustParseAddr("9.9.9.9"))).To(MatchError(safehttp.ErrSelfAddress))
		Expect(g.AllowedAddr(netip.MustParseAddr("8.8.8.8"))).To(Succeed())
	})

	It("reads at most MaxDenyList entries", func() {
		in := make([]string, safehttp.MaxDenyList+10)
		for i := range in {
			in[i] = fmt.Sprintf("10.%d.%d.1/32", i/256, i%256)
		}
		Expect(safehttp.ParseDenyList(in)).To(HaveLen(safehttp.MaxDenyList))
	})
})
