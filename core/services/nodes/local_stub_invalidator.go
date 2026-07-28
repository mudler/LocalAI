package nodes

import (
	"context"

	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/xlog"
)

// NewLocalStubInvalidator returns a replica-removed hook that drops the
// frontend's in-process stub for a model once no healthy replica of it remains
// anywhere in the cluster.
//
// Why this is needed: in distributed mode every routed model leaves a stub in
// the frontend's ModelLoader, and DistributedModelStore.Range reports local
// stubs UNION the registry rows. Every registry removal path deletes only the
// DB row, so without this hook the stub outlives the replica and the model is
// reported as loaded forever. That is the "loaded on the home page, absent from
// every node" ghost: /system reads the union and still sees the stub, while
// /api/nodes/models reads the registry and correctly sees nothing.
//
// The stub is dropped only when the LAST replica is gone. While another node
// still serves the model the frontend is right to consider it loaded, and each
// request re-routes through SmartRouter to pick a live replica anyway.
//
// This deletes the store entry directly rather than going through
// ShutdownModel: the replica is already gone from the registry, so there is
// nothing left to unload, and ShutdownModel would emit a pointless remote
// unload to a node that no longer hosts it.
func NewLocalStubInvalidator(registry *NodeRegistry, store model.ModelStore) func(modelID, nodeID string, replicaIndex int) {
	return func(modelID, nodeID string, replicaIndex int) {
		if registry == nil || store == nil || modelID == "" {
			return
		}
		if _, ok := store.Get(modelID); !ok {
			return // no local stub for this model, nothing to invalidate
		}

		// Registry rows are the source of truth for "is this model loaded
		// anywhere". Only healthy nodes with a loaded row are returned.
		remaining, err := registry.FindNodesWithModel(context.Background(), modelID)
		if err != nil {
			// Leave the stub alone rather than risk dropping a live model on a
			// transient DB error. A later removal re-runs this check, and the
			// reconciler keeps probing, so the ghost is not permanent.
			xlog.Warn("Local stub invalidation skipped: failed to count remaining replicas",
				"model", modelID, "node", nodeID, "replica", replicaIndex, "error", err)
			return
		}
		if len(remaining) > 0 {
			return
		}

		store.Delete(modelID)
		xlog.Info("Dropped local model stub after its last replica was removed",
			"model", modelID, "node", nodeID, "replica", replicaIndex)
	}
}
