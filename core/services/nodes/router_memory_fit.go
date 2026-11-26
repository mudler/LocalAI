package nodes

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/mudler/LocalAI/pkg/vram"
	"github.com/mudler/xlog"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ErrInsufficientMemory means no candidate node has enough free GPU memory for
// the model, and evicting idle models cannot free enough either. It also wraps
// ErrNoAvailableNodes, so the HTTP layer answers 503 and the caller retries:
// a model finishing its requests, a node joining or failover can change the
// answer within seconds.
var ErrInsufficientMemory = errors.New("no node has enough free memory")

// memoryFitPlan is one way to make room for a model: the node, and the idle
// replicas on it to evict first. An empty evict list means the node fits now.
type memoryFitPlan struct {
	node  BackendNode
	evict []NodeModel
}

// nodeMemoryCeiling is the most GPU memory a node can ever give one model: its
// total, or the operator's budget when that is lower.
func nodeMemoryCeiling(n *BackendNode) uint64 {
	if n.VRAMBudgetBytes > 0 && n.VRAMBudgetBytes < n.TotalVRAM {
		return n.VRAMBudgetBytes
	}
	return n.TotalVRAM
}

// freeVRAM is the GPU memory the scheduler may still hand out on a node.
func freeVRAM(n *BackendNode) uint64 {
	if n.ReservedVRAM >= n.AvailableVRAM {
		return 0
	}
	return n.AvailableVRAM - n.ReservedVRAM
}

// planMemoryFit finds the candidate node where evicting idle models frees
// enough GPU memory for a model that needs `need` bytes. It returns nil when no
// node can make room. A nil candidateNodeIDs means every healthy node.
//
// The registry does not record how much memory each replica uses, only what
// the worker reports for the whole node. The plan therefore charges each
// loaded replica an equal share of the node's used memory. The worker's next
// heartbeat reports the real free memory, and a load that still does not fit
// fails on the worker and is retried. Nodes that report no GPU memory are not
// planned here (see memoryUnknownNodes).
//
// The plan prefers the node that needs the fewest evictions, then the one with
// the most free memory already.
func (r *SmartRouter) planMemoryFit(ctx context.Context, need uint64, candidateNodeIDs []string) (*memoryFitPlan, error) {
	if r.db == nil {
		return nil, nil // no row locks, so no safe eviction
	}
	db := r.db.WithContext(ctx)
	q := db.Where("status = ? AND node_type = ? AND total_vram > 0", StatusHealthy, NodeTypeBackend)
	if candidateNodeIDs != nil {
		if len(candidateNodeIDs) == 0 {
			return nil, nil
		}
		q = q.Where("id IN ?", candidateNodeIDs)
	}
	var nodes []BackendNode
	if err := q.Order("name ASC").Find(&nodes).Error; err != nil {
		return nil, fmt.Errorf("listing nodes for a memory fit: %w", err)
	}

	var best *memoryFitPlan
	for i := range nodes {
		n := &nodes[i]
		ceiling := nodeMemoryCeiling(n)
		if ceiling < need {
			continue // even empty, this node cannot hold the model
		}
		free := freeVRAM(n)
		if free >= need {
			plan := &memoryFitPlan{node: *n}
			if best == nil || len(best.evict) > 0 || freeVRAM(&best.node) < free {
				best = plan
			}
			continue
		}
		var loaded int64
		if err := currentModelRevision(db.Model(&NodeModel{})).
			Where("node_models.node_id = ? AND node_models.state = ?", n.ID, "loaded").
			Count(&loaded).Error; err != nil {
			return nil, fmt.Errorf("counting replicas on node %s: %w", n.Name, err)
		}
		if loaded == 0 {
			continue // the memory is used by something the scheduler cannot evict
		}
		used := uint64(0)
		if ceiling > n.AvailableVRAM {
			used = ceiling - n.AvailableVRAM
		}
		share := used / uint64(loaded)
		if share == 0 {
			continue
		}
		var evictable []NodeModel
		if err := r.evictableReplicas(db.Model(&NodeModel{}), []string{n.ID}).
			Order("node_models.last_used ASC").
			Find(&evictable).Error; err != nil {
			return nil, fmt.Errorf("listing evictable replicas on node %s: %w", n.Name, err)
		}
		freed := free
		var evict []NodeModel
		for _, nm := range evictable {
			if freed >= need {
				break
			}
			evict = append(evict, nm)
			freed += share
		}
		if freed < need {
			continue
		}
		if best == nil || len(evict) < len(best.evict) ||
			(len(evict) == len(best.evict) && free > freeVRAM(&best.node)) {
			best = &memoryFitPlan{node: *n, evict: evict}
		}
	}
	return best, nil
}

// fitByEviction makes room for a model of `need` bytes on one candidate node by
// evicting idle replicas there, and returns that node. It returns nil when no
// node can make room, or when a replica in the plan stopped being evictable
// before it was removed (it took a request, for example).
func (r *SmartRouter) fitByEviction(ctx context.Context, modelID string, need uint64, candidateNodeIDs []string) *BackendNode {
	plan, err := r.planMemoryFit(ctx, need, candidateNodeIDs)
	if err != nil {
		xlog.Warn("Could not plan an eviction to make room for a model", "model", modelID, "error", err)
		return nil
	}
	if plan == nil {
		return nil
	}
	for _, nm := range plan.evict {
		if !r.evictReplica(ctx, nm) {
			xlog.Info("A replica chosen for eviction is no longer idle; not placing the model on a node without room",
				"model", modelID, "node", plan.node.Name, "replica_of", nm.ModelName)
			return nil
		}
	}
	if len(plan.evict) > 0 {
		evicted := make([]string, 0, len(plan.evict))
		for _, nm := range plan.evict {
			evicted = append(evicted, nm.ModelName)
		}
		xlog.Info("Evicted idle models to make room for a model", "model", modelID, "node", plan.node.Name,
			"required_vram", vram.FormatBytes(need), "free_vram", vram.FormatBytes(freeVRAM(&plan.node)),
			"evicted", slices.Compact(slices.Sorted(slices.Values(evicted))))
	}
	node := plan.node
	return &node
}

// evictReplica removes one replica if it is still evictable, and unloads it
// from its worker. The row is locked and checked again with the same rules as
// the LRU eviction, so a replica that took a request since the plan was made
// stays.
func (r *SmartRouter) evictReplica(ctx context.Context, nm NodeModel) bool {
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row NodeModel
		if err := r.evictableReplicas(tx.Clauses(clause.Locking{Strength: "UPDATE"}), []string{nm.NodeID}).
			Where("node_models.id = ?", nm.ID).
			First(&row).Error; err != nil {
			return err
		}
		return tx.Where("id = ?", row.ID).Delete(&NodeModel{}).Error
	})
	if err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			xlog.Warn("Failed to evict replica to make room", "node", nm.NodeID, "model", nm.ModelName, "error", err)
		}
		return false
	}
	if r.unloader != nil {
		if uerr := r.unloader.UnloadReplica(nm.NodeID, nm); uerr != nil {
			xlog.Warn("eviction unload failed (model already removed from registry)", "error", uerr)
		}
	}
	return true
}

// memoryUnknownNodes returns the candidates that report no GPU memory at all,
// such as CPU-only workers. Their fit cannot be judged from GPU memory, so a
// model with a size estimate may still be placed there the way it was before
// the memory fit existed. A nil candidateNodeIDs means every healthy node.
func (r *SmartRouter) memoryUnknownNodes(ctx context.Context, candidateNodeIDs []string) ([]string, error) {
	nodes, err := r.registry.FindNodesBySelector(ctx, nil)
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, n := range nodes {
		if n.TotalVRAM != 0 {
			continue
		}
		if candidateNodeIDs != nil && !slices.Contains(candidateNodeIDs, n.ID) {
			continue
		}
		out = append(out, n.ID)
	}
	return out, nil
}

// insufficientMemoryError is the retryable answer when no node can hold a model.
func insufficientMemoryError(modelID string, need uint64) error {
	return fmt.Errorf("%w for model %s (needs about %s): %w", ErrInsufficientMemory, modelID, vram.FormatBytes(need), ErrNoAvailableNodes)
}
