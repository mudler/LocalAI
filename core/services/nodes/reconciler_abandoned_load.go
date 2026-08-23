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
// A row is abandoned when no live load job vouches for it. Ownership is decided
// by the job's LastProgress heartbeat rather than elapsed time, because staging
// a large checkpoint legitimately runs for a long while without touching the
// replica row. That is the same signal job takeover already trusts, so a
// transfer this sweeper reclaims is one no replica is still driving.
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
		if rc.loadStillRunning(ctx, row.ModelName, now) {
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

// loadStillRunning reports whether a load job is actively driving this model.
//
// A missing job means nobody is loading it. A failed job has already given up.
// An orphaned job stopped heartbeating, which is the condition another replica
// uses to take it over, so the transfer behind it is not progressing either.
// Any error reading the job is treated as "still running": leaving a slot held
// for one more pass costs a scheduling opportunity, while removing a row out
// from under a live transfer would restart a multi-gigabyte load.
func (rc *ReplicaReconciler) loadStillRunning(ctx context.Context, modelName string, now time.Time) bool {
	job, err := rc.registry.GetLoadJob(ctx, modelName)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false
	}
	if err != nil {
		xlog.Warn("Reconciler: cannot read load job, leaving the replica slot held",
			"model", modelName, "error", err)
		return true
	}
	if job == nil {
		return false
	}
	if job.State == LoadJobStateFailed {
		return false
	}
	return !job.IsOrphaned(now)
}
