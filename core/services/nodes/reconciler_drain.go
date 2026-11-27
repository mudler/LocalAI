package nodes

import (
	"context"
	"slices"

	"github.com/mudler/xlog"
)

// reconcileDraining moves the replicas of draining nodes out. It runs in the
// replica pass, under the reconciler's advisory lock, after reconcileModel
// has placed the replacements that scheduling rules ask for: reconcileModel
// does not count a replica on a draining node, so a rule's floor is refilled
// on other nodes while the draining replica still serves.
//
// For each loaded replica on a draining node:
//
//   - past the node's drain deadline, it is removed whatever its state;
//   - when the model has as many replicas on healthy nodes as it needs, it is
//     removed once no request runs on it;
//   - otherwise it stays and serves. A model without a rule (or with a floor
//     of zero) that is still in use (pinned, a request in flight, or used
//     within the idle window) gets one replacement placed here, the way a
//     request would place it.
//
// A removed replica is unloaded from its worker. The state is all in the
// database (node status, drain deadline, replica rows), so it does not matter
// which frontend runs the pass.
func (rc *ReplicaReconciler) reconcileDraining(ctx context.Context) {
	nodes, err := rc.registry.ListDrainingNodes(ctx)
	if err != nil {
		xlog.Warn("Reconciler: failed to list draining nodes", "error", err)
		return
	}
	if len(nodes) == 0 {
		return
	}
	// The database clock decides the deadline, as it set it (DrainNode).
	pastDeadline, err := rc.registry.DrainingNodesPastDeadline(ctx)
	if err != nil {
		xlog.Warn("Reconciler: failed to list draining nodes past their deadline", "error", err)
		return
	}
	for _, node := range nodes {
		if err := ctx.Err(); err != nil {
			return
		}
		if pastDeadline[node.ID] {
			rc.releaseDrainingNode(ctx, node, "drain deadline passed")
			continue
		}
		rows, err := rc.registry.GetNodeModels(ctx, node.ID)
		if err != nil {
			xlog.Warn("Reconciler: failed to list the replicas of a draining node", "node", node.Name, "error", err)
			continue
		}
		for _, nm := range rows {
			if err := ctx.Err(); err != nil {
				return
			}
			if nm.State != "loaded" {
				// A load on the node was cancelled when the drain started;
				// the abandoned-load sweep reclaims its row.
				continue
			}
			rc.drainReplica(ctx, node, nm)
		}
	}
}

// drainReplica handles one loaded replica on a draining node.
func (rc *ReplicaReconciler) drainReplica(ctx context.Context, node BackendNode, nm NodeModel) {
	need, rule, ruled := rc.drainNeed(ctx, nm)
	var selector map[string]string
	if rule != nil {
		selector = parseSelector(rule.NodeSelector)
	}
	// Only replicas on healthy nodes take requests, so only they let this one
	// leave.
	staying, err := rc.registry.CountHealthyReplicas(ctx, nm.ModelName, selector)
	if err != nil {
		xlog.Warn("Reconciler: failed to count replicas of a draining model", "model", nm.ModelName, "error", err)
		return
	}
	if int(staying) >= need {
		if nm.InFlight > 0 {
			// Requests still run on it. Remove it on a later pass.
			return
		}
		removed, err := rc.registry.RemoveDrainedReplica(ctx, nm)
		if err != nil {
			xlog.Warn("Reconciler: failed to remove a drained replica", "node", node.Name, "model", nm.ModelName, "error", err)
			return
		}
		if !removed {
			return
		}
		rc.unloadDrained(node, nm)
		xlog.Info("Reconciler: removed a replica from a draining node",
			"node", node.Name, "model", nm.ModelName, "replica", nm.ReplicaIndex, "replicas_elsewhere", staying)
		return
	}
	if ruled {
		// reconcileModel places the replacement for the rule. Wait for it.
		return
	}
	// No rule places a replacement for this model: place one, unless a load
	// of the model already runs. A rule without a floor still names the nodes
	// the model may run on: the replacement goes to a healthy node that
	// matches its selector, or nowhere. A replacement on a node that does not
	// match would never count as staying, and every pass would place another.
	loading, err := rc.registry.CountLiveLoadJobs(ctx, nm.ModelName, nil)
	if err != nil {
		xlog.Warn("Reconciler: failed to count loads of a draining model", "model", nm.ModelName, "error", err)
		return
	}
	if loading > 0 {
		return
	}
	xlog.Info("Reconciler: placing a replacement for a replica on a draining node",
		"node", node.Name, "model", nm.ModelName)
	placement := ModelSchedulingConfig{ModelName: nm.ModelName}
	if rule != nil {
		placement.NodeSelector = rule.NodeSelector
	}
	rc.scaleUp(ctx, placement, 1)
}

// drainNeed returns how many replicas the model of nm needs on nodes that do
// not drain before nm may leave, the scheduling rule that governs the model
// (nil when none does; its selector names the nodes those replicas must run
// on), and whether the rule sets that number (and places them).
func (rc *ReplicaReconciler) drainNeed(ctx context.Context, nm NodeModel) (need int, rule *ModelSchedulingConfig, ruled bool) {
	cfg, err := rc.registry.GetGoverningScheduling(ctx, nm.ModelName)
	var selector map[string]string
	if err == nil && cfg != nil {
		rule = cfg
		selector = parseSelector(cfg.NodeSelector)
		switch {
		case cfg.SpreadAll:
			matched, merr := rc.registry.FindNodesBySelector(ctx, selector)
			if merr == nil && len(matched) > 0 {
				return len(matched), rule, true
			}
		case cfg.MinReplicas > 0:
			return cfg.MinReplicas, rule, true
		}
	}
	if rc.drainedModelInUse(nm) {
		return 1, rule, false
	}
	return 0, rule, false
}

// drainedModelInUse reports whether the model of a replica on a draining node
// is in use, so its last replica must be replaced before it leaves: the model
// is pinned (which covers warm failover targets), a request runs on the
// replica, or the replica served a request within the idle window.
func (rc *ReplicaReconciler) drainedModelInUse(nm NodeModel) bool {
	if rc.pinnedResolver != nil && slices.Contains(rc.pinnedResolver.GetPinnedModelNames(), nm.ModelName) {
		return true
	}
	if nm.InFlight > 0 {
		return true
	}
	return rc.now().Sub(nm.LastUsed) < rc.idleDelay()
}

// releaseDrainingNode removes every replica still on a draining node and
// unloads them from its worker.
func (rc *ReplicaReconciler) releaseDrainingNode(ctx context.Context, node BackendNode, why string) {
	removed, err := rc.registry.ReleaseDrainingNode(ctx, node.ID)
	if err != nil {
		xlog.Warn("Reconciler: failed to release a draining node", "node", node.Name, "error", err)
		return
	}
	for _, nm := range removed {
		rc.unloadDrained(node, nm)
	}
	if len(removed) > 0 {
		xlog.Info("Reconciler: removed the replicas left on a draining node", "node", node.Name, "reason", why, "replicas", len(removed))
	}
}

// unloadDrained stops the backend of a replica whose row has been removed. An
// error is logged only: the row is gone, so nothing routes to the backend,
// and the worker stops its backends when it shuts down.
func (rc *ReplicaReconciler) unloadDrained(node BackendNode, nm NodeModel) {
	if rc.unloader == nil || nm.Address == "" {
		return
	}
	if err := rc.unloader.UnloadReplica(node.ID, nm); err != nil {
		xlog.Warn("Reconciler: failed to unload a replica from a draining node (its row is already removed)",
			"node", node.Name, "model", nm.ModelName, "replica", nm.ReplicaIndex, "error", err)
	}
}
