package carrier

import (
	"context"
	"errors"
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

// Commands forwards the control verbs to the current set. During a change of
// carrier (see Window) a verb for one worker goes to the set that the worker is
// attached to.
type Commands struct {
	cur *atomic.Pointer[Set]
	win *Window
}

var _ nodes.NodeControl = (*Commands)(nil)

// NewCommands returns a holder over cur, which must name a set.
func NewCommands(cur *atomic.Pointer[Set]) *Commands { return &Commands{cur: cur} }

// UseWindow makes the holder route by the window. Call it before the holder is
// shared.
func (h *Commands) UseWindow(w *Window) { h.win = w }

// pick returns the set that a verb for nodeID uses.
func (h *Commands) pick(ctx context.Context, nodeID string) (*Set, error) {
	cur := h.cur.Load()
	if h.win == nil {
		return cur, nil
	}
	return h.win.route(ctx, cur, nodeID)
}

func (h *Commands) InstallBackend(nodeID, backendType, modelID, galleriesJSON, uri, name, alias string, replicaIndex int, opID string, onProgress func(workerctl.BackendInstallProgressEvent)) (*workerctl.BackendInstallReply, error) {
	s, err := h.pick(context.Background(), nodeID)
	if err != nil {
		return nil, err
	}
	return s.Commands.InstallBackend(nodeID, backendType, modelID, galleriesJSON, uri, name, alias, replicaIndex, opID, onProgress)
}

func (h *Commands) UpgradeBackend(nodeID, backendType, galleriesJSON, uri, name, alias string, replicaIndex int, opID string, onProgress func(workerctl.BackendInstallProgressEvent)) (*workerctl.BackendUpgradeReply, error) {
	s, err := h.pick(context.Background(), nodeID)
	if err != nil {
		return nil, err
	}
	return s.Commands.UpgradeBackend(nodeID, backendType, galleriesJSON, uri, name, alias, replicaIndex, opID, onProgress)
}

func (h *Commands) DeleteBackend(nodeID, backendName string) (*workerctl.BackendDeleteReply, error) {
	s, err := h.pick(context.Background(), nodeID)
	if err != nil {
		return nil, err
	}
	return s.Commands.DeleteBackend(nodeID, backendName)
}

func (h *Commands) ListBackends(nodeID string) (*workerctl.BackendListReply, error) {
	s, err := h.pick(context.Background(), nodeID)
	if err != nil {
		return nil, err
	}
	return s.Commands.ListBackends(nodeID)
}

func (h *Commands) StopBackend(nodeID, backend string) error {
	s, err := h.pick(context.Background(), nodeID)
	if err != nil {
		return err
	}
	return s.Commands.StopBackend(nodeID, backend)
}

func (h *Commands) UnloadModelOnNode(nodeID, modelName string) error {
	s, err := h.pick(context.Background(), nodeID)
	if err != nil {
		return err
	}
	return s.Commands.UnloadModelOnNode(nodeID, modelName)
}

func (h *Commands) PingNode(nodeID string) error {
	s, err := h.pick(context.Background(), nodeID)
	if err != nil {
		return err
	}
	return s.Commands.PingNode(nodeID)
}

func (h *Commands) InstallBackendOp(nodeID, backendType, modelID, galleriesJSON string, replicaIndex int, opID, operationID string, deadline time.Duration, onProgress func(workerctl.BackendInstallProgressEvent)) (*workerctl.BackendInstallReply, error) {
	s, err := h.pick(context.Background(), nodeID)
	if err != nil {
		return nil, err
	}
	return s.Commands.InstallBackendOp(nodeID, backendType, modelID, galleriesJSON, replicaIndex, opID, operationID, deadline, onProgress)
}

func (h *Commands) StopLoadOperation(ctx context.Context, nodeID string, req workerctl.ModelStopRequest) (workerctl.ModelStopReply, error) {
	s, err := h.pick(ctx, nodeID)
	if err != nil {
		return workerctl.ModelStopReply{}, err
	}
	return s.Commands.StopLoadOperation(ctx, nodeID, req)
}

func (h *Commands) OperationControl(nodeID string, req workerctl.OperationRequest) (*workerctl.OperationReply, error) {
	s, err := h.pick(context.Background(), nodeID)
	if err != nil {
		return nil, err
	}
	return s.Commands.OperationControl(nodeID, req)
}

func (h *Commands) UnloadReplica(nodeID string, replica nodes.NodeModel) error {
	s, err := h.pick(context.Background(), nodeID)
	if err != nil {
		return err
	}
	return s.Commands.UnloadReplica(nodeID, replica)
}

func (h *Commands) StopModelReplica(ctx context.Context, nodeID string, replica nodes.NodeModel, force bool) (workerctl.ModelStopReply, error) {
	s, err := h.pick(ctx, nodeID)
	if err != nil {
		return workerctl.ModelStopReply{}, err
	}
	return s.Commands.StopModelReplica(ctx, nodeID, replica, force)
}

func (h *Commands) ListRunningModels(nodeID string) (*workerctl.ModelsRunningReply, error) {
	s, err := h.pick(context.Background(), nodeID)
	if err != nil {
		return nil, err
	}
	return s.Commands.ListRunningModels(nodeID)
}

// UnloadRemoteModel stops a model on every node that holds it, as
// UnloadRemoteModelContext does.
func (h *Commands) UnloadRemoteModel(modelName string) error {
	cur := h.cur.Load()
	err := cur.Commands.UnloadRemoteModel(modelName)
	if prev := h.previous(cur); prev != nil && err != nil {
		return prev.Commands.UnloadRemoteModel(modelName)
	}
	return err
}

// previous returns the set that is draining, or nil outside a change of carrier.
func (h *Commands) previous(cur *Set) *Set {
	if h.win == nil {
		return nil
	}
	if prev := h.win.Previous(); prev != cur {
		return prev
	}
	return nil
}

// UnloadRemoteModelContext stops a model on every node that holds it. Inside the
// window the active carrier does it first: it removes the rows of the nodes it
// reached, so the nodes that are left are the ones attached to the previous
// carrier, and that one is asked for them. Its answer is the answer, because it
// saw only the nodes that the first one could not do.
func (h *Commands) UnloadRemoteModelContext(ctx context.Context, modelName string, force bool) error {
	cur := h.cur.Load()
	err := cur.Commands.UnloadRemoteModelContext(ctx, modelName, force)
	if prev := h.previous(cur); prev != nil && err != nil {
		return prev.Commands.UnloadRemoteModelContext(ctx, modelName, force)
	}
	return err
}

func (h *Commands) HasRemoteModel(ctx context.Context, modelName string) (bool, error) {
	return h.cur.Load().Commands.HasRemoteModel(ctx, modelName)
}

func (h *Commands) InstallTimeout() time.Duration { return h.cur.Load().Commands.InstallTimeout() }

// DeleteModelFiles asks every node that holds the model to delete its files.
// Inside the window both carriers ask: each reaches the nodes that are attached
// to it, and the verb is idempotent.
func (h *Commands) DeleteModelFiles(modelName string) error {
	cur := h.cur.Load()
	err := cur.Commands.DeleteModelFiles(modelName)
	if prev := h.previous(cur); prev != nil {
		err = errors.Join(err, prev.Commands.DeleteModelFiles(modelName))
	}
	return err
}

func (h *Commands) InstallBackendForce(nodeID, backendType, galleriesJSON, uri, name, alias string, replicaIndex int, opID string, onProgress func(workerctl.BackendInstallProgressEvent)) (*workerctl.BackendInstallReply, error) {
	s, err := h.pick(context.Background(), nodeID)
	if err != nil {
		return nil, err
	}
	return s.Commands.InstallBackendForce(nodeID, backendType, galleriesJSON, uri, name, alias, replicaIndex, opID, onProgress)
}

// Files forwards file staging to the current set. During a change of carrier a
// transfer for one worker goes to the set that the worker is attached to.
type Files struct {
	cur *atomic.Pointer[Set]
	win *Window
}

var _ FileCarrier = (*Files)(nil)

// NewFiles returns a holder over cur, which must name a set.
func NewFiles(cur *atomic.Pointer[Set]) *Files { return &Files{cur: cur} }

// UseWindow makes the holder route by the window. Call it before the holder is
// shared.
func (h *Files) UseWindow(w *Window) { h.win = w }

func (h *Files) pick(ctx context.Context, nodeID string) (*Set, error) {
	cur := h.cur.Load()
	if h.win == nil {
		return cur, nil
	}
	return h.win.route(ctx, cur, nodeID)
}

func (h *Files) EnsureRemote(ctx context.Context, nodeID, localPath, key string) (string, error) {
	s, err := h.pick(ctx, nodeID)
	if err != nil {
		return "", err
	}
	return s.Files.EnsureRemote(ctx, nodeID, localPath, key)
}

func (h *Files) FetchRemote(ctx context.Context, nodeID, remotePath, localDst string) error {
	s, err := h.pick(ctx, nodeID)
	if err != nil {
		return err
	}
	return s.Files.FetchRemote(ctx, nodeID, remotePath, localDst)
}

func (h *Files) FetchRemoteByKey(ctx context.Context, nodeID, key, localDst string) error {
	s, err := h.pick(ctx, nodeID)
	if err != nil {
		return err
	}
	return s.Files.FetchRemoteByKey(ctx, nodeID, key, localDst)
}

func (h *Files) AllocRemoteTemp(ctx context.Context, nodeID string) (string, error) {
	s, err := h.pick(ctx, nodeID)
	if err != nil {
		return "", err
	}
	return s.Files.AllocRemoteTemp(ctx, nodeID)
}

func (h *Files) StageRemoteToStore(ctx context.Context, nodeID, remotePath, key string) error {
	s, err := h.pick(ctx, nodeID)
	if err != nil {
		return err
	}
	return s.Files.StageRemoteToStore(ctx, nodeID, remotePath, key)
}

func (h *Files) ReleaseRemote(ctx context.Context, nodeID, key string) error {
	s, err := h.pick(ctx, nodeID)
	if err != nil {
		return err
	}
	return s.Files.ReleaseRemote(ctx, nodeID, key)
}

func (h *Files) ListRemoteDir(ctx context.Context, nodeID, keyPrefix string) ([]string, error) {
	s, err := h.pick(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	return s.Files.ListRemoteDir(ctx, nodeID, keyPrefix)
}

func (h *Files) ReleaseRemoteRequest(ctx context.Context, nodeID, requestID string, keys []string) error {
	s, err := h.pick(ctx, nodeID)
	if err != nil {
		return err
	}
	return s.Files.ReleaseRemoteRequest(ctx, nodeID, requestID, keys)
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
type Clients struct {
	cur *atomic.Pointer[Set]
	win *Window
}

var _ nodes.BackendClientFactory = (*Clients)(nil)

// NewClients returns a holder over cur, which must name a set.
func NewClients(cur *atomic.Pointer[Set]) *Clients { return &Clients{cur: cur} }

// UseWindow makes the holder build the client of a worker on the carrier that
// the worker is attached to during a change of carrier. Call it before the
// holder is shared.
func (h *Clients) UseWindow(w *Window) { h.win = w }

func (h *Clients) NewClient(nodeID, address string, parallel bool) grpc.Backend {
	cur := h.cur.Load()
	if h.win != nil {
		// A client is built without a way to fail, so a worker that is attached to
		// neither carrier gets a client of the active one, whose first call reports
		// the missing route.
		if s, err := h.win.route(context.Background(), cur, nodeID); err == nil {
			cur = s
		}
	}
	return cur.Clients.NewClient(nodeID, address, parallel)
}

// NewWorkerDialer returns a nodes.WorkerNetDialerFor that picks the set at the
// moment of each dial, not when the dial function is created. An HTTP client
// keeps its dial function for as long as it lives, so resolving earlier would
// pin it to the carrier it was created on.
func NewWorkerDialer(cur *atomic.Pointer[Set]) nodes.WorkerNetDialerFor {
	return NewRoutedWorkerDialer(cur, nil)
}

// NewRoutedWorkerDialer is NewWorkerDialer for a dial that also follows the
// window: during a change of carrier it dials through the carrier that the worker
// is attached to.
func NewRoutedWorkerDialer(cur *atomic.Pointer[Set], win *Window) nodes.WorkerNetDialerFor {
	return func(nodeID string) func(context.Context, string, string) (net.Conn, error) {
		return func(ctx context.Context, network, addr string) (net.Conn, error) {
			set := cur.Load()
			if win != nil {
				var err error
				if set, err = win.route(ctx, set, nodeID); err != nil {
					return nil, err
				}
			}
			return set.Dialer(nodeID)(ctx, network, addr)
		}
	}
}

// Agents forwards MCP requests to the agent workers of the current set.
type Agents struct {
	cur *atomic.Pointer[Set]
	win *Window
}

// NewAgents returns a holder over cur, which must name a set.
func NewAgents(cur *atomic.Pointer[Set]) *Agents { return &Agents{cur: cur} }

// UseWindow makes the holder send a request to the carrier that has agent
// workers during a change of carrier. Call it before the holder is shared.
func (h *Agents) UseWindow(w *Window) { h.win = w }

func (h *Agents) pick(ctx context.Context) *Set {
	cur := h.cur.Load()
	if h.win == nil {
		return cur
	}
	return h.win.routeAgents(ctx, cur)
}

func (h *Agents) ExecuteMCPTool(ctx context.Context, req mcpRemote.MCPToolRequest) (*mcpRemote.MCPToolResponse, error) {
	return h.pick(ctx).Agents.ExecuteMCPTool(ctx, req)
}

func (h *Agents) DiscoverMCPTools(ctx context.Context, req mcpRemote.MCPDiscoveryRequest) (*mcpRemote.MCPDiscoveryResponse, error) {
	return h.pick(ctx).Agents.DiscoverMCPTools(ctx, req)
}
