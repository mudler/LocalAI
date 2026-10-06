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

// reclaimAbandonedLoads projects orphan jobs as failed/uncertain without freeing
// their replica slots. Worker silence, failure and boot changes do not prove
// direct backend work ended or prevent a delayed frontend from issuing it.
func (rc *ReplicaReconciler) reclaimAbandonedLoads(ctx context.Context) {
	if rc.db == nil {
		return
	}

	// Sweep durable jobs independently of replica rows and new requests.
	registry := rc.registry
	jobs, err := registry.ListActiveLoadJobs(ctx)
	if err != nil {
		xlog.Warn("Cannot list load recovery jobs", "error", err)
		return
	}
	service := &LoadRecoveryService{Registry: registry}
	for _, job := range jobs {
		if _, err := service.Reconcile(ctx, job.Ref()); err != nil {
			xlog.Warn("Cannot reconcile load job", "model", job.TrackingKey, "error", err)
		}
	}
	// Do not remove reservations: direct backend work has no admission fence.

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
		return false
	case err != nil:
		xlog.Warn("Reconciler: cannot read load job, leaving the replica slot held",
			"model", row.ModelName, "error", err)
		return false
	case job.State == LoadJobStateFailed:
		return job.Generation != "" && job.TerminalUntil != nil && !job.WorkUncertain
	default:
		return false
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
