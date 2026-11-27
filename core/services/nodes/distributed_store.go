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

// Range lists the models of this process and the models the registry holds.
//
// The registry is the source of truth for remote models. A local entry without
// a process is only a stub for a model this frontend routed, and the hook that
// drops such a stub runs only in the frontend that deleted the registry rows.
// With several frontends, the others keep their stubs after a drain or a
// deregistration. So Range lists a stub only while the registry still holds its
// model, and drops the stub when it does not. If the registry query fails,
// Range lists every local entry: a failed query is no evidence that a model is
// gone.
func (s *DistributedModelStore) Range(fn func(string, *model.Model) bool) {
	ctx := context.Background()
	dbModels, dbErr := s.registry.ListAllLoadedModels(ctx)
	if dbErr != nil {
		xlog.Warn("DistributedModelStore: failed to list DB models during Range, listing local models without a check", "error", dbErr)
	}
	inRegistry := make(map[string]bool, len(dbModels))
	for _, nm := range dbModels {
		inRegistry[nm.ModelName] = true
	}

	seen := make(map[string]bool)
	stale := map[string]*model.Model{}
	stopped := false
	s.local.Range(func(id string, m *model.Model) bool {
		if dbErr == nil && m.Process() == nil && !inRegistry[id] {
			stale[id] = m
			return true
		}
		seen[id] = true
		if !fn(id, m) {
			stopped = true
			return false
		}
		return true
	})

	// Delete after the local Range returns: the local store holds its lock
	// while it iterates.
	s.dropStaleStubs(stale)

	if stopped || dbErr != nil {
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

// dropStaleStubs deletes the stubs that the registry no longer holds, so Get
// and the shutdown path stop seeing them too. A stub is deleted only if it is
// still the same entry: a request can route the model again while Range runs,
// and the new stub belongs to a replica the registry holds.
func (s *DistributedModelStore) dropStaleStubs(stale map[string]*model.Model) {
	for id, m := range stale {
		if cur, ok := s.local.Get(id); !ok || cur != m {
			continue
		}
		s.local.Delete(id)
		xlog.Debug("Dropped local model stub that no healthy node holds", "model", id)
	}
}
