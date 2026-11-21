package p2prelay_test

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"time"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/client"
	ma "github.com/multiformats/go-multiaddr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/p2prelay"
	"github.com/mudler/LocalAI/core/services/p2prelay/relayproto"
)

const (
	// protoDown: the client sends a uint64 n, the server sends n bytes.
	protoDown protocol.ID = "/test/down/1"
	// protoUp: the client sends bytes until EOF, the server answers their count.
	protoUp protocol.ID = "/test/up/1"
)

// testRelay is a gated relay on loopback.
type testRelay struct {
	*p2prelay.Relay
	h   host.Host
	key crypto.PrivKey
	cfg p2prelay.Config
}

func (t *testRelay) info() peer.AddrInfo { return peer.AddrInfo{ID: t.h.ID(), Addrs: t.h.Addrs()} }

// testConfig returns a relay config for tests: loopback addresses in
// reservations and a short stranger timeout.
func testConfig(open bool, admins ...host.Host) p2prelay.Config {
	cfg := p2prelay.DefaultConfig(open)
	cfg.DataDir = GinkgoT().TempDir()
	cfg.AllowPrivateAddrs = true
	cfg.StrangerTimeout = 2 * time.Second
	for _, a := range admins {
		cfg.Admins = append(cfg.Admins, a.ID())
	}
	return cfg
}

func startRelay(cfg p2prelay.Config) *testRelay {
	key, _, err := crypto.GenerateEd25519Key(nil)
	Expect(err).ToNot(HaveOccurred())
	return startRelayWithKey(cfg, key)
}

func startRelayWithKey(cfg p2prelay.Config, key crypto.PrivKey) *testRelay {
	r, err := p2prelay.New(cfg)
	Expect(err).ToNot(HaveOccurred())
	opts, err := r.HostOptions()
	Expect(err).ToNot(HaveOccurred())
	h, err := libp2p.New(append(opts, libp2p.Identity(key), libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))...)
	Expect(err).ToNot(HaveOccurred())
	Expect(r.Start(h)).To(Succeed())
	t := &testRelay{Relay: r, h: h, key: key, cfg: cfg}
	DeferCleanup(t.stop)
	return t
}

func (t *testRelay) stop() {
	_ = t.Close()
	_ = t.h.Close()
}

// newPeer returns a host that serves protoDown and protoUp.
func newPeer() host.Host {
	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	Expect(err).ToNot(HaveOccurred())
	h.SetStreamHandler(protoDown, func(s network.Stream) {
		defer func() { _ = s.Close() }()
		var n uint64
		if err := binary.Read(s, binary.BigEndian, &n); err != nil {
			_ = s.Reset()
			return
		}
		buf := make([]byte, 32<<10)
		for n > 0 {
			k := min(n, uint64(len(buf)))
			if _, err := s.Write(buf[:k]); err != nil {
				_ = s.Reset()
				return
			}
			n -= k
		}
	})
	h.SetStreamHandler(protoUp, func(s network.Stream) {
		defer func() { _ = s.Close() }()
		n, err := io.Copy(io.Discard, s)
		if err != nil {
			_ = s.Reset()
			return
		}
		_ = binary.Write(s, binary.BigEndian, uint64(n))
	})
	DeferCleanup(h.Close)
	return h
}

func ctxT(d time.Duration) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	DeferCleanup(cancel)
	return ctx
}

func connect(h host.Host, r *testRelay) error {
	return h.Connect(ctxT(10*time.Second), r.info())
}

func call(h host.Host, r *testRelay, req relayproto.Request) (relayproto.Response, error) {
	if err := connect(h, r); err != nil {
		return relayproto.Response{}, err
	}
	return relayproto.Call(ctxT(10*time.Second), h, r.h.ID(), req)
}

func seq() int64 { return time.Now().UnixNano() }

// push sets the admin's set to members.
func push(admin host.Host, r *testRelay, op string, members ...host.Host) relayproto.Response {
	req := relayproto.Request{Op: op, Seq: seq()}
	for _, m := range members {
		req.Peers = append(req.Peers, relayproto.Peer{ID: m.ID().String()})
	}
	resp, err := call(admin, r, req)
	Expect(err).ToNot(HaveOccurred())
	return resp
}

func reserve(h host.Host, r *testRelay) error {
	if err := connect(h, r); err != nil {
		return err
	}
	_, err := client.Reserve(ctxT(10*time.Second), h, r.info())
	return err
}

// circuit connects src to dest through the relay.
func circuit(src, dest host.Host, r *testRelay) error {
	addr, err := ma.NewMultiaddr(fmt.Sprintf("%s/p2p/%s/p2p-circuit", r.h.Addrs()[0], r.h.ID()))
	Expect(err).ToNot(HaveOccurred())
	ctx := network.WithAllowLimitedConn(ctxT(10*time.Second), "test")
	return src.Connect(ctx, peer.AddrInfo{ID: dest.ID(), Addrs: []ma.Multiaddr{addr}})
}

func limitedStream(src, dest host.Host, proto protocol.ID) (network.Stream, error) {
	ctx := network.WithAllowLimitedConn(ctxT(10*time.Second), "test")
	return src.NewStream(ctx, dest.ID(), proto)
}

// download asks dest for n bytes over the existing connection and returns
// how many arrived and how long it took.
func download(src, dest host.Host, n int64) (int64, time.Duration, error) {
	s, err := limitedStream(src, dest, protoDown)
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = s.Close() }()
	start := time.Now()
	if err := binary.Write(s, binary.BigEndian, uint64(n)); err != nil {
		return 0, 0, err
	}
	got, err := io.Copy(io.Discard, s)
	return got, time.Since(start), err
}

// upload sends n bytes to dest and returns how long it took until dest
// confirmed them.
func upload(src, dest host.Host, n int64) (time.Duration, error) {
	s, err := limitedStream(src, dest, protoUp)
	if err != nil {
		return 0, err
	}
	defer func() { _ = s.Close() }()
	start := time.Now()
	if _, err := io.CopyN(s, zeros{}, n); err != nil {
		return 0, err
	}
	if err := s.CloseWrite(); err != nil {
		return 0, err
	}
	var got uint64
	if err := binary.Read(s, binary.BigEndian, &got); err != nil {
		return 0, err
	}
	if int64(got) != n {
		return 0, fmt.Errorf("dest got %d of %d bytes", got, n)
	}
	return time.Since(start), nil
}

type zeros struct{}

func (zeros) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

// connected reports whether a and b have a live connection.
func connected(a, b host.Host) bool {
	return a.Network().Connectedness(b.ID()) == network.Connected
}
