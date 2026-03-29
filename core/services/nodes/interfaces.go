package nodes

import (
	"context"
	"time"

	grpc "github.com/mudler/LocalAI/pkg/grpc"
)

// ModelRouter is used by SmartRouter for routing decisions and model lifecycle.
type ModelRouter interface {
	FindAndLockNodeWithModel(ctx context.Context, modelName string) (*BackendNode, *NodeModel, error)
	DecrementInFlight(ctx context.Context, nodeID, modelName string) error
	IncrementInFlight(ctx context.Context, nodeID, modelName string) error
	RemoveNodeModel(ctx context.Context, nodeID, modelName string) error
	TouchNodeModel(ctx context.Context, nodeID, modelName string)
	SetNodeModel(ctx context.Context, nodeID, modelName, state, address string, initialInFlight int) error
	FindNodeWithVRAM(ctx context.Context, minBytes uint64) (*BackendNode, error)
	FindIdleNode(ctx context.Context) (*BackendNode, error)
	FindLeastLoadedNode(ctx context.Context) (*BackendNode, error)
	FindGlobalLRUModelWithZeroInFlight(ctx context.Context) (*NodeModel, error)
	FindLRUModel(ctx context.Context, nodeID string) (*NodeModel, error)
	Get(ctx context.Context, nodeID string) (*BackendNode, error)
}

// NodeHealthStore is used by HealthMonitor for node status management.
type NodeHealthStore interface {
	List(ctx context.Context) ([]BackendNode, error)
	GetNodeModels(ctx context.Context, nodeID string) ([]NodeModel, error)
	MarkOffline(ctx context.Context, nodeID string) error
	MarkUnhealthy(ctx context.Context, nodeID string) error
	MarkHealthy(ctx context.Context, nodeID string) error
	Heartbeat(ctx context.Context, nodeID string, update *HeartbeatUpdate) error
	FindStaleNodes(ctx context.Context, threshold time.Duration) ([]BackendNode, error)
	RemoveNodeModel(ctx context.Context, nodeID, modelName string) error
}

// ModelLocator is used by RemoteUnloaderAdapter for model discovery.
type ModelLocator interface {
	FindNodesWithModel(ctx context.Context, modelName string) ([]BackendNode, error)
	RemoveNodeModel(ctx context.Context, nodeID, modelName string) error
}

// ModelLookup is used by DistributedModelStore for model existence queries.
type ModelLookup interface {
	FindNodeForModel(ctx context.Context, modelName string) (*BackendNode, bool)
	ListAllLoadedModels(ctx context.Context) ([]NodeModel, error)
	Get(ctx context.Context, nodeID string) (*BackendNode, error)
}

// InFlightTracker is used by InFlightTrackingClient for request counting.
type InFlightTracker interface {
	IncrementInFlight(ctx context.Context, nodeID, modelName string) error
	DecrementInFlight(ctx context.Context, nodeID, modelName string) error
}

// NodeManager is used by HTTP endpoints for node registration and lifecycle.
type NodeManager interface {
	Register(ctx context.Context, node *BackendNode, autoApprove bool) error
	Get(ctx context.Context, nodeID string) (*BackendNode, error)
	GetByName(ctx context.Context, name string) (*BackendNode, error)
	List(ctx context.Context) ([]BackendNode, error)
	Deregister(ctx context.Context, nodeID string) error
	ApproveNode(ctx context.Context, nodeID string) error
	MarkOffline(ctx context.Context, nodeID string) error
	MarkDraining(ctx context.Context, nodeID string) error
	Heartbeat(ctx context.Context, nodeID string, update *HeartbeatUpdate) error
	GetNodeModels(ctx context.Context, nodeID string) ([]NodeModel, error)
	UpdateAuthRefs(ctx context.Context, nodeID, authUserID, apiKeyID string) error
	RemoveNodeModel(ctx context.Context, nodeID, modelName string) error
}

// BackendClientFactory creates gRPC backend clients.
type BackendClientFactory interface {
	NewClient(address string, parallel bool) grpc.Backend
}

// tokenClientFactory is the default BackendClientFactory that creates gRPC
// clients with an optional bearer token for distributed auth.
type tokenClientFactory struct {
	token string
}

func (f *tokenClientFactory) NewClient(address string, parallel bool) grpc.Backend {
	if f.token != "" {
		return grpc.NewClientWithToken(address, parallel, nil, false, f.token)
	}
	return grpc.NewClient(address, parallel, nil, false)
}
