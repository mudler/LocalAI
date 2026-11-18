package application

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/config"
	p2papi "github.com/mudler/LocalAI/core/http/endpoints/p2p"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/p2pnet"
	"github.com/mudler/LocalAI/core/services/p2ppair"
	"github.com/mudler/LocalAI/core/services/p2psplit"
	"github.com/mudler/LocalAI/core/services/workerctl"
)

// splitPorts keeps the loopback port of each split worker for the life of the
// process: a model that llama.cpp loaded with an address keeps working when
// the P2P host restarts.
//
// Distributed mode: each frontend runs its own split manager on its own host
// (state per instance), for the workers attached to no node. A split worker
// keeps one control session, to the frontend it reaches; the devices are
// shared through the pairing store. A worker attached to a backend node is
// sent to that node, which runs the manager for it; the node reports what it
// sees to the node registry (table node_split_reports), which every frontend
// reads.
var splitPorts = p2psplit.NewPortBook()

// splitReportStale is the age past which the report of a node is not used:
// its workers read as offline, and no worker is sent to it.
const splitReportStale = time.Minute

// splitNodeRegistry is what the split workers read about the backend nodes.
// *nodes.NodeRegistry is one.
type splitNodeRegistry interface {
	Get(ctx context.Context, nodeID string) (*nodes.BackendNode, error)
	List(ctx context.Context) ([]nodes.BackendNode, error)
	SplitReport(ctx context.Context, nodeID string) (nodes.StoredSplitReport, bool, error)
	SplitReports(ctx context.Context) (map[string]nodes.StoredSplitReport, error)
}

// splitNodes returns the node registry of distributed mode, or nil.
func (n *p2pNet) splitNodes() splitNodeRegistry {
	if n.nodeRegistry != nil {
		return n.nodeRegistry
	}
	if d := n.app.Distributed(); d != nil && d.Registry != nil {
		return d.Registry
	}
	return nil
}

// startSplit runs the model-split manager of mode share.
func (rt *p2pRuntime) startSplit() error {
	cfg := rt.net.app.ApplicationConfig()
	m, err := p2psplit.NewManager(rt.ctx, p2psplit.Config{
		Host:      rt.host,
		Devices:   rt.splitDevices,
		Elsewhere: rt.splitElsewhere,
		OnLlama:   cfg.LlamaCPPTunnelCallback,
		OnMLX:     cfg.MLXTunnelCallback,
		OwnRelay:  rt.ownRelay,
		Ports:     splitPorts,
	})
	if err != nil {
		return fmt.Errorf("p2p: model-split workers: %w", err)
	}
	rt.split = m
	return nil
}

func (rt *p2pRuntime) stopSplit() {
	if rt.split == nil {
		return
	}
	if err := rt.split.Close(); err != nil {
		xlog.Debug("P2P split manager close", "error", err)
	}
}

func (rt *p2pRuntime) refreshSplit() {
	if rt.split != nil {
		rt.split.Refresh()
	}
}

// splitDevices lists the active devices of kind split that are attached to
// this server, not to a node.
func (rt *p2pRuntime) splitDevices(ctx context.Context) ([]p2psplit.Device, error) {
	devs, err := rt.pair.ListDevices(ctx)
	if err != nil {
		return nil, err
	}
	var out []p2psplit.Device
	for _, d := range devs {
		if d.Kind != p2ppair.KindSplit || d.RevokedAt != nil || d.NodeID != "" {
			continue
		}
		p, err := p2pnet.DecodePeerID(d.PeerID)
		if err != nil {
			continue
		}
		out = append(out, p2psplit.Device{
			ID: d.ID, Label: d.Label, Peer: p, Engine: splitEngine(d.Scope),
			AllowSlowPath: d.AllowSlowPath, CreatedAt: d.CreatedAt,
		})
	}
	return out, nil
}

// splitElsewhere answers the hello of a split worker that this server does not
// serve: it sends a worker attached to a node to that node, and asks it to
// wait while the node has not reported where its host is.
func (rt *p2pRuntime) splitElsewhere(ctx context.Context, p p2pnet.ID) (p2psplit.Move, bool) {
	devs, err := rt.pair.ListDevices(ctx)
	if err != nil {
		return p2psplit.Move{Wait: "the server could not read its paired devices"}, true
	}
	i := slices.IndexFunc(devs, func(d p2ppair.Device) bool {
		return d.Kind == p2ppair.KindSplit && d.RevokedAt == nil && d.PeerID == p.String()
	})
	if i < 0 {
		return p2psplit.Move{}, false
	}
	d := devs[i]
	if d.NodeID == "" {
		// Attached to this server a moment ago: the manager has not
		// reloaded its devices yet.
		return p2psplit.Move{Wait: "the server is reloading its model-split workers"}, true
	}
	reg := rt.net.splitNodes()
	if reg == nil {
		return p2psplit.Move{Wait: "the worker is attached to a node, and this server is not in a cluster"}, true
	}
	rep, ok, err := reg.SplitReport(ctx, d.NodeID)
	if err != nil || !ok || rep.Report.PeerID == "" || time.Since(rep.UpdatedAt) > splitReportStale {
		return p2psplit.Move{Wait: "the node the worker is attached to has not reported its P2P address yet"}, true
	}
	node, err := p2pnet.DecodePeerID(rep.Report.PeerID)
	if err != nil {
		return p2psplit.Move{Wait: "the node the worker is attached to reported an address that is not valid"}, true
	}
	return p2psplit.Move{Peer: node, Addrs: rep.Report.Addrs}, true
}

// SplitAssignment answers a backend node with the model-split workers
// attached to it, and the gated relays this server administers.
func (a *Application) SplitAssignment(ctx context.Context, nodeID string) (workerctl.SplitAssignment, error) {
	rt, mode := a.p2p().runtime()
	if rt == nil || mode != config.P2PModeShare {
		return workerctl.SplitAssignment{}, nodes.ErrSplitUnavailable
	}
	devs, err := rt.pair.ListDevices(ctx)
	if err != nil {
		return workerctl.SplitAssignment{}, err
	}
	out := workerctl.SplitAssignment{Workers: []workerctl.SplitAssigned{}}
	if reg := rt.net.splitNodes(); reg != nil {
		if n, err := reg.Get(ctx, nodeID); err == nil {
			out.SplitServer = n.SplitServer
		}
	}
	if !out.SplitServer {
		// Not a split server: the node runs no host and serves no worker.
		// The workers still attached to it are detached at the next reload.
		return out, nil
	}
	for _, d := range devs {
		if d.Kind != p2ppair.KindSplit || d.RevokedAt != nil || d.NodeID != nodeID {
			continue
		}
		out.Workers = append(out.Workers, workerctl.SplitAssigned{
			DeviceID: d.ID, Label: d.Label, PeerID: d.PeerID, Engine: string(splitEngine(d.Scope)),
			AllowSlowPath: d.AllowSlowPath, CreatedAt: d.CreatedAt,
		})
	}
	for _, addr := range rt.ownRelayAddrs() {
		if ai, err := p2pnet.ParsePeerAddr(addr); err == nil {
			out.OwnRelays = append(out.OwnRelays, ai.ID.String())
		}
	}
	// A node that registered with the token reserves on the relays that
	// admit it. Only those: a reservation that a relay refused keeps the
	// node off that relay for minutes (the dial backoff of libp2p).
	if c := rt.checkNodeRelay(ctx, nodeID, devs); c.Peer != "" {
		out.Relays = rt.admittingRelayAddrs(c.Peer)
	}
	return out, nil
}

// checkSplitNode checks that node is a backend node of the cluster; "" is
// this server.
func (rt *p2pRuntime) checkSplitNode(ctx context.Context, node string) error {
	if node == "" {
		return nil
	}
	reg := rt.net.splitNodes()
	if reg == nil {
		return fmt.Errorf("%w: node_id needs distributed mode; use \"\" for this server", p2papi.ErrInvalid)
	}
	n, err := reg.Get(ctx, node)
	if err != nil {
		return fmt.Errorf("%w: no backend node %q", p2papi.ErrInvalid, node)
	}
	if n.NodeType == nodes.NodeTypeAgent {
		return fmt.Errorf("%w: %s is an agent node, which runs no models", p2papi.ErrInvalid, n.Name)
	}
	if !n.SplitServer {
		return fmt.Errorf("%w: %s is not a split server; declare it first (Nodes page, PUT /api/nodes/%s/split-server, or start the worker with --split-server)",
			p2papi.ErrConflict, n.Name, n.ID)
	}
	return nil
}

// defaultSplitNode is the node a new split worker attaches to when its
// invite names none: the only online split server of the cluster. With none,
// or with several, it is "" and the administrator picks.
func (rt *p2pRuntime) defaultSplitNode(ctx context.Context) string {
	reg := rt.net.splitNodes()
	if reg == nil {
		return ""
	}
	ns, err := reg.List(ctx)
	if err != nil {
		return ""
	}
	var found []string
	for _, n := range ns {
		if n.SplitServer && n.NodeType != nodes.NodeTypeAgent && nodeOnline(n) {
			found = append(found, n.ID)
		}
	}
	if len(found) != 1 {
		return ""
	}
	return found[0]
}

// nodeOnline reports whether a node takes work now.
func nodeOnline(n nodes.BackendNode) bool {
	return n.Status == nodes.StatusHealthy || n.Status == nodes.StatusDraining
}

// splitEngine is the engine a split invite named; invites made before the
// engine was recorded default to llama.cpp.
func splitEngine(s p2ppair.Scope) p2psplit.Engine {
	if e := p2psplit.Engine(s.Labels[p2psplit.EngineLabel]); e.Valid() {
		return e
	}
	return p2psplit.EngineLlama
}

// splitInviteLabels checks the engine and node labels of a split invite and
// sets the default engine, llama.cpp.
func (rt *p2pRuntime) splitInviteLabels(ctx context.Context, labels map[string]string) (map[string]string, error) {
	out, err := splitInviteLabels(labels)
	if err != nil {
		return nil, err
	}
	if _, named := out[p2ppair.NodeLabel]; !named {
		if node := rt.defaultSplitNode(ctx); node != "" {
			out[p2ppair.NodeLabel] = node
		}
	}
	if err := rt.checkSplitNode(ctx, out[p2ppair.NodeLabel]); err != nil {
		return nil, err
	}
	return out, nil
}

// splitInviteLabels checks the engine label of a split invite and sets the
// default engine, llama.cpp.
func splitInviteLabels(labels map[string]string) (map[string]string, error) {
	out := make(map[string]string, len(labels)+1)
	for k, v := range labels {
		out[k] = v
	}
	e := p2psplit.Engine(out[p2psplit.EngineLabel])
	switch {
	case e == "":
		out[p2psplit.EngineLabel] = string(p2psplit.EngineLlama)
	case !e.Valid():
		return nil, fmt.Errorf("%w: labels.engine must be %q or %q", p2papi.ErrInvalid, p2psplit.EngineLlama, p2psplit.EngineMLX)
	}
	return out, nil
}

var _ p2papi.SplitService = p2pService{}

// P2PSplit returns the model-split workers as the HTTP handlers see them.
func (a *Application) P2PSplit() p2papi.SplitService { return p2pService{n: a.p2p()} }

func (s p2pService) ListSplitWorkers(ctx context.Context) (p2papi.SplitWorkerList, error) {
	rt, err := s.share()
	if err != nil {
		return p2papi.SplitWorkerList{}, err
	}
	devs, err := rt.pair.ListDevices(ctx)
	if err != nil {
		return p2papi.SplitWorkerList{}, err
	}
	labels, ids := rt.relayLabels(), rt.relayIDs()
	reg := rt.net.splitNodes()
	out := p2papi.SplitWorkerList{Workers: []p2papi.SplitWorkerView{}, Distributed: reg != nil}
	for _, w := range rt.split.Workers() {
		i := slices.IndexFunc(devs, func(d p2ppair.Device) bool { return d.ID == w.DeviceID })
		if i < 0 {
			continue
		}
		out.Workers = append(out.Workers, splitView(w, devs[i], labels, ids))
	}
	attached, err := rt.attachedSplitViews(ctx, reg, devs, labels, ids)
	if err != nil {
		return p2papi.SplitWorkerList{}, err
	}
	out.Workers = append(out.Workers, attached...)
	slices.SortStableFunc(out.Workers, func(a, b p2papi.SplitWorkerView) int { return a.CreatedAt.Compare(b.CreatedAt) })
	for _, v := range out.Workers {
		if v.State == string(p2psplit.StateOnline) {
			out.MemoryInUse += v.MemoryBytes
		}
	}
	return out, nil
}

// attachedSplitViews returns the workers attached to backend nodes, with the
// state their node last reported. A node that reported nothing for
// splitReportStale shows its workers offline.
func (rt *p2pRuntime) attachedSplitViews(ctx context.Context, reg splitNodeRegistry, devs []p2ppair.Device, labels, ids map[string]string) ([]p2papi.SplitWorkerView, error) {
	var attached []p2ppair.Device
	for _, d := range devs {
		if d.Kind == p2ppair.KindSplit && d.RevokedAt == nil && d.NodeID != "" {
			attached = append(attached, d)
		}
	}
	if len(attached) == 0 {
		return nil, nil
	}
	reports := map[string]nodes.StoredSplitReport{}
	names := map[string]string{}
	offline := map[string]bool{}
	relays := rt.nodeRelayViews()
	if reg != nil {
		var err error
		if reports, err = reg.SplitReports(ctx); err != nil {
			return nil, err
		}
		ns, err := reg.List(ctx)
		if err != nil {
			return nil, err
		}
		for _, n := range ns {
			names[n.ID] = n.Name
			offline[n.ID] = !nodeOnline(n)
		}
	}
	out := make([]p2papi.SplitWorkerView, 0, len(attached))
	for _, d := range attached {
		v := p2papi.SplitWorkerView{
			ID: d.ID, Label: d.Label, PeerID: d.PeerID, Engine: string(splitEngine(d.Scope)),
			State: string(p2psplit.StateOffline), AllowSlowPath: d.AllowSlowPath,
			CreatedAt: d.CreatedAt, LastSeenAt: d.LastSeenAt,
			Path: p2papi.PathView{Kind: string(p2pnet.PathNone)}, NodeID: d.NodeID, NodeName: names[d.NodeID],
			NodeRelay: relays[d.NodeID], NodeOffline: offline[d.NodeID],
		}
		if v.NodeRelay == nil {
			// Attached a moment ago: the next reload decides.
			v.NodeRelay = &p2papi.SplitNodeRelay{State: p2papi.NodeRelayPending, Reason: "waiting for the next reload of the server"}
		}
		rep, ok := reports[d.NodeID]
		if ok && time.Since(rep.UpdatedAt) > splitReportStale {
			v.NodeOffline = true
		}
		if ok && time.Since(rep.UpdatedAt) <= splitReportStale {
			if i := slices.IndexFunc(rep.Report.Workers, func(w workerctl.SplitWorkerReport) bool { return w.DeviceID == d.ID }); i >= 0 {
				reportedView(&v, rep.Report.Workers[i], labels, ids)
			}
		}
		out = append(out, v)
	}
	return out, nil
}

// reportedView fills v with what a node reported about the worker.
func reportedView(v *p2papi.SplitWorkerView, w workerctl.SplitWorkerReport, labels, ids map[string]string) {
	v.State, v.Error, v.MemoryBytes, v.Name, v.Version = w.State, w.Error, w.MemoryBytes, w.Name, w.Version
	v.Addr, v.Rank, v.ConnectedSince, v.OwnRelay = w.Addr, w.Rank, w.Since, w.OwnRelay
	if p2psplit.Engine(w.Engine).Valid() {
		v.Engine = w.Engine
	}
	if w.Path.Kind == "" || w.Path.Kind == string(p2pnet.PathNone) {
		return
	}
	v.Path = p2papi.PathView{
		Kind: w.Path.Kind, Limited: w.Path.Limited, LimitBytes: w.Path.LimitBytes,
		LimitSeconds: w.Path.LimitSecond, RTTMillis: w.Path.RTTMillis,
	}
	if w.Path.Relay != "" {
		v.Path.RelayLabel = labels[w.Path.Relay]
		if v.Path.RelayLabel == "" {
			v.Path.RelayLabel = w.Path.Relay
		}
		v.RelayID = ids[w.Path.Relay]
	}
}

func (s p2pService) SetSplitWorker(ctx context.Context, id string, req p2papi.SplitWorkerUpdate) (p2papi.SplitWorkerView, error) {
	rt, err := s.share()
	if err != nil {
		return p2papi.SplitWorkerView{}, err
	}
	dev, err := rt.splitDevice(ctx, id)
	if err != nil {
		return p2papi.SplitWorkerView{}, err
	}
	if req.NodeID != nil {
		if err := rt.checkSplitNode(ctx, *req.NodeID); err != nil {
			return p2papi.SplitWorkerView{}, err
		}
	}
	if req.AllowSlowPath != nil {
		if err := rt.pair.SetAllowSlowPath(ctx, id, *req.AllowSlowPath); err != nil {
			return p2papi.SplitWorkerView{}, pairErr(err)
		}
		dev.AllowSlowPath = *req.AllowSlowPath
		xlog.Info("P2P split worker slow path", "worker", dev.Label, "allow", dev.AllowSlowPath)
	}
	if req.NodeID != nil && *req.NodeID != dev.NodeID {
		if err := rt.pair.SetSplitNode(ctx, id, *req.NodeID); err != nil {
			return p2papi.SplitWorkerView{}, pairErr(err)
		}
		dev.NodeID = *req.NodeID
		xlog.Info("P2P split worker attached", "worker", dev.Label, "node", dev.NodeID)
	}
	// Every frontend reloads the devices; this one now. A worker that moved
	// to a node loses its session here and is sent there when it comes back;
	// its node learns the change at its next report.
	rt.changed("split")
	return rt.splitWorkerView(ctx, dev)
}

// splitWorkerView returns the view of one split device.
func (rt *p2pRuntime) splitWorkerView(ctx context.Context, dev p2ppair.Device) (p2papi.SplitWorkerView, error) {
	labels, ids := rt.relayLabels(), rt.relayIDs()
	if dev.NodeID != "" {
		vs, err := rt.attachedSplitViews(ctx, rt.net.splitNodes(), []p2ppair.Device{dev}, labels, ids)
		if err != nil {
			return p2papi.SplitWorkerView{}, err
		}
		return vs[0], nil
	}
	for _, w := range rt.split.Workers() {
		if w.DeviceID == dev.ID {
			w.AllowSlowPath = dev.AllowSlowPath
			return splitView(w, dev, labels, ids), nil
		}
	}
	// Moved back from a node a moment ago: the manager has not reloaded.
	return splitView(p2psplit.Worker{
		DeviceID: dev.ID, Label: dev.Label, Peer: dev.PeerID, Engine: splitEngine(dev.Scope),
		AllowSlowPath: dev.AllowSlowPath, State: p2psplit.StateOffline, Path: p2pnet.Path{Kind: p2pnet.PathNone},
	}, dev, labels, ids), nil
}

// splitDevice returns the active split device id.
func (rt *p2pRuntime) splitDevice(ctx context.Context, id string) (p2ppair.Device, error) {
	devs, err := rt.pair.ListDevices(ctx)
	if err != nil {
		return p2ppair.Device{}, err
	}
	i := slices.IndexFunc(devs, func(d p2ppair.Device) bool {
		return d.ID == id && d.Kind == p2ppair.KindSplit && d.RevokedAt == nil
	})
	if i < 0 {
		return p2ppair.Device{}, p2papi.ErrNotFound
	}
	return devs[i], nil
}

// ownRelay reports whether relay is a gated relay that this server
// administers: its limits can change, so a model-split worker may use it
// after "Use anyway" even when it limits each connection.
func (rt *p2pRuntime) ownRelay(relay p2pnet.ID) bool {
	for _, r := range rt.relayList() {
		ai, err := p2pnet.ParsePeerAddr(r.Addr)
		if err != nil || ai.ID != relay {
			continue
		}
		return r.Mode == "gated" && r.State != "not_admin" && r.State != "denied"
	}
	return false
}

// relayIDs maps relay peer IDs to the server's IDs of the relays it knows.
func (rt *p2pRuntime) relayIDs() map[string]string {
	ids := map[string]string{}
	for _, r := range rt.relayList() {
		if ai, err := p2pnet.ParsePeerAddr(r.Addr); err == nil {
			ids[ai.ID.String()] = r.ID
		}
	}
	return ids
}

// relayLabels maps relay peer IDs to the labels of the relays the server knows.
func (rt *p2pRuntime) relayLabels() map[string]string {
	labels := map[string]string{}
	for _, r := range rt.relayList() {
		if ai, err := p2pnet.ParsePeerAddr(r.Addr); err == nil {
			labels[ai.ID.String()] = r.Label
		}
	}
	return labels
}

func splitView(w p2psplit.Worker, d p2ppair.Device, labels, ids map[string]string) p2papi.SplitWorkerView {
	v := p2papi.SplitWorkerView{
		ID: w.DeviceID, Label: w.Label, PeerID: w.Peer, Engine: string(w.Engine), State: string(w.State),
		Error: w.Error, AllowSlowPath: w.AllowSlowPath, MemoryBytes: w.MemoryBytes, Name: w.Name,
		Version: w.Version, Addr: w.Addr, Rank: w.Rank, ConnectedSince: w.Since,
		CreatedAt: d.CreatedAt, LastSeenAt: d.LastSeenAt, Path: p2papi.PathView{Kind: string(p2pnet.PathNone)},
		NodeNote: d.NodeNote,
	}
	if w.Path.Kind != p2pnet.PathNone {
		v.Path = pathView(w.Path, labels)
	}
	if w.Path.Relay != "" {
		v.RelayID = ids[w.Path.Relay.String()]
		v.OwnRelay = w.OwnRelay
	}
	return v
}
