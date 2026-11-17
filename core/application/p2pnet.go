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

	"github.com/libp2p/go-libp2p/core/event"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/mudler/xlog"
	ma "github.com/multiformats/go-multiaddr"
	manet "github.com/multiformats/go-multiaddr/net"

	"github.com/mudler/LocalAI/core/config"
	p2papi "github.com/mudler/LocalAI/core/http/endpoints/p2p"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/p2pnet"
	"github.com/mudler/LocalAI/core/services/p2ppair"
	"github.com/mudler/LocalAI/core/services/p2prelay"
	"github.com/mudler/LocalAI/internal"
)

// redeemPath is the one route an unpaired peer may call, over P2P only.
const redeemPath = "/api/p2p/redeem"

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

	// relayFactory builds the relay manager; nil when this build has none
	// (relay operations then answer 503).
	relayFactory func(relayManagerConfig) (relayManager, error)
	// reloadInterval overrides p2pReloadInterval in tests.
	reloadInterval time.Duration
}

// p2pHandlers are the HTTP handlers served over P2P.
type p2pHandlers struct {
	main   http.Handler
	redeem http.Handler
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
	srv    *http.Server
	sub    messaging.Subscription
	limit  *globalLimiter

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// peers is the paired set as last loaded into the gate.
	peersMu sync.Mutex
	peers   map[peer.ID]struct{}
	// pending is the number of pending invites as last loaded.
	pending atomic.Int32

	relayMu sync.Mutex
	inproc  io.Closer
	reason  string

	touchMu sync.Mutex
	touched map[peer.ID]time.Time
}

func (a *Application) p2p() *p2pNet {
	a.p2pNetOnce.Do(func() { a.p2pNet = &p2pNet{app: a} })
	return a.p2pNet
}

// P2P returns the P2P layer as the HTTP handlers see it.
func (a *Application) P2P() p2papi.Service { return p2pService{n: a.p2p()} }

// StartP2PNet starts the P2P layer in the mode of the settings. Mode off
// starts nothing. An error leaves the layer off; the status reports it.
func (a *Application) StartP2PNet() error { return a.p2p().restart() }

// RestartP2PNet stops the P2P layer and starts it again with the current
// settings. The settings endpoint calls it when a P2P setting changes.
func (a *Application) RestartP2PNet() error { return a.p2p().restart() }

// StopP2PNet stops the P2P layer for good; the application calls it when it
// shuts down.
func (a *Application) StopP2PNet() { a.p2p().stop() }

// ServeP2PHTTP sets the handlers served over P2P in mode share: main is the
// application's HTTP handler, which only paired peers reach; redeem serves
// POST /api/p2p/redeem to any admitted peer. Until it is called, requests
// over P2P get 503.
func (a *Application) ServeP2PHTTP(main, redeem http.Handler) {
	a.p2p().handlers.Store(&p2pHandlers{main: main, redeem: redeem})
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
	n.rt.close()
	n.rt = nil
}

// runtime returns the running host, or nil.
func (n *p2pNet) runtime() (*p2pRuntime, config.P2PMode) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.rt, n.mode
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
	boot, err := parsePeerAddrs(s.BootstrapPeers)
	if err != nil {
		return nil, mode, fmt.Errorf("p2p_bootstrap_peers: %w", err)
	}
	var relayAddrs []string
	for _, r := range s.Relays {
		relayAddrs = append(relayAddrs, r.Addr)
	}
	static, err := parsePeerAddrs(relayAddrs)
	if err != nil {
		return nil, mode, fmt.Errorf("p2p_relays: %w", err)
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
	} else {
		// Connect accepts nobody: no listen address, and the host's own empty
		// allowlist refuses every inbound connection.
		pc.ListenAddrs = []string{}
	}
	ctx, cancel := context.WithCancel(context.WithoutCancel(appContext(appCfg)))
	host, err := p2pnet.New(ctx, pc)
	if err != nil {
		cancel()
		return nil, mode, err
	}
	rt := &p2pRuntime{
		net: n, mode: mode, cfg: s, host: host, gate: gate, ctx: ctx, cancel: cancel,
		peers: map[peer.ID]struct{}{}, touched: map[peer.ID]time.Time{},
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

func parsePeerAddrs(addrs []string) ([]peer.AddrInfo, error) {
	var out []peer.AddrInfo
	for _, a := range addrs {
		ai, err := config.ParseP2PPeerAddr(a)
		if err != nil {
			return nil, err
		}
		out = append(out, *ai)
	}
	return out, nil
}

// startShare adds what mode share runs on top of the host.
func (rt *p2pRuntime) startShare() error {
	app := rt.net.app
	store, err := app.p2pPairStore()
	if err != nil {
		return err
	}
	rt.limit = newGlobalLimiter(p2ppair.NewLimiter(nil), redeemGlobalMax, redeemGlobalWindow)
	pcfg := p2ppair.Config{
		ServerPeer: rt.host.ID(),
		Addrs:      rt.linkAddrs,
		Relays:     rt,
		Limiter:    rt.limit,
	}
	if db := app.AuthDB(); db != nil {
		pcfg.Issuer = &clientKeyIssuer{app: app}
	}
	if rt.net.relayFactory != nil {
		rm, err := rt.net.relayFactory(relayManagerConfig{
			Host: rt.host, Relays: rt.cfg.Relays, SaveRelays: app.saveP2PRelays, PairedPeers: rt.pairedPeerStrings,
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
func (rt *p2pRuntime) linkAddrs() []ma.Multiaddr {
	var out []ma.Multiaddr
	for _, a := range rt.host.Addrs() {
		if manet.IsPublicAddr(a) && !isCircuit(a) {
			out = append(out, a)
		}
	}
	return out
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
	paired, err := rt.pair.PairedPeers(ctx)
	if err != nil {
		xlog.Warn("P2P: could not load the paired devices", "error", err)
		return
	}
	next := make(map[peer.ID]struct{}, len(paired))
	for _, p := range paired {
		next[p] = struct{}{}
	}
	rt.peersMu.Lock()
	var gone []peer.ID
	for p := range rt.peers {
		if _, ok := next[p]; !ok {
			gone = append(gone, p)
		}
	}
	for p := range next {
		rt.gate.Allow(p)
	}
	rt.peers = next
	rt.peersMu.Unlock()
	for _, p := range gone {
		rt.host.Revoke(p)
	}
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

func (rt *p2pRuntime) paired(p peer.ID) bool {
	rt.peersMu.Lock()
	defer rt.peersMu.Unlock()
	_, ok := rt.peers[p]
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
// nothing else. The redeem route exists only here.
func (rt *p2pRuntime) httpHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := rt.net.handlers.Load()
		ps, _ := p2papi.PeerFrom(r.Context())
		p, err := peer.Decode(ps)
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
			h.redeem.ServeHTTP(w, r)
			return
		}
		if !rt.paired(p) {
			http.Error(w, "this peer is not paired: redeem an invite first", http.StatusForbidden)
			return
		}
		rt.touch(p)
		h.main.ServeHTTP(w, r)
	})
}

// touch records that a paired device was seen, at most once a minute.
func (rt *p2pRuntime) touch(p peer.ID) {
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
	sub, err := rt.host.Libp2p().EventBus().Subscribe(new(event.EvtLocalReachabilityChanged))
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
				rt.updateRelayService(e.(event.EvtLocalReachabilityChanged).Reachability)
			}
		}
	})
	return nil
}

// relayServiceWanted says whether the server should relay for its paired
// devices at this reachability, and why not.
func relayServiceWanted(s config.P2PSettings, reach network.Reachability) (bool, string) {
	switch {
	case s.NoRelayPaired || s.EffectiveRelayMode() == config.P2PRelayOff:
		return false, "off"
	case s.EffectiveRelayMode() == config.P2PRelayOn:
		return true, ""
	case reach != network.ReachabilityPublic:
		return false, "not_public"
	}
	return true, ""
}

func (rt *p2pRuntime) updateRelayService(reach network.Reachability) {
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
	c, err := p2prelay.NewInProcess(rt.host.Libp2p(), rt.paired, lim)
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

// p2pBroadcaster is the message bus of a cluster, or nil.
func (a *Application) p2pBroadcaster() messaging.Broadcaster {
	if a.distributed == nil || a.distributed.Nats == nil {
		return nil
	}
	return a.distributed.Nats
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
