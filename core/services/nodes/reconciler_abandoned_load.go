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

// reclaimAbandonedLoads applies the load job lease rules, then removes replica
// rows whose load will never finish.
//
// It runs without any request. First it fails running jobs whose lease ran out
// and releases failed jobs whose stop window is over, so a crashed owner frees
// its model on its own. Then it removes replica rows stuck before serving.
//
// The other reconciler passes and the router's eviction query all filter
// state = "loaded", and the per-model probe skips rows without an address, so
// nothing else reclaims a row that never got that far. On a node with one
// replica slot per model, a single interrupted transfer made the model
// unschedulable there until an operator intervened.
//
// A replica row names the load attempt that created it. It is removed once that
// attempt has no job: the job was released, or another attempt replaced it.
// While the job exists, even a failed one, the row keeps its slot, because the
// work behind it may still run. Rows written without a generation (an older
// binary, or a reconciler-driven load that predates job claims) are the
// uncertain case: they are removed when their model's job is released, or when
// their node is gone. A healthy node with no job is left alone, because nothing
// proves the load stopped.
func (rc *ReplicaReconciler) reclaimAbandonedLoads(ctx context.Context) {
	if rc.db == nil {
		return
	}

	// Failed attempts whose remote work is not confirmed ended keep their stop
	// retried until the worker answers or the deadline releases them.
	rc.retryLoadStops(ctx)

	sweep, err := rc.registry.SweepLoadJobs(ctx)
	if err != nil {
		xlog.Warn("Reconciler: failed to sweep load job leases, leaving replica slots held", "error", err)
		return
	}
	if sweep.Expired > 0 || len(sweep.Released) > 0 {
		xlog.Warn("Reconciler: applied load job lease rules", "expired", sweep.Expired, "released", len(sweep.Released))
	}
	released := make(map[string]bool, len(sweep.Released))
	for _, ref := range sweep.Released {
		released[ref.TrackingKey] = true
	}

	cutoff := time.Now().Add(-abandonedLoadGrace)
	var stuck []NodeModel
	// The age grace only covers rows with no generation. A row that names its
	// attempt needs no grace: its job exists from before the row does.
	if err := rc.db.WithContext(ctx).
		Where("state IN ? AND (load_generation <> '' OR updated_at < ?)", preServingStates, cutoff).
		Find(&stuck).Error; err != nil {
		xlog.Warn("Reconciler: failed to list replicas stuck before serving", "error", err)
		return
	}

	for _, row := range stuck {
		if !rc.loadAbandoned(ctx, row, released[row.ModelName]) {
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
// transfer restarts a multi-gigabyte load.
func (rc *ReplicaReconciler) loadAbandoned(ctx context.Context, row NodeModel, jobReleased bool) bool {
	job, err := rc.registry.GetLoadJob(ctx, row.ModelName)
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound), err == nil && job == nil:
		if row.LoadGeneration != "" {
			// The attempt that made this row has no job any more.
			return true
		}
		// No generation and no job: this may be a healthy load an older binary
		// drives. Reclaim only once the node is gone, or the job it belonged to
		// was just released.
		return jobReleased || !rc.nodeHealthy(ctx, row.NodeID)
	case err != nil:
		xlog.Warn("Reconciler: cannot read load job, leaving the replica slot held",
			"model", row.ModelName, "error", err)
		return false
	default:
		// A job exists. The row is abandoned only if the job is another
		// attempt's.
		return row.LoadGeneration != "" && job.Generation != row.LoadGeneration
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
