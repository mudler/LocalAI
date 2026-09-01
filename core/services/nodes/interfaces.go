package nodes

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/mudler/LocalAI/core/services/messaging"
	grpc "github.com/mudler/LocalAI/pkg/grpc"
)

type ExactModelStopper interface {
	StopModelReplica(ctx context.Context, nodeID string, replica NodeModel, force bool) (messaging.ModelStopReply, error)
}

type ModelCleanupRegistry interface {
	ClaimModelCleanupRetries(ctx context.Context, now, leaseUntil time.Time, limit int) ([]NodeModel, error)
	RecordModelCleanupFailure(ctx context.Context, nodeID, modelName string, replicaIndex int, cleanupErr string, nextRetry time.Time) error
	RemoveClaimedModelCleanup(ctx context.Context, replica NodeModel) (bool, error)
}

// ModelRouter is used by SmartRouter for routing decisions and model lifecycle.
type ModelRouter interface {
	FindAndLockNodeWithModel(ctx context.Context, modelName string, candidateNodeIDs []string, pref *RoutePreference) (*BackendNode, *NodeModel, error)
	DecrementInFlight(ctx context.Context, nodeID, modelName string, replicaIndex int) error
	IncrementInFlight(ctx context.Context, nodeID, modelName string, replicaIndex int) error
	RemoveNodeModel(ctx context.Context, nodeID, modelName string, replicaIndex int) error
	RemoveAllNodeModelReplicas(ctx context.Context, nodeID, modelName string) error
	TouchNodeModel(ctx context.Context, nodeID, modelName string, replicaIndex int)
	SetNodeModel(ctx context.Context, nodeID, modelName string, replicaIndex int, state, address string, initialInFlight int) error
	SetNodeModelRevision(ctx context.Context, nodeID, modelName string, replicaIndex int, state, address string, initialInFlight int, revision, effectiveOptionsHash string) error
	SetNodeModelLoadInfo(ctx context.Context, nodeID, modelName string, replicaIndex int, backendType string, optsBlob []byte) error
	SetNodeModelLoadInfoRevision(ctx context.Context, nodeID, modelName string, replicaIndex int, backendType, revision string, optsBlob []byte) error
	UpsertModelLoadInfo(ctx context.Context, modelName, backendType string, optsBlob []byte) error
	UpsertModelLoadInfoRevision(ctx context.Context, modelName, backendType, revision string, optsBlob []byte) error
	GetModelLoadInfo(ctx context.Context, modelName string) (backendType string, optsBlob []byte, err error)
	GetModelLoadInfoRevision(ctx context.Context, modelName string) (backendType, revision string, optsBlob []byte, err error)
	AdvanceModelConfigRevision(ctx context.Context, modelName, revision string) ([]NodeModel, error)
	EstablishModelConfigRevision(ctx context.Context, modelName, revision string) error
	GetModelConfigRevision(ctx context.Context, modelName string) (string, error)
	GetNodeModel(ctx context.Context, nodeID, modelName string, replicaIndex int) (*NodeModel, error)
	RecordModelCleanupFailure(ctx context.Context, nodeID, modelName string, replicaIndex int, cleanupErr string, nextRetry time.Time) error
	ListModelCleanupRetries(ctx context.Context, now time.Time, limit int) ([]NodeModel, error)
	NextFreeReplicaIndex(ctx context.Context, nodeID, modelName string, maxSlots int) (int, error)
	CountReplicasOnNode(ctx context.Context, nodeID, modelName string) (int, error)
	FindNodeWithVRAM(ctx context.Context, minBytes uint64) (*BackendNode, error)
	FindIdleNode(ctx context.Context) (*BackendNode, error)
	FindLeastLoadedNode(ctx context.Context) (*BackendNode, error)
	FindGlobalLRUModelWithZeroInFlight(ctx context.Context) (*NodeModel, error)
	FindLRUModel(ctx context.Context, nodeID string) (*NodeModel, error)
	Get(ctx context.Context, nodeID string) (*BackendNode, error)
	GetModelScheduling(ctx context.Context, modelName string) (*ModelSchedulingConfig, error)
	GetGoverningScheduling(ctx context.Context, modelName string) (*ModelSchedulingConfig, error)
	FindNodesBySelector(ctx context.Context, selector map[string]string) ([]BackendNode, error)
	FindNodesWithFreeSlot(ctx context.Context, modelName string, candidateNodeIDs []string) ([]BackendNode, error)
	NarrowByDiskHeadroom(ctx context.Context, candidateNodeIDs []string, required uint64) ([]string, error)
	ReserveVRAM(ctx context.Context, nodeID string, bytes uint64) error
	ReleaseVRAM(ctx context.Context, nodeID string, bytes uint64) error
	FindNodeWithVRAMFromSet(ctx context.Context, minBytes uint64, nodeIDs []string) (*BackendNode, error)
	FindIdleNodeFromSet(ctx context.Context, nodeIDs []string) (*BackendNode, error)
	FindLeastLoadedNodeFromSet(ctx context.Context, nodeIDs []string) (*BackendNode, error)
	GetNodeLabels(ctx context.Context, nodeID string) ([]NodeLabel, error)
	FindNodesWithModel(ctx context.Context, modelName string) ([]BackendNode, error)
	LoadedReplicaStats(ctx context.Context, modelName string, candidateNodeIDs []string) ([]ReplicaCandidate, error)
	MarkUnhealthy(ctx context.Context, nodeID string) error
	LoadJobStore
}

// LoadJobStore is the durable cold-load job record SmartRouter uses to
// de-duplicate concurrent loaders across replicas without holding the per-model
// advisory lock for the whole load. See ModelLoadJob.
type LoadJobStore interface {
	ClaimLoadJob(ctx context.Context, trackingKey, owner string) (*ModelLoadJob, bool, error)
	GetLoadJob(ctx context.Context, trackingKey string) (*ModelLoadJob, error)
	UpdateLoadJob(ctx context.Context, trackingKey string, u LoadJobUpdate) error
	FailLoadJob(ctx context.Context, trackingKey, msg string) error
	DeleteLoadJob(ctx context.Context, trackingKey string) error
}

// ConcurrencyConflictResolver returns the names of configured models that
// share at least one concurrency group with the given model. It is satisfied
// by *config.ModelConfigLoader and lets the SmartRouter make group-aware
// placement decisions without importing the config package's full surface.
type ConcurrencyConflictResolver interface {
	GetModelsConflictingWith(modelName string) []string
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
	RemoveNodeModel(ctx context.Context, nodeID, modelName string, replicaIndex int) error
}

// ModelLocator is used by RemoteUnloaderAdapter for model discovery.
type ModelLocator interface {
	FindNodesWithModel(ctx context.Context, modelName string) ([]BackendNode, error)
	RemoveNodeModel(ctx context.Context, nodeID, modelName string, replicaIndex int) error
	RemoveAllNodeModelReplicas(ctx context.Context, nodeID, modelName string) error
}

// ModelLookup is used by DistributedModelStore for model existence queries.
type ModelLookup interface {
	FindNodeForModel(ctx context.Context, modelName string) (*BackendNode, bool)
	ListAllLoadedModels(ctx context.Context) ([]NodeModel, error)
	Get(ctx context.Context, nodeID string) (*BackendNode, error)
}

// InFlightTracker is used by InFlightTrackingClient for request counting.
type InFlightTracker interface {
	IncrementInFlight(ctx context.Context, nodeID, modelName string, replicaIndex int) error
	DecrementInFlight(ctx context.Context, nodeID, modelName string, replicaIndex int) error
	// RemoveNodeModel drops a stale replica row so the next request reloads the
	// model instead of routing back to a node where it is no longer loaded.
	RemoveNodeModel(ctx context.Context, nodeID, modelName string, replicaIndex int) error
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
	MarkHealthy(ctx context.Context, nodeID string) error
	Heartbeat(ctx context.Context, nodeID string, update *HeartbeatUpdate) error
	GetNodeModels(ctx context.Context, nodeID string) ([]NodeModel, error)
	UpdateAuthRefs(ctx context.Context, nodeID, authUserID, apiKeyID string) error
	RemoveNodeModel(ctx context.Context, nodeID, modelName string, replicaIndex int) error
	RemoveAllNodeModelReplicas(ctx context.Context, nodeID, modelName string) error
}

// WorkerDialerFor hands back the dial function for one worker's backend
// processes: the shape grpc.WithContextDialer wants, bound to a node.
//
// It is declared here rather than taken as a core/services/cluster type so that
// this package keeps no dependency on that one. cluster is a leaf and imports
// neither this package nor core/http; the wiring in core/application supplies
// (*cluster.WorkerDialer).GRPCDialerFor, which has exactly this shape.
type WorkerDialerFor func(nodeID string) func(ctx context.Context, addr string) (net.Conn, error)

// WorkerNetDialerFor hands back the dial function for one worker's own HTTP
// server, in the shape http.Transport.DialContext and websocket.Dialer's
// NetDialContext want. (*cluster.WorkerDialer).DialerFor bound to the http tag
// has this shape.
type WorkerNetDialerFor func(nodeID string) func(ctx context.Context, network, addr string) (net.Conn, error)

// ErrNoWorkerDialer reports that something tried to reach a worker without a
// way to reach it through the worker's tunnel.
//
// It is deliberately an ERROR and not a fallback to dialling the worker's
// advertised address. A worker that holds a tunnel need not listen on anything
// and may be behind NAT with no address to dial, so the fallback would work
// only where the tunnel was not needed: on a single-host developer setup, and
// nowhere the feature exists for. It is also not an absence error: nothing
// about it says the worker is gone.
var ErrNoWorkerDialer = errors.New("nodes: no worker tunnel dialer is configured, so this worker cannot be reached")

// BackendClientFactory creates the gRPC clients this frontend uses to reach
// model backends running on worker nodes.
//
// There is ONE method, and that is the design rather than an omission. A
// direct-dial constructor alongside it would be reachable from every call site
// that has an address, which is all of them, and the whole of this change is
// that having an address is no longer enough to reach a backend. Callers that
// genuinely want a raw address call pkg/grpc directly and are visible as such.
type BackendClientFactory interface {
	// NewClientForNode reaches a backend process running on a WORKER, through
	// that worker's tunnel. address names WHICH process on the worker; it is
	// not somewhere this process connects to.
	//
	// It returns an error rather than a client that falls back to a direct
	// dial, so that a deployment with no tunnel dialer fails where the mistake
	// is instead of quietly reopening the bypass.
	NewClientForNode(nodeID, address string, parallel bool) (grpc.Backend, error)
}

// tokenClientFactory is the BackendClientFactory for a deployment with no
// worker tunnel dialer, which is a misconfiguration rather than a mode. It
// refuses every request, loudly, and reaches no worker.
//
// It exists so that the components that take a factory have something to hold
// when none was wired, instead of a nil they would have to guard at every use.
type tokenClientFactory struct {
	token string
}

// NewClientForNode refuses. See ErrNoWorkerDialer for why this is not a direct
// dial to address. The token this factory carries is the one a working dialer
// would have used, kept only so the misconfiguration is repairable by wiring a
// dialer rather than by also re-plumbing credentials.
func (f *tokenClientFactory) NewClientForNode(nodeID, address string, _ bool) (grpc.Backend, error) {
	return nil, fmt.Errorf("reaching backend %q on node %q: %w", address, nodeID, ErrNoWorkerDialer)
}

// tunnelClientFactory reaches a worker's backend processes through the worker's
// tunnel, and is what every distributed deployment uses.
type tunnelClientFactory struct {
	token   string
	dialFor WorkerDialerFor
}

// NewTunnelClientFactory returns the factory that reaches worker backends
// through dialFor. A nil dialFor is refused rather than degraded: this
// constructor exists to close the direct-dial bypass, and one that silently
// handed back a direct-dialling factory would reopen it for the whole process.
func NewTunnelClientFactory(token string, dialFor WorkerDialerFor) (BackendClientFactory, error) {
	if dialFor == nil {
		return nil, fmt.Errorf("building the worker backend client factory: %w", ErrNoWorkerDialer)
	}
	return &tunnelClientFactory{token: token, dialFor: dialFor}, nil
}

func (f *tunnelClientFactory) NewClientForNode(nodeID, address string, parallel bool) (grpc.Backend, error) {
	if nodeID == "" {
		// Without a node there is no tunnel to pick, and the only thing left to
		// do with the address would be to dial it.
		return nil, fmt.Errorf("reaching backend %q: no node id: %w", address, ErrNoWorkerDialer)
	}
	dial := f.dialFor(nodeID)
	if dial == nil {
		return nil, fmt.Errorf("reaching backend %q on node %q: %w", address, nodeID, ErrNoWorkerDialer)
	}
	return grpc.NewClientWithDialer(address, parallel, nil, false, f.token, dial), nil
}
