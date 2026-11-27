package application

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	p2papi "github.com/mudler/LocalAI/core/http/endpoints/p2p"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/p2pnet"
	"github.com/mudler/LocalAI/core/services/p2ppair"
	"github.com/mudler/LocalAI/core/services/p2prelay"
	"github.com/mudler/LocalAI/core/services/p2psplit"
	"github.com/mudler/LocalAI/core/services/tunnel"
	"github.com/mudler/LocalAI/internal"
	ma "github.com/multiformats/go-multiaddr"
)

// redeemPath is the one route an unpaired peer may call, over P2P only.
const redeemPath = "/api/p2p/redeem"

// routesPath gives a paired device the current routes to the server. It is
// served over P2P only, like redeemPath.
const routesPath = "/api/p2p/routes"

// p2pReloadInterval is how often a running host reloads its allowlist and the
// pending invites from the store. Another frontend may pair or revoke a
// device, and an invite may expire; a broadcast makes most changes faster.
const p2pReloadInterval = 30 * time.Second

// p2pNet is the P2P layer of the server. It runs at most one host, in the mode
// of the p2p_mode setting:
//
//   - off: nothing runs.
//   - connect: a host that dials out and accepts nobody: no listen address, no
//     gate entries, no listener.
//   - share: a host with an allowlist of the paired devices, the pairing
//     service, the relay manager, the HTTP server over ProtoHTTP, and the
//     in-process relay for paired devices while the host is publicly
//     reachable.
//
// Distributed mode: every frontend has its own identity and host (state per
// instance); devices and invites live in the shared store, and every frontend
// reloads its allowlist from it on a broadcast and every 30 seconds.
type p2pNet struct {
	app *Application

	mu      sync.Mutex
	rt      *p2pRuntime
	mode    config.P2PMode
	lastErr string
	// closed is set when the application shuts down; nothing starts after.
	closed bool

	handlers atomic.Pointer[p2pHandlers]
	// tunnel serves the tunnel streams of the workers that joined over P2P.
	tunnel atomic.Pointer[P2PTunnelHandler]

	// tunnels are the forwarders of the remotes over P2P.
	tunnels p2pTunnels

	// relayFactory builds the relay manager; nil when this build has none
	// (relay operations then answer 503).
	relayFactory func(relayManagerConfig) (relayManager, error)
	// reloadInterval overrides p2pReloadInterval in tests.
	reloadInterval time.Duration
	// isLANAddr overrides p2pnet.IsLANAddr in tests, which have no private
	// address to listen on.
	isLANAddr func(p2pnet.Multiaddr) bool
	// nodeRegistry overrides the node registry of distributed mode in tests.
	nodeRegistry splitNodeRegistry
}

// p2pHandlers are the HTTP handlers served over P2P.
type p2pHandlers struct {
	main http.Handler
	// only serves the routes that exist over P2P alone.
	only http.Handler
}

// p2pRuntime is one running host and what hangs on it.
type p2pRuntime struct {
	net  *p2pNet
	mode config.P2PMode
	cfg  config.P2PSettings

	host   *p2pnet.Host
	gate   *p2pnet.Allowlist
	pair   *p2ppair.Service
	relays relayManager
	split  *p2psplit.Manager
	srv    *http.Server
	sub    messaging.Subscription
	limit  *globalLimiter
	// signInLimit counts wrong device credentials per peer only. A paired
	// peer's identity is proven by the connection, so a source bucket
	// (shared by every peer behind one relay or NAT) would only let one
	// peer lock the others out of sign-in and redeem.
	signInLimit p2ppair.Limiter

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// peers is the paired set as last loaded into the gate.
	peersMu sync.Mutex
	peers   map[p2pnet.ID]struct{}
	// workers are the paired peers that redeemed a worker invite.
	workers map[p2pnet.ID]struct{}
	// reservers are the paired peers that may reserve on the in-process
	// relay (see reservesOnRelay).
	reservers map[p2pnet.ID]struct{}
	// nodeRelays is the relay admission of the backend nodes that serve
	// split workers, by node ID, as last loaded (see splitRelayCandidates).
	nodeRelays map[string]splitRelayCandidate
	// pending is the number of pending invites as last loaded.
	pending atomic.Int32

	relayMu sync.Mutex
	inproc  io.Closer
	reason  string

	touchMu sync.Mutex
	touched map[p2pnet.ID]time.Time
}

func (a *Application) p2p() *p2pNet {
	a.p2pNetOnce.Do(func() {
		a.p2pNet = &p2pNet{app: a, relayFactory: newRelayManager}
		a.p2pNet.tunnels.n = a.p2pNet
	})
	return a.p2pNet
}

// P2P returns the P2P layer as the HTTP handlers see it.
func (a *Application) P2P() p2papi.Service { return p2pService{n: a.p2p()} }

// P2PEnabled reports whether the P2P layer runs in mode connect or share. A
// mode that failed to start reads as off.
func (a *Application) P2PEnabled() bool {
	_, mode := a.p2p().runtime()
	return mode != config.P2PModeOff && mode != ""
}

// StartP2PNet starts the P2P layer in the mode of the settings. Mode off
// starts nothing. An error leaves the layer off; the status reports it.
func (a *Application) StartP2PNet() error { return a.p2p().restart() }

// RestartP2PNet stops the P2P layer and starts it again with the current
// settings. The settings endpoint calls it when a P2P setting changes.
func (a *Application) RestartP2PNet() error { return a.p2p().restart() }

// StopP2PNet stops the P2P layer for good; the application calls it when it
// shuts down.
func (a *Application) StopP2PNet() { a.p2p().stop() }

// P2PTunnelHandler serves one stream of the cluster tunnel that a worker
// opened over P2P, for one lane.
type P2PTunnelHandler func(ctx context.Context, conn net.Conn, lane tunnel.Lane)

// ServeP2PTunnels sets the handler of the tunnel streams of workers over P2P.
// The host listens for them in mode share on a distributed frontend; until
// this is called, a stream is refused as by a frontend that is starting.
func (a *Application) ServeP2PTunnels(h P2PTunnelHandler) {
	a.p2p().tunnel.Store(&h)
}

// IsWorkerPeer reports whether peer is a device of kind worker that is paired
// with this server and not revoked. It is false outside mode share.
func (a *Application) IsWorkerPeer(_ context.Context, peer string) bool {
	rt, mode := a.p2p().runtime()
	if rt == nil || mode != config.P2PModeShare {
		return false
	}
	p, err := p2pnet.DecodePeerID(peer)
	if err != nil {
		return false
	}
	return rt.worker(p)
}

// ServeP2PHTTP sets the handlers served over P2P in mode share: main is the
// application's HTTP handler, which only paired peers reach; only serves the
// routes that exist over P2P alone: POST /api/p2p/redeem, to any admitted
// peer, and GET /api/p2p/routes and POST /api/p2p/device/session, to paired
// peers. Until it is called,
// requests over P2P get 503.
func (a *Application) ServeP2PHTTP(main, only http.Handler) {
	a.p2p().handlers.Store(&p2pHandlers{main: main, only: only})
}

// CheckP2PMode checks a requested p2p_mode against this server: share needs
// authentication, and any mode but off needs a place to keep the identity.
func (a *Application) CheckP2PMode(requested string) error {
	mode, _, err := config.ResolveP2PMode(requested, a.AuthDB() != nil)
	if err != nil || mode == config.P2PModeOff {
		return err
	}
	cfg := a.ApplicationConfig()
	_, err = config.ResolveP2PIdentityPath(cfg.P2P.IdentityPath, cfg.DataPath)
	return err
}

func (n *p2pNet) restart() error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.stopLocked()
	if n.closed {
		return nil
	}
	rt, mode, err := n.startRuntime()
	n.mode = mode
	n.lastErr = ""
	if err != nil {
		n.lastErr = err.Error()
		n.mode = config.P2PModeOff
		xlog.Error("P2P did not start", "error", err)
		return err
	}
	n.rt = rt
	if rt != nil {
		xlog.Info("P2P started", "mode", string(mode), "peer", rt.host.ID().String())
		n.tunnels.start(rt)
	}
	return nil
}

func (n *p2pNet) stop() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.stopLocked()
	n.mode = config.P2PModeOff
	n.closed = true
}

func (n *p2pNet) stopLocked() {
	if n.rt == nil {
		return
	}
	n.tunnels.stop()
	n.rt.close()
	n.rt = nil
}

// runtime returns the running host, or nil.
func (n *p2pNet) runtime() (*p2pRuntime, config.P2PMode) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.rt, n.mode
}

// p2pHostConfig is the host configuration of a mode other than off. In mode
// share it also returns the allowlist of the host.
func p2pHostConfig(mode config.P2PMode, s config.P2PSettings, idPath string) (p2pnet.Config, *p2pnet.Allowlist, error) {
	boot, err := p2pnet.ParsePeerAddrs(s.BootstrapPeers)
	if err != nil {
		return p2pnet.Config{}, nil, fmt.Errorf("p2p_bootstrap_peers: %w", err)
	}
	var relayAddrs []string
	for _, r := range s.Relays {
		relayAddrs = append(relayAddrs, r.Addr)
	}
	static, err := p2pnet.ParsePeerAddrs(relayAddrs)
	if err != nil {
		return p2pnet.Config{}, nil, fmt.Errorf("p2p_relays: %w", err)
	}
	pc := p2pnet.Config{
		IdentityPath:   idPath,
		PublicDHT:      !s.DisablePublicDHT,
		BootstrapPeers: boot,
		StaticRelays:   static,
		MDNS:           !s.DisableMDNS,
		UserAgent:      "localai/" + internal.Version,
	}
	var gate *p2pnet.Allowlist
	if mode == config.P2PModeShare {
		gate = p2pnet.NewAllowlist()
		pc.Gate = gate
		pc.ListenAddrs = s.ListenAddrs
	}
	// Mode connect listens too, on the default addresses with ports that
	// the system picks: a hole punch needs a socket the peer can reach. It
	// still accepts nobody: the host's own empty allowlist refuses every
	// inbound connection after the security handshake. Nobody dials it, so
	// it holds no relay reservation and does not announce itself or connect
	// to the local network with mDNS.
	if mode == config.P2PModeConnect {
		pc.MDNS = false
		pc.NoReservations = true
	}
	return pc, gate, nil
}

// startRuntime starts a host for the configured mode. It returns nil for off.
func (n *p2pNet) startRuntime() (*p2pRuntime, config.P2PMode, error) {
	appCfg := n.app.ApplicationConfig()
	s := appCfg.P2P
	mode, _, err := config.ResolveP2PMode(s.Mode, n.app.AuthDB() != nil)
	if err != nil {
		return nil, config.P2PModeOff, err
	}
	if mode == config.P2PModeOff {
		return nil, mode, nil
	}
	idPath, err := config.ResolveP2PIdentityPath(s.IdentityPath, appCfg.DataPath)
	if err != nil {
		return nil, mode, err
	}
	pc, gate, err := p2pHostConfig(mode, s, idPath)
	if err != nil {
		return nil, mode, err
	}
	ctx, cancel := context.WithCancel(context.WithoutCancel(appContext(appCfg)))
	host, err := p2pnet.New(ctx, pc)
	if err != nil {
		cancel()
		return nil, mode, err
	}
	rt := &p2pRuntime{
		net: n, mode: mode, cfg: s, host: host, gate: gate, ctx: ctx, cancel: cancel,
		peers: map[p2pnet.ID]struct{}{}, touched: map[p2pnet.ID]time.Time{},
	}
	if mode == config.P2PModeShare {
		if err := rt.startShare(); err != nil {
			rt.close()
			return nil, mode, err
		}
	}
	return rt, mode, nil
}

func appContext(cfg *config.ApplicationConfig) context.Context {
	if cfg.Context != nil {
		return cfg.Context
	}
	return context.Background()
}

// startShare adds what mode share runs on top of the host.
func (rt *p2pRuntime) startShare() error {
	app := rt.net.app
	store, err := app.p2pPairStore()
	if err != nil {
		return err
	}
	rt.limit = newGlobalLimiter(p2ppair.NewLimiter(nil), redeemGlobalMax, redeemGlobalWindow)
	rt.signInLimit = p2ppair.NewLimiter(nil)
	pcfg := p2ppair.Config{
		ServerPeer: rt.host.ID(),
		Addrs:      rt.linkAddrs,
		LANAddrs:   rt.lanAddrs,
		Hints:      rt.relayHints,
		Relays:     rt,
		Limiter:    rt.limit,
		// The credentials of sign-in devices are hashed with the key of the
		// codes; see p2pPairStore.
		HMACSecret: app.ApplicationConfig().Auth.APIKeyHMACSecret,
	}
	if db := app.AuthDB(); db != nil {
		pcfg.Issuer = &clientKeyIssuer{app: app}
		pcfg.OwnerName = app.p2pOwnerName
		pcfg.DeviceLimit = app.p2pDeviceLimit
	}
	if rt.net.relayFactory != nil {
		rm, err := rt.net.relayFactory(relayManagerConfig{
			Host: rt.host, LoadRelays: app.p2pRelays, SaveRelays: app.saveP2PRelays, PairedPeers: rt.pairedPeerStrings,
			NodePeers: rt.splitNodePeers,
		})
		if err != nil {
			return fmt.Errorf("p2p: relay manager: %w", err)
		}
		rt.relays = rm
		pcfg.Registrar = rm.Registrar()
	}
	svc, err := p2ppair.NewService(store, pcfg)
	if err != nil {
		return err
	}
	rt.pair = svc
	rt.reload()
	if err := rt.startSplit(); err != nil {
		return err
	}

	ln, err := rt.host.Listen(p2pnet.ProtoHTTP, nil)
	if err != nil {
		return err
	}
	rt.srv = &http.Server{
		Handler:           rt.httpHandler(),
		ReadHeaderTimeout: 30 * time.Second,
		ConnContext: func(ctx context.Context, c net.Conn) context.Context {
			if p, ok := p2pnet.PeerOf(c); ok {
				return p2papi.WithPeer(ctx, p.String())
			}
			return ctx
		},
	}
	rt.wg.Go(func() {
		if err := rt.srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			xlog.Error("P2P HTTP server stopped", "error", err)
		}
	})
	if app.Distributed() != nil {
		if err := rt.listenTunnels(); err != nil {
			return err
		}
	}
	if rt.relays != nil {
		if err := rt.relays.Start(rt.ctx); err != nil {
			xlog.Warn("P2P relay manager did not start", "error", err)
		}
	}
	if nc := app.p2pBroadcaster(); nc != nil {
		sub, err := messaging.SubscribeJSON(nc, messaging.SubjectCacheInvalidateP2PPeers, func(messaging.CacheInvalidateEvent) { rt.reload() })
		if err != nil {
			xlog.Warn("P2P: no broadcast of pairing changes; the allowlist reloads every 30 seconds", "error", err)
		} else {
			rt.sub = sub
		}
	}
	rt.wg.Go(rt.reloadLoop)
	if err := rt.watchReachability(); err != nil {
		return err
	}
	return nil
}

func (rt *p2pRuntime) close() {
	rt.cancel()
	if rt.sub != nil {
		_ = rt.sub.Unsubscribe()
	}
	if rt.srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := rt.srv.Shutdown(ctx); err != nil {
			_ = rt.srv.Close()
		}
		cancel()
	}
	rt.stopRelayService("")
	rt.stopSplit()
	if rt.relays != nil {
		if err := rt.relays.Close(); err != nil {
			xlog.Warn("P2P relay manager close", "error", err)
		}
	}
	if err := rt.host.Close(); err != nil {
		xlog.Debug("P2P host close", "error", err)
	}
	rt.wg.Wait()
}

// linkAddrs are the direct addresses an invite carries: the public ones.
func (rt *p2pRuntime) linkAddrs() []p2pnet.Multiaddr {
	var out []p2pnet.Multiaddr
	for _, a := range rt.host.Addrs() {
		if p2pnet.IsPublicAddr(a) && !isCircuit(a) {
			out = append(out, a)
		}
	}
	return out
}

// maxRelayHints is how many public relays an invite names for hole punch
// coordination; the pairing service applies the same cap to its links.
const maxRelayHints = 2

// relayHints are the public relays on which the host holds a reservation,
// for the h key of an invite: at most maxRelayHints, one address each. The
// relays of the settings are left out, because an invite names them as
// carriers. With the public DHT off, AutoRelay finds no public relay, and
// there are none.
func (rt *p2pRuntime) relayHints() []p2pnet.AddrInfo {
	return rt.publicReservations(maxRelayHints)
}

// publicReservations lists the relays of the circuit addresses of the host
// that are not relays of the settings, at most limit of them (all when limit
// is zero or less).
func (rt *p2pRuntime) publicReservations(limit int) []p2pnet.AddrInfo {
	if rt.cfg.DisablePublicDHT {
		return nil
	}
	carriers := map[string]bool{}
	for _, r := range rt.relayList() {
		carriers[r.ID] = true
	}
	return pickRelayHints(rt.host.Addrs(), carriers, limit)
}

// pickRelayHints reads the circuit addresses of addrs and returns their
// relays in the order they first appear, without the relays whose peer ID
// is in carriers and at most limit of them (all when limit is zero or
// less). Each relay keeps one transport address, in this order of
// preference: public IPv4 TCP, public IPv4 QUIC or other UDP, public IPv6,
// then any other address. TCP comes first because some mobile and guest
// networks block UDP.
func pickRelayHints(addrs []p2pnet.Multiaddr, carriers map[string]bool, limit int) []p2pnet.AddrInfo {
	var out []p2pnet.AddrInfo
	rank := map[p2pnet.ID]int{}
	for _, a := range addrs {
		relay, _ := ma.SplitFunc(a, func(c ma.Component) bool { return c.Protocol().Code == p2pnet.ProtocolCircuit })
		if len(relay) == 0 || len(relay) == len(a) {
			continue
		}
		ai, err := p2pnet.ParsePeerAddr(relay.String())
		if err != nil || len(ai.Addrs) != 1 || carriers[ai.ID.String()] {
			continue
		}
		r := hintRank(ai.Addrs[0])
		i := slices.IndexFunc(out, func(h p2pnet.AddrInfo) bool { return h.ID == ai.ID })
		switch {
		case i < 0:
			out = append(out, *ai)
			rank[ai.ID] = r
		case r < rank[ai.ID]:
			out[i].Addrs = ai.Addrs
			rank[ai.ID] = r
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// hintRank orders the transport addresses of a relay hint: lower is better.
func hintRank(a p2pnet.Multiaddr) int {
	r := 4
	if len(a) > 0 {
		switch a[0].Protocol().Code {
		case ma.P_IP4, ma.P_DNS4, ma.P_DNS:
			r = 1
			if _, err := a.ValueForProtocol(ma.P_TCP); err == nil {
				r = 0
			}
		case ma.P_IP6, ma.P_DNS6:
			r = 2
		}
	}
	if !p2pnet.IsPublicAddr(a) {
		r += 10
	}
	return r
}

// lanAddrs are the addresses of the local networks of the host: private,
// not loopback or link-local, TCP before QUIC. They let a device on the same
// network redeem an invite with no relay and no multicast discovery.
func (rt *p2pRuntime) lanAddrs() []p2pnet.Multiaddr {
	isLAN := rt.net.isLANAddr
	if isLAN == nil {
		isLAN = p2pnet.IsLANAddr
	}
	var tcp, other []p2pnet.Multiaddr
	for _, a := range rt.host.Addrs() {
		if !isLAN(a) || isCircuit(a) {
			continue
		}
		if _, err := a.ValueForProtocol(p2pnet.ProtocolTCP); err == nil {
			tcp = append(tcp, a)
		} else {
			other = append(other, a)
		}
	}
	return append(tcp, other...)
}

func (rt *p2pRuntime) reloadLoop() {
	every := rt.net.reloadInterval
	if every <= 0 {
		every = p2pReloadInterval
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-rt.ctx.Done():
			return
		case <-t.C:
			rt.reload()
		}
	}
}

// reload loads the paired set into the gate, revoking peers that left it, and
// opens pairing while an invite is pending.
func (rt *p2pRuntime) reload() {
	ctx, cancel := context.WithTimeout(rt.ctx, 10*time.Second)
	defer cancel()
	devices, err := rt.pair.ListDevices(ctx)
	if err != nil {
		xlog.Warn("P2P: could not load the paired devices", "error", err)
		return
	}
	owners, err := rt.net.app.deviceOwners(ctx, devices)
	if err != nil {
		xlog.Warn("P2P: could not load the owners of the paired devices", "error", err)
		return
	}
	next := make(map[p2pnet.ID]struct{}, len(devices))
	workers := map[p2pnet.ID]struct{}{}
	reservers := map[p2pnet.ID]struct{}{}
	for _, d := range devices {
		if d.Status() != p2ppair.DeviceActive || !admitDevice(d, owners) {
			continue
		}
		p, err := p2pnet.DecodePeerID(d.PeerID)
		if err != nil {
			continue
		}
		next[p] = struct{}{}
		if d.Kind == p2ppair.KindWorker {
			workers[p] = struct{}{}
		}
		if reservesOnRelay(d, owners) {
			reservers[p] = struct{}{}
		}
	}
	rt.peersMu.Lock()
	var gone []p2pnet.ID
	for p := range rt.peers {
		if _, ok := next[p]; !ok {
			gone = append(gone, p)
		}
	}
	var added []p2pnet.ID
	for p := range next {
		rt.gate.Allow(p)
		if _, ok := rt.peers[p]; !ok {
			added = append(added, p)
		}
	}
	rt.peers = next
	rt.workers = workers
	rt.reservers = reservers
	rt.peersMu.Unlock()
	for _, p := range gone {
		rt.host.Revoke(p)
	}
	// A newly allowed peer: its pairing connection was identified before it
	// was allowed, and a peer known from an earlier run is dialled now.
	for _, p := range added {
		rt.host.Learn(p)
		rt.host.Redial(p)
	}
	rt.reloadNodeRelays(ctx, devices)
	invites, err := rt.pair.ListInvites(ctx)
	if err != nil {
		xlog.Warn("P2P: could not load the invites", "error", err)
		return
	}
	pending := 0
	now := time.Now()
	for _, inv := range invites {
		if inv.Status(now) == p2ppair.InvitePending {
			pending++
		}
	}
	rt.pending.Store(int32(pending))
	rt.gate.SetPairing(pending > 0)
	rt.refreshSplit()
}

func (rt *p2pRuntime) pairedPeerStrings() []string {
	rt.peersMu.Lock()
	defer rt.peersMu.Unlock()
	out := make([]string, 0, len(rt.peers))
	for p := range rt.peers {
		out = append(out, p.String())
	}
	return out
}

func (rt *p2pRuntime) paired(p p2pnet.ID) bool {
	rt.peersMu.Lock()
	defer rt.peersMu.Unlock()
	_, ok := rt.peers[p]
	return ok
}

// p2pTunnelLanes maps the protocols of the tunnel to its lanes.
var p2pTunnelLanes = map[p2pnet.Protocol]tunnel.Lane{
	p2pnet.ProtoTunnelInference: tunnel.LaneInference,
	p2pnet.ProtoTunnelBulk:      tunnel.LaneBulk,
}

// listenTunnels accepts the tunnel streams of the paired workers. A paired
// device of another kind may not open one: its stream is reset by the host.
func (rt *p2pRuntime) listenTunnels() error {
	for proto, lane := range p2pTunnelLanes {
		ln, err := rt.host.Listen(proto, rt.worker)
		if err != nil {
			return err
		}
		rt.wg.Go(func() {
			for {
				conn, err := ln.Accept()
				if err != nil {
					return
				}
				go rt.serveTunnel(conn, lane)
			}
		})
	}
	return nil
}

func (rt *p2pRuntime) serveTunnel(conn net.Conn, lane tunnel.Lane) {
	h := rt.net.tunnel.Load()
	if h == nil {
		_ = tunnel.WriteHelloReply(conn, tunnel.HelloReply{Status: http.StatusServiceUnavailable, Error: "the server is starting"})
		_ = conn.Close()
		return
	}
	(*h)(rt.ctx, conn, lane)
}

// worker reports whether p is a paired worker.
func (rt *p2pRuntime) worker(p p2pnet.ID) bool {
	rt.peersMu.Lock()
	defer rt.peersMu.Unlock()
	_, ok := rt.workers[p]
	return ok
}

// changed is called after this frontend paired or revoked a device: it reloads
// now and tells the other frontends.
func (rt *p2pRuntime) changed(op string) {
	rt.reload()
	if nc := rt.net.app.p2pBroadcaster(); nc != nil {
		if err := nc.Publish(messaging.SubjectCacheInvalidateP2PPeers, messaging.CacheInvalidateEvent{Op: op}); err != nil {
			xlog.Warn("P2P: could not broadcast a pairing change", "error", err)
		}
	}
}

// httpHandler serves the application over P2P. A peer that is not paired was
// admitted only because an invite is pending: it may call the redeem route and
// nothing else. The redeem, routes and device session routes exist only here.
func (rt *p2pRuntime) httpHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := rt.net.handlers.Load()
		ps, _ := p2papi.PeerFrom(r.Context())
		p, err := p2pnet.DecodePeerID(ps)
		if err != nil {
			http.Error(w, "unknown peer", http.StatusForbidden)
			return
		}
		if h == nil {
			http.Error(w, "the server is starting", http.StatusServiceUnavailable)
			return
		}
		if r.URL.Path == redeemPath {
			if r.Method != http.MethodPost {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			h.only.ServeHTTP(w, r)
			return
		}
		if !rt.paired(p) {
			http.Error(w, "this peer is not paired: redeem an invite first", http.StatusForbidden)
			return
		}
		rt.touch(p)
		if r.URL.Path == routesPath {
			if r.Method != http.MethodGet {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			h.only.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == deviceSessionPath {
			if r.Method != http.MethodPost {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			h.only.ServeHTTP(w, r)
			return
		}
		h.main.ServeHTTP(w, r.WithContext(auth.WithDeviceSessionCheck(r.Context(), rt.deviceSessionCheck(p))))
	})
}

// touch records that a paired device was seen, at most once a minute.
func (rt *p2pRuntime) touch(p p2pnet.ID) {
	now := time.Now()
	rt.touchMu.Lock()
	if now.Sub(rt.touched[p]) < time.Minute {
		rt.touchMu.Unlock()
		return
	}
	rt.touched[p] = now
	rt.touchMu.Unlock()
	path := string(rt.host.Path(p).Kind)
	rt.wg.Go(func() {
		ctx, cancel := context.WithTimeout(rt.ctx, 5*time.Second)
		defer cancel()
		if err := rt.pair.Touch(ctx, p, path); err != nil {
			xlog.Debug("P2P: could not record a device as seen", "error", err)
		}
	})
}

// watchReachability starts and stops the in-process relay as the host's
// reachability changes.
func (rt *p2pRuntime) watchReachability() error {
	sub, err := rt.host.SubscribeReachability()
	if err != nil {
		return fmt.Errorf("p2p: watch reachability: %w", err)
	}
	rt.updateRelayService(rt.host.Reachability())
	rt.wg.Go(func() {
		defer func() { _ = sub.Close() }()
		for {
			select {
			case <-rt.ctx.Done():
				return
			case e, ok := <-sub.Out():
				if !ok {
					return
				}
				rt.updateRelayService(p2pnet.ReachabilityOf(e))
			}
		}
	})
	return nil
}

// relayServiceWanted says whether the server should relay for its paired
// devices at this reachability, and why not.
func relayServiceWanted(s config.P2PSettings, reach p2pnet.Reachability) (bool, string) {
	switch {
	case s.NoRelayPaired || s.EffectiveRelayMode() == config.P2PRelayOff:
		return false, "off"
	case s.EffectiveRelayMode() == config.P2PRelayOn:
		return true, ""
	case reach != p2pnet.ReachabilityPublic:
		return false, "not_public"
	}
	return true, ""
}

func (rt *p2pRuntime) updateRelayService(reach p2pnet.Reachability) {
	want, reason := relayServiceWanted(rt.cfg, reach)
	if !want {
		rt.stopRelayService(reason)
		return
	}
	rt.relayMu.Lock()
	defer rt.relayMu.Unlock()
	if rt.inproc != nil || rt.ctx.Err() != nil {
		return
	}
	lim := p2prelay.DefaultLimits()
	lim.RateLimit = rt.cfg.RelayRateLimit
	// Only the paired peers that belong to the instance (see
	// reservesOnRelay) may hold a reservation or open a circuit. A sign-in
	// device, or a device of a user who is not an administrator, talks to
	// the server directly and has no use for either.
	c, err := p2prelay.NewInProcess(rt.host.Libp2p(), rt.paired, lim,
		p2prelay.WithReserveAdmit(rt.mayReserve), p2prelay.WithCircuitAdmit(rt.mayReserve))
	if err != nil {
		rt.reason = "error"
		xlog.Warn("P2P: could not relay for paired devices", "error", err)
		return
	}
	rt.inproc, rt.reason = c, ""
	xlog.Info("P2P: this server now relays for its paired devices", "peer", rt.host.ID().String())
}

func (rt *p2pRuntime) stopRelayService(reason string) {
	rt.relayMu.Lock()
	defer rt.relayMu.Unlock()
	rt.reason = reason
	if rt.inproc == nil {
		return
	}
	if err := rt.inproc.Close(); err != nil {
		xlog.Debug("P2P: stop relaying", "error", err)
	}
	rt.inproc = nil
	xlog.Info("P2P: this server no longer relays for its paired devices", "reason", reason)
}

func (rt *p2pRuntime) relayServiceInfo() p2papi.RelayServiceInfo {
	rt.relayMu.Lock()
	defer rt.relayMu.Unlock()
	return p2papi.RelayServiceInfo{Running: rt.inproc != nil, Reason: rt.reason}
}

// p2pBroadcaster is the fan-out between the frontends (NATS or PostgreSQL), or
// nil on a single server.
func (a *Application) p2pBroadcaster() messaging.Broadcaster {
	if a.distributed == nil || a.distributed.Broadcaster == nil {
		return nil
	}
	return a.distributed.Broadcaster
}

// p2pRelays reads the p2p_relays setting.
func (a *Application) p2pRelays() []config.P2PRelay {
	return slices.Clone(a.ApplicationConfig().P2P.Relays)
}

// saveP2PRelays stores the relays in the p2p_relays setting, in the config and
// in the settings file. The static relays of the host change at its next
// start; the relay routes restart it after a change.
func (a *Application) saveP2PRelays(relays []config.P2PRelay) error {
	if err := config.ValidateP2PRelays(relays); err != nil {
		return err
	}
	cfg := a.ApplicationConfig()
	cfg.P2P.Relays = slices.Clone(relays)
	if cfg.DynamicConfigsDir == "" {
		return nil
	}
	persisted, err := cfg.ReadPersistedSettings()
	if err != nil {
		return err
	}
	persisted.P2PRelays = &cfg.P2P.Relays
	return cfg.WritePersistedSettings(persisted)
}
