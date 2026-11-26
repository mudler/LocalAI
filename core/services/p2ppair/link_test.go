package p2ppair_test

import (
	"errors"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/p2ppair"
	ma "github.com/multiformats/go-multiaddr"
)

func relayInfo(addr string) peer.AddrInfo {
	m, err := ma.NewMultiaddr(addr + "/p2p/" + newPeer().String())
	Expect(err).NotTo(HaveOccurred())
	ai, err := peer.AddrInfoFromP2pAddr(m)
	Expect(err).NotTo(HaveOccurred())
	return *ai
}

func fullLink() *p2ppair.Link {
	a1, _ := ma.NewMultiaddr("/ip4/203.0.113.7/tcp/4001")
	a2, _ := ma.NewMultiaddr("/ip6/2001:db8::1/udp/4001/quic-v1")
	return &p2ppair.Link{
		V: 1, Kind: p2ppair.KindRemote, Peer: newPeer(), Code: "ABCDE23456",
		Expires: time.Unix(1893456000, 0).UTC(), Label: "Ann's phone & tablet",
		Addrs:  []ma.Multiaddr{a1, a2},
		Relays: []peer.AddrInfo{relayInfo("/ip4/198.51.100.1/tcp/4001"), relayInfo("/dns4/relay.example.com/tcp/443/wss")},
		Open:   []int{1}, Pass: "pass-123_abc",
	}
}

var _ = Describe("Link", func() {
	It("round trips every field", func() {
		l := fullLink()
		got, err := p2ppair.ParseLink(l.String())
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Kind).To(Equal(l.Kind))
		Expect(got.Peer).To(Equal(l.Peer))
		Expect(got.Code).To(Equal(l.Code))
		Expect(got.Expires.Equal(l.Expires)).To(BeTrue())
		Expect(got.Label).To(Equal(l.Label))
		Expect(got.Addrs).To(HaveLen(2))
		Expect(got.Addrs[1].String()).To(Equal(l.Addrs[1].String()))
		Expect(got.Relays).To(HaveLen(2))
		Expect(got.Relays[0].ID).To(Equal(l.Relays[0].ID))
		Expect(got.Open).To(Equal([]int{1}))
		Expect(got.Pass).To(Equal("pass-123_abc"))
		Expect(got.String()).To(Equal(l.String()))
	})

	It("writes a short link with no address", func() {
		l := &p2ppair.Link{V: 1, Kind: p2ppair.KindDevice, Peer: newPeer(), Code: "ABCDE23456", Expires: t0, Label: "phone"}
		s := l.String()
		Expect(s).To(HavePrefix("localai://pair?v=1&k=device&p=12D3"))
		Expect(len(s)).To(BeNumerically("<", 140))
		_, err := p2ppair.ParseLink(s)
		Expect(err).NotTo(HaveOccurred())
	})

	It("round trips private addresses next to public ones, up to four", func() {
		l := fullLink()
		for _, s := range []string{"/ip4/192.168.1.20/tcp/4001", "/ip6/fd00::20/tcp/4001"} {
			a, err := ma.NewMultiaddr(s)
			Expect(err).NotTo(HaveOccurred())
			l.Addrs = append(l.Addrs, a)
		}
		got, err := p2ppair.ParseLink(l.String())
		Expect(err).NotTo(HaveOccurred())
		Expect(got.AddrStrings()).To(Equal(l.AddrStrings()))
		Expect(len(l.String())).To(BeNumerically("<", 1024), "a link with four addresses must stay small for a QR code")
	})

	It("ignores unknown keys", func() {
		got, err := p2ppair.ParseLink(fullLink().String() + "&zz=1&future=abc")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Code).To(Equal("ABCDE23456"))
	})

	Describe("relay hints", func() {
		withHints := func(n int) *p2ppair.Link {
			l := fullLink()
			for i := range n {
				l.Hints = append(l.Hints, relayInfo("/ip4/198.51.100."+strconv.Itoa(20+i)+"/tcp/4001"))
			}
			return l
		}

		DescribeTable("round trips",
			func(n int) {
				l := withHints(n)
				got, err := p2ppair.ParseLink(l.String())
				Expect(err).NotTo(HaveOccurred())
				Expect(got.Hints).To(HaveLen(n))
				Expect(got.HintAddrStrings()).To(Equal(l.HintAddrStrings()))
				Expect(got.String()).To(Equal(l.String()))
				Expect(got.Relays).To(HaveLen(2), "hints are not carrier relays")
				Expect(got.Open).To(Equal([]int{1}))
			},
			Entry("no hint", 0),
			Entry("one hint", 1),
			Entry("two hints", 2),
		)

		It("writes h after x, one address per hint ending in /p2p/", func() {
			l := withHints(1)
			s := l.String()
			Expect(s).To(ContainSubstring("&x=pass-123_abc&h="))
			Expect(l.HintAddrStrings()).To(HaveExactElements(HaveSuffix("/p2p/" + l.Hints[0].ID.String())))
		})

		It("parses a link whose only route is a hint", func() {
			l := &p2ppair.Link{V: 1, Kind: p2ppair.KindDevice, Peer: newPeer(), Code: "ABCDE23456", Expires: t0, Label: "phone",
				Hints: []peer.AddrInfo{relayInfo("/ip4/198.51.100.30/udp/4001/quic-v1")}}
			got, err := p2ppair.ParseLink(l.String())
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Addrs).To(BeEmpty())
			Expect(got.Relays).To(BeEmpty())
			Expect(got.Hints).To(HaveLen(1))
			Expect(got.Hints[0].ID).To(Equal(l.Hints[0].ID))
		})

		It("gives nil hints for a link without h", func() {
			got, err := p2ppair.ParseLink(fullLink().String())
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Hints).To(BeNil())
			Expect(got.HintAddrStrings()).To(BeNil())
		})

		It("keeps a link with hints valid for a parser that does not know h", func() {
			// A parser from before h treats it as an unknown key. Renaming the
			// key to one no parser knows reproduces that view of the link.
			l := withHints(2)
			old := strings.ReplaceAll(l.String(), "&h=", "&zz=")
			Expect(old).NotTo(ContainSubstring("&h="))
			got, err := p2ppair.ParseLink(old)
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Hints).To(BeNil())
			Expect(got.Code).To(Equal(l.Code))
			Expect(got.RelayAddrStrings()).To(Equal(l.RelayAddrStrings()))
			Expect(got.Pass).To(Equal(l.Pass))
		})

		It("refuses more than two hints", func() {
			_, err := p2ppair.ParseLink(withHints(3).String())
			Expect(err).To(MatchError(p2ppair.ErrBadLink))
			Expect(err.Error()).To(ContainSubstring("too many relay hints"))
		})

		It("refuses a hint that names the server or a carrier relay", func() {
			l := withHints(1)
			l.Hints[0].ID = l.Peer
			_, err := p2ppair.ParseLink(l.String())
			Expect(err).To(MatchError(p2ppair.ErrBadLink))

			l = withHints(1)
			l.Hints[0].ID = l.Relays[0].ID
			_, err = p2ppair.ParseLink(l.String())
			Expect(err).To(MatchError(p2ppair.ErrBadLink))
		})

		DescribeTable("refuses a bad hint",
			func(h string) {
				_, err := p2ppair.ParseLink(fullLink().String() + "&h=" + h)
				Expect(err).To(MatchError(p2ppair.ErrBadLink))
			},
			Entry("a bad multiaddr", "nonsense"),
			Entry("no peer ID", url.QueryEscape("/ip4/198.51.100.1/tcp/1")),
			Entry("no transport", url.QueryEscape("/p2p/"+newPeer().String())),
			Entry("over 256 bytes", url.QueryEscape("/dns4/"+strings.Repeat("a", 250)+".example.com/tcp/1/p2p/"+newPeer().String())),
		)
	})

	It("reports expiry", func() {
		l := fullLink()
		Expect(l.Expired(l.Expires.Add(-time.Second))).To(BeFalse())
		Expect(l.Expired(l.Expires)).To(BeTrue())
	})

	DescribeTable("refuses",
		func(mutate func(string) string) {
			_, err := p2ppair.ParseLink(mutate(fullLink().String()))
			Expect(err).To(HaveOccurred())
			Expect(errors.Is(err, p2ppair.ErrBadLink)).To(BeTrue())
		},
		Entry("another version", func(s string) string { return strings.Replace(s, "v=1", "v=2", 1) }),
		Entry("no version", func(s string) string { return strings.Replace(s, "v=1&", "", 1) }),
		Entry("another scheme", func(s string) string { return strings.Replace(s, "localai://", "https://", 1) }),
		Entry("another host", func(s string) string { return strings.Replace(s, "//pair", "//other", 1) }),
		Entry("an unknown kind", func(s string) string { return strings.Replace(s, "k=remote", "k=admin", 1) }),
		Entry("a bad peer ID", func(s string) string { return strings.Replace(s, "&p=", "&p=x", 1) }),
		Entry("a short code", func(s string) string { return strings.Replace(s, "ABCDE23456", "ABCDE2345", 1) }),
		Entry("a code with letters outside the alphabet", func(s string) string { return strings.Replace(s, "ABCDE23456", "ABCDE2345O", 1) }),
		Entry("a lower case code", func(s string) string { return strings.Replace(s, "ABCDE23456", "abcde23456", 1) }),
		Entry("a bad expiry", func(s string) string { return strings.Replace(s, "e=1893456000", "e=-5", 1) }),
		Entry("a repeated key", func(s string) string { return s + "&k=device" }),
		Entry("five direct addresses", func(s string) string {
			return s + "&a=%2Fip4%2F1.2.3.4%2Ftcp%2F1&a=%2Fip4%2F1.2.3.4%2Ftcp%2F2&a=%2Fip4%2F1.2.3.4%2Ftcp%2F3"
		}),
		Entry("an open index out of range", func(s string) string { return strings.Replace(s, "o=1", "o=2", 1) }),
		Entry("a relay with no peer ID", func(s string) string { return s + "&r=%2Fip4%2F1.2.3.4%2Ftcp%2F1" }),
		Entry("a bad multiaddr", func(s string) string { return s + "&a=nonsense" }),
		Entry("a link over the size limit", func(s string) string { return s + "&x=" + strings.Repeat("a", 3000) }),
		Entry("a control character in the label", func(s string) string {
			return strings.Replace(s, "&n=", "&n=%0A", 1)
		}),
	)

	It("refuses a label over 64 bytes", func() {
		l := fullLink()
		l.Label = strings.Repeat("a", 65)
		_, err := p2ppair.ParseLink(l.String())
		Expect(err).To(HaveOccurred())
	})

	It("refuses more than three relays", func() {
		l := fullLink()
		l.Open = nil
		l.Relays = append(l.Relays, relayInfo("/ip4/198.51.100.2/tcp/1"), relayInfo("/ip4/198.51.100.3/tcp/1"))
		_, err := p2ppair.ParseLink(l.String())
		Expect(err).To(HaveOccurred())
	})
})

// FuzzParseLink checks that the parser never panics and that whatever it
// accepts survives a round trip.
func FuzzParseLink(f *testing.F) {
	f.Add("localai://pair?v=1&k=device&p=12D3KooWQYhTNQdmr3ArTeUHRYzFg94BKyTkoWBDWez9kSCVe2Xo&c=ABCDE23456&e=1893456000&n=x")
	f.Add("localai://pair?v=1&k=remote&p=bad&c=&e=0&n=&o=,&r=%2Fip4%2F1.1.1.1%2Ftcp%2F1")
	f.Add("localai://pair?v=1&k=device&p=12D3KooWQYhTNQdmr3ArTeUHRYzFg94BKyTkoWBDWez9kSCVe2Xo&c=ABCDE23456&e=1893456000&n=x&h=%2Fip4%2F198.51.100.1%2Ftcp%2F4001%2Fp2p%2F12D3KooWQ6jWKvb3LfQwPzbNxQmPqYGSmrW3Fq2PTSaAVoiBAJGm")
	f.Add("localai://pair?v=1&k=device&p=12D3KooWQYhTNQdmr3ArTeUHRYzFg94BKyTkoWBDWez9kSCVe2Xo&c=ABCDE23456&e=1893456000&n=x&h=%2Fp2p%2F12D3KooWQ6jWKvb3LfQwPzbNxQmPqYGSmrW3Fq2PTSaAVoiBAJGm&h=&h=x")
	f.Add("")
	f.Fuzz(func(t *testing.T, s string) {
		g := NewWithT(t)
		l, err := p2ppair.ParseLink(s)
		if err != nil {
			g.Expect(err).To(MatchError(p2ppair.ErrBadLink), "the error must wrap ErrBadLink")
			return
		}
		out := l.String()
		again, err := p2ppair.ParseLink(out)
		g.Expect(err).ToNot(HaveOccurred(), "parsed %q but its own output %q does not parse", s, out)
		g.Expect(again.String()).To(Equal(out), "the output is not stable")
	})
}
