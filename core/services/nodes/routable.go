package nodes

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/mudler/xlog"
)

// RouteKind is a way a frontend reaches a worker.
type RouteKind string

const (
	// RouteDirect is NATS for control and a direct dial of the worker address
	// for data. It needs a worker attached to NATS that advertises an address a
	// frontend can dial.
	RouteDirect RouteKind = "direct"
	// RouteTunnel is the door that the worker holds open to a frontend, over a
	// websocket or over P2P. The worker needs no address and never talks to NATS.
	RouteTunnel RouteKind = "tunnel"
)

// Route is one usable way to reach a node. Transport, Path and Limited describe a
// tunnel route as the worker reported it, and are zero for a direct route. They
// are kept for the policies that choose between equally fit nodes.
type Route struct {
	Kind      RouteKind
	Transport string
	Path      string
	Limited   bool
}

// RoutesOf returns the usable routes of a node, from what the worker reported and
// what is stored about it. Every worker attaches the way its operator chose,
// so the router asks the node, not the cluster. The list is empty when the node cannot be reached at all.
//
//   - direct: the worker is attached to NATS and has an address that a frontend can
//     dial. An agent worker needs no address: it serves only the agent RPC, on
//     the NATS subjects of its node. A node that has not reported its routes yet
//     counts as attached to NATS. A worker that joined over P2P never has it.
//   - tunnel: the worker is attached to the tunnel. Transport, path and limited
//     come from its report.
//
// A worker that holds both has both routes, direct first.
func RoutesOf(n *BackendNode) []Route {
	attached := splitRoutes(n.Attached)
	if n.Follow == "" {
		attached = []string{RouteNATS}
	}
	var routes []Route
	if slices.Contains(attached, RouteNATS) && (n.Address != "" || n.NodeType == NodeTypeAgent) && !isP2PNode(n) {
		routes = append(routes, Route{Kind: RouteDirect})
	}
	if slices.Contains(attached, RouteTunnelID) {
		t := parseTunnelRoute(n.TunnelTransport, n.TunnelPath, n.TunnelLimited)
		routes = append(routes, Route{Kind: RouteTunnel, Transport: t.Transport, Path: t.Path, Limited: t.Limited})
	}
	return routes
}

// isP2PNode says that the worker joined over P2P: it holds a peer pin or reports
// a P2P tunnel. Such a worker is reached through its tunnel only.
func isP2PNode(n *BackendNode) bool {
	return n.PeerID != "" || n.TunnelTransport == TunnelP2P
}

// unroutableTTL is how long the router keeps its answer about which nodes can be
// reached. A request does not read the whole node table. A worker that changed
// state is seen after at most this time.
const unroutableTTL = 2 * time.Second

type unroutableCache struct {
	mu      sync.Mutex
	at      time.Time
	healthy []string
	bad     map[string]struct{}
}

// narrowByRoutable removes from candidates the nodes that have no usable route
// (RoutesOf). A nil set means any healthy node, and stays nil while every node
// has a route. When no node is left it returns ErrNoAvailableNodes with the
// cause, so the request is answered with 503 and not with a failed dial. It
// changes no row: a node that cannot be reached for now is not a node that is gone.
func (r *SmartRouter) narrowByRoutable(ctx context.Context, modelID string, candidates []string) ([]string, error) {
	healthy, bad, ok := r.unroutableNodes(ctx)
	if !ok || len(bad) == 0 {
		return candidates, nil
	}
	base := candidates
	if base == nil {
		base = healthy
	}
	keep := make([]string, 0, len(base))
	skipped := 0
	for _, id := range base {
		if _, no := bad[id]; no {
			skipped++
			continue
		}
		keep = append(keep, id)
	}
	if skipped == 0 {
		return candidates, nil
	}
	if len(keep) == 0 {
		return nil, fmt.Errorf("no node that can serve %s has a usable route: %d worker(s) have no direct address on NATS and no live tunnel: %w",
			modelID, skipped, ErrNoAvailableNodes)
	}
	return keep, nil
}

func (r *SmartRouter) unroutableNodes(ctx context.Context) (healthy []string, bad map[string]struct{}, ok bool) {
	c := &r.unroutable
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.bad != nil && time.Since(c.at) < unroutableTTL {
		return c.healthy, c.bad, true
	}
	nodes, err := r.registry.FindNodesBySelector(ctx, nil)
	if err != nil {
		xlog.Warn("Could not read the nodes to find those that cannot be reached; routing to all of them", "error", err)
		return nil, nil, false
	}
	c.healthy = c.healthy[:0:0]
	c.bad = map[string]struct{}{}
	for i := range nodes {
		c.healthy = append(c.healthy, nodes[i].ID)
		if len(RoutesOf(&nodes[i])) == 0 {
			c.bad[nodes[i].ID] = struct{}{}
		}
	}
	c.at = time.Now()
	return c.healthy, c.bad, true
}
