package application

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/config"
	p2papi "github.com/mudler/LocalAI/core/http/endpoints/p2p"
	"github.com/mudler/LocalAI/core/services/p2pnet"
	"github.com/mudler/LocalAI/core/services/p2prelaymgr"
	ma "github.com/multiformats/go-multiaddr"
)

// newP2PTestApp is an application with only what the P2P layer reads: the
// configuration (loopback only, no public DHT, no mDNS) and the auth database.
func newP2PTestApp(mode string, dataPath string, db *gorm.DB) *Application {
	ctx, cancel := context.WithCancel(context.Background())
	DeferCleanup(cancel)
	cfg := config.NewApplicationConfig(config.WithContext(ctx), config.WithDataPath(dataPath))
	cfg.Auth.APIKeyHMACSecret = "test-secret"
	cfg.P2P = config.P2PSettings{
		Mode: mode, DisablePublicDHT: true, DisableMDNS: true,
		ListenAddrs: []string{"/ip4/127.0.0.1/tcp/0"},
	}
	a := &Application{applicationConfig: cfg, authDB: db}
	a.p2p().reloadInterval = 200 * time.Millisecond
	DeferCleanup(a.StopP2PNet)
	return a
}

var _ = Describe("P2P layer lifecycle", func() {
	It("runs nothing in mode off", func() {
		dir := GinkgoT().TempDir()
		a := newP2PTestApp("off", dir, nil)
		Expect(a.StartP2PNet()).To(Succeed())
		rt, mode := a.p2p().runtime()
		Expect(rt).To(BeNil())
		Expect(mode).To(Equal(config.P2PModeOff))
		st, err := a.P2P().Status(context.Background())
		Expect(err).ToNot(HaveOccurred())
		Expect(st.Running).To(BeFalse())
		Expect(st.Enabled).To(BeFalse())
		_, err = os.Stat(filepath.Join(dir, "p2p"))
		Expect(errors.Is(err, os.ErrNotExist)).To(BeTrue(), "mode off must not even create the identity")
	})

	It("listens for hole punches in mode connect but accepts nobody", func() {
		a := newP2PTestApp("connect", GinkgoT().TempDir(), nil)
		Expect(a.StartP2PNet()).To(Succeed())
		rt, mode := a.p2p().runtime()
		Expect(mode).To(Equal(config.P2PModeConnect))
		Expect(rt).ToNot(BeNil())
		Expect(transportListenAddrs(rt)).ToNot(BeEmpty(), "a hole punch needs a socket to reach")
		Expect(rt.srv).To(BeNil())
		Expect(rt.pair).To(BeNil())
		Expect(rt.host.Gate().AdmitConn("anyone")).To(BeFalse())

		// A stranger reaches the socket, but the gate refuses it after the
		// security handshake.
		stranger, err := p2pnet.New(context.Background(), p2pnet.Config{
			IdentityPath: filepath.Join(GinkgoT().TempDir(), "stranger.key"),
			ListenAddrs:  []string{"/ip4/127.0.0.1/tcp/0"},
		})
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(stranger.Close)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, err = stranger.Dial(ctx, rt.host.ID(), p2pnet.ProtoHTTP, p2pnet.WithAddrs(loopbackAddrs(rt)...))
		Expect(err).To(HaveOccurred())
		Eventually(func() int { return len(rt.host.Libp2p().Network().ConnsToPeer(stranger.ID())) }).Should(BeZero())
		Consistently(func() int { return len(rt.host.Libp2p().Network().ConnsToPeer(stranger.ID())) }, time.Second).Should(BeZero())

		st, err := a.P2P().Status(context.Background())
		Expect(err).ToNot(HaveOccurred())
		Expect(st.Mode).To(Equal("connect"))
		Expect(st.Running).To(BeTrue())
		Expect(st.Addrs).ToNot(BeEmpty())
		Expect(st.RelayService.Reason).To(Equal("not_share"))

		_, err = a.P2P().ListInvites(context.Background())
		var me *p2papi.ModeError
		Expect(errors.As(err, &me)).To(BeTrue())
		Expect(me.Mode).To(Equal("connect"))
	})

	It("runs no mDNS and holds no relay reservation in mode connect", func() {
		s := config.P2PSettings{Relays: []config.P2PRelay{{Addr: "/ip4/192.0.2.1/tcp/4001/p2p/12D3KooWQ6jWKvb3LfQwPzbNxQmPqYGSmrW3Fq2PTSaAVoiBAJGm"}}}
		pc, gate, err := p2pHostConfig(config.P2PModeConnect, s, "id.key")
		Expect(err).ToNot(HaveOccurred())
		Expect(gate).To(BeNil())
		Expect(pc.MDNS).To(BeFalse(), "nobody dials this host, so it announces nothing on the local network")
		Expect(pc.NoReservations).To(BeTrue())
		Expect(pc.ListenAddrs).To(BeNil(), "a hole punch still needs a listener")

		pc, gate, err = p2pHostConfig(config.P2PModeShare, s, "id.key")
		Expect(err).ToNot(HaveOccurred())
		Expect(gate).ToNot(BeNil())
		Expect(pc.MDNS).To(BeTrue())
		Expect(pc.NoReservations).To(BeFalse())
		Expect(pc.StaticRelays).To(HaveLen(1))
	})

	It("refuses share while authentication is off, with the message of ResolveP2PMode", func() {
		a := newP2PTestApp("share", GinkgoT().TempDir(), nil)
		Expect(a.StartP2PNet()).To(MatchError(config.ErrP2PShareNeedsAuth))
		rt, mode := a.p2p().runtime()
		Expect(rt).To(BeNil())
		Expect(mode).To(Equal(config.P2PModeOff))
		st, _ := a.P2P().Status(context.Background())
		Expect(st.Error).To(Equal(config.ErrP2PShareNeedsAuth))
		Expect(a.CheckP2PMode("share")).To(MatchError(config.ErrP2PShareNeedsAuth))
		Expect(a.CheckP2PMode("connect")).To(Succeed())
	})

	It("refuses to start without a data path, because the identity cannot be kept", func() {
		a := newP2PTestApp("connect", "", nil)
		Expect(a.StartP2PNet()).To(MatchError(config.ErrP2PNoDataPath))
		Expect(a.CheckP2PMode("connect")).To(MatchError(config.ErrP2PNoDataPath))
		Expect(a.CheckP2PMode("off")).To(Succeed())
	})

	It("restarts on a change of mode and keeps the identity", func() {
		a := newP2PTestApp("connect", GinkgoT().TempDir(), nil)
		Expect(a.StartP2PNet()).To(Succeed())
		first, _ := a.p2p().runtime()
		id := first.host.ID()

		Expect(a.RestartP2PNet()).To(Succeed())
		second, _ := a.p2p().runtime()
		Expect(second).ToNot(BeIdenticalTo(first))
		Expect(second.host.ID()).To(Equal(id))
		Expect(first.ctx.Err()).To(HaveOccurred(), "the old host is stopped")

		a.ApplicationConfig().P2P.Mode = "off"
		Expect(a.RestartP2PNet()).To(Succeed())
		rt, mode := a.p2p().runtime()
		Expect(rt).To(BeNil())
		Expect(mode).To(Equal(config.P2PModeOff))
	})

	It("regenerates the identity and restarts with it", func() {
		a := newP2PTestApp("connect", GinkgoT().TempDir(), nil)
		Expect(a.StartP2PNet()).To(Succeed())
		rt, _ := a.p2p().runtime()
		old := rt.host.ID().String()
		st, err := a.P2P().RegenerateIdentity(context.Background())
		Expect(err).ToNot(HaveOccurred())
		Expect(st.PeerID).ToNot(BeEmpty())
		Expect(st.PeerID).ToNot(Equal(old))
	})

	It("refuses to regenerate the identity while P2P is off", func() {
		a := newP2PTestApp("off", GinkgoT().TempDir(), nil)
		_, err := a.P2P().RegenerateIdentity(context.Background())
		var me *p2papi.ModeError
		Expect(errors.As(err, &me)).To(BeTrue())
	})
})

var _ = Describe("relaying for paired devices", func() {
	DescribeTable("runs only when it should",
		func(s config.P2PSettings, reach p2pnet.Reachability, want bool, reason string) {
			got, why := relayServiceWanted(s, reach)
			Expect(got).To(Equal(want))
			Expect(why).To(Equal(reason))
		},
		Entry("auto and public", config.P2PSettings{}, p2pnet.ReachabilityPublic, true, ""),
		Entry("auto and private", config.P2PSettings{}, p2pnet.ReachabilityPrivate, false, "not_public"),
		Entry("auto and unknown", config.P2PSettings{}, p2pnet.ReachabilityUnknown, false, "not_public"),
		Entry("on and private", config.P2PSettings{RelayMode: "on"}, p2pnet.ReachabilityPrivate, true, ""),
		Entry("mode off", config.P2PSettings{RelayMode: "off"}, p2pnet.ReachabilityPublic, false, "off"),
		Entry("paired relaying off", config.P2PSettings{NoRelayPaired: true}, p2pnet.ReachabilityPublic, false, "off"),
	)
})

var _ = Describe("the global limit of wrong codes", func() {
	It("blocks every peer once the cap is reached, and frees it after the window", func() {
		now := time.Unix(0, 0)
		inner := &countingLimiter{}
		g := newGlobalLimiter(inner, 3, time.Minute)
		g.now = func() time.Time { return now }
		for range 3 {
			ok, _ := g.Attempt("a", "")
			Expect(ok).To(BeTrue())
		}
		ok, _ := g.Attempt("b", "")
		Expect(ok).To(BeFalse())
		Expect(inner.attempts).To(Equal(3))
		now = now.Add(time.Minute)
		ok, _ = g.Attempt("b", "")
		Expect(ok).To(BeTrue())
	})

	It("gives the attempt back with refund", func() {
		inner := &countingLimiter{}
		g := newGlobalLimiter(inner, 1, time.Minute)
		ok, refund := g.Attempt("a", "")
		Expect(ok).To(BeTrue())
		refund()
		refund()
		Expect(inner.attempts).To(Equal(0))
		ok, _ = g.Attempt("a", "")
		Expect(ok).To(BeTrue())
	})

})

type countingLimiter struct{ attempts int }

func (c *countingLimiter) Attempt(p2pnet.ID, string) (bool, func()) {
	c.attempts++
	return true, func() { c.attempts-- }
}

// loopbackAddrs are the IPv4 loopback addresses of a host.
func loopbackAddrs(rt *p2pRuntime) []ma.Multiaddr {
	var out []ma.Multiaddr
	for _, a := range rt.host.Libp2p().Addrs() {
		if v, err := a.ValueForProtocol(ma.P_IP4); err == nil && v == "127.0.0.1" {
			out = append(out, a)
		}
	}
	return out
}

// transportListenAddrs are the TCP and UDP listen addresses of a host. The
// relay transport always lists /p2p-circuit, which is not a socket.
func transportListenAddrs(rt *p2pRuntime) []string {
	var out []string
	for _, a := range rt.host.Libp2p().Network().ListenAddresses() {
		if !isCircuit(a) {
			out = append(out, a.String())
		}
	}
	return out
}

var _ = Describe("the relay manager on the p2p_relays setting", func() {
	It("keeps relays in the setting and maps the manager's errors", func() {
		a := newP2PTestApp("connect", GinkgoT().TempDir(), nil)
		Expect(a.StartP2PNet()).To(Succeed())
		rt, _ := a.p2p().runtime()
		rm, err := newRelayManager(relayManagerConfig{
			Host: rt.host, LoadRelays: a.p2pRelays, SaveRelays: a.saveP2PRelays,
			PairedPeers: func() []string { return nil },
		})
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(rm.Close)
		Expect(rm.List()).To(BeEmpty())

		_, err = rm.Add(context.Background(), "/ip4/192.0.2.1/tcp/4001", "home", "")
		Expect(err).To(MatchError(p2papi.ErrInvalid))
		Expect(rm.Remove(context.Background(), "nope")).To(MatchError(p2papi.ErrNotFound))
		_, err = rm.Limits(context.Background(), "nope")
		Expect(err).To(MatchError(p2papi.ErrNotFound))
		rate := int64(1)
		_, err = rm.SetLimits(context.Background(), "nope", p2papi.SetRelayLimitsRequest{RateBPS: &rate})
		Expect(err).To(MatchError(p2papi.ErrNotFound))

		store := settingsRelayStore{load: a.p2pRelays, save: a.saveP2PRelays}
		Expect(store.Save(context.Background(), []p2prelaymgr.Entry{{ID: "r", Label: "home", Addr: testRelayAddr}})).To(Succeed())
		Expect(a.ApplicationConfig().P2P.Relays).To(Equal([]config.P2PRelay{{ID: "r", Label: "home", Addr: testRelayAddr}}))
		entries, err := store.Load(context.Background())
		Expect(err).ToNot(HaveOccurred())
		Expect(entries).To(HaveLen(1))
	})
})

const testRelayAddr = "/ip4/192.0.2.2/tcp/4001/p2p/12D3KooWDpJ7As7BWAwRMfu1VU2WCqNjvq387JEYKDBj4kx6nXTN"
