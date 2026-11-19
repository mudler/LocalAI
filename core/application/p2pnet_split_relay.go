package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"maps"
	"slices"
	"time"

	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/config"
	p2papi "github.com/mudler/LocalAI/core/http/endpoints/p2p"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/p2pnet"
	"github.com/mudler/LocalAI/core/services/p2ppair"
)

// A backend node that registered with the registration token is not a paired
// device, so the gated relays of the server do not admit it. When a split
// worker is attached to such a node, the node runs a P2P host of its own; a
// worker that reaches it neither directly nor by hole punching needs a relay.
// The frontends vouch for the host of the node on the relays they administer,
// next to the paired devices, while it serves split workers.
//
// Distributed mode: who is vouched for is derived from the database every
// time the allowlist reloads (devices in the pairing store, nodes and their
// reports in the node registry), on every frontend. Each frontend pushes the
// result to its own list on each relay; the push is idempotent (a relay whose
// digest matches gets no write), so the frontends agree without a leader.
// The admission state each frontend shows is what its own relay manager
// last saw (state per instance).

// splitRelayCandidate is the relay admission of one backend node that serves
// split workers. Peer is set when the frontends vouch for the node; State and
// Reason say why not otherwise.
type splitRelayCandidate struct {
	Peer   string
	State  string
	Reason string
}

// splitRelayCandidates decides, for every backend node that has at least one
// active split worker attached, whether the frontends vouch for its split
// host on their relays. tokenHash is the SHA-256 of the registration token of
// the cluster, or empty when the cluster has none. A node that is gone from
// the registry is left out, and so is its peer.
func splitRelayCandidates(devs []p2ppair.Device, nodeList []nodes.BackendNode, reports map[string]nodes.StoredSplitReport, tokenHash string, now time.Time) map[string]splitRelayCandidate {
	attached := map[string]bool{}
	for _, d := range devs {
		if d.Kind == p2ppair.KindSplit && d.RevokedAt == nil && d.NodeID != "" {
			attached[d.NodeID] = true
		}
	}
	out := map[string]splitRelayCandidate{}
	for _, n := range nodeList {
		if !attached[n.ID] || !n.SplitServer {
			// A node that is not a split server serves no worker; its
			// workers are detached (see detachUndeclared).
			continue
		}
		rep, reported := reports[n.ID]
		var c splitRelayCandidate
		switch {
		case n.PeerID != "":
			c.State, c.Reason = p2papi.NodeRelayNotNeeded, "the node joined over P2P; the relays admit it as a paired device"
		case n.Status == nodes.StatusPending:
			c.State, c.Reason = p2papi.NodeRelayRefused, "the node is not approved yet"
		case tokenHash != "" && n.TokenHash != tokenHash:
			c.State, c.Reason = p2papi.NodeRelayRefused, "the node registered with a registration token that is no longer valid"
		case !reported || rep.PinnedPeer == "" || rep.Report.PeerID != rep.PinnedPeer:
			c.State, c.Reason = p2papi.NodeRelayPending, "the node has not reported its P2P host yet"
		case n.Status == nodes.StatusOffline || now.Sub(rep.UpdatedAt) > splitReportStale:
			c.State, c.Reason = p2papi.NodeRelayPending, "the node is offline"
		default:
			c.Peer = rep.PinnedPeer
		}
		out[n.ID] = c
	}
	return out
}

// clusterTokenHash is the SHA-256 of the registration token, or empty.
func (rt *p2pRuntime) clusterTokenHash() string {
	t := rt.net.app.ApplicationConfig().Distributed.RegistrationToken
	if t == "" {
		return ""
	}
	h := sha256.Sum256([]byte(t))
	return hex.EncodeToString(h[:])
}

// reloadNodeRelays recomputes the node peers from the node registry and
// brings the relays in line when they changed.
func (rt *p2pRuntime) reloadNodeRelays(ctx context.Context, devs []p2ppair.Device) {
	reg := rt.net.splitNodes()
	if reg == nil {
		return
	}
	nodeList, err := reg.List(ctx)
	if err != nil {
		xlog.Warn("P2P: could not load the nodes for the relays", "error", err)
		return
	}
	rt.detachUndeclared(ctx, devs, nodeList)
	reports, err := reg.SplitReports(ctx)
	if err != nil {
		xlog.Warn("P2P: could not load the split reports for the relays", "error", err)
		return
	}
	rt.setNodeRelays(splitRelayCandidates(devs, nodeList, reports, rt.clusterTokenHash(), time.Now()))
}

// setNodeRelays stores the candidates and asks the relay manager to sync when
// the set of vouched peers changed.
func (rt *p2pRuntime) setNodeRelays(next map[string]splitRelayCandidate) {
	rt.peersMu.Lock()
	before := peersOf(rt.nodeRelays)
	rt.nodeRelays = next
	after := peersOf(next)
	rt.peersMu.Unlock()
	if rt.relays != nil && !slices.Equal(before, after) {
		rt.relays.Resync()
	}
}

func peersOf(cs map[string]splitRelayCandidate) []string {
	var out []string
	for _, c := range cs {
		if c.Peer != "" {
			out = append(out, c.Peer)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// splitNodePeers lists the peers the relays admit for backend nodes.
func (rt *p2pRuntime) splitNodePeers() []string {
	rt.peersMu.Lock()
	defer rt.peersMu.Unlock()
	return peersOf(rt.nodeRelays)
}

func (rt *p2pRuntime) nodeRelay(nodeID string) (splitRelayCandidate, bool) {
	rt.peersMu.Lock()
	defer rt.peersMu.Unlock()
	c, ok := rt.nodeRelays[nodeID]
	return c, ok
}

// checkNodeRelay is called with the split report of a node: when the node
// has just become one the frontends vouch for, or stopped being one, it
// reloads now and tells the other frontends, instead of waiting for the next
// reload. It returns the candidate of the node.
func (rt *p2pRuntime) checkNodeRelay(ctx context.Context, nodeID string, devs []p2ppair.Device) splitRelayCandidate {
	reg := rt.net.splitNodes()
	if reg == nil {
		return splitRelayCandidate{}
	}
	n, err := reg.Get(ctx, nodeID)
	if err != nil {
		return splitRelayCandidate{}
	}
	reports := map[string]nodes.StoredSplitReport{}
	if rep, ok, err := reg.SplitReport(ctx, nodeID); err == nil && ok {
		reports[nodeID] = rep
	}
	now := splitRelayCandidates(devs, []nodes.BackendNode{*n}, reports, rt.clusterTokenHash(), time.Now())[nodeID]
	if was, _ := rt.nodeRelay(nodeID); was.Peer != now.Peer {
		rt.changed("split-node")
	}
	return now
}

// ownRelayAddrs are the addresses of the gated relays this server
// administers.
func (rt *p2pRuntime) ownRelayAddrs() []string {
	var out []string
	for _, r := range rt.relayList() {
		ai, err := p2pnet.ParsePeerAddr(r.Addr)
		if err == nil && rt.ownRelay(ai.ID) {
			out = append(out, r.Addr)
		}
	}
	return out
}

// admittingRelayAddrs are the addresses of the relays that admit peer.
func (rt *p2pRuntime) admittingRelayAddrs(peer string) []string {
	if rt.relays == nil {
		return nil
	}
	addrs := map[string]string{}
	for _, r := range rt.relayList() {
		addrs[r.ID] = r.Addr
	}
	var out []string
	for _, a := range rt.relays.NodeAdmission(peer) {
		if a.State == p2papi.NodeRelayAdmitted && addrs[a.RelayID] != "" {
			out = append(out, addrs[a.RelayID])
		}
	}
	slices.Sort(out)
	return out
}

// nodeRelayViews returns the relay admission of each node, by node ID.
func (rt *p2pRuntime) nodeRelayViews() map[string]*p2papi.SplitNodeRelay {
	rt.peersMu.Lock()
	cands := maps.Clone(rt.nodeRelays)
	rt.peersMu.Unlock()
	out := make(map[string]*p2papi.SplitNodeRelay, len(cands))
	for id, c := range cands {
		out[id] = rt.nodeRelayView(c)
	}
	return out
}

// nodeRelayView sums up the admission of a node on the relays: admitted when
// one relay admits it, refused when every relay that this server administers
// refused it, pending otherwise.
func (rt *p2pRuntime) nodeRelayView(c splitRelayCandidate) *p2papi.SplitNodeRelay {
	if c.Peer == "" {
		return &p2papi.SplitNodeRelay{State: c.State, Reason: c.Reason}
	}
	v := &p2papi.SplitNodeRelay{}
	if rt.relays != nil {
		for _, a := range rt.relays.NodeAdmission(c.Peer) {
			if a.State != "skipped" {
				v.Relays = append(v.Relays, a)
			}
		}
	}
	if len(v.Relays) == 0 {
		v.State, v.Reason = p2papi.NodeRelayNotNeeded, "this server administers no gated relay"
		return v
	}
	var pending, refused string
	for _, a := range v.Relays {
		switch a.State {
		case p2papi.NodeRelayAdmitted:
			v.State = p2papi.NodeRelayAdmitted
			return v
		case p2papi.NodeRelayRefused:
			if refused == "" {
				refused = a.Reason
			}
		default:
			if pending == "" {
				pending = a.Reason
			}
		}
	}
	if pending != "" {
		v.State, v.Reason = p2papi.NodeRelayPending, pending
		return v
	}
	v.State, v.Reason = p2papi.NodeRelayRefused, refused
	return v
}

// detachUndeclared attaches to no node the split workers whose node is not a
// split server any more: the admin cleared the declaration on another
// frontend, or the worker registered again without --split-server. Every
// frontend does it at its reload; the write is the same on each.
func (rt *p2pRuntime) detachUndeclared(ctx context.Context, devs []p2ppair.Device, nodeList []nodes.BackendNode) {
	undeclared := map[string]string{}
	for _, n := range nodeList {
		if !n.SplitServer {
			undeclared[n.ID] = n.Name
		}
	}
	detached := 0
	for _, d := range devs {
		name, ok := undeclared[d.NodeID]
		if d.Kind != p2ppair.KindSplit || d.RevokedAt != nil || d.NodeID == "" || !ok {
			continue
		}
		if err := rt.pair.DetachSplitNode(ctx, d.ID, undeclaredNote(name)); err != nil {
			xlog.Warn("P2P: could not detach a split worker from a node that is not a split server", "worker", d.Label, "error", err)
			continue
		}
		xlog.Warn("P2P: split worker detached: its node is not a split server any more", "worker", d.Label, "node", name)
		detached++
	}
	if detached > 0 {
		rt.refreshSplit()
	}
}

func undeclaredNote(node string) string {
	return node + " is no longer a split server; attach the worker to a split server"
}

// DetachSplitWorkers attaches the split workers of a node that is no longer a
// split server to no node, with a note, and tells the other frontends. It
// returns how many it detached.
func (a *Application) DetachSplitWorkers(ctx context.Context, nodeID string) (int, error) {
	rt, mode := a.p2p().runtime()
	if rt == nil || mode != config.P2PModeShare {
		// No pairing store in use: nothing is attached.
		return 0, nil
	}
	name := nodeID
	if reg := rt.net.splitNodes(); reg != nil {
		if n, err := reg.Get(ctx, nodeID); err == nil {
			name = n.Name
		}
	}
	devs, err := rt.pair.ListDevices(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, d := range devs {
		if d.Kind != p2ppair.KindSplit || d.RevokedAt != nil || d.NodeID != nodeID {
			continue
		}
		if err := rt.pair.DetachSplitNode(ctx, d.ID, undeclaredNote(name)); err != nil {
			return n, err
		}
		n++
	}
	rt.changed("split")
	return n, nil
}
