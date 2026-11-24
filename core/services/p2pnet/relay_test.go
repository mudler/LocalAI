package p2pnet_test

import (
	"errors"
	"io"
	"net"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	relayv2 "github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/relay"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/p2pnet"
)

// Hosts without listen addresses can only be reached through the relay, and
// cannot hole punch to a direct connection, so the path stays relayed.
var noListen = []string{}

var _ = Describe("Relays", func() {
	It("reserves on a static relay at once without AutoNAT servers, and again after the relay restarts", func() {
		relay := newTestRelay(relayv2.DefaultResources())
		server := newHost(p2pnet.Config{
			ListenAddrs:  noListen,
			StaticRelays: []peer.AddrInfo{relay.info()},
			RelayRetry:   time.Second,
		})
		Eventually(func() bool { return relay.reserved(server.ID()) }, 10*time.Second, 50*time.Millisecond).Should(BeTrue())

		relay.stop()
		relay.start()
		// The libp2p default backoff would keep the server off this relay for
		// an hour; RelayRetry brings it back in seconds.
		Eventually(func() bool { return relay.reserved(server.ID()) }, 15*time.Second, 50*time.Millisecond).Should(BeTrue())
	})

	Context("with a relay that limits each circuit", func() {
		const limitBytes = 8 << 20
		const limitDuration = 2 * time.Minute
		var relay *testRelay
		var server, client *p2pnet.Host

		BeforeEach(func() {
			res := relayv2.DefaultResources()
			res.Limit = &relayv2.RelayLimit{Data: limitBytes, Duration: limitDuration}
			relay = newTestRelay(res)
			gate := p2pnet.NewAllowlist()
			server = newHost(p2pnet.Config{
				ListenAddrs:  noListen,
				StaticRelays: []peer.AddrInfo{relay.info()},
				RelayRetry:   time.Second,
				Gate:         gate,
			})
			client = newHost(p2pnet.Config{ListenAddrs: noListen})
			gate.Allow(client.ID())
			l, err := server.Listen(protoTest, nil)
			Expect(err).ToNot(HaveOccurred())
			serve(l, echo)
			Eventually(func() bool { return relay.reserved(server.ID()) }, 10*time.Second, 50*time.Millisecond).Should(BeTrue())
		})

		dial := func(need p2pnet.Need) (net.Conn, error) {
			return client.Dial(dialCtx(), server.ID(), protoTest, p2pnet.WithRelays(relay.info()), p2pnet.WithNeed(need))
		}

		It("uses the path when the need fits the reported limits", func() {
			c, err := dial(p2pnet.Need{Bytes: 1 << 20, Duration: time.Minute})
			Expect(err).ToNot(HaveOccurred())
			defer func() { _ = c.Close() }()
			_, err = c.Write([]byte("ok"))
			Expect(err).ToNot(HaveOccurred())
			buf := make([]byte, 2)
			_, err = io.ReadFull(c, buf)
			Expect(err).ToNot(HaveOccurred())

			path := client.Path(server.ID())
			Expect(path.Kind).To(Equal(p2pnet.PathRelayed))
			Expect(path.Relay).To(Equal(relay.info().ID))
			Expect(path.Limited).To(BeTrue())
			Expect(path.LimitBytes).To(Equal(int64(limitBytes)))
			Expect(path.LimitDuration).To(Equal(limitDuration))

			Eventually(func() p2pnet.PathKind { return server.Path(client.ID()).Kind }).Should(Equal(p2pnet.PathRelayed))
			Expect(server.Path(client.ID()).LimitBytes).To(Equal(int64(limitBytes)))
		})

		It("returns PathLimitedError when the need is above the limits", func() {
			for _, need := range []p2pnet.Need{{Bytes: 100 << 20}, {Duration: time.Hour}, p2pnet.NeedBulk} {
				_, err := dial(need)
				Expect(errors.Is(err, p2pnet.ErrPathLimited)).To(BeTrue(), "need %+v: %v", need, err)
				var ple *p2pnet.PathLimitedError
				Expect(errors.As(err, &ple)).To(BeTrue())
				Expect(ple.Need).To(Equal(need))
				Expect(ple.Path.Kind).To(Equal(p2pnet.PathRelayed))
				Expect(ple.Path.LimitBytes).To(Equal(int64(limitBytes)))
			}
		})

		It("uses the path anyway with the zero need", func() {
			c, err := dial(p2pnet.Need{})
			Expect(err).ToNot(HaveOccurred())
			Expect(c.Close()).To(Succeed())
		})
	})
})

var _ = Describe("Need", func() {
	limited := p2pnet.Path{Kind: p2pnet.PathRelayed, Limited: true, LimitBytes: 1000, LimitDuration: time.Minute}
	DescribeTable("SatisfiedBy",
		func(need p2pnet.Need, path p2pnet.Path, want bool) {
			Expect(need.SatisfiedBy(path)).To(Equal(want))
		},
		Entry("any need on a direct path", p2pnet.NeedBulk, p2pnet.Path{Kind: p2pnet.PathDirect}, true),
		Entry("zero need on a limited path", p2pnet.Need{}, limited, true),
		Entry("bytes within the limit", p2pnet.Need{Bytes: 1000}, limited, true),
		Entry("bytes above the limit", p2pnet.Need{Bytes: 1001}, limited, false),
		Entry("time above the limit", p2pnet.Need{Duration: 2 * time.Minute}, limited, false),
		Entry("bulk on a data-limited path", p2pnet.NeedBulk, limited, false),
		Entry("bulk on a time-only limit", p2pnet.NeedBulk, p2pnet.Path{Limited: true, LimitDuration: time.Minute}, true),
		Entry("any need on unknown limits", p2pnet.Need{Bytes: 1}, p2pnet.Path{Limited: true}, false),
	)
})
