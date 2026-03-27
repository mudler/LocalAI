package nodes

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/xlog"
)

// NodeCommandSender abstracts NATS-based commands to worker nodes.
// Used by HTTP endpoint handlers to avoid coupling to the concrete RemoteUnloaderAdapter.
type NodeCommandSender interface {
	InstallBackend(nodeID, backendType, modelID, galleriesJSON string) (*messaging.BackendInstallReply, error)
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
	registry *NodeRegistry
	nats     *messaging.Client
}

// NewRemoteUnloaderAdapter creates a new adapter.
func NewRemoteUnloaderAdapter(registry *NodeRegistry, nats *messaging.Client) *RemoteUnloaderAdapter {
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
	nodes, err := a.registry.FindNodesWithModel(modelName)
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
		// Remove model from registry — the node will handle the actual cleanup
		a.registry.RemoveNodeModel(node.ID, modelName)
	}

	return nil
}

// InstallBackend sends a backend.install request-reply to a worker node.
// The worker installs the backend from gallery (if not already installed),
// starts the gRPC process, and replies when ready.
// Timeout: 5 minutes (gallery install can take a while).
func (a *RemoteUnloaderAdapter) InstallBackend(nodeID, backendType, modelID, galleriesJSON string) (*messaging.BackendInstallReply, error) {
	subject := messaging.SubjectNodeBackendInstall(nodeID)
	xlog.Info("Sending NATS backend.install", "nodeID", nodeID, "backend", backendType, "modelID", modelID)

	req := messaging.BackendInstallRequest{
		Backend:          backendType,
		ModelID:          modelID,
		BackendGalleries: galleriesJSON,
	}
	reqData, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshalling install request: %w", err)
	}

	replyData, err := a.nats.Request(subject, reqData, 5*time.Minute)
	if err != nil {
		return nil, fmt.Errorf("backend.install request to node %s: %w", nodeID, err)
	}

	var reply messaging.BackendInstallReply
	if err := json.Unmarshal(replyData, &reply); err != nil {
		return nil, fmt.Errorf("unmarshalling install reply: %w", err)
	}

	return &reply, nil
}

// ListBackends queries a worker node for its installed backends via NATS request-reply.
func (a *RemoteUnloaderAdapter) ListBackends(nodeID string) (*messaging.BackendListReply, error) {
	subject := messaging.SubjectNodeBackendList(nodeID)
	xlog.Debug("Sending NATS backend.list", "nodeID", nodeID)

	reqData, err := json.Marshal(messaging.BackendListRequest{})
	if err != nil {
		return nil, fmt.Errorf("marshalling list request: %w", err)
	}

	replyData, err := a.nats.Request(subject, reqData, 30*time.Second)
	if err != nil {
		return nil, fmt.Errorf("backend.list request to node %s: %w", nodeID, err)
	}

	var reply messaging.BackendListReply
	if err := json.Unmarshal(replyData, &reply); err != nil {
		return nil, fmt.Errorf("unmarshalling list reply: %w", err)
	}

	return &reply, nil
}

// StopBackend tells a worker node to stop a specific gRPC backend process.
// If backend is empty, the worker stops ALL backends.
// The node stays registered and can receive another InstallBackend later.
func (a *RemoteUnloaderAdapter) StopBackend(nodeID, backend string) error {
	subject := messaging.SubjectNodeBackendStop(nodeID)
	if backend == "" {
		return a.nats.Conn().Publish(subject, nil)
	}
	req := struct {
		Backend string `json:"backend"`
	}{Backend: backend}
	data, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshalling stop request: %w", err)
	}
	return a.nats.Conn().Publish(subject, data)
}

// DeleteBackend tells a worker node to delete a backend (stop + remove files).
func (a *RemoteUnloaderAdapter) DeleteBackend(nodeID, backendName string) (*messaging.BackendDeleteReply, error) {
	subject := messaging.SubjectNodeBackendDelete(nodeID)
	xlog.Info("Sending NATS backend.delete", "nodeID", nodeID, "backend", backendName)

	req := messaging.BackendDeleteRequest{Backend: backendName}
	reqData, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshalling delete request: %w", err)
	}

	replyData, err := a.nats.Request(subject, reqData, 2*time.Minute)
	if err != nil {
		return nil, fmt.Errorf("backend.delete request to node %s: %w", nodeID, err)
	}

	var reply messaging.BackendDeleteReply
	if err := json.Unmarshal(replyData, &reply); err != nil {
		return nil, fmt.Errorf("unmarshalling delete reply: %w", err)
	}
	return &reply, nil
}

// UnloadModelOnNode sends a model.unload request to a specific node.
// The worker calls gRPC Free() to release GPU memory.
func (a *RemoteUnloaderAdapter) UnloadModelOnNode(nodeID, modelName string) error {
	subject := messaging.SubjectNodeModelUnload(nodeID)
	xlog.Info("Sending NATS model.unload", "nodeID", nodeID, "model", modelName)

	req := messaging.ModelUnloadRequest{ModelName: modelName}
	reqData, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshalling unload request: %w", err)
	}

	replyData, err := a.nats.Request(subject, reqData, 30*time.Second)
	if err != nil {
		return fmt.Errorf("model.unload request to node %s: %w", nodeID, err)
	}

	var reply messaging.ModelUnloadReply
	if err := json.Unmarshal(replyData, &reply); err != nil {
		return fmt.Errorf("unmarshalling unload reply: %w", err)
	}
	if !reply.Success {
		return fmt.Errorf("model.unload on node %s: %s", nodeID, reply.Error)
	}
	return nil
}

// DeleteModelFiles sends model.delete to all nodes that have the model cached.
// This removes model files from worker disks.
func (a *RemoteUnloaderAdapter) DeleteModelFiles(modelName string) error {
	nodes, err := a.registry.FindNodesWithModel(modelName)
	if err != nil || len(nodes) == 0 {
		xlog.Debug("No nodes with model for file deletion", "model", modelName)
		return nil
	}

	for _, node := range nodes {
		subject := messaging.SubjectNodeModelDelete(node.ID)
		xlog.Info("Sending NATS model.delete", "nodeID", node.ID, "model", modelName)

		req := messaging.ModelDeleteRequest{ModelName: modelName}
		reqData, err := json.Marshal(req)
		if err != nil {
			xlog.Warn("model.delete marshal failed", "node", node.Name, "error", err)
			continue
		}

		replyData, err := a.nats.Request(subject, reqData, 30*time.Second)
		if err != nil {
			xlog.Warn("model.delete failed on node", "node", node.Name, "error", err)
			continue
		}
		var reply messaging.ModelDeleteReply
		if err := json.Unmarshal(replyData, &reply); err != nil {
			xlog.Warn("model.delete reply unmarshal failed", "node", node.Name, "error", err)
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
	return a.nats.Conn().Publish(subject, nil)
}
