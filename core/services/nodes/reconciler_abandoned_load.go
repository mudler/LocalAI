package nodes

import (
	"context"
	"errors"
	"time"

	"github.com/mudler/xlog"
	"gorm.io/gorm"
)

const (
	// abandonedLoadGrace is how long a replica row may sit in a pre-serving
	// state before the sweeper will consider it at all.
	//
	// It exists to cover the window between creating the replica row and
	// writing the load job that vouches for it. Without it a load could be
	// reclaimed in the moment before its own job row exists. It is not the
	// thing that protects a long transfer: the job heartbeat does that.
	abandonedLoadGrace = 5 * time.Minute
)

// preServingStates are the replica states that hold a slot without being able
// to serve a request. NextFreeReplicaIndex counts every state except
// "unloading", so a row parked in one of these occupies capacity while
// answering nothing.
var preServingStates = []string{"loading", "staging"}

// reclaimAbandonedLoads removes replica rows whose load will never finish.
//
// The other reconciler passes and the router's eviction query all filter
// state = "loaded", and the per-model probe skips rows without an address, so
// nothing reclaimed a row that never got that far. On a node with one replica
// slot per model, a single interrupted transfer made the model unschedulable
// there until an operator intervened: scheduling saw no free slot, and eviction
// found nothing it was allowed to evict.
//
// A row is only reclaimed when something proves the load is not progressing:
// either a load job that has failed or stopped heartbeating, or, for a row with
// no job at all, a node that is no longer healthy.
//
// The no-job case has to be conservative. Only the request path creates load
// jobs; the reconciler's own scale-up loads a replica without one. Treating a
// missing job as proof of abandonment would let this sweeper delete a healthy
// reconciler-driven transfer the moment it ran past the grace period, which for
// a multi-gigabyte checkpoint is every time. A healthy node with no job is
// therefore left alone; when the node is gone, nothing can be progressing and
// the row is safe to reclaim.
func (rc *ReplicaReconciler) reclaimAbandonedLoads(ctx context.Context) {
	if rc.db == nil {
		return
	}

	cutoff := time.Now().Add(-abandonedLoadGrace)
	var stuck []NodeModel
	if err := rc.db.WithContext(ctx).
		Where("state IN ? AND updated_at < ?", preServingStates, cutoff).
		Find(&stuck).Error; err != nil {
		xlog.Warn("Reconciler: failed to list replicas stuck before serving", "error", err)
		return
	}

	now := time.Now()
	for _, row := range stuck {
		if !rc.loadAbandoned(ctx, row, now) {
			continue
		}
		if err := rc.registry.RemoveNodeModel(ctx, row.NodeID, row.ModelName, row.ReplicaIndex); err != nil {
			xlog.Warn("Reconciler: failed to reclaim abandoned load",
				"node", row.NodeID, "model", row.ModelName, "replica", row.ReplicaIndex,
				"state", row.State, "error", err)
			continue
		}
		xlog.Warn("Reconciler: reclaimed a replica slot held by a load nobody is driving",
			"node", row.NodeID, "model", row.ModelName, "replica", row.ReplicaIndex, "state", row.State)
	}
}

// loadAbandoned reports whether this row's load has demonstrably stopped.
//
// Every uncertain case answers false. Leaving a slot held for another pass
// costs one scheduling opportunity; reclaiming a row out from under a live
// transfer restarts a multi-gigabyte load and, on a single-slot node, makes the
// model unschedulable there for as long as the retry loop runs.
func (rc *ReplicaReconciler) loadAbandoned(ctx context.Context, row NodeModel, now time.Time) bool {
	job, err := rc.registry.GetLoadJob(ctx, row.ModelName)
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound), err == nil && job == nil:
		// No job: only the request path creates them, so this may be a healthy
		// reconciler-driven load. Reclaim only once its node is gone.
		return !rc.nodeHealthy(ctx, row.NodeID)
	case err != nil:
		xlog.Warn("Reconciler: cannot read load job, leaving the replica slot held",
			"model", row.ModelName, "error", err)
		return false
	case job.State == LoadJobStateFailed:
		return true
	default:
		return job.IsOrphaned(now)
	}
}

// nodeHealthy reports whether the row's node is still healthy. An unreadable
// node counts as healthy so a database blip cannot trigger a reclaim.
func (rc *ReplicaReconciler) nodeHealthy(ctx context.Context, nodeID string) bool {
	node, err := rc.registry.Get(ctx, nodeID)
	if err != nil || node == nil {
		xlog.Warn("Reconciler: cannot read node for a stuck replica, leaving the slot held",
			"node", nodeID, "error", err)
		return true
	}
	return node.Status == StatusHealthy
}
