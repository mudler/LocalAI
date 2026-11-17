package p2pnet_test

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/net/connmgr"
	relayv2 "github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/relay"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/p2pnet"
)

func TestP2PNet(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "p2pnet test suite")
}

var loopback = []string{"/ip4/127.0.0.1/tcp/0"}

// newHost starts a host on loopback with a fresh identity and no public
// DHT, and closes it when the spec ends.
func newHost(cfg p2pnet.Config) *p2pnet.Host {
	GinkgoHelper()
	if cfg.IdentityPath == "" {
		cfg.IdentityPath = filepath.Join(GinkgoT().TempDir(), "identity.key")
	}
	if cfg.ListenAddrs == nil {
		cfg.ListenAddrs = loopback
	}
	h, err := p2pnet.New(context.Background(), cfg)
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(h.Close)
	return h
}

// addrInfo returns what a peer needs to dial h directly.
func addrInfo(h *p2pnet.Host) peer.AddrInfo {
	return peer.AddrInfo{ID: h.ID(), Addrs: h.Libp2p().Addrs()}
}

// pair makes server admit client and gives client server's addresses.
func pair(server, client *p2pnet.Host) {
	server.Gate().(*p2pnet.Allowlist).Allow(client.ID())
	client.Libp2p().Peerstore().AddAddrs(server.ID(), server.Libp2p().Addrs(), time.Hour)
}

func dialCtx() context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	DeferCleanup(cancel)
	return ctx
}

// serve accepts connections on l and runs fn on each until l closes.
func serve(l net.Listener, fn func(net.Conn)) {
	go func() {
		defer GinkgoRecover()
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer GinkgoRecover()
				fn(c)
			}()
		}
	}()
}

func echo(c net.Conn) {
	_, _ = io.Copy(c, c)
	_ = c.Close()
}

// testRelay is a circuit v2 relay in this process that can restart with the
// same identity and port.
type testRelay struct {
	key  crypto.PrivKey
	addr string
	res  relayv2.Resources
	h    host.Host
	r    *relayv2.Relay
}

func newTestRelay(res relayv2.Resources) *testRelay {
	GinkgoHelper()
	k, _, err := crypto.GenerateEd25519Key(rand.Reader)
	Expect(err).ToNot(HaveOccurred())
	t := &testRelay{key: k, addr: loopback[0], res: res}
	t.start()
	// Keep the port for restarts.
	t.addr = t.h.Addrs()[0].String()
	DeferCleanup(t.stop)
	return t
}

func (t *testRelay) start() {
	GinkgoHelper()
	cm, err := connmgr.NewConnManager(10, 100)
	Expect(err).ToNot(HaveOccurred())
	h, err := libp2p.New(libp2p.Identity(t.key), libp2p.ListenAddrStrings(t.addr), libp2p.ConnectionManager(cm), libp2p.DisableRelay())
	Expect(err).ToNot(HaveOccurred())
	r, err := relayv2.New(h, relayv2.WithResources(t.res))
	Expect(err).ToNot(HaveOccurred())
	t.h, t.r = h, r
}

func (t *testRelay) stop() {
	if t.h == nil {
		return
	}
	_ = t.r.Close()
	_ = t.h.Close()
	t.h, t.r = nil, nil
}

func (t *testRelay) info() peer.AddrInfo {
	ai, err := peer.AddrInfoFromString(fmt.Sprintf("%s/p2p/%s", t.addr, peer.ID(mustID(t.key))))
	Expect(err).ToNot(HaveOccurred())
	return *ai
}

// reserved reports whether p holds a reservation on the relay.
func (t *testRelay) reserved(p peer.ID) bool {
	if t.h == nil {
		return false
	}
	ti := t.h.ConnManager().GetTagInfo(p)
	if ti == nil {
		return false
	}
	_, ok := ti.Tags["relay-reservation"]
	return ok
}

func mustID(k crypto.PrivKey) peer.ID {
	id, err := peer.IDFromPrivateKey(k)
	Expect(err).ToNot(HaveOccurred())
	return id
}
