package p2pnet

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/libp2p/go-libp2p"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	"github.com/libp2p/go-libp2p/core/event"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/libp2p/go-libp2p/core/routing"
	"github.com/libp2p/go-libp2p/p2p/discovery/mdns"
	"github.com/libp2p/go-libp2p/p2p/net/connmgr"
	"github.com/libp2p/go-libp2p/p2p/protocol/holepunch"
	"github.com/mudler/xlog"
	ma "github.com/multiformats/go-multiaddr"
)

// ProtoHTTP is the protocol of LocalAI's HTTP API over P2P.
const ProtoHTTP protocol.ID = "/localai/http/1"

// The protocols of the cluster tunnel over P2P: a worker opens one stream for
// each lane of its tunnel to a frontend. The lanes are separate protocols so
// that the bulk lane can be dialled with a Need of its own.
const (
	ProtoTunnelInference protocol.ID = "/localai/tunnel/1/inference"
	ProtoTunnelBulk      protocol.ID = "/localai/tunnel/1/bulk"
)

// DefaultRelayRetry is the default of Config.RelayRetry.
const DefaultRelayRetry = 30 * time.Second

const (
	defaultUserAgent = "localai"
	mdnsServiceName  = "localai-p2p"
	connectTimeout   = 15 * time.Second
	connLowWater     = 100
	connHighWater    = 400
	connGracePeriod  = time.Minute
)

// DefaultListenAddrs are the addresses a host listens on when
// Config.ListenAddrs is nil: TCP and QUIC on every IPv4 and IPv6 interface,
// on ports that the system picks.
var DefaultListenAddrs = []string{
	"/ip4/0.0.0.0/tcp/0",
	"/ip4/0.0.0.0/udp/0/quic-v1",
	"/ip6/::/tcp/0",
	"/ip6/::/udp/0/quic-v1",
}

// Config configures a Host.
type Config struct {
	// IdentityPath is the file of the host's private key. It is created on
	// the first start (see LoadOrCreateIdentity). Required.
	IdentityPath string
	// ListenAddrs are the multiaddrs to listen on. Nil means
	// DefaultListenAddrs. An empty, non-nil slice means no listener: the
	// host only dials out, and peers reach it through its relays.
	ListenAddrs []string
	// PublicDHT runs a client of the public IPFS DHT, which finds public
	// relays that coordinate hole punching.
	PublicDHT bool
	// BootstrapPeers are connected at start. When empty and PublicDHT is on,
	// the libp2p default bootstrap peers are used.
	BootstrapPeers []peer.AddrInfo
	// StaticRelays are the relays the host keeps a reservation on, so that
	// peers behind a NAT can reach it.
	StaticRelays []peer.AddrInfo
	// RelayRetry is how long the host waits before it asks a relay again for
	// a reservation after a failure, and the shortest time between two
	// searches for relay candidates. Zero means DefaultRelayRetry. The
	// libp2p default is one hour, which leaves a host without a relay for an
	// hour after its relay restarts.
	RelayRetry time.Duration
	// MDNS finds peers on the local network.
	MDNS bool
	// PortMapping asks the router for a port mapping (UPnP, NAT-PMP).
	PortMapping bool
	// Gate admits peers and streams. Nil means an empty Allowlist: no
	// inbound connection is admitted.
	Gate Gate
	// UserAgent is sent in the identify protocol. Empty means "localai".
	UserAgent string
}

// Host is a libp2p host with LocalAI's admission rules. It is safe for
// concurrent use.
type Host struct {
	h     host.Host
	dht   atomic.Pointer[dht.IpfsDHT]
	gate  Gate
	mdns  mdns.Service
	punch *punchTracer
	reach atomic.Int32
	sub   event.Subscription

	static []peer.AddrInfo

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu        sync.Mutex
	listeners map[protocol.ID]*listener
	closed    bool
}

// New starts a host. ctx bounds the start only; the host runs until Close.
func New(ctx context.Context, cfg Config) (*Host, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	key, err := LoadOrCreateIdentity(cfg.IdentityPath)
	if err != nil {
		return nil, err
	}
	gate := cfg.Gate
	if gate == nil {
		gate = NewAllowlist()
	}
	listen := cfg.ListenAddrs
	if listen == nil {
		listen = DefaultListenAddrs
	}
	ua := cfg.UserAgent
	if ua == "" {
		ua = defaultUserAgent
	}
	boot := cfg.BootstrapPeers
	if len(boot) == 0 && cfg.PublicDHT {
		boot = dht.GetDefaultBootstrapPeerAddrInfos()
	}

	hctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	n := &Host{
		gate:      gate,
		punch:     newPunchTracer(),
		static:    cfg.StaticRelays,
		ctx:       hctx,
		cancel:    cancel,
		listeners: map[protocol.ID]*listener{},
	}
	cm, err := connmgr.NewConnManager(connLowWater, connHighWater, connmgr.WithGracePeriod(connGracePeriod))
	if err != nil {
		cancel()
		return nil, fmt.Errorf("p2pnet: connection manager: %w", err)
	}
	opts := []libp2p.Option{
		libp2p.Identity(key),
		libp2p.UserAgent(ua),
		libp2p.ConnectionManager(cm),
		libp2p.ConnectionGater(connGater{gate: gate}),
		// The relay transport dials and accepts relayed connections.
		// AutoRelay needs it set before the libp2p defaults are applied.
		libp2p.EnableRelay(),
		libp2p.EnableHolePunching(holepunch.WithTracer(n.punch)),
		libp2p.EnableNATService(),
		libp2p.EnableAutoNATv2(),
	}
	if len(listen) == 0 {
		opts = append(opts, libp2p.NoListenAddrs)
	} else {
		opts = append(opts, libp2p.ListenAddrStrings(listen...))
	}
	if cfg.PortMapping {
		opts = append(opts, libp2p.NATPortMap())
	}
	if len(cfg.StaticRelays) > 0 || cfg.PublicDHT {
		opts = append(opts, libp2p.EnableAutoRelayWithPeerSource(n.relayCandidates, autoRelayOptions(cfg)...))
	}
	if cfg.PublicDHT {
		opts = append(opts, libp2p.Routing(func(h host.Host) (routing.PeerRouting, error) {
			d, err := dht.New(hctx, h, dht.Mode(dht.ModeClient), dht.BootstrapPeers(boot...))
			if err != nil {
				return nil, err
			}
			n.dht.Store(d)
			return d, nil
		}))
	}
	h, err := libp2p.New(opts...)
	if err != nil {
		cancel()
		if d := n.dht.Load(); d != nil {
			_ = d.Close()
		}
		return nil, fmt.Errorf("p2pnet: start host: %w", err)
	}
	n.h = h
	if err := n.start(cfg, boot); err != nil {
		_ = n.Close()
		return nil, err
	}
	return n, nil
}

func (n *Host) start(cfg Config, boot []peer.AddrInfo) error {
	n.reach.Store(int32(network.ReachabilityUnknown))
	sub, err := n.h.EventBus().Subscribe(new(event.EvtLocalReachabilityChanged))
	if err != nil {
		return fmt.Errorf("p2pnet: watch reachability: %w", err)
	}
	n.sub = sub
	n.wg.Go(func() {
		for e := range sub.Out() {
			n.reach.Store(int32(e.(event.EvtLocalReachabilityChanged).Reachability)) // #nosec G115 -- Reachability is a small enum, so the value fits in an int32
		}
	})
	n.h.Network().Notify(&network.NotifyBundle{DisconnectedF: func(nw network.Network, c network.Conn) {
		if len(nw.ConnsToPeer(c.RemotePeer())) == 0 {
			n.punch.forget(c.RemotePeer())
		}
	}})
	if len(cfg.StaticRelays) > 0 {
		if err := kickAutoRelay(n.h); err != nil {
			return err
		}
	}
	if d := n.dht.Load(); d != nil {
		if err := d.Bootstrap(n.ctx); err != nil {
			return fmt.Errorf("p2pnet: bootstrap DHT: %w", err)
		}
	}
	// Start-up does not wait for bootstrap peers or relays: a slow or dead
	// peer must not delay the server.
	n.connectAll(boot)
	n.connectAll(cfg.StaticRelays)
	if cfg.MDNS {
		// mDNS only finds peers on the local network, so a host that cannot
		// start it still works. A host with no listen address, such as one in
		// mode connect, has no address to announce and the start fails.
		svc := mdns.NewMdnsService(n.h, mdnsServiceName, mdnsNotifee{n: n})
		if err := svc.Start(); err != nil {
			xlog.Warn("p2p: local network discovery (mDNS) is off", "error", err)
		} else {
			n.mdns = svc
		}
	}
	return nil
}

func (n *Host) connectAll(ais []peer.AddrInfo) {
	for _, ai := range ais {
		n.wg.Go(func() {
			ctx, cancel := context.WithTimeout(n.ctx, connectTimeout)
			defer cancel()
			if err := n.h.Connect(ctx, ai); err != nil {
				xlog.Debug("p2p: connect failed", "peer", ai.ID.String(), "error", err)
			}
		})
	}
}

type mdnsNotifee struct{ n *Host }

func (m mdnsNotifee) HandlePeerFound(ai peer.AddrInfo) {
	if ai.ID == m.n.h.ID() {
		return
	}
	m.n.connectAll([]peer.AddrInfo{ai})
}

// ID returns the host's peer ID.
func (n *Host) ID() peer.ID { return n.h.ID() }

// Addrs returns the host's addresses, each ending in /p2p/<peer ID>.
func (n *Host) Addrs() []ma.Multiaddr {
	addrs, err := peer.AddrInfoToP2pAddrs(&peer.AddrInfo{ID: n.h.ID(), Addrs: n.h.Addrs()})
	if err != nil {
		return nil
	}
	return addrs
}

// Reachability returns what AutoNAT last found out about the host:
// public, private (behind a NAT) or unknown.
func (n *Host) Reachability() network.Reachability {
	return network.Reachability(n.reach.Load())
}

// Gate returns the host's admission gate.
func (n *Host) Gate() Gate { return n.gate }

// Libp2p returns the underlying libp2p host. Only the relay packages and
// tests use it, for example to attach a relay service with its own ACL.
func (n *Host) Libp2p() host.Host { return n.h }

// Revoke stops admitting p (when the gate supports it, as Allowlist does)
// and closes every connection to p, which ends its streams at once.
func (n *Host) Revoke(p peer.ID) {
	if d, ok := n.gate.(interface{ Disallow(peer.ID) }); ok {
		d.Disallow(p)
	}
	if err := n.h.Network().ClosePeer(p); err != nil {
		xlog.Debug("p2p: close revoked peer", "peer", p.String(), "error", err)
	}
}

// Close stops the host and waits for its background work.
func (n *Host) Close() error {
	n.mu.Lock()
	if n.closed {
		n.mu.Unlock()
		return nil
	}
	n.closed = true
	ls := make([]*listener, 0, len(n.listeners))
	for _, l := range n.listeners {
		ls = append(ls, l)
	}
	n.mu.Unlock()
	for _, l := range ls {
		_ = l.Close()
	}
	n.cancel()
	var errs []error
	if n.mdns != nil {
		errs = append(errs, n.mdns.Close())
	}
	if d := n.dht.Load(); d != nil {
		errs = append(errs, d.Close())
	}
	errs = append(errs, n.h.Close())
	if n.sub != nil {
		errs = append(errs, n.sub.Close())
	}
	n.wg.Wait()
	return errors.Join(errs...)
}
