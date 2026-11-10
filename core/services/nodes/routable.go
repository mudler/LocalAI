package nodes

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/xlog"
)

// NodeRoutableOn says whether a frontend can reach the worker of n on the carrier
// c. A worker is reached on c when it is attached to c. A worker that is between
// carriers, and can follow c, is reached soon enough. A worker that cannot follow
// c, for example one that has no address and holds only the tunnel while the
// cluster is on NATS, is not: the recorded address of its backend is a loopback
// address of the worker, and a dial to it from a frontend is refused.
//
// A worker that reports no capabilities predates carrier switching: it is on NATS
// and cannot follow.
func NodeRoutableOn(n *BackendNode, c cluster.Carrier) bool {
	follow := splitCarriers(n.Follow)
	attached := splitCarriers(n.Attached)
	if len(follow) == 0 {
		attached = []cluster.Carrier{cluster.CarrierNATS}
	}
	if slices.Contains(attached, c) {
		return true
	}
	return slices.Contains(follow, c) && n.FollowError == ""
}

// unroutableTTL is how long the router keeps its answer about which nodes can be
// reached. A request does not read the whole node table. A worker that changed
// state is seen after at most this time.
const unroutableTTL = 2 * time.Second

type unroutableCache struct {
	mu      sync.Mutex
	at      time.Time
	carrier cluster.Carrier
	healthy []string
	bad     map[string]struct{}
}

// narrowByRoutable removes from candidates the nodes that cannot be reached on
// the active carrier. A nil set means any healthy node, and stays nil while every
// node can be reached. When no node is left it returns ErrNoAvailableNodes with
// the cause, so the request is answered with 503 and not with a failed dial. It
// changes no row: a node that cannot be reached for now is not a node that is gone.
func (r *SmartRouter) narrowByRoutable(ctx context.Context, modelID string, candidates []string) ([]string, error) {
	if r.activeCarrier == nil {
		return candidates, nil
	}
	active := r.activeCarrier()
	if active == "" {
		return candidates, nil
	}
	healthy, bad, ok := r.unroutableNodes(ctx, active)
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
		return nil, fmt.Errorf("no node that can serve %s can be reached on the %s carrier: %d worker(s) are attached to another carrier and cannot follow it: %w",
			modelID, active, skipped, ErrNoAvailableNodes)
	}
	return keep, nil
}

func (r *SmartRouter) unroutableNodes(ctx context.Context, active cluster.Carrier) (healthy []string, bad map[string]struct{}, ok bool) {
	c := &r.unroutable
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.bad != nil && c.carrier == active && time.Since(c.at) < unroutableTTL {
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
		if !NodeRoutableOn(&nodes[i], active) {
			c.bad[nodes[i].ID] = struct{}{}
		}
	}
	c.at, c.carrier = time.Now(), active
	return c.healthy, c.bad, true
}
