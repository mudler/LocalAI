package carrier

import (
	"context"
	"net"
	"sync/atomic"
	"time"

	mcpRemote "github.com/mudler/LocalAI/core/services/mcp"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/workerctl"
	grpc "github.com/mudler/LocalAI/pkg/grpc"
)

// Every holder below does one thing per call: load the pointer once and call
// the same method on that set. Loading once pins a multi-step operation of the
// carrier to one set, and a call that is already running is never moved. The
// holders take no lock and allocate nothing of their own.

// Commands forwards the control verbs to the current set.
type Commands struct{ cur *atomic.Pointer[Set] }

var _ nodes.NodeControl = (*Commands)(nil)

// NewCommands returns a holder over cur, which must name a set.
func NewCommands(cur *atomic.Pointer[Set]) *Commands { return &Commands{cur: cur} }

func (h *Commands) InstallBackend(nodeID, backendType, modelID, galleriesJSON, uri, name, alias string, replicaIndex int, opID string, onProgress func(workerctl.BackendInstallProgressEvent)) (*workerctl.BackendInstallReply, error) {
	return h.cur.Load().Commands.InstallBackend(nodeID, backendType, modelID, galleriesJSON, uri, name, alias, replicaIndex, opID, onProgress)
}

func (h *Commands) UpgradeBackend(nodeID, backendType, galleriesJSON, uri, name, alias string, replicaIndex int, opID string, onProgress func(workerctl.BackendInstallProgressEvent)) (*workerctl.BackendUpgradeReply, error) {
	return h.cur.Load().Commands.UpgradeBackend(nodeID, backendType, galleriesJSON, uri, name, alias, replicaIndex, opID, onProgress)
}

func (h *Commands) DeleteBackend(nodeID, backendName string) (*workerctl.BackendDeleteReply, error) {
	return h.cur.Load().Commands.DeleteBackend(nodeID, backendName)
}

func (h *Commands) ListBackends(nodeID string) (*workerctl.BackendListReply, error) {
	return h.cur.Load().Commands.ListBackends(nodeID)
}

func (h *Commands) StopBackend(nodeID, backend string) error {
	return h.cur.Load().Commands.StopBackend(nodeID, backend)
}

func (h *Commands) UnloadModelOnNode(nodeID, modelName string) error {
	return h.cur.Load().Commands.UnloadModelOnNode(nodeID, modelName)
}

func (h *Commands) PingNode(nodeID string) error { return h.cur.Load().Commands.PingNode(nodeID) }

func (h *Commands) InstallBackendOp(nodeID, backendType, modelID, galleriesJSON string, replicaIndex int, opID, operationID string, deadline time.Duration, onProgress func(workerctl.BackendInstallProgressEvent)) (*workerctl.BackendInstallReply, error) {
	return h.cur.Load().Commands.InstallBackendOp(nodeID, backendType, modelID, galleriesJSON, replicaIndex, opID, operationID, deadline, onProgress)
}

func (h *Commands) StopLoadOperation(ctx context.Context, nodeID string, req workerctl.ModelStopRequest) (workerctl.ModelStopReply, error) {
	return h.cur.Load().Commands.StopLoadOperation(ctx, nodeID, req)
}

func (h *Commands) OperationControl(nodeID string, req workerctl.OperationRequest) (*workerctl.OperationReply, error) {
	return h.cur.Load().Commands.OperationControl(nodeID, req)
}

func (h *Commands) UnloadReplica(nodeID string, replica nodes.NodeModel) error {
	return h.cur.Load().Commands.UnloadReplica(nodeID, replica)
}

func (h *Commands) StopModelReplica(ctx context.Context, nodeID string, replica nodes.NodeModel, force bool) (workerctl.ModelStopReply, error) {
	return h.cur.Load().Commands.StopModelReplica(ctx, nodeID, replica, force)
}

func (h *Commands) ListRunningModels(nodeID string) (*workerctl.ModelsRunningReply, error) {
	return h.cur.Load().Commands.ListRunningModels(nodeID)
}

func (h *Commands) UnloadRemoteModel(modelName string) error {
	return h.cur.Load().Commands.UnloadRemoteModel(modelName)
}

func (h *Commands) UnloadRemoteModelContext(ctx context.Context, modelName string, force bool) error {
	return h.cur.Load().Commands.UnloadRemoteModelContext(ctx, modelName, force)
}

func (h *Commands) HasRemoteModel(ctx context.Context, modelName string) (bool, error) {
	return h.cur.Load().Commands.HasRemoteModel(ctx, modelName)
}

func (h *Commands) InstallTimeout() time.Duration { return h.cur.Load().Commands.InstallTimeout() }

func (h *Commands) DeleteModelFiles(modelName string) error {
	return h.cur.Load().Commands.DeleteModelFiles(modelName)
}

func (h *Commands) InstallBackendForce(nodeID, backendType, galleriesJSON, uri, name, alias string, replicaIndex int, opID string, onProgress func(workerctl.BackendInstallProgressEvent)) (*workerctl.BackendInstallReply, error) {
	return h.cur.Load().Commands.InstallBackendForce(nodeID, backendType, galleriesJSON, uri, name, alias, replicaIndex, opID, onProgress)
}

// Files forwards file staging to the current set.
type Files struct{ cur *atomic.Pointer[Set] }

var _ FileCarrier = (*Files)(nil)

// NewFiles returns a holder over cur, which must name a set.
func NewFiles(cur *atomic.Pointer[Set]) *Files { return &Files{cur: cur} }

func (h *Files) EnsureRemote(ctx context.Context, nodeID, localPath, key string) (string, error) {
	return h.cur.Load().Files.EnsureRemote(ctx, nodeID, localPath, key)
}

func (h *Files) FetchRemote(ctx context.Context, nodeID, remotePath, localDst string) error {
	return h.cur.Load().Files.FetchRemote(ctx, nodeID, remotePath, localDst)
}

func (h *Files) FetchRemoteByKey(ctx context.Context, nodeID, key, localDst string) error {
	return h.cur.Load().Files.FetchRemoteByKey(ctx, nodeID, key, localDst)
}

func (h *Files) AllocRemoteTemp(ctx context.Context, nodeID string) (string, error) {
	return h.cur.Load().Files.AllocRemoteTemp(ctx, nodeID)
}

func (h *Files) StageRemoteToStore(ctx context.Context, nodeID, remotePath, key string) error {
	return h.cur.Load().Files.StageRemoteToStore(ctx, nodeID, remotePath, key)
}

func (h *Files) ReleaseRemote(ctx context.Context, nodeID, key string) error {
	return h.cur.Load().Files.ReleaseRemote(ctx, nodeID, key)
}

func (h *Files) ListRemoteDir(ctx context.Context, nodeID, keyPrefix string) ([]string, error) {
	return h.cur.Load().Files.ListRemoteDir(ctx, nodeID, keyPrefix)
}

func (h *Files) ReleaseRemoteRequest(ctx context.Context, nodeID, requestID string, keys []string) error {
	return h.cur.Load().Files.ReleaseRemoteRequest(ctx, nodeID, requestID, keys)
}

// WorkQueue forwards Enqueue to the current set. Producers always use the
// active carrier.
type WorkQueue struct{ cur *atomic.Pointer[Set] }

var _ messaging.WorkQueue = (*WorkQueue)(nil)

// NewWorkQueue returns a holder over cur, which must name a set.
func NewWorkQueue(cur *atomic.Pointer[Set]) *WorkQueue { return &WorkQueue{cur: cur} }

func (h *WorkQueue) Enqueue(ctx context.Context, kind messaging.WorkKind, payload any) error {
	return h.cur.Load().WorkQueue.Enqueue(ctx, kind, payload)
}

// Clients forwards backend client construction to the current set. A client
// built before a swap keeps the connection it has; the code that caches
// clients drops them when the carrier changes.
type Clients struct{ cur *atomic.Pointer[Set] }

var _ nodes.BackendClientFactory = (*Clients)(nil)

// NewClients returns a holder over cur, which must name a set.
func NewClients(cur *atomic.Pointer[Set]) *Clients { return &Clients{cur: cur} }

func (h *Clients) NewClient(nodeID, address string, parallel bool) grpc.Backend {
	return h.cur.Load().Clients.NewClient(nodeID, address, parallel)
}

// NewWorkerDialer returns a nodes.WorkerNetDialerFor that picks the set at the
// moment of each dial, not when the dial function is created. An HTTP client
// keeps its dial function for as long as it lives, so resolving earlier would
// pin it to the carrier it was created on.
func NewWorkerDialer(cur *atomic.Pointer[Set]) nodes.WorkerNetDialerFor {
	return func(nodeID string) func(context.Context, string, string) (net.Conn, error) {
		return func(ctx context.Context, network, addr string) (net.Conn, error) {
			return cur.Load().Dialer(nodeID)(ctx, network, addr)
		}
	}
}

// Agents forwards MCP requests to the agent workers of the current set.
type Agents struct{ cur *atomic.Pointer[Set] }

// NewAgents returns a holder over cur, which must name a set.
func NewAgents(cur *atomic.Pointer[Set]) *Agents { return &Agents{cur: cur} }

func (h *Agents) ExecuteMCPTool(ctx context.Context, req mcpRemote.MCPToolRequest) (*mcpRemote.MCPToolResponse, error) {
	return h.cur.Load().Agents.ExecuteMCPTool(ctx, req)
}

func (h *Agents) DiscoverMCPTools(ctx context.Context, req mcpRemote.MCPDiscoveryRequest) (*mcpRemote.MCPDiscoveryResponse, error) {
	return h.cur.Load().Agents.DiscoverMCPTools(ctx, req)
}
