package nodes

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/xlog"
	"gorm.io/gorm"
)

// ErrModelSwapDisabled means every candidate node is full (its model limit
// or its memory) and model swap is off on all of them, so nothing may be
// unloaded to make room. It wraps ErrNoAvailableNodes: the HTTP layer answers
// 503 with Retry-After, and a model that goes idle and is unloaded by the
// idle sweep makes room.
var ErrModelSwapDisabled = errors.New("model swap is off and every candidate node is full")

func swapDisabledError(modelID, why string) error {
	return fmt.Errorf("%w for %s (%s): %w", ErrModelSwapDisabled, modelID, why, ErrNoAvailableNodes)
}

// policySource is what the router reads the memory policy from. The node
// registry implements it; a router built on another ModelRouter (tests)
// falls back to the default policy.
type policySource interface {
	MemoryPolicy() MemoryPolicy
	NodePolicy(n *BackendNode) NodePolicy
}

func (r *SmartRouter) clusterPolicy() MemoryPolicy {
	if src, ok := r.registry.(policySource); ok {
		return src.MemoryPolicy()
	}
	return DefaultMemoryPolicy()
}

func (r *SmartRouter) nodePolicy(n *BackendNode) NodePolicy {
	if src, ok := r.registry.(policySource); ok {
		return src.NodePolicy(n)
	}
	return DefaultMemoryPolicy().For(n)
}

// evictionOrder is the ORDER BY of an eviction pick: the largest replica
// first with size-aware eviction, the least recently used otherwise (and as
// the tie-break).
func (r *SmartRouter) evictionOrder() string {
	if r.clusterPolicy().SizeAwareEviction {
		return "node_models.size_bytes DESC, node_models.last_used ASC"
	}
	return "node_models.last_used ASC"
}

// forceEviction reports whether eviction may take a replica that is serving
// requests (force_eviction_when_busy).
func (r *SmartRouter) forceEviction() bool {
	return r.clusterPolicy().ForceEvictionWhenBusy
}

// maxPlacementEvictionWait bounds how long choosing a node waits for a busy
// replica to become evictable when the caller has no deadline (the
// reconciler, or a request without one). It is the wait of a router with
// no cluster settings (5 x 500ms). The LRU retry settings can ask for much
// more (30 x 1s by default), and a placement that waits that long holds up
// the reconciler's whole pass.
const maxPlacementEvictionWait = 2500 * time.Millisecond

// evictionRetries is how often, and how far apart, an eviction waits for a
// busy replica to become idle: the LRU retry settings, capped so the wait
// ends before the request's own deadline.
func (r *SmartRouter) evictionRetries(ctx context.Context) (int, time.Duration) {
	src, ok := r.registry.(interface{ HasMemoryPolicy() bool })
	if !ok || !src.HasMemoryPolicy() {
		// No cluster settings attached: the short wait of a router alone.
		return 5, 500 * time.Millisecond
	}
	p := r.clusterPolicy()
	retries, interval := p.LRUMaxRetries, p.LRURetryInterval
	if retries < 1 {
		retries = 1
	}
	if interval <= 0 {
		interval = 500 * time.Millisecond
	}
	if deadline, ok := ctx.Deadline(); ok {
		budget := time.Until(deadline)
		if fit := int(budget / interval); fit < retries {
			retries = max(fit, 1)
		}
	}
	return retries, interval
}

// placementRetries is evictionRetries for the choice of a node: with no
// deadline on ctx the wait is bounded by maxPlacementEvictionWait. The
// eviction that runs after the claim (makeRoom) keeps the full LRU wait.
func (r *SmartRouter) placementRetries(ctx context.Context) (int, time.Duration) {
	retries, interval := r.evictionRetries(ctx)
	if _, ok := ctx.Deadline(); !ok {
		if fit := int(maxPlacementEvictionWait / interval); fit < retries {
			retries = max(fit, 1)
		}
	}
	return retries, interval
}

// swapNodes keeps the candidates on which model swap is on. A nil
// candidates means every healthy backend node. It reports whether any
// candidate had swap off.
func (r *SmartRouter) swapNodes(ctx context.Context, candidates []string) ([]string, bool, error) {
	nodes, err := r.candidateNodes(ctx, candidates)
	if err != nil {
		return nil, false, err
	}
	out := []string{}
	off := false
	for i := range nodes {
		if r.nodePolicy(&nodes[i]).ModelSwap {
			out = append(out, nodes[i].ID)
		} else {
			off = true
		}
	}
	return out, off, nil
}

// candidateNodes reads the healthy backend nodes among candidates (every
// one when candidates is nil).
func (r *SmartRouter) candidateNodes(ctx context.Context, candidates []string) ([]BackendNode, error) {
	all, err := r.registry.FindNodesBySelector(ctx, nil)
	if err != nil {
		return nil, err
	}
	out := make([]BackendNode, 0, len(all))
	for _, n := range all {
		if n.NodeType != "" && n.NodeType != NodeTypeBackend {
			continue
		}
		if candidates != nil && !slices.Contains(candidates, n.ID) {
			continue
		}
		out = append(out, n)
	}
	return out, nil
}

// modelCounter counts the model slots taken on a node (see
// NodeRegistry.CountNodeReplicas). The node registry implements it.
type modelCounter interface {
	CountNodeReplicas(ctx context.Context, nodeID, forModel string) (int, error)
}

// narrowByModelLimit applies the per-node model limit (max_active_backends)
// to the candidates. Nodes under their limit stay. When every candidate is at
// its limit, the nodes with model swap on stay: makeRoom evicts there once
// the load is claimed (makeRoomForLimit). With swap off everywhere the
// placement fails with ErrModelSwapDisabled.
func (r *SmartRouter) narrowByModelLimit(ctx context.Context, modelID string, candidates []string) ([]string, error) {
	counter, ok := r.registry.(modelCounter)
	if !ok {
		return candidates, nil
	}
	nodes, err := r.candidateNodes(ctx, candidates)
	if err != nil {
		return nil, err
	}
	var under, fullSwap []string
	limited := false
	for i := range nodes {
		p := r.nodePolicy(&nodes[i])
		if p.MaxActiveBackends <= 0 {
			under = append(under, nodes[i].ID)
			continue
		}
		limited = true
		n, err := counter.CountNodeReplicas(ctx, nodes[i].ID, modelID)
		if err != nil {
			return nil, err
		}
		switch {
		case n < p.MaxActiveBackends:
			under = append(under, nodes[i].ID)
		case p.ModelSwap:
			fullSwap = append(fullSwap, nodes[i].ID)
		}
	}
	switch {
	case !limited:
		return candidates, nil
	case len(under) > 0:
		return under, nil
	case len(fullSwap) > 0:
		// Place on the node of the least recently used idle replica; the
		// eviction itself runs after the claim (makeRoomForLimit).
		_, node, err := r.planLRUEviction(ctx, fullSwap)
		if err != nil {
			return nil, fmt.Errorf("every node is at its model limit: %w", errors.Join(err, ErrNoAvailableNodes))
		}
		return []string{node.ID}, nil
	}
	return nil, swapDisabledError(modelID, "every node is at its model limit")
}

// makeRoomForLimit keeps the claimed node within its model limit: it evicts
// the least recently used idle replicas there until the new replica fits.
// makeRoom runs it after the claim, so a lost claim evicts nothing, and
// after the other evictions, so it counts what is really left on the node.
func (r *SmartRouter) makeRoomForLimit(ctx context.Context, modelID string, p *replicaPlacement) error {
	counter, ok := r.registry.(modelCounter)
	if !ok || p.node == nil {
		return nil
	}
	pol := r.nodePolicy(p.node)
	if pol.MaxActiveBackends <= 0 {
		return nil
	}
	n, err := counter.CountNodeReplicas(ctx, p.node.ID, modelID)
	if err != nil {
		return fmt.Errorf("counting the models on %s: %w", p.node.Name, err)
	}
	if n < pol.MaxActiveBackends {
		return nil
	}
	if !pol.ModelSwap {
		return swapDisabledError(modelID, fmt.Sprintf("node %s is at its model limit", p.node.Name))
	}
	for range n - pol.MaxActiveBackends + 1 {
		if _, err := r.evictLRUAndFreeNodeFrom(ctx, []string{p.node.ID}); err != nil {
			return fmt.Errorf("node %s is at its model limit and eviction failed: %w", p.node.Name, errors.Join(err, ErrNoAvailableNodes))
		}
	}
	xlog.Info("Evicted to stay within the model limit of a node", "model", modelID, "node", p.node.Name, "limit", pol.MaxActiveBackends)
	return nil
}

// CountNodeReplicas counts the model slots taken on a node: the replicas
// that are loaded, loading or staging, and the live cold loads of other
// models claimed on the node that have no replica yet. A claimed load gets
// its replica row only at staging, after it made room, so without the
// claims two loads placed at the same time would each see a free slot.
// forModel is the model being placed: its own claim is the load that asks.
func (r *NodeRegistry) CountNodeReplicas(ctx context.Context, nodeID, forModel string) (int, error) {
	var rows int64
	db := r.db.WithContext(ctx)
	if err := db.Model(&NodeModel{}).
		Where("node_id = ? AND state IN ?", nodeID, []string{"loaded", "loading", "staging"}).
		Count(&rows).Error; err != nil {
		return 0, err
	}
	claims, err := r.countUnstagedClaimsOnNode(ctx, nodeID, forModel)
	if err != nil {
		return 0, err
	}
	return int(rows) + claims, nil
}

// SetReplicaSize records the memory a replica was placed with, for the
// largest-first eviction order.
func (r *NodeRegistry) SetReplicaSize(ctx context.Context, nodeID, modelName string, replicaIndex int, bytes uint64) error {
	return r.db.WithContext(ctx).Model(&NodeModel{}).
		Where("node_id = ? AND model_name = ? AND replica_index = ?", nodeID, modelName, replicaIndex).
		Update("size_bytes", bytes).Error
}

// replicaSize is the memory estimate of a replica: the one its placement
// used, else a fresh estimate of its options.
func (r *SmartRouter) replicaSize(ctx context.Context, placed *replicaPlacement, opts *pb.ModelOptions) uint64 {
	if placed != nil && placed.estimatedVRAM > 0 {
		return placed.estimatedVRAM
	}
	if opts == nil {
		return 0
	}
	if r.vramEstimator != nil {
		return r.vramEstimator(ctx, opts)
	}
	return r.estimateModelVRAM(ctx, opts)
}

// recordReplicaSize stores the size estimate of a replica that just loaded.
// A failure only costs the size-aware order its precision.
func (r *SmartRouter) recordReplicaSize(ctx context.Context, nodeID, modelID string, replicaIndex int, bytes uint64) {
	if bytes == 0 {
		return
	}
	setter, ok := r.registry.(interface {
		SetReplicaSize(context.Context, string, string, int, uint64) error
	})
	if !ok {
		return
	}
	if err := setter.SetReplicaSize(ctx, nodeID, modelID, replicaIndex, bytes); err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		xlog.Debug("Could not record the size of a replica", "model", modelID, "node", nodeID, "error", err)
	}
}

// swapCandidates narrows an eviction to the candidate nodes with model swap
// on. With swap off on every candidate the eviction is refused with
// ErrModelSwapDisabled; with no candidate left at all, ErrNoAvailableNodes.
func (r *SmartRouter) swapCandidates(ctx context.Context, candidates []string) ([]string, error) {
	out, off, err := r.swapNodes(ctx, candidates)
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("context cancelled while waiting for eviction: %w", ctx.Err())
		}
		return nil, err
	}
	if len(out) == 0 {
		if off {
			return nil, fmt.Errorf("%w: %w", ErrModelSwapDisabled, ErrNoAvailableNodes)
		}
		return nil, fmt.Errorf("no healthy node to evict from: %w", ErrNoAvailableNodes)
	}
	return out, nil
}
