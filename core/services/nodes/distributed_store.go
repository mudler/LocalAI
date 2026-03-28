package nodes

import (
	"context"

	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/xlog"
)

// DistributedModelStore wraps a local in-memory store with PostgreSQL-backed
// lookup via NodeRegistry. Models that aren't in the local cache are looked up
// in the database — this makes shutdown, listing, and watchdog work even when
// the frontend process restarted or a different instance loaded the model.
type DistributedModelStore struct {
	local    model.ModelStore
	registry ModelLookup
}

func NewDistributedModelStore(local model.ModelStore, registry ModelLookup) *DistributedModelStore {
	return &DistributedModelStore{local: local, registry: registry}
}

// Get checks the local cache first, then falls back to PostgreSQL.
// If a model is found in the DB but not locally, a Model stub is constructed
// with the remote node's address and cached locally.
func (s *DistributedModelStore) Get(id string) (*model.Model, bool) {
	if m, ok := s.local.Get(id); ok {
		return m, true
	}

	// Fall back to DB
	node, found := s.registry.FindNodeForModel(context.Background(), id)
	if !found {
		return nil, false
	}

	xlog.Debug("DistributedModelStore: found model in DB, caching locally", "model", id, "node", node.Address)
	// Stub with remote address; nil process is intentional (remote model).
	// The gRPC client is lazily created by Model.GRPC() from the address.
	m := model.NewModel(id, node.Address, nil)
	s.local.Set(id, m)
	return m, true
}

// Set delegates to the local cache. The DB record is already written by
// SmartRouter.Route() via SetNodeModel, so no DB write is needed here.
func (s *DistributedModelStore) Set(id string, m *model.Model) {
	s.local.Set(id, m)
}

// Delete delegates to the local cache. DB cleanup is handled by
// RemoteUnloaderAdapter.UnloadRemoteModel which is called from deleteProcess.
func (s *DistributedModelStore) Delete(id string) {
	s.local.Delete(id)
}

// Range iterates local models first, then queries the DB for any additional
// models not in the local cache.
func (s *DistributedModelStore) Range(fn func(string, *model.Model) bool) {
	// Track which IDs we've already visited
	seen := make(map[string]bool)

	s.local.Range(func(id string, m *model.Model) bool {
		seen[id] = true
		return fn(id, m)
	})

	// Query DB for models not in local cache
	ctx := context.Background()
	dbModels, err := s.registry.ListAllLoadedModels(ctx)
	if err != nil {
		xlog.Warn("DistributedModelStore: failed to list DB models during Range", "error", err)
		return
	}

	for _, nm := range dbModels {
		if seen[nm.ModelName] {
			continue
		}
		seen[nm.ModelName] = true

		// Look up the node address
		node, err := s.registry.Get(ctx, nm.NodeID)
		if err != nil {
			xlog.Warn("DistributedModelStore: failed to get node for model", "model", nm.ModelName, "nodeID", nm.NodeID, "error", err)
			continue
		}

		m := model.NewModel(nm.ModelName, node.Address, nil)
		if !fn(nm.ModelName, m) {
			return
		}
	}
}
