package p2prelay_test

import (
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/protocol/ping"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/p2prelay"
	"github.com/mudler/LocalAI/core/services/p2prelay/relayproto"
)

// The cases follow the gate tests of the relay design (a) to (i).
var _ = Describe("gated relay", func() {
	var (
		srv, cli host.Host
		r        *testRelay
	)

	BeforeEach(func() {
		srv, cli = newPeer(), newPeer()
		r = startRelay(testConfig(false, srv))
		Expect(push(srv, r, relayproto.OpReplace, cli).Count).To(Equal(1))
		Expect(reserve(srv, r)).To(Succeed())
	})

	It("(a) drops an unpaired peer after the handshake", func() {
		rogue := newPeer()
		_ = connect(rogue, r)
		Eventually(func() bool { return connected(rogue, r.h) }, 5*time.Second).Should(BeFalse())
		Expect(reserve(rogue, r)).ToNot(Succeed())
		Expect(circuit(rogue, srv, r)).ToNot(Succeed())
	})

	It("(b) relays a transfer between paired peers on a limited connection", func() {
		Expect(circuit(cli, srv, r)).To(Succeed())
		conns := cli.Network().ConnsToPeer(srv.ID())
		Expect(conns).To(HaveLen(1))
		Expect(conns[0].Stat().Limited).To(BeTrue())
		n, _, err := download(cli, srv, 4<<20)
		Expect(err).ToNot(HaveOccurred())
		Expect(n).To(BeEquivalentTo(4 << 20))
		_, err = upload(cli, srv, 1<<20)
		Expect(err).ToNot(HaveOccurred())
		Expect(r.Status().CircuitsTotal).To(BeEquivalentTo(1))
	})

	It("(c) cuts a live circuit on revoke and refuses the reconnection", func() {
		r2 := startRelay(func() p2prelay.Config {
			c := testConfig(false, srv)
			c.RateLimit = 256 << 10 // keeps the transfer running
			return c
		}())
		push(srv, r2, relayproto.OpReplace, cli)
		Expect(reserve(srv, r2)).To(Succeed())
		Expect(circuit(cli, srv, r2)).To(Succeed())
		type result struct {
			n   int64
			err error
		}
		done := make(chan result, 1)
		go func() {
			n, _, err := download(cli, srv, 64<<20)
			done <- result{n, err}
		}()
		time.Sleep(500 * time.Millisecond)
		Expect(push(srv, r2, relayproto.OpRemove, cli).Count).To(Equal(0))
		var res result
		Eventually(done, 5*time.Second).Should(Receive(&res))
		Expect(res.err).To(HaveOccurred())
		Expect(res.n).To(BeNumerically("<", 64<<20))
		_ = connect(cli, r2)
		Eventually(func() bool { return connected(cli, r2.h) }, 5*time.Second).Should(BeFalse())
		Expect(reserve(cli, r2)).ToNot(Succeed())
	})

	It("(e) gives a member the public status only and no admin operation", func() {
		_, err := call(cli, r, relayproto.Request{Op: relayproto.OpList})
		Expect(err).To(MatchError(ContainSubstring("not an admin")))
		_, err = call(cli, r, relayproto.Request{Op: relayproto.OpAdd, Seq: seq(), Peers: []relayproto.Peer{{ID: cli.ID().String()}}})
		Expect(err).To(MatchError(ContainSubstring("not an admin")))
		resp, err := call(cli, r, relayproto.Request{Op: relayproto.OpStatus})
		Expect(err).ToNot(HaveOccurred())
		Expect(resp.Admin).To(BeFalse())
		Expect(resp.Status.Mode).To(Equal(relayproto.ModeGated))
		Expect(resp.Status.CircuitMaxBytes).To(Equal(p2prelay.DefaultCircuitMaxBytes))
		Expect(resp.Status.YourAddr).ToNot(BeEmpty())
		Expect(resp.Status.Members).To(BeZero())
		Expect(resp.Status.Admins).To(BeZero())

		admin, err := call(srv, r, relayproto.Request{Op: relayproto.OpStatus})
		Expect(err).ToNot(HaveOccurred())
		Expect(admin.Admin).To(BeTrue())
		Expect(admin.Digest).To(Equal(relayproto.Digest([]peer.ID{cli.ID()})))
		Expect(admin.Status.Members).To(Equal(1))
		Expect(admin.Status.Reserv).To(BeEquivalentTo(1))
	})

	It("(e) refuses the admin stream to an unpaired peer", func() {
		_, err := call(newPeer(), r, relayproto.Request{Op: relayproto.OpStatus})
		Expect(err).To(HaveOccurred())
	})

	It("(h) keeps the sets of two admins apart and denies cross-tenant circuits", func() {
		srv2, cli2 := newPeer(), newPeer()
		shared := startRelay(testConfig(false, srv, srv2))
		push(srv, shared, relayproto.OpReplace, cli)
		push(srv2, shared, relayproto.OpReplace, cli2)
		Expect(reserve(srv, shared)).To(Succeed())
		Expect(reserve(srv2, shared)).To(Succeed())

		Expect(circuit(cli2, srv2, shared)).To(Succeed())
		Expect(circuit(cli, srv2, shared)).ToNot(Succeed())
		Expect(circuit(cli2, srv, shared)).ToNot(Succeed())

		list, err := call(srv2, shared, relayproto.Request{Op: relayproto.OpList})
		Expect(err).ToNot(HaveOccurred())
		Expect(list.Peers).To(ConsistOf(relayproto.Peer{ID: cli2.ID().String()}))

		// Removing a peer of another tenant changes nothing.
		Expect(push(srv2, shared, relayproto.OpRemove, cli).Count).To(Equal(1))
		Expect(circuit(cli, srv, shared)).To(Succeed())
		_, _, err = download(cli, srv, 1024)
		Expect(err).ToNot(HaveOccurred())
	})

	It("(i) ends a circuit at its data limit", func() {
		c := testConfig(false, srv)
		c.CircuitMaxBytes = 256 << 10
		small := startRelay(c)
		push(srv, small, relayproto.OpReplace, cli)
		Expect(reserve(srv, small)).To(Succeed())
		Expect(circuit(cli, srv, small)).To(Succeed())
		n, _, err := download(cli, srv, 4<<20)
		Expect(err).To(HaveOccurred())
		Expect(n).To(BeNumerically("<=", 256<<10))
	})

	It("refuses a circuit when the relay is full", func() {
		c := testConfig(false, srv)
		c.MaxCircuits = 1
		full := startRelay(c)
		other := newPeer()
		push(srv, full, relayproto.OpReplace, cli, other)
		Expect(reserve(srv, full)).To(Succeed())
		Expect(circuit(cli, srv, full)).To(Succeed())
		Expect(circuit(other, srv, full)).ToNot(Succeed())
	})
})

var _ = Describe("relay restart (d)", func() {
	It("keeps the allowlist and the claimed admin", func() {
		srv, cli := newPeer(), newPeer()
		cfg := testConfig(false)
		r := startRelay(cfg)
		Expect(r.Status().Mode).To(Equal(relayproto.ModeUnclaimed))
		_, err := call(srv, r, relayproto.Request{Op: relayproto.OpClaim, Code: r.ClaimCode()})
		Expect(err).ToNot(HaveOccurred())
		push(srv, r, relayproto.OpReplace, cli)
		r.stop()

		r2 := startRelayWithKey(cfg, r.key)
		st := r2.Status()
		Expect(st.Mode).To(Equal(relayproto.ModeGated))
		Expect(st.Admins).To(Equal(1))
		Expect(st.Members).To(Equal(1))
		Expect(r2.ClaimCode()).To(BeEmpty())
		// The member is served before the admin comes back.
		Expect(reserve(cli, r2)).To(Succeed())
		resp, err := call(srv, r2, relayproto.Request{Op: relayproto.OpStatus})
		Expect(err).ToNot(HaveOccurred())
		Expect(resp.Admin).To(BeTrue())
		Expect(resp.Count).To(Equal(1))
	})
})

var _ = Describe("open relay (f)", func() {
	It("serves any peer, says so and limits the bandwidth by default", func() {
		r := startRelay(testConfig(true))
		st := r.Status()
		Expect(st.Mode).To(Equal(relayproto.ModeOpen))
		Expect(st.RateBPS).To(Equal(p2prelay.DefaultOpenRateLimit))
		a, b := newPeer(), newPeer()
		Expect(reserve(a, r)).To(Succeed())
		Expect(circuit(b, a, r)).To(Succeed())
		n, _, err := download(b, a, 16<<10)
		Expect(err).ToNot(HaveOccurred())
		Expect(n).To(BeEquivalentTo(16 << 10))
	})
})

var _ = Describe("claim", func() {
	It("accepts the code once and refuses a wrong one", func() {
		r := startRelay(testConfig(false))
		code := r.ClaimCode()
		Expect(code).To(MatchRegexp(`^[0-9A-F]{4}-[0-9A-F]{4}-[0-9A-F]{4}$`))
		rogue, owner, late := newPeer(), newPeer(), newPeer()

		// An unclaimed relay grants nothing but the claim.
		Expect(reserve(rogue, r)).ToNot(Succeed())
		_, err := call(rogue, r, relayproto.Request{Op: relayproto.OpClaim, Code: "0000-0000-0000"})
		Expect(err).To(MatchError(ContainSubstring("claim refused")))

		resp, err := call(owner, r, relayproto.Request{Op: relayproto.OpClaim, Code: code})
		Expect(err).ToNot(HaveOccurred())
		Expect(resp.Admin).To(BeTrue())
		Expect(r.Status().Mode).To(Equal(relayproto.ModeGated))
		Expect(r.ClaimCode()).To(BeEmpty())

		_, err = call(late, r, relayproto.Request{Op: relayproto.OpClaim, Code: code})
		Expect(err).To(HaveOccurred())
		Eventually(func() bool { return connected(rogue, r.h) }, 5*time.Second).Should(BeFalse())
		Expect(reserve(rogue, r)).ToNot(Succeed())
	})

	It("caps the claim tries per IP", func() {
		r := startRelay(testConfig(false))
		for range 5 {
			_, err := call(newPeer(), r, relayproto.Request{Op: relayproto.OpClaim, Code: "0000-0000-0000"})
			Expect(err).To(HaveOccurred())
		}
		_, err := call(newPeer(), r, relayproto.Request{Op: relayproto.OpClaim, Code: r.ClaimCode()})
		Expect(err).To(MatchError(ContainSubstring("claim refused")))
	})
})

var _ = Describe("relay pass", func() {
	var (
		srv, member, stranger host.Host
		r                     *testRelay
		cfg                   p2prelay.Config
	)
	const code = "pass-code-0123456789"

	BeforeEach(func() {
		srv, member, stranger = newPeer(), newPeer(), newPeer()
		cfg = testConfig(false, srv)
	})

	start := func() {
		r = startRelay(cfg)
		push(srv, r, relayproto.OpReplace, member)
		Expect(reserve(srv, r)).To(Succeed())
	}

	mint := func(c string, ttl int) {
		_, err := call(srv, r, relayproto.Request{Op: relayproto.OpPass,
			Pass: &relayproto.Pass{Code: c, TTLSeconds: ttl, Label: "laptop"}})
		Expect(err).ToNot(HaveOccurred())
	}

	usePass := func(h host.Host, c string) error {
		if err := connect(h, r); err != nil {
			return err
		}
		_, err := relayproto.UsePass(ctxT(10*time.Second), h, r.h.ID(), c)
		return err
	}

	It("admits a stranger as a pending member that the admin confirms", func() {
		start()
		mint(code, 600)
		Expect(r.Status().Passes).To(Equal(1))
		Expect(usePass(stranger, code)).To(Succeed())
		Expect(r.Status().Pending).To(Equal(1))

		// It can reserve and be reached through the relay by its admin.
		Expect(reserve(stranger, r)).To(Succeed())
		Expect(circuit(srv, stranger, r)).To(Succeed())
		Expect(circuit(stranger, srv, r)).To(Succeed())

		list, err := call(srv, r, relayproto.Request{Op: relayproto.OpList})
		Expect(err).ToNot(HaveOccurred())
		Expect(list.Peers).To(ContainElement(relayproto.Peer{ID: stranger.ID().String(), Label: "laptop", Pending: true}))
		Expect(list.Count).To(Equal(1))

		resp := push(srv, r, relayproto.OpAdd, stranger)
		Expect(resp.Count).To(Equal(2))
		Expect(r.Status().Pending).To(BeZero())
		// It outlives the stranger timeout now.
		Consistently(func() bool { return connected(stranger, r.h) }, cfg.StrangerTimeout+time.Second).Should(BeTrue())
	})

	It("refuses a replay, an unknown code and an expired pass with one answer", func() {
		start()
		mint(code, 600)
		Expect(usePass(stranger, code)).To(Succeed())

		// Keep a second pass pending. With none pending the gater drops a
		// stranger at the handshake, and the error is then a transport error
		// whose text depends on timing (a read or a write failure). A pending
		// pass lets each try reach the pass stream and get the same answer.
		mint("other-code", 600)

		replay := usePass(newPeer(), code)
		Expect(replay).To(HaveOccurred())
		unknown := usePass(newPeer(), "not-a-code-at-all")
		Expect(unknown).To(HaveOccurred())

		mint("short-lived-code", 1)
		time.Sleep(1500 * time.Millisecond)
		expired := usePass(newPeer(), "short-lived-code")
		Expect(expired).To(HaveOccurred())

		Expect(replay.Error()).To(Equal(unknown.Error()))
		Expect(expired.Error()).To(Equal(unknown.Error()))
	})

	It("drops a pending member when its time ends", func() {
		cfg.PendingTTL = time.Second
		start()
		mint(code, 600)
		Expect(usePass(stranger, code)).To(Succeed())
		Expect(reserve(stranger, r)).To(Succeed())
		Eventually(func() bool { return connected(stranger, r.h) }, 5*time.Second).Should(BeFalse())
		Expect(reserve(stranger, r)).ToNot(Succeed())
	})

	It("cancels an unused pass and the pending member of a used one", func() {
		start()
		mint(code, 600)
		mint("second-code-0123", 600)
		_, err := call(srv, r, relayproto.Request{Op: relayproto.OpPassCancel, Pass: &relayproto.Pass{Code: code}})
		Expect(err).ToNot(HaveOccurred())
		Expect(usePass(stranger, code)).ToNot(Succeed())

		other := newPeer()
		Expect(usePass(other, "second-code-0123")).To(Succeed())
		Expect(reserve(other, r)).To(Succeed())
		_, err = call(srv, r, relayproto.Request{Op: relayproto.OpPassCancel, Pass: &relayproto.Pass{Code: "second-code-0123"}})
		Expect(err).ToNot(HaveOccurred())
		Eventually(func() bool { return connected(other, r.h) }, 5*time.Second).Should(BeFalse())
		Expect(reserve(other, r)).ToNot(Succeed())
	})

	It("gives a peer one try per connection", func() {
		start()
		mint(code, 600)
		Expect(usePass(stranger, "wrong-code-000000")).ToNot(Succeed())
		Expect(usePass(stranger, code)).ToNot(Succeed())
	})

	It("caps the tries per IP", func() {
		start()
		mint(code, 600)
		for range 5 {
			Expect(usePass(newPeer(), "wrong-code-000000")).ToNot(Succeed())
		}
		Expect(usePass(stranger, code)).ToNot(Succeed())
	})

	It("lets a stranger open the pass stream and nothing else", func() {
		start()
		mint(code, 600)
		Expect(connect(stranger, r)).To(Succeed())
		Expect(connected(stranger, r.h)).To(BeTrue())

		Expect(reserve(stranger, r)).ToNot(Succeed())
		res := <-ping.Ping(ctxT(5*time.Second), stranger, r.h.ID())
		Expect(res.Error).To(HaveOccurred())
		_, err := relayproto.Call(ctxT(5*time.Second), stranger, r.h.ID(), relayproto.Request{Op: relayproto.OpStatus})
		Expect(err).To(HaveOccurred())
		s, err := stranger.NewStream(ctxT(5*time.Second), r.h.ID(), "/ipfs/id/1.0.0")
		if err == nil {
			_, err = s.Read(make([]byte, 1))
		}
		Expect(err).To(HaveOccurred())
		Expect(circuit(stranger, srv, r)).ToNot(Succeed())

		// The stranger timer closes the connection.
		Eventually(func() bool { return connected(stranger, r.h) }, cfg.StrangerTimeout+3*time.Second).Should(BeFalse())
	})

	It("drops strangers at the handshake when no pass is pending", func() {
		start()
		_ = connect(stranger, r)
		Eventually(func() bool { return connected(stranger, r.h) }, 5*time.Second).Should(BeFalse())
	})
})
