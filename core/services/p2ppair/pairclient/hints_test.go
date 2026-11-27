package pairclient_test

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/net/connmgr"
	relayv2 "github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/relay"
	ma "github.com/multiformats/go-multiaddr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/p2pnet"
	"github.com/mudler/LocalAI/core/services/p2ppair"
	"github.com/mudler/LocalAI/core/services/p2ppair/pairclient"
)

// fakeServer is a server host in this process that serves the redeem route
// while pairing is open and counts the redeem requests it receives.
type fakeServer struct {
	host    *p2pnet.Host
	redeems atomic.Int32
}

func newHost(cfg p2pnet.Config) *p2pnet.Host {
	GinkgoHelper()
	cfg.IdentityPath = filepath.Join(GinkgoT().TempDir(), "id.key")
	// The test relays listen on loopback.
	cfg.PrivateHints = true
	if cfg.ListenAddrs == nil {
		cfg.ListenAddrs = []string{"/ip4/127.0.0.1/tcp/0"}
	}
	h, err := p2pnet.New(context.Background(), cfg)
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(h.Close)
	return h
}

func startFakeServer(listen []string, relays ...peer.AddrInfo) *fakeServer {
	GinkgoHelper()
	gate := p2pnet.NewAllowlist()
	gate.SetPairing(true)
	s := &fakeServer{host: newHost(p2pnet.Config{ListenAddrs: listen, StaticRelays: relays, RelayRetry: time.Second, Gate: gate})}
	l, err := s.host.Listen(p2pnet.ProtoHTTP, nil)
	Expect(err).ToNot(HaveOccurred())
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+pairclient.RedeemPath, func(w http.ResponseWriter, _ *http.Request) {
		s.redeems.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"device_id": "dev-1", "kind": "device", "phrase": "apple banana cherry",
			"server": map[string]string{"peer_id": s.host.ID().String(), "name": "test"},
		})
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(l) }()
	DeferCleanup(srv.Close)
	return s
}

// testRelay is a circuit v2 relay with the default limits of a public
// relay.
type testRelay struct {
	h host.Host
}

func startRelay() *testRelay {
	GinkgoHelper()
	cm, err := connmgr.NewConnManager(10, 100)
	Expect(err).ToNot(HaveOccurred())
	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"), libp2p.ConnectionManager(cm), libp2p.DisableRelay())
	Expect(err).ToNot(HaveOccurred())
	r, err := relayv2.New(h, relayv2.WithResources(relayv2.DefaultResources()))
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(func() { _ = r.Close(); _ = h.Close() })
	return &testRelay{h: h}
}

func (r *testRelay) info() peer.AddrInfo { return peer.AddrInfo{ID: r.h.ID(), Addrs: r.h.Addrs()} }

// waitReserved waits until p holds a reservation on the relay.
func (r *testRelay) waitReserved(p peer.ID) {
	GinkgoHelper()
	Eventually(func() bool {
		ti := r.h.ConnManager().GetTagInfo(p)
		if ti == nil {
			return false
		}
		_, ok := ti.Tags["relay-reservation"]
		return ok
	}, 10*time.Second, 50*time.Millisecond).Should(BeTrue())
}

func link(server peer.ID) *p2ppair.Link {
	return &p2ppair.Link{V: 1, Kind: p2ppair.KindDevice, Peer: server, Code: "ABCDE23456", Expires: time.Now().Add(time.Hour)}
}

var _ = Describe("Redeem with hint relays", func() {
	BeforeEach(func() {
		DeferCleanup(pairclient.SetPunchWait(2 * time.Second))
	})

	It("redeems over a direct address as before", func() {
		server := startFakeServer(nil)
		device := newHost(p2pnet.Config{})
		l := link(server.host.ID())
		l.Addrs = server.host.Libp2p().Addrs()

		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		out, err := pairclient.Redeem(ctx, device, l, pairclient.Request{Name: "phone"})
		Expect(err).ToNot(HaveOccurred())
		Expect(out.DeviceID).To(Equal("dev-1"))
		Expect(out.Server.PeerID).To(Equal(server.host.ID().String()))
		Expect(server.redeems.Load()).To(Equal(int32(1)))
	})

	It("returns ErrNeedsRelay and sends no code when no direct path opens", func() {
		hint := startRelay()
		server := startFakeServer([]string{}, hint.info())
		hint.waitReserved(server.host.ID())
		device := newHost(p2pnet.Config{})
		l := link(server.host.ID())
		l.Hints = []peer.AddrInfo{hint.info()}

		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_, err := pairclient.Redeem(ctx, device, l, pairclient.Request{Name: "phone"})
		Expect(err).To(MatchError(pairclient.ErrNeedsRelay))
		Expect(err).To(MatchError(pairclient.ErrUnreachable))
		Expect(errors.Is(err, p2pnet.ErrNoDirectPath)).To(BeTrue(), "%v", err)
		Expect(err).ToNot(MatchError(pairclient.ErrNoRoute))
		Consistently(server.redeems.Load, 300*time.Millisecond).Should(BeZero())
	})

	It("dials through the hints of the link and redeems over the direct path of the hole punch", func() {
		hint := startRelay()
		server := startFakeServer([]string{}, hint.info())
		hint.waitReserved(server.host.ID())
		device := newHost(p2pnet.Config{})
		l := link(server.host.ID())
		l.Hints = []peer.AddrInfo{hint.info()}
		DeferCleanup(pairclient.SetPunchWait(10 * time.Second))

		go func() {
			defer GinkgoRecover()
			// Loopback is not public, so no real hole punch can succeed here.
			// Once the device reached the server through the hint, the
			// server dials it back directly, as the punch would.
			Eventually(func() bool {
				for _, c := range server.host.Libp2p().Network().ConnsToPeer(device.ID()) {
					if _, err := c.RemoteMultiaddr().ValueForProtocol(ma.P_CIRCUIT); err == nil {
						return true
					}
				}
				return false
			}, 10*time.Second, 20*time.Millisecond).Should(BeTrue())
			ctx, cancel := context.WithTimeout(network.WithForceDirectDial(context.Background(), "test"), 5*time.Second)
			defer cancel()
			Expect(server.host.Libp2p().Connect(ctx, peer.AddrInfo{ID: device.ID(), Addrs: device.Libp2p().Addrs()})).To(Succeed())
		}()

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		out, err := pairclient.Redeem(ctx, device, l, pairclient.Request{Name: "phone"})
		Expect(err).ToNot(HaveOccurred())
		Expect(out.DeviceID).To(Equal("dev-1"))
		Expect(server.redeems.Load()).To(Equal(int32(1)))
	})
})

// unusedID returns a fresh peer ID that nothing listens for.
func unusedID() peer.ID {
	_, pub, err := crypto.GenerateEd25519Key(rand.Reader)
	Expect(err).ToNot(HaveOccurred())
	id, err := peer.IDFromPublicKey(pub)
	Expect(err).ToNot(HaveOccurred())
	return id
}

var _ = Describe("DialOptions", func() {
	It("names no route only when the link has no address, relay or hint", func() {
		DeferCleanup(pairclient.SetPunchWait(time.Second))
		device := newHost(p2pnet.Config{ListenAddrs: []string{}})
		l := link(unusedID())
		hint, err := peer.AddrInfoFromString(fmt.Sprintf("/ip4/127.0.0.1/tcp/1/p2p/%s", unusedID()))
		Expect(err).ToNot(HaveOccurred())
		l.Hints = []peer.AddrInfo{*hint}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_, err = pairclient.Redeem(ctx, device, l, pairclient.Request{Name: "x"})
		Expect(err).To(MatchError(pairclient.ErrUnreachable))
		Expect(err).ToNot(MatchError(pairclient.ErrNoRoute))
		Expect(err).ToNot(MatchError(pairclient.ErrNeedsRelay))
	})
})
