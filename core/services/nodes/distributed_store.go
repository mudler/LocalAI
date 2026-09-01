package nodes

import (
	"context"
	"fmt"

	grpc "github.com/mudler/LocalAI/pkg/grpc"
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
	// clients builds the gRPC client for a model that lives on a worker.
	//
	// It is not optional in a real deployment, and the reason is the second
	// construction path this store used to be: a *model.Model built with a nil
	// client makes pkg/model.Model.GRPC dial its raw address with gRPC's own
	// dialer the first time anything touches it, which is exactly the direct
	// dial to a worker's advertised address the tunnel replaces. That path is
	// reached in production, by ShutdownModel's Free and by the backend
	// monitor's Status, so it is not theoretical.
	clients BackendClientFactory
}

// NewDistributedModelStore returns the store, which reaches a remote model's
// backend through clients.
//
// A nil clients is a programming error and is treated as one: Range refuses to
// synthesise a model it cannot give a working client to, rather than handing
// back one that silently dials the worker's address. See the field comment.
func NewDistributedModelStore(local model.ModelStore, registry ModelLookup, clients BackendClientFactory) *DistributedModelStore {
	return &DistributedModelStore{local: local, registry: registry, clients: clients}
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

		// NewModelWithClient, never NewModel: a model built without a client
		// lazily dials its address with gRPC's default dialer the first time
		// anything calls GRPC() on it, which reaches a worker only while
		// workers still listen on a routable address. Building the client here
		// means the bypass has no path left rather than an unused one.
		client, err := s.clientFor(nm.NodeID, node.Address)
		if err != nil {
			xlog.Error("DistributedModelStore: not listing a remote model it cannot reach",
				"model", nm.ModelName, "nodeID", nm.NodeID, "error", err)
			continue
		}
		m := model.NewModelWithClient(nm.ModelName, node.Address, client)
		if !fn(nm.ModelName, m) {
			return
		}
	}
}

// clientFor builds the backend client for a model running on a worker.
//
// It fails rather than falling back. A fallback here would be invisible: the
// listing would look complete, shutdown would appear to work, and the direct
// dial underneath it would succeed on a single-host developer setup and fail
// against every worker that has no inbound port.
func (s *DistributedModelStore) clientFor(nodeID, address string) (grpc.Backend, error) {
	if s.clients == nil {
		return nil, fmt.Errorf("no backend client factory is wired into the distributed model store: %w", ErrNoWorkerDialer)
	}
	return s.clients.NewClientForNode(nodeID, address, false)
}
