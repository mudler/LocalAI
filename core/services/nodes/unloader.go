package nodes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/xlog"
)

// NodeCommandSender abstracts NATS-based commands to worker nodes.
// Used by HTTP endpoint handlers to avoid coupling to the concrete RemoteUnloaderAdapter.
//
// InstallBackend is idempotent: the worker short-circuits if the backend is
// already running for the requested (modelID, replica) slot. Routine model
// loads and admin installs both call this.
//
// UpgradeBackend is the destructive force-reinstall path: the worker stops
// every live process for the backend, re-pulls the gallery artifact, and
// replies. Caller (DistributedBackendManager.UpgradeBackend) handles
// rolling-update fallback to the legacy install Force=true path on
// nats.ErrNoResponders for old workers that don't subscribe to the new
// backend.upgrade subject.
type NodeCommandSender interface {
	InstallBackend(nodeID, backendType, modelID, galleriesJSON, uri, name, alias string, replicaIndex int, opID string, onProgress func(messaging.BackendInstallProgressEvent)) (*messaging.BackendInstallReply, error)
	UpgradeBackend(nodeID, backendType, galleriesJSON, uri, name, alias string, replicaIndex int, opID string, onProgress func(messaging.BackendInstallProgressEvent)) (*messaging.BackendUpgradeReply, error)
	DeleteBackend(nodeID, backendName string) (*messaging.BackendDeleteReply, error)
	ListBackends(nodeID string) (*messaging.BackendListReply, error)
	StopBackend(nodeID, backend string) error
	UnloadModelOnNode(nodeID, modelName string) error
}

// RemoteUnloaderAdapter implements NodeCommandSender and model.RemoteModelUnloader
// by publishing NATS events for backend process lifecycle. The worker process
// subscribes and handles the actual process start/stop.
//
// This mirrors the local ModelLoader's startProcess()/deleteProcess() but
// over NATS for remote nodes.
type RemoteUnloaderAdapter struct {
	registry       ModelLocator
	nats           messaging.MessagingClient
	installTimeout time.Duration
	upgradeTimeout time.Duration
}

// NewRemoteUnloaderAdapter creates a new adapter. installTimeout and
// upgradeTimeout govern the NATS request-reply deadlines for backend.install
// and backend.upgrade respectively. Use
// DistributedConfig.BackendInstallTimeoutOrDefault() /
// BackendUpgradeTimeoutOrDefault() at construction.
func NewRemoteUnloaderAdapter(registry ModelLocator, nats messaging.MessagingClient, installTimeout, upgradeTimeout time.Duration) *RemoteUnloaderAdapter {
	return &RemoteUnloaderAdapter{
		registry:       registry,
		nats:           nats,
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
)

// UnloadRemoteModel finds the node(s) hosting the given model and tells them
// to stop their backend process via NATS backend.stop event.
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
		xlog.Info("Sending NATS backend.stop to node", "model", modelName, "node", node.Name, "nodeID", node.ID, "force", force)
		if err := a.stopBackend(node.ID, modelName, force); err != nil {
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

// InstallBackend sends a backend.install request-reply to a worker node.
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
// For force-reinstall (admin-driven Upgrade), use UpgradeBackend instead -
// it lives on a different NATS subject so it cannot head-of-line-block
// routine load traffic on the same worker.
func (a *RemoteUnloaderAdapter) InstallBackend(
	nodeID, backendType, modelID, galleriesJSON, uri, name, alias string,
	replicaIndex int,
	opID string,
	onProgress func(messaging.BackendInstallProgressEvent),
) (*messaging.BackendInstallReply, error) {
	subject := messaging.SubjectNodeBackendInstall(nodeID)
	xlog.Info("Sending NATS backend.install", "nodeID", nodeID, "backend", backendType, "modelID", modelID, "replica", replicaIndex, "opID", opID)

	// Subscribe to the per-op progress subject BEFORE publishing the install
	// request so we don't miss early events.
	sub := a.subscribeProgress(nodeID, opID, onProgress)

	reply, err := messaging.RequestJSON[messaging.BackendInstallRequest, messaging.BackendInstallReply](a.nats, subject, messaging.BackendInstallRequest{
		Backend:          backendType,
		ModelID:          modelID,
		BackendGalleries: galleriesJSON,
		URI:              uri,
		Name:             name,
		Alias:            alias,
		ReplicaIndex:     int32(replicaIndex),
		OpID:             opID,
	}, a.installTimeout)

	if sub != nil {
		if unsubscribeErr := sub.Unsubscribe(); unsubscribeErr != nil {
			xlog.Warn("Failed to unsubscribe from backend install progress", "nodeID", nodeID, "backend", backendType, "opID", opID, "error", unsubscribeErr)
		}
	}

	if err != nil && isNATSTimeout(err) {
		return nil, fmt.Errorf("%w (subject=%s nodeID=%s backend=%s): %v",
			galleryop.ErrWorkerStillInstalling, subject, nodeID, backendType, err)
	}
	return reply, err
}

// subscribeProgress subscribes to the per-op backend-install progress subject
// so the master can stream per-node download ticks while a worker installs or
// upgrades. Returns nil (and subscribes to nothing) when onProgress is nil or
// opID is empty — the reconciler-driven retry path and legacy callers stay
// silent at no cost. Shared by InstallBackend, UpgradeBackend, and the legacy
// force-install fallback: an upgrade is a force-reinstall, so it reuses the
// install-progress subject rather than minting a new one (no new NATS
// permission, no new rolling-update compat surface). Caller must Unsubscribe
// the returned subscription after the request completes.
func (a *RemoteUnloaderAdapter) subscribeProgress(nodeID, opID string, onProgress func(messaging.BackendInstallProgressEvent)) messaging.Subscription {
	if onProgress == nil || opID == "" {
		return nil
	}
	progressSubject := messaging.SubjectNodeBackendInstallProgress(nodeID, opID)
	s, subErr := a.nats.Subscribe(progressSubject, func(raw []byte) {
		var ev messaging.BackendInstallProgressEvent
		if err := json.Unmarshal(raw, &ev); err != nil {
			xlog.Debug("malformed backend progress event", "subject", progressSubject, "error", err)
			return
		}
		// Goroutine guard: a slow onProgress callback must not stall the NATS
		// reader thread. Events spawn one goroutine each, so ordering at the
		// consumer is best-effort; the worker debounces to ~250ms which dwarfs
		// goroutine scheduling jitter, and its final Flush() is the terminal tick.
		go onProgress(ev)
	})
	if subErr != nil {
		xlog.Warn("Failed to subscribe to backend progress subject; proceeding without progress streaming",
			"subject", progressSubject, "error", subErr)
		return nil
	}
	return s
}

// UpgradeBackend sends a backend.upgrade request-reply to a worker node.
// The worker stops every live process for this backend, force-reinstalls
// from the gallery (overwriting the on-disk artifact), and replies. The
// next routine InstallBackend call spawns a fresh process with the new
// binary - upgrade itself does not start a process.
//
// When opID is non-empty and onProgress is set, the master subscribes to the
// per-op progress subject before firing the request so a long force-reinstall
// streams per-node download ticks instead of blocking opaque at progress 0.
//
// Timeout: configured via DistributedConfig.BackendUpgradeTimeoutOrDefault
// (default 15m). Real-world worst case observed: 8-10 minutes for large
// CUDA-l4t backend images on Jetson over WiFi.
func (a *RemoteUnloaderAdapter) UpgradeBackend(nodeID, backendType, galleriesJSON, uri, name, alias string, replicaIndex int, opID string, onProgress func(messaging.BackendInstallProgressEvent)) (*messaging.BackendUpgradeReply, error) {
	subject := messaging.SubjectNodeBackendUpgrade(nodeID)
	xlog.Info("Sending NATS backend.upgrade", "nodeID", nodeID, "backend", backendType, "replica", replicaIndex, "opID", opID)

	sub := a.subscribeProgress(nodeID, opID, onProgress)

	reply, err := messaging.RequestJSON[messaging.BackendUpgradeRequest, messaging.BackendUpgradeReply](a.nats, subject, messaging.BackendUpgradeRequest{
		Backend:          backendType,
		BackendGalleries: galleriesJSON,
		URI:              uri,
		Name:             name,
		Alias:            alias,
		ReplicaIndex:     int32(replicaIndex),
		OpID:             opID,
	}, a.upgradeTimeout)

	if sub != nil {
		if unsubscribeErr := sub.Unsubscribe(); unsubscribeErr != nil {
			xlog.Warn("Failed to unsubscribe from backend upgrade progress", "nodeID", nodeID, "backend", backendType, "opID", opID, "error", unsubscribeErr)
		}
	}

	if err != nil && isNATSTimeout(err) {
		return nil, fmt.Errorf("%w (subject=%s nodeID=%s backend=%s): %v",
			galleryop.ErrWorkerStillInstalling, subject, nodeID, backendType, err)
	}
	if err == nil {
		a.dropStoppedReplicaRows(nodeID, "backend.upgrade", backendType, reply.StoppedProcessKeys, reply.ReportsStoppedProcesses)
	}
	return reply, err
}

// installWithForceFallback is the rolling-update fallback used by
// DistributedBackendManager.UpgradeBackend when backend.upgrade returns
// nats.ErrNoResponders (the worker is on a pre-2026-05-08 build that
// doesn't subscribe to the new subject). It re-fires the legacy
// backend.install with Force=true. Drop this once every worker is on
// 2026-05-08 or newer.
func (a *RemoteUnloaderAdapter) installWithForceFallback(nodeID, backendType, galleriesJSON, uri, name, alias string, replicaIndex int, opID string, onProgress func(messaging.BackendInstallProgressEvent)) (*messaging.BackendInstallReply, error) {
	subject := messaging.SubjectNodeBackendInstall(nodeID)
	xlog.Warn("Falling back to legacy backend.install Force=true (old worker)", "nodeID", nodeID, "backend", backendType)

	sub := a.subscribeProgress(nodeID, opID, onProgress)

	reply, err := messaging.RequestJSON[messaging.BackendInstallRequest, messaging.BackendInstallReply](a.nats, subject, messaging.BackendInstallRequest{
		Backend:          backendType,
		BackendGalleries: galleriesJSON,
		URI:              uri,
		Name:             name,
		Alias:            alias,
		ReplicaIndex:     int32(replicaIndex),
		Force:            true,
		OpID:             opID,
	}, a.upgradeTimeout)

	if sub != nil {
		if unsubscribeErr := sub.Unsubscribe(); unsubscribeErr != nil {
			xlog.Warn("Failed to unsubscribe from legacy backend install progress", "nodeID", nodeID, "backend", backendType, "opID", opID, "error", unsubscribeErr)
		}
	}

	if err != nil && isNATSTimeout(err) {
		return nil, fmt.Errorf("%w (subject=%s nodeID=%s backend=%s): %v",
			galleryop.ErrWorkerStillInstalling, subject, nodeID, backendType, err)
	}
	return reply, err
}

// ListBackends queries a worker node for its installed backends via NATS request-reply.
func (a *RemoteUnloaderAdapter) ListBackends(nodeID string) (*messaging.BackendListReply, error) {
	subject := messaging.SubjectNodeBackendList(nodeID)
	xlog.Debug("Sending NATS backend.list", "nodeID", nodeID)

	return messaging.RequestJSON[messaging.BackendListRequest, messaging.BackendListReply](a.nats, subject, messaging.BackendListRequest{}, 30*time.Second)
}

// StopBackend tells a worker node to stop a specific gRPC backend process.
// If backend is empty, the worker stops ALL backends.
// The node stays registered and can receive another InstallBackend later.
func (a *RemoteUnloaderAdapter) StopBackend(nodeID, backend string) error {
	return a.stopBackend(nodeID, backend, false)
}

func (a *RemoteUnloaderAdapter) stopBackend(nodeID, backend string, force bool) error {
	subject := messaging.SubjectNodeBackendStop(nodeID)
	if backend == "" && !force {
		return a.nats.Publish(subject, nil)
	}
	return a.nats.Publish(subject, messaging.BackendStopRequest{Backend: backend, Force: force})
}

// DeleteBackend tells a worker node to delete a backend (stop + remove files).
func (a *RemoteUnloaderAdapter) DeleteBackend(nodeID, backendName string) (*messaging.BackendDeleteReply, error) {
	subject := messaging.SubjectNodeBackendDelete(nodeID)
	xlog.Info("Sending NATS backend.delete", "nodeID", nodeID, "backend", backendName)

	reply, err := messaging.RequestJSON[messaging.BackendDeleteRequest, messaging.BackendDeleteReply](a.nats, subject, messaging.BackendDeleteRequest{Backend: backendName}, 2*time.Minute)
	if err != nil {
		return reply, err
	}
	a.dropStoppedReplicaRows(nodeID, "backend.delete", backendName, reply.StoppedProcessKeys, reply.ReportsStoppedProcesses)
	return reply, nil
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
	subject := messaging.SubjectNodeModelUnload(nodeID)
	xlog.Info("Sending NATS model.unload", "nodeID", nodeID, "model", modelName)

	reply, err := messaging.RequestJSON[messaging.ModelUnloadRequest, messaging.ModelUnloadReply](a.nats, subject, messaging.ModelUnloadRequest{ModelName: modelName}, 30*time.Second)
	if err != nil {
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
	nodes, err := a.registry.FindNodesWithModel(context.Background(), modelName)
	if err != nil || len(nodes) == 0 {
		xlog.Debug("No nodes with model for file deletion", "model", modelName)
		return nil
	}

	for _, node := range nodes {
		subject := messaging.SubjectNodeModelDelete(node.ID)
		xlog.Info("Sending NATS model.delete", "nodeID", node.ID, "model", modelName)

		reply, err := messaging.RequestJSON[messaging.ModelDeleteRequest, messaging.ModelDeleteReply](a.nats, subject, messaging.ModelDeleteRequest{ModelName: modelName}, 30*time.Second)
		if err != nil {
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
	subject := messaging.SubjectNodeStop(nodeID)
	return a.nats.Publish(subject, nil)
}

// isNATSTimeout returns true if err looks like a NATS request-reply timeout.
// nats.ErrTimeout is the canonical sentinel; context.DeadlineExceeded can
// also surface depending on the client's path; we accept both, plus a
// string-match fallback for clients that return a bare error.
func isNATSTimeout(err error) bool {
	if errors.Is(err, nats.ErrTimeout) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	return err != nil && strings.Contains(err.Error(), "nats: timeout")
}
