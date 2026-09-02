package nodes

import (
	"context"
	"time"

	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/xlog"
)

// maxNodeLivenessRetries bounds how many excluded nodes a single scheduling
// attempt discards before giving up. Each discarded node is marked unhealthy,
// so the bound only has to cover one burst of dead workers rather than the
// whole fleet.
const maxNodeLivenessRetries = 3

// NodePresenceReader answers what this deployment can say about one worker's
// tunnel, given the window a departure has to outlive before it counts.
//
// A narrow port rather than the whole registry, because absence is the only
// thing the scheduler has any business reading from it, and a wider dependency
// here would be one every scheduling spec had to build.
// (*cluster.Registry).Presence has exactly this shape.
type NodePresenceReader interface {
	Presence(ctx context.Context, nodeID string, grace time.Duration) (cluster.Presence, error)
}

// nodeMayTakeWork reports whether the scheduler may place work on a node.
//
// Only cluster.PresenceGone excludes, and it is a fact every replica reads
// identically from the database rather than one this replica inferred from a
// timeout. That is the whole change: absence used to be nats.ErrNoResponders,
// which is one frontend's observation that nobody answered it within a budget,
// and two replicas asking at the same moment could disagree.
//
// The other three values are not absence and none of them may exclude.
// PresenceReconnecting is a worker re-homing its tunnel between replicas, or
// one whose owning replica just died holding it: excluding it costs capacity,
// and the demotion that follows is what turns a rolling frontend restart into a
// fleet-wide eviction. PresenceUnknown is a worker that has never dialled or
// whose departure aged out of retention, and the registry cannot say which, so
// the scheduler places work and the install that follows reports its own
// failure. A query that FAILS is not an answer at all: excluding on an
// infrastructure failure would evict capacity for a reason that has nothing to
// do with the worker, and a database hiccup would take out the fleet.
//
// It is deliberately NOT named for a route. A route to a worker is
// ErrWorkerUnroutable, a different condition that nobody may act on; this reads
// the one fact in the deployment that can say a worker is gone.
//
// When no presence reader is configured there is nothing to consult and every
// node may take work, which preserves the behaviour of deployments that run no
// cluster registry.
func (r *SmartRouter) nodeMayTakeWork(ctx context.Context, node *BackendNode) bool {
	if r.presence == nil || node == nil {
		return true
	}
	p, err := r.presence.Presence(ctx, node.ID, r.reconnectGrace)
	if err != nil {
		xlog.Warn("Could not read node presence; scheduling as if the node were present",
			"node", node.Name, "nodeID", node.ID, "error", err)
		return true
	}
	return p != cluster.PresenceGone
}

// pickReachableNode calls selectNode until it yields a node nodeMayTakeWork
// does not exclude, and returns nil when it cannot find one.
//
// An excluded node is marked unhealthy before the next attempt. That both
// removes it from the next selection, which queries only healthy nodes, and
// tells every other scheduler in the cluster what this one just learned, so the
// discovery is not repeated one failed request at a time. The demotion is
// status-only (see NodeRegistry.MarkUnhealthy): it stops placement, it does not
// delete a row, so it stays inside what a routing fact licenses.
func (r *SmartRouter) pickReachableNode(ctx context.Context, selectNode func() *BackendNode) *BackendNode {
	for range maxNodeLivenessRetries {
		node := selectNode()
		if node == nil {
			return nil
		}
		if r.nodeMayTakeWork(ctx, node) {
			return node
		}
		xlog.Warn("Scheduled node has no tunnel and its departure outlived the reconnect grace, marking unhealthy and re-scheduling",
			"node", node.Name, "nodeID", node.ID, "grace", r.reconnectGrace)
		if err := r.registry.MarkUnhealthy(ctx, node.ID); err != nil {
			// Without the demotion the next selection would hand back the same
			// node, so stop rather than spin.
			xlog.Warn("Failed to mark departed node unhealthy",
				"node", node.Name, "nodeID", node.ID, "error", err)
			return nil
		}
	}
	return nil
}
