package p2pnet_test

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/peerstore"
	relayv2 "github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/relay"
	ma "github.com/multiformats/go-multiaddr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/p2pnet"
	"github.com/mudler/LocalAI/pkg/httpclient"
)

// The test punch wait is short: loopback addresses are not public, so no
// hole punch can succeed, and a failing dial waits this long.
const testPunchWait = 2 * time.Second

// streamLog records the streams that a listener accepted and whether any of
// them came over a relayed connection.
type streamLog struct {
	accepted atomic.Int32
	relayed  atomic.Int32
}

func (l *streamLog) record(c net.Conn) {
	l.accepted.Add(1)
	if pc, ok := c.(*p2pnet.Conn); ok && isCircuit(pc.Stream.Conn()) {
		l.relayed.Add(1)
	}
}

func isCircuit(c network.Conn) bool {
	for _, comp := range c.RemoteMultiaddr() {
		if comp.Protocol().Name == "p2p-circuit" {
			return true
		}
	}
	return false
}

// connsThrough counts the connections from h to p that go through relay.
func connsThrough(h *p2pnet.Host, p, relay peer.ID) int {
	n := 0
	for _, c := range h.Libp2p().Network().ConnsToPeer(p) {
		if isCircuit(c) && relayIn(c, relay) {
			n++
		}
	}
	return n
}

func relayIn(c network.Conn, relay peer.ID) bool {
	for _, comp := range c.RemoteMultiaddr() {
		if comp.Protocol().Name == "p2p" && comp.Value() == relay.String() {
			return true
		}
	}
	return false
}

// carrierResources are the limits of a gated relay in these tests: limited,
// but large enough for the tests' traffic.
func carrierResources() relayv2.Resources {
	res := relayv2.DefaultResources()
	res.Limit = &relayv2.RelayLimit{Data: 8 << 20, Duration: 2 * time.Minute}
	return res
}

var _ = Describe("Dial with hint relays", func() {
	var hint *testRelay
	var client *p2pnet.Host
	var log *streamLog

	// startServer starts a server that holds a reservation on relays, admits
	// client and echoes on protoTest.
	startServer := func(listen []string, relays ...*testRelay) *p2pnet.Host {
		GinkgoHelper()
		gate := p2pnet.NewAllowlist(client.ID())
		var static []peer.AddrInfo
		for _, r := range relays {
			static = append(static, r.info())
		}
		server := newHost(p2pnet.Config{ListenAddrs: listen, StaticRelays: static, RelayRetry: time.Second, Gate: gate})
		l, err := server.Listen(protoTest, nil)
		Expect(err).ToNot(HaveOccurred())
		serve(l, func(c net.Conn) {
			log.record(c)
			echo(c)
		})
		for _, r := range relays {
			Eventually(func() bool { return r.reserved(server.ID()) }, 10*time.Second, 50*time.Millisecond).Should(BeTrue())
		}
		return server
	}

	BeforeEach(func() {
		// The default resources limit each circuit, as a public relay does.
		hint = newTestRelay(relayv2.DefaultResources())
		client = newHost(p2pnet.Config{})
		log = &streamLog{}
	})

	It("waits for a direct path, opens no stream over the hint and returns NoDirectPathError", func() {
		server := startServer(noListen, hint)
		paths := server.WatchPaths(context.Background())

		start := time.Now()
		_, err := client.Dial(dialCtx(), server.ID(), protoTest,
			p2pnet.WithHints(hint.info()), p2pnet.WithPunchWait(testPunchWait))
		Expect(time.Since(start)).To(BeNumerically(">=", testPunchWait))
		Expect(errors.Is(err, p2pnet.ErrNoDirectPath)).To(BeTrue(), "%v", err)
		var nde *p2pnet.NoDirectPathError
		Expect(errors.As(err, &nde)).To(BeTrue())
		Expect(nde.Peer).To(Equal(server.ID()))
		Expect(nde.Relay).To(Equal(hint.info().ID))

		// The device reached the server through the hint, so that a hole
		// punch could run, but sent nothing over it and closed it.
		Eventually(paths).Should(Receive(SatisfyAll(
			HaveField("Peer", client.ID()),
			HaveField("Path.Kind", p2pnet.PathRelayed),
		)))
		Expect(log.accepted.Load()).To(BeZero())
		Eventually(func() int { return connsThrough(client, server.ID(), hint.info().ID) }).Should(BeZero())
		Consistently(func() int32 { return log.accepted.Load() }, 500*time.Millisecond).Should(BeZero())
	})

	It("uses the direct connection that opens during the wait and closes the hint connection", func() {
		server := startServer(loopback, hint)
		go func() {
			defer GinkgoRecover()
			// A direct connection opens while the dial waits, as after a
			// hole punch, which also forces a direct dial.
			time.Sleep(500 * time.Millisecond)
			ctx, cancel := context.WithTimeout(network.WithForceDirectDial(context.Background(), "test"), 5*time.Second)
			defer cancel()
			Expect(client.Libp2p().Connect(ctx, addrInfo(server))).To(Succeed())
		}()

		c, err := client.Dial(dialCtx(), server.ID(), protoTest,
			p2pnet.WithHints(hint.info()), p2pnet.WithPunchWait(5*time.Second))
		Expect(err).ToNot(HaveOccurred())
		defer func() { _ = c.Close() }()
		_, err = c.Write([]byte("ok"))
		Expect(err).ToNot(HaveOccurred())
		buf := make([]byte, 2)
		_, err = io.ReadFull(c, buf)
		Expect(err).ToNot(HaveOccurred())

		Expect(client.Path(server.ID()).Kind).ToNot(Equal(p2pnet.PathRelayed))
		Expect(log.accepted.Load()).To(Equal(int32(1)))
		Expect(log.relayed.Load()).To(BeZero())
		Eventually(func() int { return connsThrough(client, server.ID(), hint.info().ID) }).Should(BeZero())
	})

	// In a hole punch the direct connection may reach the dialling host as
	// an inbound connection: the peer's direct dial before the punch, the
	// switched roles of the last attempt, or a TCP simultaneous open that
	// the listening socket accepts. The dialling host's empty allowlist
	// refuses inbound peers, so it must admit the peer it waits for, and
	// only that peer.
	It("admits the inbound direct connection of the peer it waits for, and no stranger", func() {
		client = newHost(p2pnet.Config{ListenAddrs: loopback})
		server := startServer(noListen, hint)
		stranger := newHost(p2pnet.Config{ListenAddrs: noListen})
		strangerDone := make(chan struct{})
		go func() {
			defer GinkgoRecover()
			// Wait until the client reaches the server through the hint, as
			// a hole punch does, then dial back directly.
			Eventually(func() int { return connsThrough(server, client.ID(), hint.info().ID) }, 5*time.Second, 20*time.Millisecond).
				Should(BeNumerically(">", 0))
			ctx, cancel := context.WithTimeout(network.WithForceDirectDial(context.Background(), "test"), 5*time.Second)
			defer cancel()
			// The dialler may finish its handshake before the client's gate
			// closes the connection, so the result is checked on the client.
			_ = stranger.Libp2p().Connect(ctx, addrInfo(client))
			close(strangerDone)
			Expect(server.Libp2p().Connect(ctx, addrInfo(client))).To(Succeed())
		}()

		c, err := client.Dial(dialCtx(), server.ID(), protoTest,
			p2pnet.WithHints(hint.info()), p2pnet.WithPunchWait(5*time.Second))
		Expect(err).ToNot(HaveOccurred())
		defer func() { _ = c.Close() }()
		_, err = c.Write([]byte("ok"))
		Expect(err).ToNot(HaveOccurred())
		buf := make([]byte, 2)
		_, err = io.ReadFull(c, buf)
		Expect(err).ToNot(HaveOccurred())

		pc, ok := c.(*p2pnet.Conn)
		Expect(ok).To(BeTrue())
		Expect(isCircuit(pc.Stream.Conn())).To(BeFalse())
		Expect(pc.Stream.Conn().Stat().Direction).To(Equal(network.DirInbound))
		Expect(log.relayed.Load()).To(BeZero())

		// A stranger that dialled in during the same wait was refused.
		Eventually(strangerDone).Should(BeClosed())
		Eventually(func() []network.Conn { return client.Libp2p().Network().ConnsToPeer(stranger.ID()) }).Should(BeEmpty())

		// After the dial the server gets no new connection either: the
		// admission lasted only while the dial waited.
		Expect(client.Libp2p().Network().ClosePeer(server.ID())).To(Succeed())
		ctx, cancel := context.WithTimeout(network.WithForceDirectDial(context.Background(), "test"), 5*time.Second)
		defer cancel()
		_ = server.Libp2p().Connect(ctx, addrInfo(client))
		Eventually(func() []network.Conn { return client.Libp2p().Network().ConnsToPeer(server.ID()) }).Should(BeEmpty())
		Consistently(func() []network.Conn { return client.Libp2p().Network().ConnsToPeer(server.ID()) }, 300*time.Millisecond).Should(BeEmpty())
	})

	It("never sends over a hint connection, even when a later dial has no hints", func() {
		server := startServer(noListen, hint)
		_, err := client.Dial(dialCtx(), server.ID(), protoTest,
			p2pnet.WithHints(hint.info()), p2pnet.WithPunchWait(testPunchWait))
		Expect(errors.Is(err, p2pnet.ErrNoDirectPath)).To(BeTrue(), "%v", err)

		// The peerstore may still know the circuit address through the hint,
		// for example from identify. A plain dial does not use it.
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, err = client.Dial(ctx, server.ID(), protoTest)
		Expect(err).To(HaveOccurred())
		Expect(log.accepted.Load()).To(BeZero())
		Expect(connsThrough(client, server.ID(), hint.info().ID)).To(BeZero())
	})

	Context("relays that no dial named as carriers", func() {
		// circuit is the address of p through r that identify or a DHT
		// lookup stores: the peer announces its circuit address through
		// each relay it holds a reservation on.
		circuit := func(r *testRelay) ma.Multiaddr {
			GinkgoHelper()
			ai := r.info()
			m, err := ma.NewMultiaddr(ai.Addrs[0].String() + "/p2p/" + ai.ID.String() + "/p2p-circuit")
			Expect(err).ToNot(HaveOccurred())
			return m
		}

		It("sends nothing through another public relay of the peer that the peerstore knows", func() {
			other := newTestRelay(relayv2.DefaultResources())
			server := startServer(noListen, hint, other)
			client.Libp2p().Peerstore().AddAddr(server.ID(), circuit(other), peerstore.RecentlyConnectedAddrTTL)
			dead, err := ma.NewMultiaddr("/ip4/127.0.0.1/tcp/1")
			Expect(err).ToNot(HaveOccurred())

			// The direct address fails, so the first phase may take any
			// limited path: it must not take the one through the other relay.
			_, err = client.Dial(dialCtx(), server.ID(), protoTest,
				p2pnet.WithAddrs(dead), p2pnet.WithHints(hint.info()), p2pnet.WithPunchWait(testPunchWait))
			Expect(errors.Is(err, p2pnet.ErrNoDirectPath)).To(BeTrue(), "%v", err)
			Expect(log.accepted.Load()).To(BeZero())

			// A later plain dial does not use it either.
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_, err = client.Dial(ctx, server.ID(), protoTest)
			Expect(err).To(HaveOccurred())
			Expect(log.accepted.Load()).To(BeZero())
			Expect(connsThrough(client, server.ID(), other.info().ID)).To(BeZero())
		})

		It("keeps the hint of a peer when a dial to another peer names the same relay as a carrier", func() {
			server := startServer(noListen, hint)
			_, err := client.Dial(dialCtx(), server.ID(), protoTest,
				p2pnet.WithHints(hint.info()), p2pnet.WithPunchWait(testPunchWait))
			Expect(errors.Is(err, p2pnet.ErrNoDirectPath)).To(BeTrue(), "%v", err)

			// The invite of another server names the same public relay as a
			// relay that may carry its data.
			other := newHost(p2pnet.Config{ListenAddrs: noListen})
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_, _ = client.Dial(ctx, other.ID(), protoTest, p2pnet.WithRelays(hint.info()))
			cancel()

			client.Libp2p().Peerstore().AddAddr(server.ID(), circuit(hint), peerstore.RecentlyConnectedAddrTTL)
			ctx, cancel = context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_, err = client.Dial(ctx, server.ID(), protoTest)
			Expect(err).To(HaveOccurred())
			Expect(log.accepted.Load()).To(BeZero())
		})

		It("keeps a carrier of a peer when a dial to another peer names the same relay as a hint", func() {
			shared := newTestRelay(carrierResources())
			carried := startServer(noListen, shared)
			c, err := client.Dial(dialCtx(), carried.ID(), protoTest, p2pnet.WithRelays(shared.info()))
			Expect(err).ToNot(HaveOccurred())
			Expect(c.Close()).To(Succeed())

			// Another server's invite names the relay as a hint.
			hinted := newHost(p2pnet.Config{ListenAddrs: noListen})
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_, _ = client.Dial(ctx, hinted.ID(), protoTest, p2pnet.WithHints(shared.info()), p2pnet.WithPunchWait(time.Second))
			cancel()

			// The first server still gets its data over the relay, also on a
			// dial that does not repeat the options.
			c, err = client.Dial(dialCtx(), carried.ID(), protoTest)
			Expect(err).ToNot(HaveOccurred())
			defer func() { _ = c.Close() }()
			_, err = c.Write([]byte("ok"))
			Expect(err).ToNot(HaveOccurred())
			buf := make([]byte, 2)
			_, err = io.ReadFull(c, buf)
			Expect(err).ToNot(HaveOccurred())
			Expect(client.Path(carried.ID()).Relay).To(Equal(shared.info().ID))
		})
	})

	It("does not dial a hint at a private address unless the host allows it", func() {
		client = newHostWith(p2pnet.Config{})
		server := startServer(noListen, hint)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, err := client.Dial(ctx, server.ID(), protoTest,
			p2pnet.WithHints(hint.info(), hint.info()), p2pnet.WithPunchWait(testPunchWait))
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, p2pnet.ErrNoDirectPath)).To(BeFalse(), "the host never reached the server: %v", err)
		Expect(connsThrough(client, server.ID(), hint.info().ID)).To(BeZero())
		Expect(log.accepted.Load()).To(BeZero())
	})

	Context("phase order", func() {
		var carrier *testRelay

		BeforeEach(func() {
			carrier = newTestRelay(carrierResources())
		})

		It("prefers a direct address to a carrier and a hint", func() {
			server := startServer(loopback, carrier, hint)
			c, err := client.Dial(dialCtx(), server.ID(), protoTest,
				p2pnet.WithAddrs(server.Libp2p().Addrs()...),
				p2pnet.WithRelays(carrier.info()), p2pnet.WithHints(hint.info()))
			Expect(err).ToNot(HaveOccurred())
			Expect(c.Close()).To(Succeed())
			Expect(client.Path(server.ID()).Kind).To(Equal(p2pnet.PathDirect))
			Expect(connsThrough(client, server.ID(), hint.info().ID)).To(BeZero())
		})

		It("prefers a carrier relay to a hint and sends over the limited carrier", func() {
			server := startServer(noListen, carrier, hint)
			c, err := client.Dial(dialCtx(), server.ID(), protoTest,
				p2pnet.WithRelays(carrier.info()), p2pnet.WithHints(hint.info()),
				p2pnet.WithPunchWait(testPunchWait))
			Expect(err).ToNot(HaveOccurred())
			defer func() { _ = c.Close() }()
			_, err = c.Write([]byte("ok"))
			Expect(err).ToNot(HaveOccurred())
			buf := make([]byte, 2)
			_, err = io.ReadFull(c, buf)
			Expect(err).ToNot(HaveOccurred())

			path := client.Path(server.ID())
			Expect(path.Kind).To(Equal(p2pnet.PathRelayed))
			Expect(path.Relay).To(Equal(carrier.info().ID))
			Expect(path.Limited).To(BeTrue())
			Expect(connsThrough(client, server.ID(), hint.info().ID)).To(BeZero())
			Expect(log.relayed.Load()).To(Equal(int32(1)))
		})

		It("tries the hints only after the carrier failed", func() {
			server := startServer(noListen, carrier, hint)
			carrier.stop()
			_, err := client.Dial(dialCtx(), server.ID(), protoTest,
				p2pnet.WithRelays(carrier.info()), p2pnet.WithHints(hint.info()),
				p2pnet.WithPunchWait(testPunchWait))
			Expect(errors.Is(err, p2pnet.ErrNoDirectPath)).To(BeTrue(), "%v", err)
			Expect(log.accepted.Load()).To(BeZero())
		})
	})
})

var _ = Describe("Forward with hint relays", func() {
	var client, server *p2pnet.Host
	var log *streamLog

	startForward := func(opts ...p2pnet.DialOption) (string, chan p2pnet.ForwardStatus) {
		GinkgoHelper()
		states := make(chan p2pnet.ForwardStatus, 16)
		fwd, err := client.Forward(context.Background(), server.ID(), p2pnet.ProtoHTTP, freePort(),
			p2pnet.WithDialOptions(opts...),
			p2pnet.WithStatus(func(s p2pnet.ForwardStatus) { states <- s }))
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(fwd.Close)
		return "http://" + fwd.Addr().String(), states
	}

	startServer := func(relays ...*testRelay) {
		GinkgoHelper()
		var static []peer.AddrInfo
		for _, r := range relays {
			static = append(static, r.info())
		}
		server = newHost(p2pnet.Config{
			ListenAddrs: noListen, StaticRelays: static, RelayRetry: time.Second,
			Gate: p2pnet.NewAllowlist(client.ID()),
		})
		l, err := server.Listen(p2pnet.ProtoHTTP, nil)
		Expect(err).ToNot(HaveOccurred())
		srv := &http.Server{Handler: testHandler(), ReadHeaderTimeout: 5 * time.Second, ConnState: func(c net.Conn, st http.ConnState) {
			if st == http.StateNew {
				log.record(c)
			}
		}}
		go func() { _ = srv.Serve(l) }()
		DeferCleanup(srv.Close)
		for _, r := range relays {
			Eventually(func() bool { return r.reserved(server.ID()) }, 10*time.Second, 50*time.Millisecond).Should(BeTrue())
		}
	}

	BeforeEach(func() {
		client = newHost(p2pnet.Config{})
		log = &streamLog{}
	})

	It("refuses a hint connection and reports that a relay is needed", func() {
		hint := newTestRelay(relayv2.DefaultResources())
		startServer(hint)
		base, states := startForward(p2pnet.WithHints(hint.info()), p2pnet.WithPunchWait(testPunchWait))

		_, err := httpclient.NewWithTimeout(30 * time.Second).Get(base + "/hello")
		Expect(err).To(HaveOccurred())
		var st p2pnet.ForwardStatus
		Eventually(states, 30*time.Second).Should(Receive(&st))
		Expect(st.State).To(Equal(p2pnet.ForwardDown))
		Expect(errors.Is(st.Err, p2pnet.ErrNoDirectPath)).To(BeTrue(), "%v", st.Err)
		Expect(log.accepted.Load()).To(BeZero())
	})

	It("forwards over a limited gated relay", func() {
		carrier := newTestRelay(carrierResources())
		startServer(carrier)
		base, states := startForward(p2pnet.WithRelays(carrier.info()))

		resp, err := httpclient.New().Get(base + "/hello")
		Expect(err).ToNot(HaveOccurred())
		defer func() { _ = resp.Body.Close() }()
		body, err := io.ReadAll(resp.Body)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(body)).To(Equal("hello " + client.ID().String()))
		var st p2pnet.ForwardStatus
		Eventually(states).Should(Receive(&st))
		Expect(st.State).To(Equal(p2pnet.ForwardUp))
		Expect(st.Path.Kind).To(Equal(p2pnet.PathRelayed))
		Expect(st.Path.Limited).To(BeTrue())
		Expect(log.relayed.Load()).To(Equal(int32(1)))
	})
})

var _ = Describe("HintsOption", func() {
	It("parses hint relays and refuses an address without a peer ID", func() {
		k, _, err := crypto.GenerateEd25519Key(rand.Reader)
		Expect(err).ToNot(HaveOccurred())
		_, err = p2pnet.HintsOption([]string{"/ip4/192.0.2.1/tcp/4001/p2p/" + mustID(k).String()})
		Expect(err).ToNot(HaveOccurred())
		_, err = p2pnet.HintsOption([]string{"/ip4/192.0.2.1/tcp/4001"})
		Expect(err).To(HaveOccurred())
	})
})
