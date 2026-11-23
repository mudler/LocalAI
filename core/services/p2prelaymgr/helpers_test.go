package p2prelaymgr_test

import (
	"context"
	"net"
	"path/filepath"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/p2pnet"
	"github.com/mudler/LocalAI/core/services/p2prelay"
	"github.com/mudler/LocalAI/core/services/p2prelay/relayproto"
	"github.com/mudler/LocalAI/core/services/p2prelaymgr"
)

// testRelay is a relay on loopback that can be stopped and started again on
// the same port with the same key.
type testRelay struct {
	cfg  p2prelay.Config
	key  crypto.PrivKey
	port string

	relay *p2prelay.Relay
	h     host.Host
}

func newTestRelay(cfg p2prelay.Config) *testRelay {
	cfg.AllowPrivateAddrs = true
	cfg.StrangerTimeout = 5 * time.Second
	if cfg.DataDir == "" {
		cfg.DataDir = GinkgoT().TempDir()
	}
	key, _, err := crypto.GenerateEd25519Key(nil)
	Expect(err).ToNot(HaveOccurred())
	t := &testRelay{cfg: cfg, key: key, port: "0"}
	t.start()
	DeferCleanup(t.stop)
	return t
}

func (t *testRelay) start() {
	r, err := p2prelay.New(t.cfg)
	Expect(err).ToNot(HaveOccurred())
	opts, err := r.HostOptions()
	Expect(err).ToNot(HaveOccurred())
	var h host.Host
	// The port of a stopped relay can take a moment to free.
	Eventually(func() error {
		h, err = libp2p.New(append(opts, libp2p.Identity(t.key),
			libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/"+t.port))...)
		return err
	}).WithTimeout(10 * time.Second).WithPolling(100 * time.Millisecond).Should(Succeed())
	Expect(r.Start(h)).To(Succeed())
	t.relay, t.h = r, h
	if t.port == "0" {
		_, p, _ := net.SplitHostPort(tcpHostPort(h.Addrs()))
		t.port = p
	}
}

func tcpHostPort(addrs []ma.Multiaddr) string {
	for _, a := range addrs {
		ip, err1 := a.ValueForProtocol(ma.P_IP4)
		port, err2 := a.ValueForProtocol(ma.P_TCP)
		if err1 == nil && err2 == nil {
			return net.JoinHostPort(ip, port)
		}
	}
	Fail("relay has no TCP address")
	return ""
}

func (t *testRelay) stop() {
	if t.relay == nil {
		return
	}
	_ = t.relay.Close()
	_ = t.h.Close()
	t.relay, t.h = nil, nil
}

// restart stops the relay and starts it again, optionally with an empty
// data directory.
func (t *testRelay) restart(freshData bool) {
	t.stop()
	if freshData {
		t.cfg.DataDir = GinkgoT().TempDir()
	}
	t.start()
}

func (t *testRelay) addr() string {
	return "/ip4/127.0.0.1/tcp/" + t.port + "/p2p/" + t.h.ID().String()
}

func (t *testRelay) id() string { return t.h.ID().String() }

// gatedCfg returns the config of a gated relay with the admins.
func gatedCfg(admins ...peer.ID) p2prelay.Config {
	cfg := p2prelay.DefaultConfig(false)
	cfg.Admins = admins
	return cfg
}

// server is a LocalAI server: a P2P host with a mutable paired set.
type server struct {
	host *p2pnet.Host

	mu     sync.Mutex
	paired []string
	nodes  []string
}

func newServer() *server {
	h, err := p2pnet.New(context.Background(), p2pnet.Config{
		IdentityPath: filepath.Join(GinkgoT().TempDir(), "identity.key"),
		ListenAddrs:  []string{"/ip4/127.0.0.1/tcp/0"},
	})
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(h.Close)
	return &server{host: h}
}

func (s *server) pairedPeers() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.paired...)
}

func (s *server) setPaired(ids ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.paired = ids
}

func (s *server) nodePeers() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.nodes...)
}

func (s *server) setNodes(ids ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nodes = ids
}

// seq returns the sequence number of the admin set of s on the relay: it
// changes with every write to the set.
func (s *server) seq(relayID string) int64 {
	rid, err := peer.Decode(relayID)
	Expect(err).ToNot(HaveOccurred())
	resp, err := relayproto.Call(ctx10(), s.host.Libp2p(), rid, relayproto.Request{Op: relayproto.OpStatus})
	Expect(err).ToNot(HaveOccurred())
	return resp.Seq
}

func randomPeer() string {
	k, _, err := crypto.GenerateEd25519Key(nil)
	Expect(err).ToNot(HaveOccurred())
	id, err := peer.IDFromPrivateKey(k)
	Expect(err).ToNot(HaveOccurred())
	return id.String()
}

// manager builds a manager for s. fast makes the loop tick quickly.
func (s *server) manager(store p2prelaymgr.Store, fast bool) p2prelaymgr.Manager {
	cfg := p2prelaymgr.Config{Host: s.host, Store: store, PairedPeers: s.pairedPeers, NodePeers: s.nodePeers}
	if fast {
		cfg.Tick, cfg.SyncInterval, cfg.Retry = 100*time.Millisecond, 300*time.Millisecond, 100*time.Millisecond
	}
	m, err := p2prelaymgr.New(cfg)
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(m.Close)
	return m
}

func ctx10() context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	DeferCleanup(cancel)
	return ctx
}

// members lists the members the relay holds for admin, read as that admin.
func (s *server) members(relayID string) []string {
	rid, err := peer.Decode(relayID)
	Expect(err).ToNot(HaveOccurred())
	resp, err := relayproto.Call(ctx10(), s.host.Libp2p(), rid, relayproto.Request{Op: relayproto.OpList})
	if err != nil {
		// The relay may be down; callers poll.
		return nil
	}
	var out []string
	for _, p := range resp.Peers {
		if !p.Pending {
			out = append(out, p.ID)
		}
	}
	return out
}
