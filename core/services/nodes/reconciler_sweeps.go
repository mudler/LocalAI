package nodes

import (
	"context"
	"slices"
	"time"

	"github.com/mudler/xlog"
	"gorm.io/gorm"
)

// The memory sweeps of the reconciler apply the cluster memory settings to
// the replicas on the workers: the idle unload (watchdog_idle_*) and the
// memory reclaimer (memory_reclaimer_*). They run in the replica pass, under
// the reconciler's advisory lock, so one frontend sweeps at a time.

// sweepCandidates lists the loaded, idle replicas on healthy nodes that a
// sweep may unload, oldest first: not pinned (which covers warm failover
// targets) and above the min_replicas of their scheduling rule (0 when the
// model has no rule). nodeID narrows to one node when set.
func (rc *ReplicaReconciler) sweepCandidates(ctx context.Context, nodeID string, idleBefore time.Time) ([]NodeModel, error) {
	db := rc.registry.db.WithContext(ctx)
	q := currentModelRevision(db).
		Joins("JOIN backend_nodes ON backend_nodes.id = node_models.node_id").
		Where("node_models.state = ? AND node_models.in_flight = 0 AND backend_nodes.status = ?", "loaded", StatusHealthy).
		Where(`(SELECT COUNT(*) FROM node_models nm2 WHERE nm2.model_name = node_models.model_name AND nm2.state = 'loaded'
    AND nm2.node_id NOT IN (SELECT id FROM backend_nodes WHERE status = ?))
  > COALESCE((SELECT MAX(sc.min_replicas) FROM model_scheduling_configs sc WHERE COALESCE(NULLIF(sc.target_model, ''), sc.model_name) = node_models.model_name), 0)`, StatusDraining)
	if nodeID != "" {
		q = q.Where("node_models.node_id = ?", nodeID)
	}
	if !idleBefore.IsZero() {
		q = q.Where("node_models.last_used < ?", idleBefore)
	}
	if rc.pinnedResolver != nil {
		if pinned := rc.pinnedResolver.GetPinnedModelNames(); len(pinned) > 0 {
			q = q.Where("node_models.model_name NOT IN ?", pinned)
		}
	}
	var out []NodeModel
	err := q.Order("node_models.last_used ASC").Find(&out).Error
	return out, err
}

// unloadForSweep removes one replica row and unloads it from its worker. It
// does nothing when the replica took a request since the sweep read it (see
// RemoveIdleNodeModel).
func (rc *ReplicaReconciler) unloadForSweep(ctx context.Context, nm NodeModel, why string) bool {
	removed, err := rc.registry.RemoveIdleNodeModel(ctx, nm)
	if err != nil {
		xlog.Warn("Reconciler: failed to remove model record", "node", nm.NodeID, "model", nm.ModelName, "error", err)
		return false
	}
	if !removed {
		xlog.Debug("Reconciler: replica is no longer idle, not unloading it", "reason", why, "model", nm.ModelName, "node", nm.NodeID, "replica", nm.ReplicaIndex)
		return false
	}
	if err := rc.unloader.UnloadReplica(nm.NodeID, nm); err != nil {
		xlog.Warn("Reconciler: unload failed (model already removed from registry)", "error", err)
	}
	xlog.Info("Reconciler: unloaded a replica", "reason", why, "model", nm.ModelName, "node", nm.NodeID, "replica", nm.ReplicaIndex)
	return true
}

// sweepIdle unloads the replicas idle longer than the idle timeout, when the
// idle unload is on. A model keeps its rule's min_replicas; a model with no
// rule may go to zero and loads again on the next request. An autoscaled
// model also keeps one idle replica, as scaleDownIdle does: with none left
// idle the reconciler sees every replica busy and loads one again.
func (rc *ReplicaReconciler) sweepIdle(ctx context.Context) {
	p := rc.registry.MemoryPolicy()
	if rc.unloader == nil || !p.IdleEnabled || p.IdleTimeout <= 0 {
		return
	}
	idle, err := rc.sweepCandidates(ctx, "", rc.now().Add(-p.IdleTimeout))
	if err != nil {
		xlog.Warn("Reconciler: listing idle replicas failed", "error", err)
		return
	}
	for _, nm := range idle {
		// The floor was checked per row against the count before this
		// sweep; check again as replicas of the same model go. loadedCount
		// reads the rows, so it already leaves out the ones removed.
		floor := rc.minReplicas(ctx, nm.ModelName)
		if rc.loadedCount(ctx, nm.ModelName) <= floor {
			continue
		}
		if rc.lastIdleOfAutoscaled(ctx, nm) {
			continue
		}
		rc.unloadForSweep(ctx, nm, "idle timeout")
	}
}

// lastIdleOfAutoscaled reports whether nm is the only idle loaded replica of
// a model with an autoscaling rule, on the nodes the rule selects. Removing
// it would leave every replica busy, which makes the reconciler scale up.
func (rc *ReplicaReconciler) lastIdleOfAutoscaled(ctx context.Context, nm NodeModel) bool {
	var rule ModelSchedulingConfig
	err := rc.registry.db.WithContext(ctx).
		Where("COALESCE(NULLIF(target_model, ''), model_name) = ? AND (min_replicas > 0 OR max_replicas > 0 OR spread_all)", nm.ModelName).
		First(&rule).Error
	if err != nil {
		return false // no autoscaling rule (or unreadable: the floor still holds)
	}
	selected := func() *gorm.DB {
		return currentModelRevision(rc.registry.db.WithContext(ctx).Model(&NodeModel{})).
			Scopes(onNodesMatching(parseSelector(rule.NodeSelector))).
			Where("node_models.model_name = ? AND node_models.state = ? AND node_models.in_flight = 0", nm.ModelName, "loaded")
	}
	var self, others int64
	if selected().Where("node_models.id = ?", nm.ID).Count(&self).Error != nil || self == 0 {
		return false // not on a node the rule counts
	}
	if selected().Where("node_models.id <> ?", nm.ID).Count(&others).Error != nil {
		return true
	}
	return others == 0
}

// sweepMemoryPressure unloads, on each node whose memory use is above its
// reclaimer threshold, the least recently used idle replica. One per node and
// tick: the next heartbeat reports the memory it freed.
func (rc *ReplicaReconciler) sweepMemoryPressure(ctx context.Context) {
	if rc.unloader == nil {
		return
	}
	nodes, err := rc.registry.FindNodesBySelector(ctx, nil)
	if err != nil {
		return
	}
	for i := range nodes {
		n := &nodes[i]
		p := rc.registry.NodePolicy(n)
		if !p.ReclaimerEnabled || p.ReclaimerThreshold <= 0 {
			continue
		}
		used, ok := memoryUse(n)
		if !ok || used <= p.ReclaimerThreshold {
			continue
		}
		cands, err := rc.sweepCandidates(ctx, n.ID, time.Time{})
		if err != nil || len(cands) == 0 {
			if err == nil {
				xlog.Debug("Reconciler: node above its memory threshold has no idle replica to unload", "node", n.Name, "use", used)
			}
			continue
		}
		if rc.registry.MemoryPolicy().SizeAwareEviction {
			slices.SortStableFunc(cands, func(a, b NodeModel) int {
				switch {
				case a.SizeBytes > b.SizeBytes:
					return -1
				case a.SizeBytes < b.SizeBytes:
					return 1
				}
				return 0
			})
		}
		xlog.Warn("Reconciler: node memory above its threshold, unloading an idle replica",
			"node", n.Name, "use", used, "threshold", p.ReclaimerThreshold)
		rc.unloadForSweep(ctx, cands[0], "memory threshold")
	}
}

// memoryUse is the share of a node's memory in use: GPU memory within its
// budget when the node reports GPU memory, RAM otherwise.
func memoryUse(n *BackendNode) (float64, bool) {
	if ceiling := nodeMemoryCeiling(n); ceiling > 0 {
		free := min(n.AvailableVRAM, ceiling)
		return float64(ceiling-free) / float64(ceiling), true
	}
	if n.TotalRAM > 0 {
		free := min(n.AvailableRAM, n.TotalRAM)
		return float64(n.TotalRAM-free) / float64(n.TotalRAM), true
	}
	return 0, false
}

func (rc *ReplicaReconciler) minReplicas(ctx context.Context, model string) int {
	var floor int
	rc.registry.db.WithContext(ctx).Model(&ModelSchedulingConfig{}).
		Select("COALESCE(MAX(min_replicas), 0)").
		Where("COALESCE(NULLIF(target_model, ''), model_name) = ?", model).
		Scan(&floor)
	return floor
}

func (rc *ReplicaReconciler) loadedCount(ctx context.Context, model string) int {
	var n int64
	rc.registry.db.WithContext(ctx).Model(&NodeModel{}).
		Where("model_name = ? AND state = ?", model, "loaded").Count(&n)
	return int(n)
}

// idleDelay is how long a replica above its rule's floor stays loaded while
// idle: the idle timeout when the idle unload is on, else the fixed
// scale-down delay of the reconciler.
func (rc *ReplicaReconciler) idleDelay() time.Duration {
	if p := rc.registry.MemoryPolicy(); p.IdleEnabled && p.IdleTimeout > 0 {
		return p.IdleTimeout
	}
	return rc.scaleDownDelay
}
