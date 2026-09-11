package nodes

import (
	"context"
	"errors"

	"github.com/mudler/xlog"
	"github.com/nats-io/nats.go"
)

// maxNodeLivenessRetries bounds how many unreachable nodes a single scheduling
// attempt discards before giving up. Each discarded node is marked unhealthy,
// so the bound only has to cover one burst of dead workers rather than the
// whole fleet.
const maxNodeLivenessRetries = 3

// nodeAnswersOnBus reports whether a node still has a live subscription.
//
// Only nats.ErrNoResponders means "absent". Any other outcome, a timeout or a
// transport hiccup, leaves the node eligible: wrongly excluding a node that is
// merely slow costs real capacity, while the install that follows already
// reports its own failure. When no command sender is configured there is no bus
// to consult and every node is treated as reachable, which preserves the
// behaviour of deployments that do not run one.
func (r *SmartRouter) nodeAnswersOnBus(node *BackendNode) bool {
	if r.unloader == nil || node == nil {
		return true
	}
	err := r.unloader.PingNode(node.ID)
	return !errors.Is(err, nats.ErrNoResponders)
}

// pickReachableNode calls selectNode until it yields a node that still answers
// on the bus, and returns nil when it cannot find one.
//
// A node that does not answer is marked unhealthy before the next attempt. That
// both removes it from the next selection, which queries only healthy nodes,
// and tells every other scheduler in the cluster what this one just learned, so
// the discovery is not repeated one failed request at a time.
func (r *SmartRouter) pickReachableNode(ctx context.Context, selectNode func() *BackendNode) *BackendNode {
	for range maxNodeLivenessRetries {
		node := selectNode()
		if node == nil {
			return nil
		}
		if r.nodeAnswersOnBus(node) {
			return node
		}
		xlog.Warn("Scheduled node is not answering on the bus, marking unhealthy and re-scheduling",
			"node", node.Name, "nodeID", node.ID)
		if err := r.registry.MarkUnhealthy(ctx, node.ID); err != nil {
			// Without the demotion the next selection would hand back the same
			// node, so stop rather than spin.
			xlog.Warn("Failed to mark unreachable node unhealthy",
				"node", node.Name, "nodeID", node.ID, "error", err)
			return nil
		}
	}
	return nil
}
