package worker

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/services/p2pnet"
	"github.com/mudler/LocalAI/core/services/p2psplit"
	"github.com/mudler/LocalAI/core/services/workerctl"
	"github.com/mudler/LocalAI/internal"
)

// A backend node lends its models to the model-split workers attached to it.
// A split worker pairs with a frontend; the administrator attaches it to this
// node, and the frontend sends the worker here. The node runs the split
// manager on a P2P host, forwards a loopback port to each worker and sets the
// environment that llama.cpp and MLX read when a model loads, as a standalone
// server does.
//
// Only a split server does this. The worker becomes one with its
// --split-server flag or by the admin's choice on the frontend; the node
// learns the declaration in the same answer, and starts or stops its host
// without a restart.
//
// The node learns its workers from the frontend (POST /api/node/{id}/split)
// at every heartbeat interval, and reports in the same call the endpoint of
// its host and the state of each worker. The list it holds is a copy of the
// database of the cluster; a node that restarts asks again. When the call
// fails the node keeps its last list.

// splitSyncTimeout bounds one exchange with the frontend.
const splitSyncTimeout = 15 * time.Second

// splitConfig configures a splitNode.
type splitConfig struct {
	// Sync sends the report and returns the workers attached to the node.
	Sync func(ctx context.Context, rep workerctl.SplitReport) (*workerctl.SplitAssignment, error)
	// Interval is the time between two exchanges.
	Interval time.Duration
	// Join is the P2P side of a node that joined over P2P; its host serves
	// the workers. Nil for a node that registered with the token.
	Join *p2pJoin
	// NewHost starts the host of a node that registered with the token, the
	// first time a worker is attached to it, with a reservation on relays:
	// the gated relays of the frontend that admit this node.
	NewHost func(ctx context.Context, gate *p2pnet.Allowlist, relays []p2pnet.AddrInfo) (*p2pnet.Host, error)
	// OnLlama and OnMLX receive the addresses of the workers in use.
	OnLlama func(addrs []string)
	OnMLX   func(hostfile []string)
}

// splitNode runs the model-split workers attached to this node.
type splitNode struct {
	cfg   splitConfig
	ports *p2psplit.PortBook

	// assigned is the last answer of the frontend.
	assigned atomic.Pointer[workerctl.SplitAssignment]

	mu      sync.Mutex
	host    *p2pnet.Host
	gate    *p2pnet.Allowlist
	ownHost bool
	// relays are the relays the own host keeps a reservation on, as the
	// frontend named them.
	relays []string
	// asked is the sorted list of relays the frontend named when the own host
	// started. It is what a later answer is compared with: relays has only the
	// addresses that parsed, so it differs from the answer when one is bad, and
	// a comparison with it would restart the host at every heartbeat.
	asked []string
	mgr   *p2psplit.Manager
	// allowed are the worker peers this node admits.
	allowed map[p2pnet.ID]struct{}
}

func newSplitNode(cfg splitConfig) *splitNode {
	s := &splitNode{cfg: cfg, ports: p2psplit.NewPortBook(), allowed: map[p2pnet.ID]struct{}{}}
	s.assigned.Store(&workerctl.SplitAssignment{})
	return s
}

// run exchanges with the frontend at once and then at every interval, until
// ctx ends. It closes the manager and a host of its own when it returns.
func (s *splitNode) run(ctx context.Context) {
	defer s.close()
	t := time.NewTicker(s.cfg.Interval)
	defer t.Stop()
	for {
		s.sync(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// sync does one exchange with the frontend and applies its answer.
func (s *splitNode) sync(ctx context.Context) {
	cctx, cancel := context.WithTimeout(ctx, splitSyncTimeout)
	a, err := s.cfg.Sync(cctx, s.report())
	cancel()
	if err != nil {
		if ctx.Err() == nil {
			xlog.Debug("Could not exchange the model-split workers with the frontend; keeping the last list", "error", err)
		}
		return
	}
	if err := s.apply(ctx, *a); err != nil {
		xlog.Warn("Could not serve the model-split workers attached to this node", "error", err)
	}
}

// report is the endpoint of the host and the state of each worker.
func (s *splitNode) report() workerctl.SplitReport {
	s.mu.Lock()
	h, m, relays := s.host, s.mgr, s.relays
	s.mu.Unlock()
	var rep workerctl.SplitReport
	if h == nil {
		return rep
	}
	rep.PeerID = h.ID().String()
	for _, a := range h.Addrs() {
		rep.Addrs = append(rep.Addrs, a.String())
	}
	// The addresses through the relays: a split worker dials the node with
	// the addresses of this report only.
	for _, r := range relays {
		a := r + "/p2p-circuit/p2p/" + rep.PeerID
		if !slices.Contains(rep.Addrs, a) {
			rep.Addrs = append(rep.Addrs, a)
		}
	}
	if m == nil {
		return rep
	}
	for _, w := range m.Workers() {
		rep.Workers = append(rep.Workers, workerReport(w))
	}
	return rep
}

func workerReport(w p2psplit.Worker) workerctl.SplitWorkerReport {
	r := workerctl.SplitWorkerReport{
		DeviceID: w.DeviceID, State: string(w.State), Error: w.Error, OwnRelay: w.OwnRelay,
		MemoryBytes: w.MemoryBytes, Name: w.Name, Version: w.Version, Engine: string(w.Engine),
		Addr: w.Addr, Rank: w.Rank, Since: w.Since,
		Path: workerctl.SplitPath{
			Kind: string(w.Path.Kind), Limited: w.Path.Limited, LimitBytes: w.Path.LimitBytes,
			LimitSecond: int64(w.Path.LimitDuration / time.Second), RTTMillis: w.Path.RTT.Milliseconds(),
		},
	}
	if w.Path.Relay != "" {
		r.Path.Relay = w.Path.Relay.String()
	}
	return r
}

// apply makes the node serve the workers of a. A node that is not a split
// server runs nothing: it stops the manager and closes a host of its own.
// A split server starts its host and the manager at once, admits the peers
// of new workers and revokes the peers of the workers that left.
func (s *splitNode) apply(ctx context.Context, a workerctl.SplitAssignment) error {
	if !a.SplitServer {
		a.Workers = nil
	}
	s.assigned.Store(&a)
	want := map[p2pnet.ID]struct{}{}
	for _, w := range a.Workers {
		if p, err := p2pnet.DecodePeerID(w.PeerID); err == nil {
			want[p] = struct{}{}
		}
	}

	s.mu.Lock()
	if !a.SplitServer {
		m, h, own, allowed := s.mgr, s.host, s.ownHost, s.allowed
		s.mgr, s.host, s.gate, s.ownHost, s.relays, s.asked = nil, nil, nil, false, nil, nil
		s.allowed = map[p2pnet.ID]struct{}{}
		s.mu.Unlock()
		if m != nil {
			_ = m.Close()
			xlog.Info("This node is no longer a split server: it stopped serving model-split workers")
		}
		switch {
		case h != nil && own:
			_ = h.Close()
		case h != nil:
			// The host of the P2P join stays; it stops admitting the workers.
			for p := range allowed {
				h.Revoke(p)
			}
		}
		return nil
	}
	if s.ownHost && len(a.Relays) == 0 {
		// The relays no longer admit the node: forget them, so that a later
		// admission starts the host again instead of waiting out the dial
		// backoff of a refused reservation.
		s.relays, s.asked = nil, nil
	}
	if s.ownHost && s.host != nil && len(a.Relays) > 0 && !slices.Equal(s.asked, sortedCopy(a.Relays)) {
		// New relays: the frontend names a relay only once it admits the
		// node, and a host reserves on the relays it started with, so start
		// it again. The identity stays, and the workers come back at their
		// next session.
		xlog.Info("Starting the P2P host for model-split workers again with new relays", "relays", len(a.Relays))
		oldM, oldH := s.mgr, s.host
		s.host, s.gate, s.mgr = nil, nil, nil
		s.allowed = map[p2pnet.ID]struct{}{}
		// Closing waits for connections to end, so it runs without the lock.
		// Only this goroutine changes the fields, so the gap is safe.
		s.mu.Unlock()
		if oldM != nil {
			_ = oldM.Close()
		}
		_ = oldH.Close()
		s.mu.Lock()
	}
	if s.host == nil {
		// Starting a host dials and listens, so it runs without the lock.
		s.mu.Unlock()
		st, err := s.startHost(ctx, a.Relays)
		s.mu.Lock()
		if err != nil {
			s.mu.Unlock()
			return err
		}
		s.host, s.gate, s.ownHost, s.relays, s.asked = st.host, st.gate, st.own, st.kept, st.asked
	}
	for p := range want {
		if _, ok := s.allowed[p]; !ok {
			s.gate.Allow(p)
			s.allowed[p] = struct{}{}
		}
	}
	var gone []p2pnet.ID
	for p := range s.allowed {
		if _, ok := want[p]; !ok {
			gone = append(gone, p)
			delete(s.allowed, p)
		}
	}
	h, m := s.host, s.mgr
	s.mu.Unlock()

	if m == nil {
		nm, err := p2psplit.NewManager(ctx, p2psplit.Config{
			Host: h, Devices: s.devices, OwnRelay: s.ownRelay, Ports: s.ports,
			OnLlama: s.cfg.OnLlama, OnMLX: s.cfg.OnMLX,
			Elsewhere: func(context.Context, p2pnet.ID) (p2psplit.Move, bool) {
				// The frontend sent a worker that this node has not
				// heard of yet: it asks the frontend again later.
				return p2psplit.Move{Wait: "the node does not have this worker yet"}, true
			},
		})
		if err != nil {
			return err
		}
		s.mu.Lock()
		s.mgr = nm
		s.mu.Unlock()
		xlog.Info("This node is a split server: serving the model-split workers attached to it", "peer", h.ID().String(), "workers", len(want))
	} else {
		m.Refresh()
	}
	for _, p := range gone {
		h.Revoke(p)
	}
	return nil
}

// startedHost is a host that startHost made or took.
type startedHost struct {
	host *p2pnet.Host
	gate *p2pnet.Allowlist
	// own tells that the node started the host and closes it.
	own bool
	// kept are the relays that parsed, asked is the sorted list as named.
	kept, asked []string
}

// startHost takes the host of a node that joined over P2P, or starts one of
// its own. It does not touch the fields of s.
func (s *splitNode) startHost(ctx context.Context, relays []string) (startedHost, error) {
	if s.cfg.Join != nil {
		return startedHost{host: s.cfg.Join.host, gate: s.cfg.Join.gate}, nil
	}
	if s.cfg.NewHost == nil {
		return startedHost{}, errors.New("this node has no P2P host for model-split workers")
	}
	var static []p2pnet.AddrInfo
	var kept []string
	for _, r := range relays {
		ai, err := p2pnet.ParsePeerAddr(r)
		if err != nil {
			xlog.Warn("Ignoring a relay of the frontend with a bad address", "relay", r, "error", err)
			continue
		}
		static = append(static, *ai)
		kept = append(kept, r)
	}
	gate := p2pnet.NewAllowlist()
	h, err := s.cfg.NewHost(ctx, gate, static)
	if err != nil {
		return startedHost{}, err
	}
	xlog.Info("Started the P2P host for model-split workers", "peer", h.ID().String(), "relays", len(static))
	return startedHost{host: h, gate: gate, own: true, kept: kept, asked: sortedCopy(relays)}, nil
}

// sortedCopy returns a sorted copy of l, so that two lists with the same
// relays in another order compare equal.
func sortedCopy(l []string) []string {
	c := slices.Clone(l)
	slices.Sort(c)
	return c
}

// devices lists the attached workers for the manager.
func (s *splitNode) devices(context.Context) ([]p2psplit.Device, error) {
	a := s.assigned.Load()
	out := make([]p2psplit.Device, 0, len(a.Workers))
	for _, w := range a.Workers {
		p, err := p2pnet.DecodePeerID(w.PeerID)
		if err != nil {
			continue
		}
		e := p2psplit.Engine(w.Engine)
		if !e.Valid() {
			e = p2psplit.EngineLlama
		}
		out = append(out, p2psplit.Device{
			ID: w.DeviceID, Label: w.Label, Peer: p, Engine: e,
			AllowSlowPath: w.AllowSlowPath, CreatedAt: w.CreatedAt,
		})
	}
	return out, nil
}

// ownRelay reports whether a relay is one the frontend administers.
func (s *splitNode) ownRelay(relay p2pnet.ID) bool {
	return slices.Contains(s.assigned.Load().OwnRelays, relay.String())
}

// close stops the manager and a host of the node's own. The host of a node
// that joined over P2P belongs to the join.
func (s *splitNode) close() {
	s.mu.Lock()
	m, h, own := s.mgr, s.host, s.ownHost
	s.mgr, s.host = nil, nil
	s.mu.Unlock()
	if m != nil {
		_ = m.Close()
	}
	if own && h != nil {
		_ = h.Close()
	}
}

// splitHost starts the P2P host of a node that registered with the token.
func (c *Config) splitHost(ctx context.Context, gate *p2pnet.Allowlist, relays []p2pnet.AddrInfo) (*p2pnet.Host, error) {
	boot, err := p2pnet.ParsePeerAddrs(c.P2PBootstrap)
	if err != nil {
		return nil, err
	}
	return p2pnet.New(ctx, p2pnet.Config{
		IdentityPath:   c.identityPath(),
		ListenAddrs:    c.P2PListen,
		PublicDHT:      true,
		BootstrapPeers: boot,
		StaticRelays:   relays,
		MDNS:           true,
		Gate:           gate,
		UserAgent:      "localai-worker/" + internal.Version,
	})
}
