package nodes

import (
	"context"
	"net"
	"time"

	"github.com/mudler/LocalAI/core/services/workerctl"
	grpc "github.com/mudler/LocalAI/pkg/grpc"
)

type ExactModelStopper interface {
	StopModelReplica(ctx context.Context, nodeID string, replica NodeModel, force bool) (workerctl.ModelStopReply, error)
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
	FindLRUModel(ctx context.Context, nodeID string, excludeModels []string) (*NodeModel, error)
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
	UpdateLoadJob(ctx context.Context, ref LoadJobRef, u LoadJobUpdate) error
	FailLoadJob(ctx context.Context, ref LoadJobRef, msg string, workMayRun bool) error
	DeleteLoadJob(ctx context.Context, ref LoadJobRef) error
	DeleteFailedLoadJob(ctx context.Context, ref LoadJobRef) error
	ConfirmLoadOp(ctx context.Context, ref LoadJobRef) error
}

// ReplicaUnloader unloads one replica by the address its row recorded. The
// eviction and scale-down paths use it: they delete the row first, so the
// address must travel with the call.
type ReplicaUnloader interface {
	UnloadReplica(nodeID string, replica NodeModel) error
}

// LoadOperationInstaller starts a backend as a load operation the worker bounds.
type LoadOperationInstaller interface {
	InstallBackendOp(nodeID, backendType, modelID, galleriesJSON string, replicaIndex int, opID, operationID string, deadline time.Duration, onProgress func(workerctl.BackendInstallProgressEvent)) (*workerctl.BackendInstallReply, error)
}

// LoadOperationStopper is the only path that kills remote load work. It stops
// one operation by id and never "any running backend".
type LoadOperationStopper interface {
	StopLoadOperation(ctx context.Context, nodeID string, req workerctl.ModelStopRequest) (workerctl.ModelStopReply, error)
}

// LoadOperationRenewer renews and completes load operations on a worker node.
type LoadOperationRenewer interface {
	OperationControl(nodeID string, req workerctl.OperationRequest) (*workerctl.OperationReply, error)
}

// LoadAttemptStopper is what stopping a failed or cancelled attempt needs: the
// operation stop for a worker that names operations, and the exact-address stop
// for a worker that does not.
type LoadAttemptStopper interface {
	LoadOperationStopper
	ExactModelStopper
}

// LoadOperationControl is the part of a carrier that bounds, renews and stops
// remote load work. Every carrier of NodeCommandSender must provide it; there
// is no degraded mode in which a sender silently lacks it. A worker that cannot
// name operations is a fact about that worker, reported by its install reply
// (BackendInstallReply.ReportsOperations), and is handled per node.
//
// What a carrier owes the callers:
//
//   - Every method is a request and a reply with its own timeout. A call that
//     gets no reply in time returns an error. It must never block for longer
//     than its documented bound, because the owner loop and the reconciler call
//     these from timers.
//
//   - OperationControl carries renewals and completions. Callers send a renewal
//     every 5 seconds for each running load. The worker kills an operation that
//     goes 90 seconds without one, so a carrier must deliver a renewal within
//     one cadence (5 s, bounded by the 5 s call timeout) or return an error
//     promptly. A lost renewal costs nothing until the kill TTL. A completion is
//     retried by the caller and its loss is not fatal.
//
//   - StopLoadOperation is idempotent. It addresses one operation by operation
//     id, process key, process instance and address, and the worker refuses
//     unless the ones given match its own records. A repeat after a stop
//     returns a reply with Terminated set, not an error. A reply with Error set
//     is the worker's refusal: the worker is present and said no.
//
//   - InstallBackendOp is InstallBackend that names the operation and its
//     longest run as a duration. The deadline is relative so clocks do not
//     matter.
//
//   - UnloadReplica names the replica's address. With no address nothing is
//     sent: a carrier must never ask a worker to pick a process.
//
//   - Errors keep the four conditions apart (see .agents/distributed-seams.md).
//     ErrNoRoute is a routing fact only: no route from here right now. A
//     timeout is not ErrNoRoute. An unreachable peer is not a verdict about the
//     worker. A worker's own answer, including a refusal, is a nil error with
//     the refusal in the reply. Only the carrier maps its own sentinel onto
//     ErrNoRoute.
type LoadOperationControl interface {
	LoadOperationInstaller
	LoadOperationStopper
	LoadOperationRenewer
	ReplicaUnloader
	ExactModelStopper
}

// ConcurrencyConflictResolver returns the names of configured models that
// share at least one concurrency group with the given model. It is satisfied
// by *config.ModelConfigLoader and lets the SmartRouter make group-aware
// placement decisions without importing the config package's full surface.
type ConcurrencyConflictResolver interface {
	GetModelsConflictingWith(modelName string) []string
}

// PinnedModelResolver reports which configured models are pinned. Satisfied
// by *config.ModelConfigLoader. The router's eviction paths and the
// reconciler's idle scale-down exclude these models so `pinned: true` holds
// cluster-wide, not just against the per-node watchdog (#11101). Deliberate
// teardown (admin unload, model delete, node drain) intentionally bypasses it.
type PinnedModelResolver interface {
	GetPinnedModelNames() []string
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

// BackendClientFactory creates gRPC backend clients. It takes the node id
// because a dialer that must know WHICH node it is reaching, as a tunnel does,
// cannot recover it from the address; a direct dialer ignores it.
type BackendClientFactory interface {
	NewClient(nodeID, address string, parallel bool) grpc.Backend
}

// tokenClientFactory is the default BackendClientFactory that creates gRPC
// clients with an optional bearer token for distributed auth.
type tokenClientFactory struct {
	token string
}

func (f *tokenClientFactory) NewClient(_, address string, parallel bool) grpc.Backend {
	if f.token != "" {
		return grpc.NewClientWithToken(address, parallel, nil, false, f.token)
	}
	return grpc.NewClient(address, parallel, nil, false)
}

// WorkerNetDialerFor returns the dial function that reaches one worker's own
// HTTP server, in the shape http.Transport.DialContext and
// websocket.Dialer.NetDialContext take. It is keyed by node id, not address,
// because two workers can report the same HTTP address (NAT, loopback) and a
// tunnel must still reach the right one.
type WorkerNetDialerFor func(nodeID string) func(ctx context.Context, network, addr string) (net.Conn, error)

// DirectWorkerNetDialer dials the address it is handed, whatever the node.
func DirectWorkerNetDialer() WorkerNetDialerFor {
	// Aggressive keepalive suits the long LAN transfers the file stager makes.
	dial := (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 15 * time.Second}).DialContext
	return func(string) func(context.Context, string, string) (net.Conn, error) { return dial }
}
