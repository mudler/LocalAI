package nodes

import (
	"context"
	"fmt"
)

// ClusterMemory reports the memory budget a model actually gets in a
// distributed deployment: that of the single largest healthy backend node.
//
// The largest node, not the fleet total. A model loads into one node, so
// summing a fleet of four 16GB cards into 64GB would tell an admin a 40GB
// model fits when no node can ever hold it. Naming the node is part of the
// answer for the same reason: "fits" is only meaningful somewhere.
type ClusterMemory struct {
	NodeID      string `json:"node_id"`
	NodeName    string `json:"node_name"`
	TotalMemory uint64 `json:"total_memory"`
	IsGPU       bool   `json:"is_gpu"`
	NodeCount   int    `json:"node_count"`
}

// HealthyNodeMemory reports the largest model budget any single healthy backend
// node can offer, or nil when the cluster can answer nothing.
//
// A nil reading is not an error. It means the caller should size against
// whatever it sized against before, which keeps a registry hiccup or an
// empty cluster from marking the entire catalog as too large.
//
// Only healthy backend nodes count, the same predicate the scheduler places
// against, so a drained worker stops advertising hardware the cluster cannot
// currently use.
func (r *NodeRegistry) HealthyNodeMemory(ctx context.Context) (*ClusterMemory, error) {
	var nodes []BackendNode
	if err := r.db.WithContext(ctx).
		Where("status = ? AND node_type = ?", StatusHealthy, NodeTypeBackend).
		Find(&nodes).Error; err != nil {
		return nil, fmt.Errorf("listing healthy backend node memory: %w", err)
	}

	var best *ClusterMemory
	count := 0
	for _, node := range nodes {
		budget, isGPU := nodeModelBudget(node)
		if budget == 0 {
			continue
		}
		count++
		if best == nil || betterBudget(budget, isGPU, best.TotalMemory, best.IsGPU) {
			best = &ClusterMemory{
				NodeID:      node.ID,
				NodeName:    node.Name,
				TotalMemory: budget,
				IsGPU:       isGPU,
			}
		}
	}
	if best == nil {
		return nil, nil
	}
	best.NodeCount = count
	return best, nil
}

// nodeModelBudget reports how much memory a model may occupy on one node, the
// per-node form of the same question core/gallery answers for a single host:
// VRAM when the node has a GPU, system RAM otherwise.
//
// An operator-set VRAM budget wins over raw VRAM. The scheduler already refuses
// a load above that ceiling, so sizing against the raw total would advertise a
// fit the cluster then rejects.
func nodeModelBudget(node BackendNode) (uint64, bool) {
	if node.TotalVRAM > 0 {
		if node.VRAMBudgetBytes > 0 && node.VRAMBudgetBytes < node.TotalVRAM {
			return node.VRAMBudgetBytes, true
		}
		return node.TotalVRAM, true
	}
	return node.TotalRAM, false
}

// betterBudget ranks one node's budget against the incumbent's.
//
// A GPU node always beats a CPU node, however much system RAM the CPU node
// holds: a 512GB CPU box will serve a 70B model at a speed nobody would pick
// over a 24GB card, so reporting the CPU box as the cluster's capability would
// recommend models the cluster cannot usefully run.
func betterBudget(budget uint64, isGPU bool, bestBudget uint64, bestIsGPU bool) bool {
	if isGPU != bestIsGPU {
		return isGPU
	}
	return budget > bestBudget
}
