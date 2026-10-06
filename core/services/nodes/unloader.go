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
// rolling-update fallback to the legacy install Force=true path.
//
// PingNode returns ErrNoRoute when nothing answers for the node, which is the
// only condition callers may read as "this node cannot be given work".
// UpgradeBackend returns ErrNoRoute on an old worker that does not serve
// backend.upgrade, and the caller falls back to the legacy install.
type NodeCommandSender interface {
	InstallBackend(nodeID, backendType, modelID, galleriesJSON, uri, name, alias string, replicaIndex int, opID string, onProgress func(workerctl.BackendInstallProgressEvent)) (*workerctl.BackendInstallReply, error)
	UpgradeBackend(nodeID, backendType, galleriesJSON, uri, name, alias string, replicaIndex int, opID string, onProgress func(workerctl.BackendInstallProgressEvent)) (*workerctl.BackendUpgradeReply, error)
	DeleteBackend(nodeID, backendName string) (*workerctl.BackendDeleteReply, error)
	ListBackends(nodeID string) (*workerctl.BackendListReply, error)
	StopBackend(nodeID, backend string) error
	UnloadModelOnNode(nodeID, modelName string) error
	// PingNode reports whether the node is still subscribed on the bus. It
	// returns ErrNoRoute when nothing answers for the node.
	PingNode(nodeID string) error
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
	_ ExactModelStopper                = (*RemoteUnloaderAdapter)(nil)
)

const exactModelStopTimeout = 10 * time.Second

// StopModelReplica stops only the process represented by replica. Configuration
// cleanup intentionally has no backend.stop fallback: an old worker that does
// not understand this request leaves the quarantine row for a later retry.
func (a *RemoteUnloaderAdapter) StopModelReplica(ctx context.Context, nodeID string, replica NodeModel, force bool) (workerctl.ModelStopReply, error) {
	return a.stopModelExact(ctx, nodeID, workerctl.ModelStopRequest{
		ModelName:       replica.ModelName,
		ProcessKey:      model.BackendProcessKey(replica.ModelName, replica.ReplicaIndex),
		ExpectedAddress: replica.Address,
		Force:           force,
		ConfigRevision:  replica.ConfigRevision,
	})
}

// StopLoadOperation stops the process of one load operation, addressed by the
// operation id (the load job generation) and the process key. It is the only
// call that kills remote load work. It names the operation, never a model: the
// worker refuses unless the operation, the process key and, when given, the
// address and process instance all match its own records, so a stop of a load
// that finished cannot become an unload of the model.
func (a *RemoteUnloaderAdapter) StopLoadOperation(ctx context.Context, nodeID string, req workerctl.ModelStopRequest) (workerctl.ModelStopReply, error) {
	if req.OperationID == "" {
		return workerctl.ModelStopReply{}, errors.New("a load stop needs an operation id")
	}
	return a.stopModelExact(ctx, nodeID, req)
}

func (a *RemoteUnloaderAdapter) stopModelExact(ctx context.Context, nodeID string, req workerctl.ModelStopRequest) (workerctl.ModelStopReply, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, exactModelStopTimeout)
	defer cancel()

	type result struct {
		reply *workerctl.ModelStopReply
		err   error
	}
	done := make(chan result, 1)
	go func() {
		reply, err := controlRequestJSON[workerctl.ModelStopRequest, workerctl.ModelStopReply](a.nats, messaging.SubjectNodeModelStop(nodeID), req, exactModelStopTimeout)
		done <- result{reply: reply, err: err}
	}()

	select {
	case <-ctx.Done():
		return workerctl.ModelStopReply{}, ctx.Err()
	case result := <-done:
		if result.err != nil {
			return workerctl.ModelStopReply{}, result.err
		}
		return *result.reply, nil
	}
}

// OperationControl renews and completes load operations on a node. A worker
// that predates operations never answers; the caller treats that as a node it
// cannot confirm stops on.
func (a *RemoteUnloaderAdapter) OperationControl(nodeID string, req workerctl.OperationRequest) (*workerctl.OperationReply, error) {
	return controlRequestJSON[workerctl.OperationRequest, workerctl.OperationReply](a.nats, messaging.SubjectNodeModelOp(nodeID), req, operationControlTimeout)
}

const operationControlTimeout = 5 * time.Second

// InstallBackendOp is InstallBackend for a load operation: the request carries
// the operation id and the longest the load may run, so the worker can bound it.
func (a *RemoteUnloaderAdapter) InstallBackendOp(nodeID, backendType, modelID, galleriesJSON string, replicaIndex int, opID, operationID string, deadline time.Duration, onProgress func(workerctl.BackendInstallProgressEvent)) (*workerctl.BackendInstallReply, error) {
	return a.installBackend(nodeID, workerctl.BackendInstallRequest{
		Backend:          backendType,
		ModelID:          modelID,
		BackendGalleries: galleriesJSON,
		ReplicaIndex:     int32(replicaIndex),
		OpID:             opID,
		OperationID:      operationID,
		DeadlineMs:       deadline.Milliseconds(),
	}, onProgress)
}

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
	seenNodeIDs := make(map[string]struct{}, len(nodes))
	for _, node := range nodes {
		if _, seen := seenNodeIDs[node.ID]; seen {
			continue
		}
		seenNodeIDs[node.ID] = struct{}{}
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
	onProgress func(workerctl.BackendInstallProgressEvent),
) (*workerctl.BackendInstallReply, error) {
	return a.installBackend(nodeID, workerctl.BackendInstallRequest{
		Backend:          backendType,
		ModelID:          modelID,
		BackendGalleries: galleriesJSON,
		URI:              uri,
		Name:             name,
		Alias:            alias,
		ReplicaIndex:     int32(replicaIndex),
		OpID:             opID,
	}, onProgress)
}

func (a *RemoteUnloaderAdapter) installBackend(nodeID string, req workerctl.BackendInstallRequest, onProgress func(workerctl.BackendInstallProgressEvent)) (*workerctl.BackendInstallReply, error) {
	backendType, modelID, replicaIndex, opID := req.Backend, req.ModelID, int(req.ReplicaIndex), req.OpID
	subject := messaging.SubjectNodeBackendInstall(nodeID)
	xlog.Info("Sending NATS backend.install", "nodeID", nodeID, "backend", backendType, "modelID", modelID, "replica", replicaIndex, "opID", opID)

	// Subscribe to the per-op progress subject BEFORE publishing the install
	// request so we don't miss early events.
	sub := a.subscribeProgress(nodeID, opID, onProgress)

	reply, err := controlRequestJSON[workerctl.BackendInstallRequest, workerctl.BackendInstallReply](a.nats, subject, req, a.installTimeout)

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
func (a *RemoteUnloaderAdapter) subscribeProgress(nodeID, opID string, onProgress func(workerctl.BackendInstallProgressEvent)) messaging.Subscription {
	if onProgress == nil || opID == "" {
		return nil
	}
	progressSubject := messaging.SubjectNodeBackendInstallProgress(nodeID, opID)
	s, subErr := a.nats.Subscribe(progressSubject, func(raw []byte) {
		var ev workerctl.BackendInstallProgressEvent
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
func (a *RemoteUnloaderAdapter) UpgradeBackend(nodeID, backendType, galleriesJSON, uri, name, alias string, replicaIndex int, opID string, onProgress func(workerctl.BackendInstallProgressEvent)) (*workerctl.BackendUpgradeReply, error) {
	subject := messaging.SubjectNodeBackendUpgrade(nodeID)
	xlog.Info("Sending NATS backend.upgrade", "nodeID", nodeID, "backend", backendType, "replica", replicaIndex, "opID", opID)

	sub := a.subscribeProgress(nodeID, opID, onProgress)

	reply, err := controlRequestJSON[workerctl.BackendUpgradeRequest, workerctl.BackendUpgradeReply](a.nats, subject, workerctl.BackendUpgradeRequest{
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
// ErrNoRoute (the worker is on a pre-2026-05-08 build that
// doesn't subscribe to the new subject). It re-fires the legacy
// backend.install with Force=true. Drop this once every worker is on
// 2026-05-08 or newer.
func (a *RemoteUnloaderAdapter) installWithForceFallback(nodeID, backendType, galleriesJSON, uri, name, alias string, replicaIndex int, opID string, onProgress func(workerctl.BackendInstallProgressEvent)) (*workerctl.BackendInstallReply, error) {
	subject := messaging.SubjectNodeBackendInstall(nodeID)
	xlog.Warn("Falling back to legacy backend.install Force=true (old worker)", "nodeID", nodeID, "backend", backendType)

	sub := a.subscribeProgress(nodeID, opID, onProgress)

	reply, err := controlRequestJSON[workerctl.BackendInstallRequest, workerctl.BackendInstallReply](a.nats, subject, workerctl.BackendInstallRequest{
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
func (a *RemoteUnloaderAdapter) ListBackends(nodeID string) (*workerctl.BackendListReply, error) {
	subject := messaging.SubjectNodeBackendList(nodeID)
	xlog.Debug("Sending NATS backend.list", "nodeID", nodeID)

	return controlRequestJSON[workerctl.BackendListRequest, workerctl.BackendListReply](a.nats, subject, workerctl.BackendListRequest{}, 30*time.Second)
}

// PingNode checks that a worker still has a live subscription on the bus.
//
// A node's status in the database comes from its HTTP heartbeat, which is a
// separate channel from NATS. A worker that has died stops answering on NATS
// at once but keeps its healthy status until the heartbeat ages out, so the
// scheduler could pick a node that could not be given work and the request
// failed with "no responders available".
//
// The subject asked has to be one every worker in the fleet subscribes to, or
// this check condemns the workers that do not. models.running was the obvious
// choice and the wrong one: it arrived in 4.6, so a 4.5 worker that is alive
// and serving never answers it, and a model pinned to that node could never be
// scheduled. backend.list has been part of the worker protocol far longer, so
// it is the safer question to ask.
//
// A worker that answers anything is alive. Only when every subject reports no
// route is the node treated as absent, so adding a newer subject here can
// never condemn an older worker.
func (a *RemoteUnloaderAdapter) PingNode(nodeID string) error {
	subjects := []string{
		messaging.SubjectNodeBackendList(nodeID),
		messaging.SubjectNodeModelsRunning(nodeID),
	}
	var lastErr error
	for _, subject := range subjects {
		_, err := controlRequestJSON[workerctl.BackendListRequest, workerctl.BackendListReply](
			a.nats, subject, workerctl.BackendListRequest{}, 5*time.Second)
		if err == nil {
			return nil
		}
		if !errors.Is(err, ErrNoRoute) {
			// Reached someone, or failed for a reason that is not absence.
			// Either way the node is not proven gone.
			return nil
		}
		lastErr = err
	}
	return lastErr
}

// ListRunningModels asks a worker node which model backend processes it
// currently has running, via NATS request-reply.
//
// The timeout is short on purpose: the worker answers straight out of its
// in-memory process table, so a slow reply means the worker itself is in
// trouble, and the caller treats no-answer as "don't know" rather than as
// "nothing running".
func (a *RemoteUnloaderAdapter) ListRunningModels(nodeID string) (*workerctl.ModelsRunningReply, error) {
	subject := messaging.SubjectNodeModelsRunning(nodeID)
	return controlRequestJSON[workerctl.ModelsRunningRequest, workerctl.ModelsRunningReply](
		a.nats, subject, workerctl.ModelsRunningRequest{}, 10*time.Second)
}

// backendStopAckTimeout bounds the wait for a worker's backend.stop reply.
//
// A single stop is bounded by the worker's 5s best-effort Free plus the kill,
// so this leaves comfortable headroom. It is also the stall a worker that
// predates the reply imposes on every stop, which is why it is not generous:
// such a worker performs the stop and simply never answers, so the controller
// waits out the full budget before falling back to the old assumption.
const backendStopAckTimeout = 15 * time.Second

// StopBackend tells a worker node to stop a specific gRPC backend process.
// If backend is empty, the worker stops ALL backends.
// The node stays registered and can receive another InstallBackend later.
func (a *RemoteUnloaderAdapter) StopBackend(nodeID, backend string) error {
	return a.stopBackend(nodeID, backend, false)
}

// stopBackend asks a worker to stop a backend and waits for it to say what it
// did.
//
// This was a bare Publish, which returned nil as soon as the local publish
// succeeded and so reported success for a stop that killed nothing or failed
// outright. The unload endpoint answered HTTP 200 while the backend kept
// running and holding its VRAM, and the endpoint's own "backend stop failed"
// branch was unreachable.
//
// A silent worker is NOT treated as a failure. A worker that predates
// BackendStopReply still receives the request and still stops the backend; it
// only lacks the reply. Failing here would break every stop against a fleet
// that has not been upgraded yet, so a timeout degrades to the previous
// assumption and says so in the log.
//
// Only silence degrades. A transport error — the connection is closed, nothing
// subscribes to this node's subject at all — is reported, because under the
// old Publish it was reported too, and callers such as UnloadRemoteModel rely
// on that to skip the registry cleanup for a node they could not reach.
func (a *RemoteUnloaderAdapter) stopBackend(nodeID, backend string, force bool) error {
	subject := messaging.SubjectNodeBackendStop(nodeID)
	req := workerctl.BackendStopRequest{Backend: backend, Force: force}

	reply, err := controlRequestJSON[workerctl.BackendStopRequest, workerctl.BackendStopReply](
		a.nats, subject, req, backendStopAckTimeout)
	if err != nil {
		if isStrictRequestTimeout(err) {
			xlog.Warn("Worker did not acknowledge backend.stop; assuming an older worker delivered it",
				"nodeID", nodeID, "backend", backend, "force", force)
			return nil
		}
		return fmt.Errorf("backend.stop on node %s: %w", nodeID, err)
	}
	if !reply.Success {
		return fmt.Errorf("backend.stop on node %s: %s", nodeID, reply.Error)
	}
	// An empty list from a worker that enumerates what it stopped is the answer
	// to "was anything actually running under that name" — and the answer is
	// no. That is not an error: stopping a backend that is not running leaves
	// the caller in the state it asked for. It is worth saying out loud,
	// because a caller that expected to reclaim VRAM did not.
	if reply.ReportsStoppedProcesses && len(reply.StoppedProcessKeys) == 0 {
		xlog.Warn("backend.stop matched no running process on the worker",
			"nodeID", nodeID, "backend", backend)
		return nil
	}
	xlog.Info("Worker stopped backend processes",
		"nodeID", nodeID, "backend", backend, "stopped", reply.StoppedProcessKeys)
	return nil
}

// DeleteBackend tells a worker node to delete a backend (stop + remove files).
func (a *RemoteUnloaderAdapter) DeleteBackend(nodeID, backendName string) (*workerctl.BackendDeleteReply, error) {
	subject := messaging.SubjectNodeBackendDelete(nodeID)
	xlog.Info("Sending NATS backend.delete", "nodeID", nodeID, "backend", backendName)

	reply, err := controlRequestJSON[workerctl.BackendDeleteRequest, workerctl.BackendDeleteReply](a.nats, subject, workerctl.BackendDeleteRequest{Backend: backendName}, 2*time.Minute)
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

// UnloadReplica sends model.unload for one replica, naming its address. The
// worker calls gRPC Free() on exactly that process. A replica with no address
// never reached a backend, so there is nothing to free and nothing is sent: the
// worker is never asked to pick a process on its own.
//
// Callers that remove the replica row before they unload must pass the row they
// read first. Looking the address up afterwards finds nothing.
func (a *RemoteUnloaderAdapter) UnloadReplica(nodeID string, replica NodeModel) error {
	if replica.Address == "" {
		return nil
	}
	xlog.Info("Sending NATS model.unload", "nodeID", nodeID, "model", replica.ModelName, "replica", replica.ReplicaIndex)
	reply, err := controlRequestJSON[workerctl.ModelUnloadRequest, workerctl.ModelUnloadReply](a.nats, messaging.SubjectNodeModelUnload(nodeID),
		workerctl.ModelUnloadRequest{ModelName: replica.ModelName, Address: replica.Address}, 30*time.Second)
	if err != nil {
		return err
	}
	if !reply.Success {
		return fmt.Errorf("model.unload on node %s: %s", nodeID, reply.Error)
	}
	return nil
}

// UnloadModelOnNode unloads every replica of the model recorded on the node,
// each by address. It reads the replicas from the registry, so it only works
// while their rows still exist. A caller that deletes the rows first uses
// UnloadReplica with the row it read.
func (a *RemoteUnloaderAdapter) UnloadModelOnNode(nodeID, modelName string) error {
	lister, ok := a.registry.(interface {
		GetNodeModels(ctx context.Context, nodeID string) ([]NodeModel, error)
	})
	if !ok {
		return nil
	}
	replicas, err := lister.GetNodeModels(context.Background(), nodeID)
	if err != nil {
		return err
	}
	for _, replica := range replicas {
		if replica.ModelName != modelName {
			continue
		}
		if err := a.UnloadReplica(nodeID, replica); err != nil {
			return err
		}
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

		reply, err := controlRequestJSON[workerctl.ModelDeleteRequest, workerctl.ModelDeleteReply](a.nats, subject, workerctl.ModelDeleteRequest{ModelName: modelName}, 30*time.Second)
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
