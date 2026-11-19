// Package p2prelay is the gated relay of LocalAI P2P (`local-ai p2p relay`):
// a circuit v2 relay that serves only the peers its admins vouch for.
//
// Admins are LocalAI servers, named by peer ID in Config.Admins or claimed
// once with the code an unclaimed relay prints. Each admin keeps its own set
// of members on the relay over relayproto.AdminID. The secure channel
// authenticates every peer, so there is no password or token. The gate has
// four layers:
//
//  1. The connection gater drops inbound peers that are not admitted right
//     after the handshake.
//  2. Reservations are granted only to admins and members.
//  3. A circuit is allowed only if one admin vouches for both ends, so the
//     tenants of a shared relay cannot reach each other.
//  4. The admin protocol lets an admin read and change only its own set;
//     members can read the public status and nothing else.
//
// A relay pass lets a new device in before its server knows its peer ID: at
// invite time the admin registers a single-use code with a TTL (OpPass); the
// device presents it on relayproto.PassID and becomes a pending member of
// that admin's set until the admin confirms it with add or the pending time
// ends. While a pass or the claim is open the gater lets strangers finish the
// handshake, but they may open only that one stream, and only for
// Config.StrangerTimeout.
//
// The state (sets, claimed admins, pass hashes) lives in
// <DataDir>/allowlist.json, so a restarted relay serves its members before
// any admin reconnects; the admins push their whole set again on reconnect.
// The relay is a standalone process with per-instance state.
//
// Usage:
//
//	r, err := p2prelay.New(cfg)
//	opts, err := r.HostOptions()
//	h, err := libp2p.New(append(opts, libp2p.Identity(key),
//		libp2p.ListenAddrStrings(listen...), libp2p.EnableNATService())...)
//	err = r.Start(h)
//	defer r.Close()
//
// The caller owns the host, its identity and its listen addresses. It should
// enable the AutoNAT service (libp2p.EnableNATService) on the relay host:
// servers behind the relay then learn that they are private and start
// AutoRelay even when no other AutoNAT server answers them.
package p2prelay

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	relayv2 "github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/relay"
	"github.com/mudler/xlog"
	ma "github.com/multiformats/go-multiaddr"
	manet "github.com/multiformats/go-multiaddr/net"
	"golang.org/x/time/rate"

	"github.com/mudler/LocalAI/core/services/p2prelay/relayproto"
)

// Version is reported in Status.
const Version = "1"

const (
	adminStreamTimeout  = 15 * time.Second
	adminStreamsPerPeer = 2
	adminWritesPerSec   = 10
	sweepInterval       = time.Second
	statsInterval       = 10 * time.Minute
)

// Relay is a gated circuit v2 relay. Create it with New, build the host with
// HostOptions, then Start it.
type Relay struct {
	cfg    Config
	gate   *gate
	tracer *tracer
	start  time.Time

	claimTries *attempts
	passIP     *attempts
	passPeer   *attempts

	// limitsMu serialises changes of the limits.
	limitsMu sync.Mutex
	// lastRestart is when the circuit service last restarted for a change of
	// the limits. limitsMu guards it.
	lastRestart time.Time

	mu     sync.Mutex
	h      host.Host
	relay  *relayv2.Relay
	rl     *rateLimitedHost
	notify *network.NotifyBundle
	cancel context.CancelFunc
	done   chan struct{}
	busy   map[peer.ID]int
	writes map[peer.ID]*rate.Limiter
	// tried marks the peers that used their one pass attempt on their
	// current connection.
	tried map[peer.ID]bool
}

// New loads the state from cfg.DataDir and prepares the gate. It does not
// open the network: see HostOptions and Start.
func New(cfg Config) (*Relay, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	cfg.Admins = slices.Clone(cfg.Admins)
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return nil, fmt.Errorf("p2prelay: data dir: %w", err)
	}
	g, err := newGate(&cfg)
	if err != nil {
		return nil, err
	}
	if err := applyStoredLimits(&cfg); err != nil {
		return nil, err
	}
	tr := &tracer{}
	g.circuits = tr.circuits.Load
	return &Relay{
		cfg:        cfg,
		gate:       g,
		tracer:     tr,
		claimTries: newAttempts(5, time.Minute),
		passIP:     newAttempts(5, time.Minute),
		passPeer:   newAttempts(3, time.Minute),
		busy:       map[peer.ID]int{},
		writes:     map[peer.ID]*rate.Limiter{},
		tried:      map[peer.ID]bool{},
	}, nil
}

// HostOptions returns the libp2p options the relay host must be built with:
// the connection gater, a resource manager with the per-IP cap and the
// stream filter, the announced addresses, public reachability and no relay
// client. Call it once per host.
func (r *Relay) HostOptions() ([]libp2p.Option, error) {
	rm, err := newResourceManager(&r.cfg, r.gate)
	if err != nil {
		return nil, err
	}
	opts := []libp2p.Option{
		libp2p.ConnectionGater(r.gate),
		libp2p.ResourceManager(rm),
		libp2p.ForceReachabilityPublic(),
		libp2p.DisableRelay(),
	}
	if len(r.cfg.Announce) > 0 || r.cfg.PublicIP != nil {
		opts = append(opts, libp2p.AddrsFactory(r.announce))
	}
	return opts, nil
}

// announce is the AddrsFactory: Config.Announce as is, or the host addresses
// with their IPv4 address replaced by Config.PublicIP.
func (r *Relay) announce(in []ma.Multiaddr) []ma.Multiaddr {
	if len(r.cfg.Announce) > 0 {
		return slices.Clone(r.cfg.Announce)
	}
	pub, err := ma.NewComponent("ip4", r.cfg.PublicIP.String())
	if err != nil {
		return in
	}
	seen := map[string]bool{}
	var out []ma.Multiaddr
	for _, a := range in {
		if len(a) > 0 && a[0].Code() == ma.P_IP4 {
			a = append(ma.Multiaddr{*pub}, a[1:]...)
		}
		if !seen[a.String()] {
			seen[a.String()] = true
			out = append(out, a)
		}
	}
	return out
}

// Start runs the relay on h, which must have been built with HostOptions.
// The relay does not close h.
func (r *Relay) Start(h host.Host) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.h != nil {
		return errors.New("p2prelay: already started")
	}
	r.h = h
	r.rl = newRateLimitedHost(h, r.cfg.RateLimit, r.cfg.TotalRateLimit)
	rel, err := r.newCircuitRelayLocked()
	if err != nil {
		r.h, r.rl = nil, nil
		return err
	}
	r.relay, r.start = rel, time.Now()
	h.SetStreamHandler(relayproto.AdminID, r.handleAdmin)
	h.SetStreamHandler(relayproto.PassID, r.handlePass)
	r.notify = &network.NotifyBundle{ConnectedF: r.connected, DisconnectedF: r.disconnected}
	h.Network().Notify(r.notify)

	ctx, cancel := context.WithCancel(context.Background())
	r.cancel, r.done = cancel, make(chan struct{})
	go r.loop(ctx)

	st := r.statusLocked(nil)
	xlog.Info("relay: started", "mode", st.Mode, "peer_id", st.PeerID, "admins", st.Admins, "members", st.Members,
		"circuit_max_bytes", st.CircuitMaxBytes, "circuit_max_seconds", st.CircuitMaxSeconds, "rate_bps", st.RateBPS,
		"total_rate_bps", st.TotalRateBPS, "max_circuits", r.cfg.MaxCircuits)
	if st.Mode == relayproto.ModeOpen {
		xlog.Warn("relay: OPEN RELAY: any peer can reserve and relay through this host")
	}
	return nil
}

// newCircuitRelayLocked starts the circuit v2 service on the rate-limited
// host with the current limits. The caller holds mu.
func (r *Relay) newCircuitRelayLocked() (*relayv2.Relay, error) {
	ropts := []relayv2.Option{
		relayv2.WithResources(r.cfg.Limits.resources()),
		relayv2.WithACL(r.gate),
		relayv2.WithMetricsTracer(r.tracer),
	}
	if r.cfg.AllowPrivateAddrs || !hasPublicAddr(r.h.Addrs()) {
		if !r.cfg.AllowPrivateAddrs {
			xlog.Warn("relay: no public address; reservations carry private addresses")
		}
		ropts = append(ropts, relayv2.WithReservationAddressFilter(func(ma.Multiaddr) bool { return true }))
	}
	rel, err := relayv2.New(r.rl, ropts...)
	if err != nil {
		return nil, fmt.Errorf("p2prelay: start relay: %w", err)
	}
	return rel, nil
}

// Close stops the relay and removes its handlers. It does not close the host.
func (r *Relay) Close() error {
	r.mu.Lock()
	if r.h == nil || r.cancel == nil {
		r.mu.Unlock()
		return nil
	}
	h, rel, cancel, done := r.h, r.relay, r.cancel, r.done
	r.cancel = nil
	r.mu.Unlock()

	cancel()
	<-done
	h.RemoveStreamHandler(relayproto.AdminID)
	h.RemoveStreamHandler(relayproto.PassID)
	h.Network().StopNotify(r.notify)
	if rel == nil {
		// A failed restart of the circuit service left none.
		return nil
	}
	return rel.Close()
}

func hasPublicAddr(addrs []ma.Multiaddr) bool {
	return slices.ContainsFunc(addrs, manet.IsPublicAddr)
}

// ClaimCode returns the one-time claim code of an unclaimed relay, or "".
func (r *Relay) ClaimCode() string {
	r.gate.mu.RLock()
	defer r.gate.mu.RUnlock()
	return r.gate.claimCode
}

// Addrs returns the addresses to give to admins, with /p2p/<id>: public
// addresses first and loopback last. It is empty before Start.
func (r *Relay) Addrs() []ma.Multiaddr {
	r.mu.Lock()
	h := r.h
	r.mu.Unlock()
	if h == nil {
		return nil
	}
	rank := func(a ma.Multiaddr) int {
		switch {
		case manet.IsPublicAddr(a):
			return 0
		case manet.IsIPLoopback(a):
			return 2
		}
		return 1
	}
	addrs := slices.Clone(h.Addrs())
	slices.SortStableFunc(addrs, func(a, b ma.Multiaddr) int { return cmp.Compare(rank(a), rank(b)) })
	self, err := ma.NewComponent("p2p", h.ID().String())
	if err != nil {
		return nil
	}
	out := make([]ma.Multiaddr, 0, len(addrs))
	for _, a := range addrs {
		out = append(out, a.AppendComponent(self))
	}
	return out
}

// Status returns the full status of the relay.
func (r *Relay) Status() relayproto.Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.statusLocked(nil)
}

func (r *Relay) statusLocked(from ma.Multiaddr) relayproto.Status {
	now := time.Now()
	admins, members, pending, passes := r.gate.counts(now)
	st := relayproto.Status{
		Version:           Version,
		Mode:              r.gate.mode(),
		Admins:            admins,
		Members:           members,
		Pending:           pending,
		Passes:            passes,
		Reserv:            r.tracer.reservations.Load(),
		Circuits:          r.tracer.circuits.Load(),
		CircuitsTotal:     r.tracer.circuitsTotal.Load(),
		Denied:            r.tracer.denied.Load(),
		BytesRelayed:      r.tracer.bytes.Load(),
		CircuitMaxBytes:   r.cfg.CircuitMaxBytes,
		CircuitMaxSeconds: int64(r.cfg.CircuitMaxDuration / time.Second),
		RateBPS:           r.cfg.RateLimit,
		Ceiling:           r.cfg.Ceiling.reported(),
		TotalRateBPS:      r.cfg.TotalRateLimit,
		MaxCircuits:       r.cfg.MaxCircuits,
		MaxReservations:   r.cfg.MaxReservations,
	}
	if r.h != nil {
		st.PeerID = r.h.ID().String()
		st.UptimeSeconds = int64(now.Sub(r.start) / time.Second)
	}
	if from != nil {
		st.YourAddr = from.String()
	}
	return st
}

// publicStatus is what a member (not an admin) may read.
func publicStatus(st relayproto.Status) *relayproto.Status {
	return &relayproto.Status{
		Version:           st.Version,
		Mode:              st.Mode,
		PeerID:            st.PeerID,
		CircuitMaxBytes:   st.CircuitMaxBytes,
		CircuitMaxSeconds: st.CircuitMaxSeconds,
		RateBPS:           st.RateBPS,
		Ceiling:           st.Ceiling,
		YourAddr:          st.YourAddr,
	}
}

func (r *Relay) loop(ctx context.Context) {
	defer close(r.done)
	sweep := time.NewTicker(sweepInterval)
	defer sweep.Stop()
	stats := time.NewTicker(statsInterval)
	defer stats.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-sweep.C:
			r.disconnect(r.gate.sweep(now), "pending membership expired")
			r.claimTries.prune(now)
			r.passIP.prune(now)
			r.passPeer.prune(now)
		case <-stats.C:
			st := r.Status()
			xlog.Info("relay: stats", "mode", st.Mode, "reservations", st.Reserv, "circuits", st.Circuits,
				"circuits_total", st.CircuitsTotal, "denied", st.Denied, "bytes", st.BytesRelayed)
			if st.Mode == relayproto.ModeOpen {
				xlog.Warn("relay: OPEN RELAY: any peer can reserve and relay through this host")
			}
		}
	}
}

// disconnect closes every connection of ids. That ends their reservations
// and their circuits; the gater refuses them when they come back.
func (r *Relay) disconnect(ids []peer.ID, why string) {
	r.mu.Lock()
	h := r.h
	r.mu.Unlock()
	if h == nil {
		return
	}
	for _, id := range ids {
		n := len(h.Network().ConnsToPeer(id))
		if err := h.Network().ClosePeer(id); err != nil {
			xlog.Debug("relay: close peer", "peer", id, "error", err)
		}
		xlog.Info("relay: revoked", "peer", id, "connections", n, "reason", why)
	}
}

// connected starts the stranger timer: a peer that is let in only to claim
// the relay or to present a pass is closed after StrangerTimeout unless it
// has become a member by then.
func (r *Relay) connected(_ network.Network, c network.Conn) {
	if c.Stat().Direction != network.DirInbound || r.gate.admitted(c.RemotePeer()) {
		return
	}
	p := c.RemotePeer()
	time.AfterFunc(r.cfg.StrangerTimeout, func() {
		if !r.gate.admitted(p) {
			xlog.Debug("relay: closing stranger connection", "peer", p)
			_ = c.Close()
		}
	})
}

func (r *Relay) disconnected(n network.Network, c network.Conn) {
	p := c.RemotePeer()
	if n.Connectedness(p) == network.Connected {
		return
	}
	r.mu.Lock()
	delete(r.tried, p)
	r.mu.Unlock()
}

// ipKey groups addresses for the per-IP caps: one IPv4 address, or one IPv6
// /56 (the usual allocation of one home).
func ipKey(a ma.Multiaddr) string {
	ip, err := manet.ToIP(a)
	if err != nil {
		return a.String()
	}
	if v4 := ip.To4(); v4 != nil {
		return v4.String()
	}
	return ip.Mask(net.CIDRMask(56, 128)).String()
}
