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
	if n.ReservedVRAM+n.HeadroomVRAM >= n.AvailableVRAM {
		return 0
	}
	return n.AvailableVRAM - n.ReservedVRAM - n.HeadroomVRAM
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
		if !r.nodePolicy(n).ModelSwap {
			continue // nothing may be unloaded on this node for a load
		}
		if err := r.evictableReplicas(db.Model(&NodeModel{}), []string{n.ID}).
			Order(r.evictionOrder()).
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

// makeRoom runs the eviction that p planned on its node, if it is still
// needed, and settles the replica slot. The caller runs it only once it owns
// the load of the model on that node, so a lost claim evicts nothing and two
// frontends never evict for the same load.
//
// It fails with a retryable error when a replica in the plan stopped being
// evictable (it took a request, for example): the node then has no room, and
// placing the model there anyway would fail on the worker or push out a model
// that is serving.
func (r *SmartRouter) makeRoom(ctx context.Context, modelID string, p *replicaPlacement) error {
	room := p.room
	p.room = roomPlan{}
	if err := r.runRoomPlan(ctx, modelID, p, room); err != nil {
		return err
	}
	// The model limit last, on a fresh count: an eviction of the plan may
	// have been skipped (the node had the memory by then, or a slot came
	// free), so the plan's evictions are not counted in advance.
	return r.makeRoomForLimit(ctx, modelID, p)
}

// runRoomPlan runs the memory and LRU evictions of room on the node of p.
func (r *SmartRouter) runRoomPlan(ctx context.Context, modelID string, p *replicaPlacement, room roomPlan) error {
	if len(room.memory) > 0 && r.stillShortOfMemory(ctx, p) {
		for _, nm := range room.memory {
			if !r.evictReplica(ctx, nm) {
				xlog.Info("A replica chosen for eviction is no longer idle; not placing the model on a node without room",
					"model", modelID, "node", p.node.Name, "replica_of", nm.ModelName)
				return insufficientMemoryError(modelID, p.estimatedVRAM)
			}
		}
		evicted := make([]string, 0, len(room.memory))
		for _, nm := range room.memory {
			evicted = append(evicted, nm.ModelName)
		}
		xlog.Info("Evicted idle models to make room for a model", "model", modelID, "node", p.node.Name,
			"required_vram", vram.FormatBytes(p.estimatedVRAM), "free_vram", vram.FormatBytes(freeVRAM(p.node)),
			"evicted", slices.Compact(slices.Sorted(slices.Values(evicted))))
	}
	if room.lru == nil {
		return nil
	}
	maxSlots := max(p.node.MaxReplicasPerModel, 1)
	if room.slotPending {
		// A slot may have come free since the plan.
		idx, err := r.registry.NextFreeReplicaIndex(ctx, p.node.ID, modelID, maxSlots)
		if err == nil {
			p.replicaIdx = idx
			return nil
		}
		if !errors.Is(err, ErrNoFreeSlot) {
			return fmt.Errorf("determining free replica slot on %s: %w", p.node.Name, err)
		}
	}
	if !r.evictReplica(ctx, *room.lru) {
		// The planned replica took a request or is gone. Another idle one on
		// the same node will do; the load is claimed for this node.
		if _, err := r.evictLRUAndFreeNodeFrom(ctx, []string{p.node.ID}); err != nil {
			return fmt.Errorf("no room on %s for %s and eviction failed: %w", p.node.Name, modelID, errors.Join(err, ErrNoAvailableNodes))
		}
	} else {
		xlog.Info("Evicted LRU model to free capacity",
			"node", room.lru.NodeID, "model", room.lru.ModelName, "lastUsed", room.lru.LastUsed, "for", modelID)
	}
	if room.slotPending {
		idx, err := r.registry.NextFreeReplicaIndex(ctx, p.node.ID, modelID, maxSlots)
		if err != nil {
			return fmt.Errorf("no replica slot on %s after eviction: %w", p.node.Name, err)
		}
		p.replicaIdx = idx
	}
	return nil
}

// stillShortOfMemory reads the node again and reports whether it still lacks
// the memory p needs. A read that fails keeps the plan.
func (r *SmartRouter) stillShortOfMemory(ctx context.Context, p *replicaPlacement) bool {
	node, err := r.registry.Get(ctx, p.node.ID)
	if err != nil || node == nil {
		return true
	}
	return freeVRAM(node) < p.estimatedVRAM
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
