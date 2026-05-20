package nodes

import (
	"context"
	"fmt"
	"time"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/xlog"
)

// backendStopRequest is the request payload for backend.stop (fire-and-forget).
type backendStopRequest struct {
	Backend string `json:"backend"`
}

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
	InstallBackend(nodeID, backendType, modelID, galleriesJSON, uri, name, alias string, replicaIndex int) (*messaging.BackendInstallReply, error)
	UpgradeBackend(nodeID, backendType, galleriesJSON, uri, name, alias string, replicaIndex int) (*messaging.BackendUpgradeReply, error)
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
	registry ModelLocator
	nats     messaging.MessagingClient
}

// NewRemoteUnloaderAdapter creates a new adapter.
func NewRemoteUnloaderAdapter(registry ModelLocator, nats messaging.MessagingClient) *RemoteUnloaderAdapter {
	return &RemoteUnloaderAdapter{
		registry: registry,
		nats:     nats,
	}
}

// UnloadRemoteModel finds the node(s) hosting the given model and tells them
// to stop their backend process via NATS backend.stop event.
// The worker process handles: Free() → kill process.
// This is called by ModelLoader.deleteProcess() when process == nil (remote model).
func (a *RemoteUnloaderAdapter) UnloadRemoteModel(modelName string) error {
	ctx := context.Background()
	nodes, err := a.registry.FindNodesWithModel(ctx, modelName)
	if err != nil || len(nodes) == 0 {
		xlog.Debug("No remote nodes found with model", "model", modelName)
		return nil
	}

	for _, node := range nodes {
		xlog.Info("Sending NATS backend.stop to node", "model", modelName, "node", node.Name, "nodeID", node.ID)
		if err := a.StopBackend(node.ID, modelName); err != nil {
			xlog.Warn("Failed to send backend.stop", "node", node.Name, "error", err)
			continue
		}
		// Remove every replica of this model on the node — the worker will
		// handle the actual process cleanup.
		a.registry.RemoveAllNodeModelReplicas(ctx, node.ID, modelName)
	}

	return nil
}

// InstallBackend sends a backend.install request-reply to a worker node.
// Idempotent on the worker: if the (modelID, replica) process is already
// running, the worker short-circuits and returns its address; if the binary
// is on disk, the worker just spawns a process; only a missing binary
// triggers a full gallery pull.
//
// Timeout: 3 minutes. Most calls return in under 2 seconds (process already
// running). The 3-minute ceiling covers the cold-binary spawn-after-download
// case while still failing fast enough to surface real worker hangs.
//
// For force-reinstall (admin-driven Upgrade), use UpgradeBackend instead —
// it lives on a different NATS subject so it cannot head-of-line-block
// routine load traffic on the same worker.
func (a *RemoteUnloaderAdapter) InstallBackend(nodeID, backendType, modelID, galleriesJSON, uri, name, alias string, replicaIndex int) (*messaging.BackendInstallReply, error) {
	subject := messaging.SubjectNodeBackendInstall(nodeID)
	xlog.Info("Sending NATS backend.install", "nodeID", nodeID, "backend", backendType, "modelID", modelID, "replica", replicaIndex)

	return messaging.RequestJSON[messaging.BackendInstallRequest, messaging.BackendInstallReply](a.nats, subject, messaging.BackendInstallRequest{
		Backend:          backendType,
		ModelID:          modelID,
		BackendGalleries: galleriesJSON,
		URI:              uri,
		Name:             name,
		Alias:            alias,
		ReplicaIndex:     int32(replicaIndex),
	}, 3*time.Minute)
}

// UpgradeBackend sends a backend.upgrade request-reply to a worker node.
// The worker stops every live process for this backend, force-reinstalls
// from the gallery (overwriting the on-disk artifact), and replies. The
// next routine InstallBackend call spawns a fresh process with the new
// binary — upgrade itself does not start a process.
//
// Timeout: 15 minutes. Real-world worst case observed: 8–10 minutes for
// large CUDA-l4t backend images on Jetson over WiFi.
func (a *RemoteUnloaderAdapter) UpgradeBackend(nodeID, backendType, galleriesJSON, uri, name, alias string, replicaIndex int) (*messaging.BackendUpgradeReply, error) {
	subject := messaging.SubjectNodeBackendUpgrade(nodeID)
	xlog.Info("Sending NATS backend.upgrade", "nodeID", nodeID, "backend", backendType, "replica", replicaIndex)

	return messaging.RequestJSON[messaging.BackendUpgradeRequest, messaging.BackendUpgradeReply](a.nats, subject, messaging.BackendUpgradeRequest{
		Backend:          backendType,
		BackendGalleries: galleriesJSON,
		URI:              uri,
		Name:             name,
		Alias:            alias,
		ReplicaIndex:     int32(replicaIndex),
	}, 15*time.Minute)
}

// installWithForceFallback is the rolling-update fallback used by
// DistributedBackendManager.UpgradeBackend when backend.upgrade returns
// nats.ErrNoResponders (the worker is on a pre-2026-05-08 build that
// doesn't subscribe to the new subject). It re-fires the legacy
// backend.install with Force=true. Drop this once every worker is on
// 2026-05-08 or newer.
func (a *RemoteUnloaderAdapter) installWithForceFallback(nodeID, backendType, galleriesJSON, uri, name, alias string, replicaIndex int) (*messaging.BackendInstallReply, error) {
	subject := messaging.SubjectNodeBackendInstall(nodeID)
	xlog.Warn("Falling back to legacy backend.install Force=true (old worker)", "nodeID", nodeID, "backend", backendType)

	return messaging.RequestJSON[messaging.BackendInstallRequest, messaging.BackendInstallReply](a.nats, subject, messaging.BackendInstallRequest{
		Backend:          backendType,
		BackendGalleries: galleriesJSON,
		URI:              uri,
		Name:             name,
		Alias:            alias,
		ReplicaIndex:     int32(replicaIndex),
		Force:            true,
	}, 15*time.Minute)
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
	subject := messaging.SubjectNodeBackendStop(nodeID)
	if backend == "" {
		return a.nats.Publish(subject, nil)
	}
	req := struct {
		Backend string `json:"backend"`
	}{Backend: backend}
	return a.nats.Publish(subject, req)
}

// DeleteBackend tells a worker node to delete a backend (stop + remove files).
func (a *RemoteUnloaderAdapter) DeleteBackend(nodeID, backendName string) (*messaging.BackendDeleteReply, error) {
	subject := messaging.SubjectNodeBackendDelete(nodeID)
	xlog.Info("Sending NATS backend.delete", "nodeID", nodeID, "backend", backendName)

	return messaging.RequestJSON[messaging.BackendDeleteRequest, messaging.BackendDeleteReply](a.nats, subject, messaging.BackendDeleteRequest{Backend: backendName}, 2*time.Minute)
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
