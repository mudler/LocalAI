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
	clients  BackendClientFactory
}

// DistributedModelStoreOption changes how a DistributedModelStore is built.
type DistributedModelStoreOption func(*DistributedModelStore)

// WithClientFactory makes the models that Range builds from the registry carry a
// client from the factory. Without it they carry none, and the first caller that
// asks one for its client gets a plain TCP dial to the address of the node. That
// is a dial that bypasses the carrier: it fails for a worker that holds a tunnel
// and has no address, and it is the one place left that builds a client outside
// the dial seam.
func WithClientFactory(f BackendClientFactory) DistributedModelStoreOption {
	return func(s *DistributedModelStore) { s.clients = f }
}

func NewDistributedModelStore(local model.ModelStore, registry ModelLookup, opts ...DistributedModelStoreOption) *DistributedModelStore {
	s := &DistributedModelStore{local: local, registry: registry}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// stub builds the model that stands for a replica that the registry holds and
// this process has not loaded.
func (s *DistributedModelStore) stub(nodeID, modelName, address string) *model.Model {
	if s.clients == nil {
		return model.NewModel(modelName, address, nil)
	}
	return model.NewModelWithClient(modelName, address, s.clients.NewClient(nodeID, address, false))
}

// Get checks the local cache only. In distributed mode, models must be routed
// through SmartRouter which handles in-flight tracking, file staging, and load
// balancing. The DB fallback was removed to prevent bare model stubs that
// bypass these mechanisms.
func (s *DistributedModelStore) Get(id string) (*model.Model, bool) {
	return s.local.Get(id)
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

	stopped := false
	s.local.Range(func(id string, m *model.Model) bool {
		seen[id] = true
		if !fn(id, m) {
			stopped = true
			return false
		}
		return true
	})
	if stopped {
		return // caller said stop, respect it
	}

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

		m := s.stub(nm.NodeID, nm.ModelName, node.Address)
		if !fn(nm.ModelName, m) {
			return
		}
	}
}
