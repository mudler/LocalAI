package p2prelaymgr_test

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime/pprof"
	"strings"
	"time"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/client"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/p2ppair"
	"github.com/mudler/LocalAI/core/services/p2prelay"
	"github.com/mudler/LocalAI/core/services/p2prelay/relayproto"
	"github.com/mudler/LocalAI/core/services/p2prelaymgr"
)

// drainGoroutines counts the running goroutines that drain the queue of a relay.
func drainGoroutines() int {
	var b bytes.Buffer
	_ = pprof.Lookup("goroutine").WriteTo(&b, 2)
	return strings.Count(b.String(), "(*manager).startDrain.func1")
}

var _ = Describe("Manager", func() {
	var (
		srv   *server
		alice string
		bob   string
	)
	BeforeEach(func() {
		srv = newServer()
		alice, bob = randomPeer(), randomPeer()
		srv.setPaired(alice, bob)
	})

	Describe("Add", func() {
		It("pushes the paired set to a relay that lists the server as admin", func() {
			relay := newTestRelay(gatedCfg(srv.host.ID()))
			m := srv.manager(nil, false)

			r, err := m.Add(ctx10(), relay.addr(), "home", "")
			Expect(err).ToNot(HaveOccurred())
			Expect(r.State).To(Equal(p2prelaymgr.StateInSync))
			Expect(r.Mode).To(Equal(p2prelaymgr.ModeGated))
			Expect(r.ID).To(Equal(relay.id()))
			Expect(r.Label).To(Equal("home"))
			Expect(r.Admins).To(Equal(1))
			Expect(r.Limits.CircuitBytes).To(BeNumerically(">", 0))
			Expect(srv.members(relay.id())).To(ConsistOf(alice, bob))
			Expect(m.List()).To(HaveLen(1))
		})

		It("rejects an address without a peer ID", func() {
			m := srv.manager(nil, false)
			_, err := m.Add(ctx10(), "/ip4/127.0.0.1/tcp/4001", "", "")
			Expect(err).To(MatchError(p2prelaymgr.ErrBadAddr))
		})

		It("adds an unreachable relay and says so", func() {
			relay := newTestRelay(gatedCfg(srv.host.ID()))
			addr := relay.addr()
			relay.stop()
			m := srv.manager(nil, false)

			r, err := m.Add(ctx10(), addr, "", "")
			Expect(err).ToNot(HaveOccurred())
			Expect(r.State).To(Equal(p2prelaymgr.StateUnreachable))
			Expect(r.Health).To(ContainSubstring("cannot connect"))
		})

		It("reports an open relay without pushing anything", func() {
			cfg := gatedCfg()
			cfg.Open = true
			relay := newTestRelay(cfg)
			m := srv.manager(nil, false)

			r, err := m.Add(ctx10(), relay.addr(), "", "")
			Expect(err).ToNot(HaveOccurred())
			Expect(r.State).To(Equal(p2prelaymgr.StateOpen))
			Expect(r.Mode).To(Equal(p2prelaymgr.ModeOpen))
		})

		It("reports a relay that does not know the server as denied", func() {
			relay := newTestRelay(gatedCfg(newServer().host.ID()))
			m := srv.manager(nil, false)

			r, err := m.Add(ctx10(), relay.addr(), "", "")
			Expect(err).ToNot(HaveOccurred())
			Expect(r.State).To(Equal(p2prelaymgr.StateDenied))
		})

		It("reports a member that is not an admin", func() {
			admin := newServer()
			relay := newTestRelay(gatedCfg(admin.host.ID()))
			admin.setPaired(srv.host.ID().String())
			_, err := admin.manager(nil, false).Add(ctx10(), relay.addr(), "", "")
			Expect(err).ToNot(HaveOccurred())

			r, err := srv.manager(nil, false).Add(ctx10(), relay.addr(), "", "")
			Expect(err).ToNot(HaveOccurred())
			Expect(r.State).To(Equal(p2prelaymgr.StateNotAdmin))
		})

		It("is idempotent for the same relay", func() {
			relay := newTestRelay(gatedCfg(srv.host.ID()))
			m := srv.manager(nil, false)
			_, err := m.Add(ctx10(), relay.addr(), "one", "")
			Expect(err).ToNot(HaveOccurred())
			r, err := m.Add(ctx10(), relay.addr(), "two", "")
			Expect(err).ToNot(HaveOccurred())
			Expect(m.List()).To(HaveLen(1))
			Expect(r.Label).To(Equal("two"))
		})
	})

	Describe("Claim", func() {
		It("claims an unclaimed relay with the code given to Add", func() {
			relay := newTestRelay(gatedCfg())
			code := relay.relay.ClaimCode()
			Expect(code).ToNot(BeEmpty())
			m := srv.manager(nil, false)

			r, err := m.Add(ctx10(), relay.addr(), "", code)
			Expect(err).ToNot(HaveOccurred())
			Expect(r.State).To(Equal(p2prelaymgr.StateInSync))
			Expect(srv.members(relay.id())).To(ConsistOf(alice, bob))
		})

		It("keeps an unclaimed relay unclaimed without a code", func() {
			relay := newTestRelay(gatedCfg())
			m := srv.manager(nil, false)
			r, err := m.Add(ctx10(), relay.addr(), "", "")
			Expect(err).ToNot(HaveOccurred())
			Expect(r.State).To(Equal(p2prelaymgr.StateUnclaimed))

			r, err = m.Claim(ctx10(), r.ID, relay.relay.ClaimCode())
			Expect(err).ToNot(HaveOccurred())
			Expect(r.State).To(Equal(p2prelaymgr.StateInSync))
		})

		It("refuses a wrong code and stays unclaimed", func() {
			relay := newTestRelay(gatedCfg())
			m := srv.manager(nil, false)
			r, err := m.Add(ctx10(), relay.addr(), "", "")
			Expect(err).ToNot(HaveOccurred())

			r, err = m.Claim(ctx10(), r.ID, "AAAA-BBBB-CCCC")
			Expect(err).To(MatchError(p2prelaymgr.ErrClaimRefused))
			Expect(r.State).To(Equal(p2prelaymgr.StateUnclaimed))
		})
	})

	Describe("Sync and Check", func() {
		It("reports a difference, and Sync repairs it", func() {
			relay := newTestRelay(gatedCfg(srv.host.ID()))
			m := srv.manager(nil, false)
			r, err := m.Add(ctx10(), relay.addr(), "", "")
			Expect(err).ToNot(HaveOccurred())

			carol := randomPeer()
			srv.setPaired(alice, bob, carol)
			c, err := m.Check(ctx10(), r.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(c.AdminOK).To(BeTrue())
			Expect(c.InSync).To(BeFalse())
			Expect(c.Relay.State).To(Equal(p2prelaymgr.StateOutOfSync))
			Expect(srv.members(relay.id())).To(ConsistOf(alice, bob))

			r, err = m.Sync(ctx10(), r.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(r.State).To(Equal(p2prelaymgr.StateInSync))
			Expect(srv.members(relay.id())).To(ConsistOf(alice, bob, carol))
		})

		It("runs every step of a check on a healthy relay", func() {
			relay := newTestRelay(gatedCfg(srv.host.ID()))
			m := srv.manager(nil, false)
			r, err := m.Add(ctx10(), relay.addr(), "", "")
			Expect(err).ToNot(HaveOccurred())

			c, err := m.Check(ctx10(), r.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(c.Connected).To(BeTrue(), "%v", c.Detail)
			Expect(c.Reserved).To(BeTrue(), "%v", c.Detail)
			Expect(c.AdminOK).To(BeTrue(), "%v", c.Detail)
			Expect(c.CircuitOK).To(BeTrue(), "%v", c.Detail)
			Expect(c.InSync).To(BeTrue(), "%v", c.Detail)
			Expect(c.Detail).ToNot(BeEmpty())
			// The test peer leaves nothing behind.
			Expect(relay.relay.Status().Passes).To(BeZero())
			Expect(relay.relay.Status().Members).To(Equal(2))
		})

		It("checks an unreachable relay without error", func() {
			relay := newTestRelay(gatedCfg(srv.host.ID()))
			m := srv.manager(nil, false)
			r, err := m.Add(ctx10(), relay.addr(), "", "")
			Expect(err).ToNot(HaveOccurred())
			relay.stop()

			c, err := m.Check(ctx10(), r.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(c.Connected).To(BeFalse())
			Expect(c.Relay.State).To(Equal(p2prelaymgr.StateUnreachable))
		})

		It("returns ErrNotFound for an unknown relay", func() {
			m := srv.manager(nil, false)
			_, err := m.Check(ctx10(), "nope")
			Expect(err).To(MatchError(p2prelaymgr.ErrNotFound))
			_, err = m.Sync(ctx10(), "nope")
			Expect(err).To(MatchError(p2prelaymgr.ErrNotFound))
			Expect(m.Remove(ctx10(), "nope")).To(MatchError(p2prelaymgr.ErrNotFound))
		})
	})

	Describe("the loop", func() {
		It("repairs a relay that restarted with an empty state", func() {
			relay := newTestRelay(gatedCfg(srv.host.ID()))
			m := srv.manager(nil, true)
			_, err := m.Add(ctx10(), relay.addr(), "", "")
			Expect(err).ToNot(HaveOccurred())
			Expect(m.Start(ctx10())).To(Succeed())
			Expect(srv.members(relay.id())).To(HaveLen(2))

			relay.restart(true)
			Expect(relay.relay.Status().Members).To(BeZero())

			Eventually(func() int { return relay.relay.Status().Members }).
				WithTimeout(20 * time.Second).WithPolling(100 * time.Millisecond).Should(Equal(2))
			Eventually(func() string { return m.List()[0].State }).
				WithTimeout(10 * time.Second).Should(Equal(p2prelaymgr.StateInSync))
		})

		It("repairs a relay whose list drifted, by the digest comparison", func() {
			relay := newTestRelay(gatedCfg(srv.host.ID()))
			m := srv.manager(nil, true)
			_, err := m.Add(ctx10(), relay.addr(), "", "")
			Expect(err).ToNot(HaveOccurred())
			Expect(m.Start(ctx10())).To(Succeed())

			// Another change on the relay that the server did not make.
			_, err = relayproto.Call(ctx10(), srv.host.Libp2p(), relay.h.ID(), relayproto.Request{
				Op: relayproto.OpAdd, Seq: time.Now().UnixNano(),
				Peers: []relayproto.Peer{{ID: randomPeer()}},
			})
			Expect(err).ToNot(HaveOccurred())

			Eventually(func() []string { return srv.members(relay.id()) }).
				WithTimeout(20 * time.Second).Should(ConsistOf(alice, bob))
		})

		It("marks a relay that goes away unreachable and recovers", func() {
			relay := newTestRelay(gatedCfg(srv.host.ID()))
			m := srv.manager(nil, true)
			_, err := m.Add(ctx10(), relay.addr(), "", "")
			Expect(err).ToNot(HaveOccurred())
			Expect(m.Start(ctx10())).To(Succeed())

			relay.stop()
			Eventually(func() string { return m.List()[0].State }).
				WithTimeout(20 * time.Second).Should(Equal(p2prelaymgr.StateUnreachable))
			relay.start()
			Eventually(func() string { return m.List()[0].State }).
				WithTimeout(30 * time.Second).Should(Equal(p2prelaymgr.StateInSync))
		})
	})

	Describe("PeerAdded and PeerRemoved", func() {
		It("sends the change to every relay", func() {
			r1 := newTestRelay(gatedCfg(srv.host.ID()))
			r2 := newTestRelay(gatedCfg(srv.host.ID()))
			m := srv.manager(nil, true)
			_, err := m.Add(ctx10(), r1.addr(), "", "")
			Expect(err).ToNot(HaveOccurred())
			_, err = m.Add(ctx10(), r2.addr(), "", "")
			Expect(err).ToNot(HaveOccurred())
			Expect(m.Start(ctx10())).To(Succeed())

			carol := randomPeer()
			srv.setPaired(alice, bob, carol)
			m.PeerAdded(carol)
			for _, r := range []*testRelay{r1, r2} {
				Eventually(func() []string { return srv.members(r.id()) }).
					WithTimeout(10 * time.Second).Should(ConsistOf(alice, bob, carol))
			}

			srv.setPaired(alice, carol)
			m.PeerRemoved(bob)
			for _, r := range []*testRelay{r1, r2} {
				Eventually(func() []string { return srv.members(r.id()) }).
					WithTimeout(10 * time.Second).Should(ConsistOf(alice, carol))
			}
		})

		It("retries until the relay is back", func() {
			relay := newTestRelay(gatedCfg(srv.host.ID()))
			m := srv.manager(nil, true)
			_, err := m.Add(ctx10(), relay.addr(), "", "")
			Expect(err).ToNot(HaveOccurred())
			Expect(m.Start(ctx10())).To(Succeed())

			relay.stop()
			carol := randomPeer()
			srv.setPaired(alice, bob, carol)
			m.PeerAdded(carol)
			time.Sleep(500 * time.Millisecond)
			relay.start()

			Eventually(func() []string { return srv.members(relay.id()) }).
				WithTimeout(30 * time.Second).Should(ConsistOf(alice, bob, carol))
		})
	})

	Describe("relay passes", func() {
		It("registers a pass that a stranger can use, and cancels it", func() {
			relay := newTestRelay(gatedCfg(srv.host.ID()))
			m := srv.manager(nil, false)
			r, err := m.Add(ctx10(), relay.addr(), "", "")
			Expect(err).ToNot(HaveOccurred())

			Expect(m.RegisterPass(ctx10(), r.ID, "pass-one", 5*time.Minute)).To(Succeed())
			Expect(relay.relay.Status().Passes).To(Equal(1))
			stranger, err := libp2p.New(libp2p.NoListenAddrs)
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(stranger.Close)
			Expect(stranger.Connect(ctx10(), peer.AddrInfo{ID: relay.h.ID(), Addrs: relay.h.Addrs()})).To(Succeed())
			_, err = relayproto.UsePass(ctx10(), stranger, relay.h.ID(), "pass-one")
			Expect(err).ToNot(HaveOccurred())

			Expect(m.RegisterPass(ctx10(), r.ID, "pass-two", 5*time.Minute)).To(Succeed())
			Expect(m.CancelPass(ctx10(), r.ID, "pass-two")).To(Succeed())
			stranger2, err := libp2p.New(libp2p.NoListenAddrs)
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(stranger2.Close)
			Expect(stranger2.Connect(ctx10(), peer.AddrInfo{ID: relay.h.ID(), Addrs: relay.h.Addrs()})).To(Succeed())
			_, err = relayproto.UsePass(ctx10(), stranger2, relay.h.ID(), "pass-two")
			Expect(err).To(HaveOccurred())
		})

		It("fails for an unreachable relay", func() {
			relay := newTestRelay(gatedCfg(srv.host.ID()))
			m := srv.manager(nil, false)
			r, err := m.Add(ctx10(), relay.addr(), "", "")
			Expect(err).ToNot(HaveOccurred())
			relay.stop()
			Expect(m.RegisterPass(ctx10(), r.ID, "x", time.Minute)).ToNot(Succeed())
		})

		It("serves the pairing service through the adapter", func() {
			relay := newTestRelay(gatedCfg(srv.host.ID()))
			m := srv.manager(nil, false)
			r, err := m.Add(ctx10(), relay.addr(), "", "")
			Expect(err).ToNot(HaveOccurred())

			var reg p2ppair.PassRegistrar = p2prelaymgr.NewPassRegistrar(m)
			pr := p2ppair.Relay{ID: r.ID}
			pass, err := reg.RegisterPass(ctx10(), p2ppair.PassRequest{Relay: pr, InviteID: "inv-1", Code: "ignored", TTL: time.Minute})
			Expect(err).ToNot(HaveOccurred())
			Expect(pass).ToNot(BeEmpty())
			Expect(relay.relay.Status().Passes).To(Equal(1))
			Expect(reg.CancelPass(ctx10(), pr, "inv-1")).To(Succeed())
			Expect(relay.relay.Status().Passes).To(BeZero())
			Expect(reg.CancelPass(ctx10(), pr, "inv-1")).ToNot(Succeed())
		})
	})

	Describe("Remove and the store", func() {
		It("persists relays and loads them again", func() {
			relay := newTestRelay(gatedCfg(srv.host.ID()))
			path := filepath.Join(GinkgoT().TempDir(), "relays.json")
			m := srv.manager(p2prelaymgr.NewFileStore(path), false)
			r, err := m.Add(ctx10(), relay.addr(), "home", "")
			Expect(err).ToNot(HaveOccurred())

			st, err := os.Stat(path)
			Expect(err).ToNot(HaveOccurred())
			Expect(st.Mode().Perm()).To(Equal(os.FileMode(0o600)))

			m2 := srv.manager(p2prelaymgr.NewFileStore(path), false)
			Expect(m2.List()).To(HaveLen(1))
			Expect(m2.List()[0].Label).To(Equal("home"))
			Expect(m2.List()[0].State).To(Equal(p2prelaymgr.StateChecking))

			Expect(m.Remove(ctx10(), r.ID)).To(Succeed())
			Expect(m.List()).To(BeEmpty())
			m3 := srv.manager(p2prelaymgr.NewFileStore(path), false)
			Expect(m3.List()).To(BeEmpty())
		})

		It("stops the goroutine that drains the queue of a relay that was removed", func() {
			relay := newTestRelay(gatedCfg(srv.host.ID()))
			m := srv.manager(nil, true)
			Expect(m.Start(ctx10())).To(Succeed())
			r, err := m.Add(ctx10(), relay.addr(), "home", "")
			Expect(err).ToNot(HaveOccurred())
			Expect(drainGoroutines()).To(Equal(1))

			Expect(m.Remove(ctx10(), r.ID)).To(Succeed())
			Eventually(drainGoroutines, 5*time.Second, 50*time.Millisecond).Should(BeZero())
		})

		It("keeps the list in memory by default", func() {
			s := p2prelaymgr.NewMemoryStore(p2prelaymgr.Entry{ID: "x", Addr: "/ip4/127.0.0.1/tcp/1/p2p/" + randomPeer()})
			m := srv.manager(s, false)
			Expect(m.List()).To(HaveLen(1))
		})
	})

	Describe("Limits", func() {
		i64 := func(n int64) *int64 { return &n }

		It("reads the limits and tells an admin it can change them", func() {
			relay := newTestRelay(gatedCfg(srv.host.ID()))
			m := srv.manager(nil, false)
			r, err := m.Add(ctx10(), relay.addr(), "", "")
			Expect(err).ToNot(HaveOccurred())

			info, err := m.Limits(ctx10(), r.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(info.CanChange).To(BeTrue())
			Expect(info.Limits.CircuitBytes).To(BeEquivalentTo(p2prelay.DefaultCircuitMaxBytes))
			Expect(info.Ceiling).To(Equal(p2prelaymgr.Limits{
				CircuitBytes:   p2prelay.DefaultCeilingCircuitMaxBytes,
				CircuitSeconds: int64(p2prelay.DefaultCeilingCircuitMaxDuration / time.Second),
				RateBPS:        p2prelay.DefaultCeilingRateLimit,
			}))
		})

		It("refuses a value above the ceiling with an error that names it", func() {
			cfg := gatedCfg(srv.host.ID())
			cfg.Ceiling = p2prelay.Ceiling{CircuitMaxBytes: 4 << 20}
			cfg.CircuitMaxBytes = 1 << 20
			relay := newTestRelay(cfg)
			m := srv.manager(nil, false)
			r, err := m.Add(ctx10(), relay.addr(), "", "")
			Expect(err).ToNot(HaveOccurred())
			_, err = m.SetLimits(ctx10(), r.ID, p2prelaymgr.LimitsPatch{CircuitBytes: i64(5 << 20)})
			Expect(err).To(MatchError(p2prelaymgr.ErrInvalidLimits))
			Expect(err.Error()).To(ContainSubstring("4 MiB"))
			info, err := m.Limits(ctx10(), r.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(info.Limits.CircuitBytes).To(BeEquivalentTo(1 << 20))
			Expect(info.Ceiling.CircuitBytes).To(BeEquivalentTo(4 << 20))
		})

		It("changes the limits, keeps the reservation and reports what the relay now runs", func() {
			relay := newTestRelay(gatedCfg(srv.host.ID()))
			m := srv.manager(nil, false)
			r, err := m.Add(ctx10(), relay.addr(), "", "")
			Expect(err).ToNot(HaveOccurred())

			info, err := m.SetLimits(ctx10(), r.ID, p2prelaymgr.LimitsPatch{
				CircuitBytes: i64(3 << 20), CircuitSeconds: i64(120), RateBPS: i64(64 << 10),
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(info.Restarted).To(BeTrue())
			Expect(info.Limits).To(Equal(p2prelaymgr.Limits{CircuitBytes: 3 << 20, CircuitSeconds: 120, RateBPS: 64 << 10}))
			Expect(m.List()[0].Limits).To(Equal(info.Limits))
			Expect(relay.relay.Status().CircuitMaxBytes).To(BeEquivalentTo(3 << 20))

			// The server holds a reservation again, with the new limits.
			Eventually(func() error {
				_, err := client.Reserve(ctx10(), srv.host.Libp2p(), peer.AddrInfo{ID: relay.h.ID(), Addrs: relay.h.Addrs()})
				return err
			}).Should(Succeed())
		})

		It("refuses a server that is not an admin and an open relay", func() {
			other := newTestRelay(gatedCfg(newServer().host.ID()))
			m := srv.manager(nil, false)
			r, err := m.Add(ctx10(), other.addr(), "", "")
			Expect(err).ToNot(HaveOccurred())
			_, err = m.SetLimits(ctx10(), r.ID, p2prelaymgr.LimitsPatch{RateBPS: i64(1)})
			Expect(err).To(MatchError(p2prelaymgr.ErrNotAdmin))

			cfg := p2prelay.DefaultConfig(true)
			open := newTestRelay(cfg)
			r, err = m.Add(ctx10(), open.addr(), "", "")
			Expect(err).ToNot(HaveOccurred())
			info, err := m.Limits(ctx10(), r.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(info.CanChange).To(BeFalse())
			Expect(info.Limits.RateBPS).To(BeEquivalentTo(p2prelay.DefaultOpenRateLimit))
			_, err = m.SetLimits(ctx10(), r.ID, p2prelaymgr.LimitsPatch{RateBPS: i64(0)})
			Expect(err).To(MatchError(p2prelaymgr.ErrNotAdmin))
		})

		It("reports a refused value and an unknown relay", func() {
			relay := newTestRelay(gatedCfg(srv.host.ID()))
			m := srv.manager(nil, false)
			r, err := m.Add(ctx10(), relay.addr(), "", "")
			Expect(err).ToNot(HaveOccurred())
			_, err = m.SetLimits(ctx10(), r.ID, p2prelaymgr.LimitsPatch{RateBPS: i64(-5)})
			Expect(err).To(MatchError(p2prelaymgr.ErrInvalidLimits))
			_, err = m.Limits(ctx10(), "nope")
			Expect(err).To(MatchError(p2prelaymgr.ErrNotFound))
		})
	})
})
