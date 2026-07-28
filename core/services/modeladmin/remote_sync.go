package modeladmin

import (
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/pkg/model"

	"github.com/mudler/xlog"
)

// opDelete is the CacheInvalidateEvent.Op value the gallery delete path and the
// admin delete endpoint use; a delete must prune (a reload-from-path cannot).
const opDelete = "delete"

// ApplyRemoteChange refreshes this replica's in-memory model state from a peer
// replica's model-config change broadcast (messaging.CacheInvalidateEvent on
// SubjectCacheInvalidateModels). It is the subscriber-side counterpart to
// GalleryService.BroadcastModelsChanged.
//
// The op matters because LoadModelConfigsFromPath is additive: it loads every
// YAML on disk into the loader but never removes an entry whose file is gone.
// So a delete cannot be propagated by a plain reload - the deleted element must
// be explicitly pruned. Specifically:
//
//   - op == "delete" with a named element: prune that element from the loader.
//   - otherwise: reload all configs from disk (picks up creates and edits).
//
// In both cases, when an element is named, any running instance on this replica
// is shut down (best-effort) so the next request rebuilds it from the new
// config instead of serving the stale one - mirroring what the originating
// replica does on a local edit/delete.
//
// ml may be nil (no running instances to shut down). modelsPath and opts are
// forwarded to LoadModelConfigsFromPath.
func ApplyRemoteChange(cl *config.ModelConfigLoader, ml *model.ModelLoader, modelsPath string, evt messaging.CacheInvalidateEvent, opts ...config.ConfigLoaderOption) error {
	if evt.Op == opDelete && evt.Element != "" {
		cl.RemoveModelConfig(evt.Element)
	} else if err := cl.LoadModelConfigsFromPath(modelsPath, opts...); err != nil {
		return err
	}

	// Drop any running instance of the affected model so the next request
	// rebuilds it from the refreshed config instead of serving the stale one.
	// Best-effort: the model may not be loaded on this replica, which surfaces
	// as a benign error here.
	if ml != nil && evt.Element != "" {
		if err := ml.ShutdownModel(evt.Element); err != nil {
			xlog.Debug("ApplyRemoteChange: could not shut down model instance (likely not loaded)",
				"model", evt.Element, "error", err)
		}
	}
	return nil
}
