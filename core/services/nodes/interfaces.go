package nodes

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/mudler/LocalAI/core/services/cluster"
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
// A function type rather than a *cluster.WorkerDialer, so nothing here is bound
// to that concrete type and a spec can supply a dial without building a tunnel
// registry, a peer pool and a database. It is NOT to avoid a dependency: this
// package already imports core/services/cluster (registry.go, for Migrate), and
// an earlier version of this comment claimed otherwise. The dependency that
// does matter runs the other way, and cluster is held to it by go list -deps.
//
// core/application supplies (*cluster.WorkerDialer).GRPCDialerFor, which has
// exactly this shape.
type WorkerDialerFor func(nodeID string) func(ctx context.Context, addr string) (net.Conn, error)

// WorkerNetDialerFor hands back the dial function for one worker's own HTTP
// server, in the shape http.Transport.DialContext and websocket.Dialer's
// NetDialContext want. (*cluster.WorkerDialer).DialerFor bound to the http tag
// has this shape.
type WorkerNetDialerFor func(nodeID string) func(ctx context.Context, network, addr string) (net.Conn, error)

// ErrWorkerUnroutable reports that this frontend could not get a request to a
// worker's backend, and says NOTHING about whether that worker or its backend
// is alive.
//
// It is the fifth condition, on this side of the package boundary. A worker's
// presence is its HEARTBEAT, and this package owns that; a route to it is a
// separate fact owned by core/services/cluster, and the two now differ. A
// worker can be registered, heartbeating and serving every request another
// replica sends it while being unroutable from here: it has not dialled its
// tunnel yet after a frontend-first upgrade, the replica holding its tunnel is
// restarting, the ownership row is a moment stale, this replica has no peer
// mesh. Every one of those used to be indistinguishable from "the backend
// process died", because gRPC reports both as codes.Unavailable.
//
// Everything in this package that DELETES a node_models row must consult it
// first. That is the phase's stated catastrophe in its concrete form: a row
// deleted here is a model reclaimed and reloaded elsewhere, so mistaking a peer
// link blip for a dead backend evicts healthy work across the fleet at once.
var ErrWorkerUnroutable = errors.New("nodes: this frontend has no route to that worker")

// ErrNoWorkerDialer reports that something tried to reach a worker without a
// way to reach it through the worker's tunnel.
//
// It is deliberately an ERROR and not a fallback to dialling the worker's
// advertised address. A worker that holds a tunnel need not listen on anything
// and may be behind NAT with no address to dial, so the fallback would work
// only where the tunnel was not needed: on a single-host developer setup, and
// nowhere the feature exists for.
//
// It is a SPECIALISATION of ErrWorkerUnroutable rather than a sibling, so the
// single check every reaping path makes covers both. The difference between
// them is only when they happen: this one is a boot-time misconfiguration, and
// the general one is a running deployment losing a route for a moment. Neither
// is a statement about the worker.
var ErrNoWorkerDialer = fmt.Errorf("%w: no worker tunnel dialer is configured", ErrWorkerUnroutable)

// unroutable reports why a call on client never reached the backend, or nil
// when it did reach one.
//
// This is where core/services/cluster's five conditions cross the package
// boundary. They cannot cross on the RPC error: gRPC turns any dialer failure
// into codes.Unavailable with the cause flattened into a message, and
// codes.Unavailable is ALSO what a backend process that has died produces.
// pkg/grpc records the dialer's error VALUE instead, so cluster.ErrNoRoute and
// whatever sits under it are still matchable here.
//
// A client that reports nothing (no custom dialer, or a test double) yields
// nil, which means "the call reached a backend" and preserves the behaviour
// every non-distributed caller has always had. Decorators are looked through;
// see grpc.BackendUnwrapper for why that is not optional.
//
// A WORKER'S OWN REFUSAL also yields nil, and that is the second half of the
// contract rather than a loophole. cluster.Dial keeps the three tunnelproto
// sentinels out of the ErrNoRoute umbrella precisely so this function can tell
// them apart, and for a whole phase nothing did: a backend process that crashed
// on a healthy worker is no longer a dead listener's codes.Unavailable, it is
// the worker refusing the stream with cluster.ErrStreamTargetUnavailable, which
// gRPC then flattens into codes.Unavailable anyway. Reporting that as
// unroutable made every reap path answer ProbeUnknown and leave the row, so the
// replica slot never freed and (at the default MaxReplicasPerModel=1) the only
// remaining cleanup was LRU eviction of HEALTHY models. A worker that answers
// has demonstrated it is there, so the answer is evidence about its backend and
// the reap guards may act on it.
//
// All three sentinels, not only the unavailable one, and the difference is
// worth stating because two of them are not observations about the process. An
// unknown tag means this worker does not serve gRPC streams at all; an invalid
// request means the stored address is not a port in this worker's range.
// Neither clears on its own, so a row that carries one is unreachable from
// EVERY replica for as long as it exists, and reaping it converges: the model
// is reloaded somewhere that works and re-registers a usable address. The
// condition the phase refuses to reap on is a TRANSIENT one, and none of these
// is transient.
//
// That last sentence is a claim about the WORKER, not about this file, and it
// held only after the worker stopped answering a request frame that merely
// arrived late with ErrStreamRequestInvalid. It did, and the frontend's half of
// that contract is that a transient condition arrives as the fourth code:
// cluster.ErrStreamNotServed is not in cluster.IsWorkerAnswer, so it reaches
// here under the no-route umbrella and reaps nothing. If a worker ever starts
// sending one of the three for something that clears on its own, this comment
// becomes false and a live model gets evicted; the guard against that is at the
// worker, in Tunnel.accept and classifyServiceFailure, and it is stated there.
//
// A reply code this frontend does not recognise is deliberately not in the set
// either (see cluster.IsWorkerAnswer), so a newer worker's vocabulary reaches
// an older frontend as "no route" and costs a retry rather than a row.
func unroutable(client grpc.Backend) error {
	// LastDialErrorOf and not a type assertion: the assertion could not see
	// past a decorator, and SmartRouter hands every routed client out wrapped.
	dialErr := grpc.LastDialErrorOf(client)
	if dialErr == nil {
		return nil
	}
	if cluster.IsWorkerAnswer(dialErr) {
		return nil
	}
	// Multi-%w: the umbrella this package acts on, and the cluster condition
	// underneath it, both stay matchable.
	return fmt.Errorf("%w: %w", ErrWorkerUnroutable, dialErr)
}

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

// unroutableHostSuffix is appended to a node id to build a Host for a worker
// that reports no HTTP address.
//
// .invalid is reserved by RFC 2606 and resolves nowhere, which is the point:
// the string exists ONLY to fill the host component of a URL, and a value that
// could resolve would be one a future refactor could accidentally connect to.
const unroutableHostSuffix = ".worker.invalid:80"

// WorkerHTTPHost is the host to put in a URL addressed to a worker's own HTTP
// server.
//
// A tunnel-only worker has no inbound address to report, and after this phase
// it does not need one: the `http` stream tag ignores the target entirely and
// the worker routes the stream to its own server wherever that bound. But an
// http.Request still needs a host, so refusing an empty HTTPAddress would
// refuse exactly the workers the tunnel exists for. This returns a name that
// identifies the node for logs and for the Host header, and that nothing can
// connect to.
//
// It is NOT a dial target and never becomes one. Every caller pairs it with a
// transport whose DialContext is that node's tunnel, so the host is read and
// discarded; see cluster.WorkerDialer.DialerFor and the `http` tag.
func WorkerHTTPHost(nodeID, httpAddress string) string {
	if httpAddress != "" {
		return httpAddress
	}
	return nodeID + unroutableHostSuffix
}
