package nodes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/workerctl"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/xlog"
)

// NodeCommandSender abstracts the control commands a frontend issues to a
// worker node. They travel over the worker's tunnel as HTTP under
// workerctl.Prefix; see RemoteUnloaderAdapter.
//
// InstallBackend is idempotent: the worker short-circuits if the backend is
// already running for the requested (modelID, replica) slot. Routine model
// loads and admin installs both call this.
//
// UpgradeBackend is the destructive force-reinstall path: the worker stops
// every live process for the backend, re-pulls the gallery artifact, and
// replies. Caller (DistributedBackendManager.UpgradeBackend) handles
// rolling-update fallback to the legacy install Force=true path on
// ErrWorkerControlUnsupported, which is what a worker older than the
// backend.upgrade verb answers.
type NodeCommandSender interface {
	InstallBackend(nodeID, backendType, modelID, galleriesJSON, uri, name, alias string, replicaIndex int, opID string, onProgress func(messaging.BackendInstallProgressEvent)) (*messaging.BackendInstallReply, error)
	UpgradeBackend(nodeID, backendType, galleriesJSON, uri, name, alias string, replicaIndex int, opID string, onProgress func(messaging.BackendInstallProgressEvent)) (*messaging.BackendUpgradeReply, error)
	DeleteBackend(nodeID, backendName string) (*messaging.BackendDeleteReply, error)
	ListBackends(nodeID string) (*messaging.BackendListReply, error)
	StopBackend(nodeID, backend string) error
	UnloadModelOnNode(nodeID, modelName string) error
}

// RemoteUnloaderAdapter implements NodeCommandSender and
// model.RemoteModelUnloader by issuing control RPCs to the worker over its
// tunnel. The worker serves them on the loopback HTTP server it already runs
// (see core/services/worker/control_routes.go) and handles the actual process
// start/stop.
//
// This mirrors the local ModelLoader's startProcess()/deleteProcess() but for
// remote nodes.
//
// Every verb it sends, for every kind of worker, is a control RPC. It holds no
// publisher at all, which is why re-routing any of them back onto the bus is a
// change to this struct and to every caller of the constructor rather than to
// one branch.
type RemoteUnloaderAdapter struct {
	registry       ModelLocator
	control        *ControlClient
	installTimeout time.Duration
	upgradeTimeout time.Duration
}

// NewRemoteUnloaderAdapter creates a new adapter. control carries every verb.
// installTimeout and upgradeTimeout bound the backend.install and
// backend.upgrade RPCs respectively; use
// DistributedConfig.BackendInstallTimeoutOrDefault() /
// BackendUpgradeTimeoutOrDefault() at construction.
func NewRemoteUnloaderAdapter(registry ModelLocator, control *ControlClient, installTimeout, upgradeTimeout time.Duration) *RemoteUnloaderAdapter {
	return &RemoteUnloaderAdapter{
		registry:       registry,
		control:        control,
		installTimeout: installTimeout,
		upgradeTimeout: upgradeTimeout,
	}
}

// InstallTimeout returns the configured backend.install round-trip timeout.
// Used by DistributedBackendManager to push NextRetryAt out by this duration
// when a worker times out replying but is still installing in the background.
func (a *RemoteUnloaderAdapter) InstallTimeout() time.Duration {
	return a.installTimeout
}

// Compile-time proof that the adapter still satisfies the loader's optional
// extensions. Both are consumed via runtime type assertion in deleteProcess, so
// a signature drift here would silently downgrade behavior — losing force
// propagation, or making ShutdownModel unable to tell a cluster-wide miss from
// a completed unload — rather than failing the build.
var (
	_ model.RemoteModelUnloader        = (*RemoteUnloaderAdapter)(nil)
	_ model.RemoteModelContextUnloader = (*RemoteUnloaderAdapter)(nil)
	_ model.RemoteModelPresenceChecker = (*RemoteUnloaderAdapter)(nil)
	_ ExactModelStopper                = (*RemoteUnloaderAdapter)(nil)
)

const exactModelStopTimeout = 10 * time.Second

// StopModelReplica stops only the process represented by replica. Configuration
// cleanup intentionally has no backend.stop fallback: an old worker that does
// not understand this request leaves the quarantine row for a later retry.
//
// The caller's context is carried into the RPC rather than run alongside it on
// a goroutine, which is what the request/reply carrier needed because it took a
// timeout and not a context. Abandoning the request is safe: the worker's
// model.stop handler deliberately drops the caller's context and runs the stop
// to completion, so a frontend that gives up cannot leave a half-stopped
// process or an unreturned port behind.
func (a *RemoteUnloaderAdapter) StopModelReplica(ctx context.Context, nodeID string, replica NodeModel, force bool) (messaging.ModelStopReply, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, exactModelStopTimeout)
	defer cancel()

	var reply messaging.ModelStopReply
	err := a.control.Call(ctx, nodeID, workerctl.PathModelStop, messaging.ModelStopRequest{
		ModelName:       replica.ModelName,
		ProcessKey:      model.BackendProcessKey(replica.ModelName, replica.ReplicaIndex),
		ExpectedAddress: replica.WorkerLocalAddress,
		Force:           force,
		ConfigRevision:  replica.ConfigRevision,
	}, &reply)
	if err != nil {
		return messaging.ModelStopReply{}, err
	}
	return reply, nil
}

// UnloadRemoteModel finds the node(s) hosting the given model and tells each
// to stop its backend process, over that node's own tunnel whatever kind of
// worker it is.
// The worker process handles a bounded Free() followed by process termination;
// forced shutdown skips Free().
// This is called by ModelLoader.deleteProcess() when process == nil (remote model).
func (a *RemoteUnloaderAdapter) UnloadRemoteModel(modelName string) error {
	return a.UnloadRemoteModelContext(context.Background(), modelName, false)
}

// HasRemoteModel reports whether any node currently holds the model. It exists
// because UnloadRemoteModel is idempotent and so cannot signal "there was
// nothing to stop"; ShutdownModel consults this first so it can answer 404 for
// a model loaded neither locally nor anywhere in the cluster, instead of the
// misleading 500 "model not found" that a local-store miss used to produce.
func (a *RemoteUnloaderAdapter) HasRemoteModel(ctx context.Context, modelName string) (bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	nodes, err := a.registry.FindNodesWithModel(ctx, modelName)
	if err != nil {
		return false, fmt.Errorf("finding nodes with model %q: %w", modelName, err)
	}
	return len(nodes) > 0, nil
}

// UnloadRemoteModelContext is the cancellation-aware extension used by the
// model loader to preserve forced shutdown across the distributed boundary.
func (a *RemoteUnloaderAdapter) UnloadRemoteModelContext(ctx context.Context, modelName string, force bool) error {
	if ctx == nil {
		ctx = context.Background()
	}
	nodes, err := a.registry.FindNodesWithModel(ctx, modelName)
	if err != nil {
		return fmt.Errorf("finding nodes with model %q: %w", modelName, err)
	}
	if len(nodes) == 0 {
		// Unloading is idempotent by contract: cleanup paths (model deletion,
		// config edits, watchdog eviction) legitimately run against an
		// already-unloaded model and must not fail. Callers that need to tell
		// this case apart use HasRemoteModel before unloading.
		xlog.Debug("No remote nodes found with model", "model", modelName)
		return nil
	}

	var unloadErr error
	for _, node := range nodes {
		xlog.Info("Sending backend.stop to node", "model", modelName, "node", node.Name, "nodeID", node.ID, "force", force)
		if err := a.stopBackend(ctx, node.ID, modelName, force); err != nil {
			xlog.Warn("Failed to send backend.stop", "node", node.Name, "error", err)
			unloadErr = errors.Join(unloadErr, fmt.Errorf("stopping model on node %s: %w", node.ID, err))
			continue
		}
		// Remove every replica of this model on the node — the worker will
		// handle the actual process cleanup.
		if err := a.registry.RemoveAllNodeModelReplicas(ctx, node.ID, modelName); err != nil {
			unloadErr = errors.Join(unloadErr, fmt.Errorf("removing model replicas from node %s: %w", node.ID, err))
		}
	}

	return unloadErr
}

// installProgressBridge adapts an install or upgrade's progress sink to
// CallStreaming's line callback, which carries a subject the client does not
// interpret.
//
// It makes two decisions a caller of InstallBackend must not have to make.
//
// A line naming a SUBJECT is a re-broadcast request and not install progress,
// so it is dropped here rather than delivered as a tick. backend.install and
// backend.upgrade are a BACKEND worker's verbs, and MayBroadcast denies a
// backend worker every subject, so this path has nothing to publish and no
// broadcaster to publish it on. Delivering it to onProgress instead would put a
// worker's arbitrary JSON through a decode into an install-progress event and
// report whatever fell out as the state of a download.
//
// A line this frontend cannot decode costs a tick and never the operation,
// because progress is transient by contract while the reply is the worker's
// verdict.
//
// It returns nil for a nil sink so CallStreaming keeps its "no callback, no
// work" path, rather than a non-nil closure wrapping a nil function.
func installProgressBridge(nodeID, path string, onProgress func(messaging.BackendInstallProgressEvent)) func(string, json.RawMessage) {
	if onProgress == nil {
		return nil
	}
	return func(subject string, raw json.RawMessage) {
		if subject != "" {
			xlog.Warn("refusing a re-broadcast request on a backend control stream",
				"node", nodeID, "path", path, "subject", subject)
			return
		}
		var ev messaging.BackendInstallProgressEvent
		if err := json.Unmarshal(raw, &ev); err != nil {
			xlog.Debug("unreadable control progress line", "node", nodeID, "path", path, "error", err)
			return
		}
		onProgress(ev)
	}
}

// InstallBackend asks a worker node to install a backend and start its process.
// Idempotent on the worker: if the (modelID, replica) process is already
// running, the worker short-circuits and returns its address; if the binary
// is on disk, the worker just spawns a process; only a missing binary
// triggers a full gallery pull.
//
// Timeout: configured via DistributedConfig.BackendInstallTimeoutOrDefault
// (default 15m). Most calls return in under 2 seconds (process already
// running). The 15-minute ceiling covers the cold-binary spawn-after-download
// case on slow links (Jetson Wi-Fi, multi-GB CUDA images) while still
// failing fast enough to surface real worker hangs.
//
// Progress needs no subscription and no window to miss events in: the worker
// writes its download ticks into THIS response ahead of the terminal reply, so
// there is nothing to arrange before the request is sent.
//
// For force-reinstall (admin-driven Upgrade), use UpgradeBackend instead.
func (a *RemoteUnloaderAdapter) InstallBackend(
	nodeID, backendType, modelID, galleriesJSON, uri, name, alias string,
	replicaIndex int,
	opID string,
	onProgress func(messaging.BackendInstallProgressEvent),
) (*messaging.BackendInstallReply, error) {
	xlog.Info("Sending backend.install", "nodeID", nodeID, "backend", backendType, "modelID", modelID, "replica", replicaIndex, "opID", opID)

	ctx, cancel := context.WithTimeout(context.Background(), a.installTimeout)
	defer cancel()

	var reply messaging.BackendInstallReply
	err := a.control.CallStreaming(ctx, nodeID, workerctl.PathBackendInstall, messaging.BackendInstallRequest{
		Backend:          backendType,
		ModelID:          modelID,
		BackendGalleries: galleriesJSON,
		URI:              uri,
		Name:             name,
		Alias:            alias,
		ReplicaIndex:     int32(replicaIndex),
		OpID:             opID,
	}, &reply, installProgressBridge(nodeID, workerctl.PathBackendInstall, onProgress))
	if err != nil {
		if isRequestTimeout(err) {
			return nil, fmt.Errorf("%w (nodeID=%s backend=%s): %v",
				galleryop.ErrWorkerStillInstalling, nodeID, backendType, err)
		}
		return nil, err
	}
	return &reply, nil
}

// UpgradeBackend asks a worker node to force-reinstall a backend.
// The worker stops every live process for this backend, force-reinstalls
// from the gallery (overwriting the on-disk artifact), and replies. The
// next routine InstallBackend call spawns a fresh process with the new
// binary - upgrade itself does not start a process.
//
// Timeout: configured via DistributedConfig.BackendUpgradeTimeoutOrDefault
// (default 15m). Real-world worst case observed: 8-10 minutes for large
// CUDA-l4t backend images on Jetson over WiFi.
func (a *RemoteUnloaderAdapter) UpgradeBackend(nodeID, backendType, galleriesJSON, uri, name, alias string, replicaIndex int, opID string, onProgress func(messaging.BackendInstallProgressEvent)) (*messaging.BackendUpgradeReply, error) {
	xlog.Info("Sending backend.upgrade", "nodeID", nodeID, "backend", backendType, "replica", replicaIndex, "opID", opID)

	ctx, cancel := context.WithTimeout(context.Background(), a.upgradeTimeout)
	defer cancel()

	var reply messaging.BackendUpgradeReply
	err := a.control.CallStreaming(ctx, nodeID, workerctl.PathBackendUpgrade, messaging.BackendUpgradeRequest{
		Backend:          backendType,
		BackendGalleries: galleriesJSON,
		URI:              uri,
		Name:             name,
		Alias:            alias,
		ReplicaIndex:     int32(replicaIndex),
		OpID:             opID,
	}, &reply, installProgressBridge(nodeID, workerctl.PathBackendUpgrade, onProgress))
	if err != nil {
		if isRequestTimeout(err) {
			return nil, fmt.Errorf("%w (nodeID=%s backend=%s): %v",
				galleryop.ErrWorkerStillInstalling, nodeID, backendType, err)
		}
		return nil, err
	}
	a.dropStoppedReplicaRows(nodeID, "backend.upgrade", backendType, reply.StoppedProcessKeys, reply.ReportsStoppedProcesses)
	return &reply, nil
}

// installWithForceFallback is the rolling-update fallback used by
// DistributedBackendManager.UpgradeBackend when backend.upgrade reports that
// the worker does not serve that verb (a pre-2026-05-08 build). It re-fires
// the legacy backend.install with Force=true. Drop this once every worker is
// on 2026-05-08 or newer.
func (a *RemoteUnloaderAdapter) installWithForceFallback(nodeID, backendType, galleriesJSON, uri, name, alias string, replicaIndex int, opID string, onProgress func(messaging.BackendInstallProgressEvent)) (*messaging.BackendInstallReply, error) {
	xlog.Warn("Falling back to legacy backend.install Force=true (old worker)", "nodeID", nodeID, "backend", backendType)

	ctx, cancel := context.WithTimeout(context.Background(), a.upgradeTimeout)
	defer cancel()

	var reply messaging.BackendInstallReply
	err := a.control.CallStreaming(ctx, nodeID, workerctl.PathBackendInstall, messaging.BackendInstallRequest{
		Backend:          backendType,
		BackendGalleries: galleriesJSON,
		URI:              uri,
		Name:             name,
		Alias:            alias,
		ReplicaIndex:     int32(replicaIndex),
		Force:            true,
		OpID:             opID,
	}, &reply, installProgressBridge(nodeID, workerctl.PathBackendInstall, onProgress))
	if err != nil {
		if isRequestTimeout(err) {
			return nil, fmt.Errorf("%w (nodeID=%s backend=%s): %v",
				galleryop.ErrWorkerStillInstalling, nodeID, backendType, err)
		}
		return nil, err
	}
	return &reply, nil
}

// Control-RPC budgets. Each is the deadline the corresponding NATS
// request/reply carried, kept unchanged so this cutover changes the carrier and
// not how long the frontend waits.
const (
	backendListTimeout   = 30 * time.Second
	modelsRunningTimeout = 10 * time.Second
	backendStopTimeout   = 30 * time.Second
	backendDeleteTimeout = 2 * time.Minute
	modelUnloadTimeout   = 30 * time.Second
	modelDeleteTimeout   = 30 * time.Second
	nodeStopTimeout      = 30 * time.Second
)

// ListBackends queries a worker node for its installed backends.
func (a *RemoteUnloaderAdapter) ListBackends(nodeID string) (*messaging.BackendListReply, error) {
	xlog.Debug("Sending backend.list", "nodeID", nodeID)

	ctx, cancel := context.WithTimeout(context.Background(), backendListTimeout)
	defer cancel()

	var reply messaging.BackendListReply
	if err := a.control.Call(ctx, nodeID, workerctl.PathBackendList, messaging.BackendListRequest{}, &reply); err != nil {
		return nil, err
	}
	return &reply, nil
}

// ListRunningModels asks a worker node which model backend processes it
// currently has running.
//
// The timeout is short on purpose: the worker answers straight out of its
// in-memory process table, so a slow reply means the worker itself is in
// trouble, and the caller treats no-answer as "don't know" rather than as
// "nothing running".
func (a *RemoteUnloaderAdapter) ListRunningModels(nodeID string) (*messaging.ModelsRunningReply, error) {
	ctx, cancel := context.WithTimeout(context.Background(), modelsRunningTimeout)
	defer cancel()

	var reply messaging.ModelsRunningReply
	if err := a.control.Call(ctx, nodeID, workerctl.PathModelsRunning, messaging.ModelsRunningRequest{}, &reply); err != nil {
		return nil, err
	}
	return &reply, nil
}

// StopBackend tells a worker node to stop a specific gRPC backend process.
// If backend is empty, the worker stops ALL backends.
// The node stays registered and can receive another InstallBackend later.
func (a *RemoteUnloaderAdapter) StopBackend(nodeID, backend string) error {
	ctx, cancel := context.WithTimeout(context.Background(), backendStopTimeout)
	defer cancel()
	return a.stopBackend(ctx, nodeID, backend, false)
}

// stopBackend sends one backend.stop over the node's own tunnel.
//
// It does not ask what KIND of worker the node is, and there is nothing left
// for the answer to change. A backend worker kills the process and recycles
// the port; an agent worker runs no backend processes and closes the MCP
// sessions it cached for that backend. Both serve the verb on the same control
// path, so the frontend states the fact and the worker decides what it means.
func (a *RemoteUnloaderAdapter) stopBackend(ctx context.Context, nodeID, backend string, force bool) error {
	// An empty Backend is what the worker reads as "stop everything"; see
	// decodeBackendStopRequest.
	return a.control.Call(ctx, nodeID, workerctl.PathBackendStop,
		messaging.BackendStopRequest{Backend: backend, Force: force}, nil)
}

// DeleteBackend tells a worker node to delete a backend (stop + remove files).
func (a *RemoteUnloaderAdapter) DeleteBackend(nodeID, backendName string) (*messaging.BackendDeleteReply, error) {
	xlog.Info("Sending backend.delete", "nodeID", nodeID, "backend", backendName)

	ctx, cancel := context.WithTimeout(context.Background(), backendDeleteTimeout)
	defer cancel()

	var reply messaging.BackendDeleteReply
	if err := a.control.Call(ctx, nodeID, workerctl.PathBackendDelete, messaging.BackendDeleteRequest{Backend: backendName}, &reply); err != nil {
		return nil, err
	}
	a.dropStoppedReplicaRows(nodeID, "backend.delete", backendName, reply.StoppedProcessKeys, reply.ReportsStoppedProcesses)
	return &reply, nil
}

// dropStoppedReplicaRows removes the NodeModel rows addressing processes a
// worker just terminated.
//
// Why eagerly, rather than leaving it to the existing health checks: stopping a
// process returns its gRPC port to the worker's allocator, and the next backend
// started there can be handed that same port. Until the row is gone it names a
// live address, so both SmartRouter.probeHealth and the HealthMonitor per-model
// probe — which verify liveness, not identity — pass against whatever now
// occupies the port, and the request is served by the wrong backend rather than
// failing. Nothing else on the delete/upgrade path tells the controller the
// address just became invalid, unlike model.unload which drops its rows itself.
//
// reported=false means the worker predates this reply field. Its empty list is
// then indistinguishable from "stopped nothing", so it must NOT be read as a
// completed cleanup: leave the rows alone and fall back to the probe-based
// staleness recovery that was the only mechanism before this change.
func (a *RemoteUnloaderAdapter) dropStoppedReplicaRows(nodeID, op, backendName string, processKeys []string, reported bool) {
	if !reported {
		xlog.Debug("Worker did not report stopped processes; relying on probe-based staleness recovery",
			"nodeID", nodeID, "op", op, "backend", backendName)
		return
	}
	ctx := context.Background()
	for _, key := range processKeys {
		modelName, replicaIndex, ok := model.ParseBackendProcessKey(key)
		if !ok {
			// Acting on a guess could evict the row of a healthy sibling replica.
			xlog.Warn("Ignoring unparseable process key reported by worker",
				"nodeID", nodeID, "op", op, "backend", backendName, "processKey", key)
			continue
		}
		xlog.Info("Dropping replica row for a process the worker stopped",
			"nodeID", nodeID, "op", op, "backend", backendName, "model", modelName, "replica", replicaIndex)
		if err := a.registry.RemoveNodeModel(ctx, nodeID, modelName, replicaIndex); err != nil {
			// Best-effort: probe-based recovery remains the backstop, and failing
			// the operator's delete over a bookkeeping error would be worse than
			// the stale row this prevents.
			xlog.Warn("Failed to drop replica row for a stopped process",
				"nodeID", nodeID, "op", op, "model", modelName, "replica", replicaIndex, "error", err)
		}
	}
}

// UnloadModelOnNode sends a model.unload request to a specific node.
// The worker calls gRPC Free() to release GPU memory.
func (a *RemoteUnloaderAdapter) UnloadModelOnNode(nodeID, modelName string) error {
	xlog.Info("Sending model.unload", "nodeID", nodeID, "model", modelName)

	ctx, cancel := context.WithTimeout(context.Background(), modelUnloadTimeout)
	defer cancel()

	var reply messaging.ModelUnloadReply
	if err := a.control.Call(ctx, nodeID, workerctl.PathModelUnload, messaging.ModelUnloadRequest{ModelName: modelName}, &reply); err != nil {
		return err
	}
	if !reply.Success {
		return fmt.Errorf("model.unload on node %s: %s", nodeID, reply.Error)
	}
	return nil
}

// DeleteModelFiles sends model.delete to all nodes that have the model cached.
// This removes model files from worker disks.
func (a *RemoteUnloaderAdapter) DeleteModelFiles(modelName string) error {
	ctx, cancel := context.WithTimeout(context.Background(), modelDeleteTimeout)
	defer cancel()

	nodes, err := a.registry.FindNodesWithModel(ctx, modelName)
	if err != nil || len(nodes) == 0 {
		xlog.Debug("No nodes with model for file deletion", "model", modelName)
		return nil
	}

	for _, node := range nodes {
		xlog.Info("Sending model.delete", "nodeID", node.ID, "model", modelName)

		var reply messaging.ModelDeleteReply
		if err := a.control.Call(ctx, node.ID, workerctl.PathModelDelete, messaging.ModelDeleteRequest{ModelName: modelName}, &reply); err != nil {
			xlog.Warn("model.delete failed on node", "node", node.Name, "error", err)
			continue
		}
		if !reply.Success {
			xlog.Warn("model.delete failed on node", "node", node.Name, "error", reply.Error)
		}
	}
	return nil
}

// StopNode tells a worker node to shut down entirely (deregister + exit).
func (a *RemoteUnloaderAdapter) StopNode(nodeID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), nodeStopTimeout)
	defer cancel()
	return a.control.Call(ctx, nodeID, workerctl.PathNodeStop, struct{}{}, nil)
}

// isRequestTimeout reports whether a control RPC ended because its budget ran
// out rather than because the worker said anything.
//
// context.DeadlineExceeded is the ONE signal, and it is matchable because
// controlFailure wraps the caller's own ctx.Err(). The bus sentinel it used to
// accept alongside is gone with the bus: every verb this adapter sends now
// travels over the worker's tunnel, so a nats.ErrTimeout could only arrive from
// a carrier nothing here uses. The string match that carrier needed is
// deliberately not reproduced either, in any spelling: a message that merely
// quotes a timeout is not one, and a worker error that happened to contain the
// phrase would be reported as still-installing forever.
func isRequestTimeout(err error) bool {
	return errors.Is(err, context.DeadlineExceeded)
}
