package carrier

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	mcpTools "github.com/mudler/LocalAI/core/http/endpoints/mcp"
	mcpRemote "github.com/mudler/LocalAI/core/services/mcp"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/workerctl"
	grpc "github.com/mudler/LocalAI/pkg/grpc"
)

// Every worker-facing holder below forwards a call for one node through the
// route table: the routes of the node in order, moving on only when a route
// proves that the worker received nothing (see RouteTable). A call that is
// already running is never moved.

// Commands forwards the control verbs to the route of each node.
type Commands struct {
	t *RouteTable
}

var _ nodes.NodeControl = (*Commands)(nil)

// NewCommands returns a holder over the routes of t.
func NewCommands(t *RouteTable) *Commands { return &Commands{t: t} }

func (h *Commands) InstallBackend(nodeID, backendType, modelID, galleriesJSON, uri, name, alias string, replicaIndex int, opID string, onProgress func(workerctl.BackendInstallProgressEvent), env ...map[string]string) (*workerctl.BackendInstallReply, error) {
	return call(context.Background(), h.t, nodeID, func(r *WorkerRoute) (*workerctl.BackendInstallReply, error) {
		return r.Commands.InstallBackend(nodeID, backendType, modelID, galleriesJSON, uri, name, alias, replicaIndex, opID, onProgress, env...)
	})
}

func (h *Commands) UpgradeBackend(nodeID, backendType, galleriesJSON, uri, name, alias string, replicaIndex int, opID string, onProgress func(workerctl.BackendInstallProgressEvent)) (*workerctl.BackendUpgradeReply, error) {
	return call(context.Background(), h.t, nodeID, func(r *WorkerRoute) (*workerctl.BackendUpgradeReply, error) {
		return r.Commands.UpgradeBackend(nodeID, backendType, galleriesJSON, uri, name, alias, replicaIndex, opID, onProgress)
	})
}

func (h *Commands) DeleteBackend(nodeID, backendName string) (*workerctl.BackendDeleteReply, error) {
	return call(context.Background(), h.t, nodeID, func(r *WorkerRoute) (*workerctl.BackendDeleteReply, error) {
		return r.Commands.DeleteBackend(nodeID, backendName)
	})
}

func (h *Commands) ListBackends(nodeID string) (*workerctl.BackendListReply, error) {
	return call(context.Background(), h.t, nodeID, func(r *WorkerRoute) (*workerctl.BackendListReply, error) {
		return r.Commands.ListBackends(nodeID)
	})
}

func (h *Commands) StopBackend(nodeID, backend string) error {
	return call0(context.Background(), h.t, nodeID, func(r *WorkerRoute) error {
		return r.Commands.StopBackend(nodeID, backend)
	})
}

func (h *Commands) UnloadModelOnNode(nodeID, modelName string) error {
	return call0(context.Background(), h.t, nodeID, func(r *WorkerRoute) error {
		return r.Commands.UnloadModelOnNode(nodeID, modelName)
	})
}

func (h *Commands) PingNode(nodeID string) error {
	return call0(context.Background(), h.t, nodeID, func(r *WorkerRoute) error {
		return r.Commands.PingNode(nodeID)
	})
}

func (h *Commands) InstallBackendOp(nodeID, backendType, modelID, galleriesJSON string, replicaIndex int, opID, operationID string, deadline time.Duration, onProgress func(workerctl.BackendInstallProgressEvent), env ...map[string]string) (*workerctl.BackendInstallReply, error) {
	return call(context.Background(), h.t, nodeID, func(r *WorkerRoute) (*workerctl.BackendInstallReply, error) {
		return r.Commands.InstallBackendOp(nodeID, backendType, modelID, galleriesJSON, replicaIndex, opID, operationID, deadline, onProgress, env...)
	})
}

func (h *Commands) StopLoadOperation(ctx context.Context, nodeID string, req workerctl.ModelStopRequest) (workerctl.ModelStopReply, error) {
	return call(ctx, h.t, nodeID, func(r *WorkerRoute) (workerctl.ModelStopReply, error) {
		return r.Commands.StopLoadOperation(ctx, nodeID, req)
	})
}

func (h *Commands) OperationControl(nodeID string, req workerctl.OperationRequest) (*workerctl.OperationReply, error) {
	return call(context.Background(), h.t, nodeID, func(r *WorkerRoute) (*workerctl.OperationReply, error) {
		return r.Commands.OperationControl(nodeID, req)
	})
}

func (h *Commands) UnloadReplica(nodeID string, replica nodes.NodeModel) error {
	return call0(context.Background(), h.t, nodeID, func(r *WorkerRoute) error {
		return r.Commands.UnloadReplica(nodeID, replica)
	})
}

func (h *Commands) StopModelReplica(ctx context.Context, nodeID string, replica nodes.NodeModel, force bool) (workerctl.ModelStopReply, error) {
	return call(ctx, h.t, nodeID, func(r *WorkerRoute) (workerctl.ModelStopReply, error) {
		return r.Commands.StopModelReplica(ctx, nodeID, replica, force)
	})
}

func (h *Commands) ListRunningModels(nodeID string) (*workerctl.ModelsRunningReply, error) {
	return call(context.Background(), h.t, nodeID, func(r *WorkerRoute) (*workerctl.ModelsRunningReply, error) {
		return r.Commands.ListRunningModels(nodeID)
	})
}

// UnloadRemoteModel stops a model on every node that holds it, as
// UnloadRemoteModelContext does.
func (h *Commands) UnloadRemoteModel(modelName string) error {
	ctx := context.Background()
	return h.fanout(ctx, func(c nodes.NodeControl) error {
		return c.UnloadRemoteModel(modelName)
	}, func(s nodes.ScopedFanout, include func(string) bool) error {
		return s.UnloadRemoteModelScoped(ctx, modelName, false, include)
	})
}

// UnloadRemoteModelContext stops a model on every node that holds it. Each node
// is asked on its own first route (fanout).
func (h *Commands) UnloadRemoteModelContext(ctx context.Context, modelName string, force bool) error {
	return h.fanout(ctx, func(c nodes.NodeControl) error {
		return c.UnloadRemoteModelContext(ctx, modelName, force)
	}, func(s nodes.ScopedFanout, include func(string) bool) error {
		return s.UnloadRemoteModelScoped(ctx, modelName, force, include)
	})
}

// DeleteModelFiles asks every node that holds the model to delete its files, each
// on its own first route.
func (h *Commands) DeleteModelFiles(modelName string) error {
	return h.fanout(context.Background(), func(c nodes.NodeControl) error {
		return c.DeleteModelFiles(modelName)
	}, func(s nodes.ScopedFanout, include func(string) bool) error {
		return s.DeleteModelFilesScoped(modelName, include)
	})
}

// fanout runs a verb that goes to every node that holds a model. A fan-out has
// no node to route by, so every route that the table holds runs the scoped form
// of the verb for the nodes whose first route it is (pick). A node whose routes
// cannot be told goes to the first route that the table holds, whose own error
// then reports it. A fan-out does not move a node to its next route: the scoped
// verb does not say which node failed and how.
//
// With one route, or with a route whose sender has no scoped form, the plain
// verb runs on the first route alone.
func (h *Commands) fanout(ctx context.Context, plain func(nodes.NodeControl) error, scoped func(nodes.ScopedFanout, func(string) bool) error) error {
	held := h.t.held()
	if len(held) == 0 {
		return fmt.Errorf("%w: this frontend holds no route to any worker", nodes.ErrNoRoute)
	}
	senders := make([]nodes.ScopedFanout, len(held))
	for i, r := range held {
		s, ok := r.Commands.(nodes.ScopedFanout)
		if !ok {
			return plain(held[0].Commands)
		}
		senders[i] = s
	}
	if len(held) == 1 {
		return plain(held[0].Commands)
	}
	firstOf := func(nodeID string) *WorkerRoute {
		if r, err := h.t.pick(ctx, nodeID); err == nil {
			return r
		}
		return held[0]
	}
	var errs error
	for i, r := range held {
		errs = errors.Join(errs, scoped(senders[i], func(nodeID string) bool { return firstOf(nodeID) == r }))
	}
	return errs
}

// HasRemoteModel reads the registry, the same on every route.
func (h *Commands) HasRemoteModel(ctx context.Context, modelName string) (bool, error) {
	r := h.t.first()
	if r == nil {
		return false, fmt.Errorf("%w: this frontend holds no route to any worker", nodes.ErrNoRoute)
	}
	return r.Commands.HasRemoteModel(ctx, modelName)
}

// InstallTimeout is the configured bound of an install, the same on every route.
func (h *Commands) InstallTimeout() time.Duration {
	if r := h.t.first(); r != nil {
		return r.Commands.InstallTimeout()
	}
	return 0
}

func (h *Commands) InstallBackendForce(nodeID, backendType, galleriesJSON, uri, name, alias string, replicaIndex int, opID string, onProgress func(workerctl.BackendInstallProgressEvent)) (*workerctl.BackendInstallReply, error) {
	return call(context.Background(), h.t, nodeID, func(r *WorkerRoute) (*workerctl.BackendInstallReply, error) {
		return r.Commands.InstallBackendForce(nodeID, backendType, galleriesJSON, uri, name, alias, replicaIndex, opID, onProgress)
	})
}

// Files forwards file staging to the route of each node.
type Files struct {
	t *RouteTable
}

var _ FileCarrier = (*Files)(nil)

// NewFiles returns a holder over the routes of t.
func NewFiles(t *RouteTable) *Files { return &Files{t: t} }

func (h *Files) EnsureRemote(ctx context.Context, nodeID, localPath, key string) (string, error) {
	return call(ctx, h.t, nodeID, func(r *WorkerRoute) (string, error) {
		return r.Files.EnsureRemote(ctx, nodeID, localPath, key)
	})
}

func (h *Files) FetchRemote(ctx context.Context, nodeID, remotePath, localDst string) error {
	return call0(ctx, h.t, nodeID, func(r *WorkerRoute) error {
		return r.Files.FetchRemote(ctx, nodeID, remotePath, localDst)
	})
}

func (h *Files) FetchRemoteByKey(ctx context.Context, nodeID, key, localDst string) error {
	return call0(ctx, h.t, nodeID, func(r *WorkerRoute) error {
		return r.Files.FetchRemoteByKey(ctx, nodeID, key, localDst)
	})
}

func (h *Files) AllocRemoteTemp(ctx context.Context, nodeID string) (string, error) {
	return call(ctx, h.t, nodeID, func(r *WorkerRoute) (string, error) {
		return r.Files.AllocRemoteTemp(ctx, nodeID)
	})
}

func (h *Files) StageRemoteToStore(ctx context.Context, nodeID, remotePath, key string) error {
	return call0(ctx, h.t, nodeID, func(r *WorkerRoute) error {
		return r.Files.StageRemoteToStore(ctx, nodeID, remotePath, key)
	})
}

func (h *Files) ReleaseRemote(ctx context.Context, nodeID, key string) error {
	return call0(ctx, h.t, nodeID, func(r *WorkerRoute) error {
		return r.Files.ReleaseRemote(ctx, nodeID, key)
	})
}

func (h *Files) ListRemoteDir(ctx context.Context, nodeID, keyPrefix string) ([]string, error) {
	return call(ctx, h.t, nodeID, func(r *WorkerRoute) ([]string, error) {
		return r.Files.ListRemoteDir(ctx, nodeID, keyPrefix)
	})
}

func (h *Files) ReleaseRemoteRequest(ctx context.Context, nodeID, requestID string, keys []string) error {
	return call0(ctx, h.t, nodeID, func(r *WorkerRoute) error {
		return r.Files.ReleaseRemoteRequest(ctx, nodeID, requestID, keys)
	})
}

// Clients builds the backend client of a node on the first route of the node.
// A client keeps the route it was built on; the code that caches clients drops
// them when a node goes away.
type Clients struct {
	t *RouteTable
}

var _ nodes.BackendClientFactory = (*Clients)(nil)

// NewClients returns a holder over the routes of t.
func NewClients(t *RouteTable) *Clients { return &Clients{t: t} }

func (h *Clients) NewClient(nodeID, address string, parallel bool) grpc.Backend {
	r, err := h.t.pick(context.Background(), nodeID)
	if err != nil {
		// A client is built without a way to fail, so a node with no route gets a
		// client of the first route that the table holds, whose first call
		// reports the missing route.
		if r = h.t.first(); r == nil {
			return nil
		}
	}
	return r.Clients.NewClient(nodeID, address, parallel)
}

// NewRoutedWorkerDialer returns a nodes.WorkerNetDialerFor that picks the route
// at the moment of each dial, not when the dial function is created. An HTTP
// client keeps its dial function for as long as it lives, so resolving earlier
// would pin it to the route it was created on. A dial that finds no route
// (nodes.ErrNoRoute) moves to the next route of the node: nothing was sent.
func NewRoutedWorkerDialer(t *RouteTable) nodes.WorkerNetDialerFor {
	return func(nodeID string) func(context.Context, string, string) (net.Conn, error) {
		return func(ctx context.Context, network, addr string) (net.Conn, error) {
			return t.dial(ctx, nodeID, network, addr)
		}
	}
}

// Agents forwards MCP requests to an agent worker on the routes that the table
// holds. A request that names no worker goes to the direct route (the NATS queue
// group), then to the tunnel (a worker that the frontend picks). A request that
// names a worker (ExecuteMCPToolOn, DiscoverMCPToolsOn) goes on the routes of
// that worker. A request moves to the next route only when the failure proves
// that no worker took it.
type Agents struct {
	t *RouteTable
}

// NewAgents returns a holder over the routes of t.
func NewAgents(t *RouteTable) *Agents { return &Agents{t: t} }

func agentCall[T any](t *RouteTable, fn func(a mcpTools.AgentControl) (T, error)) (T, error) {
	var (
		v   T
		err = fmt.Errorf("%w: no route of this frontend carries the agent RPC", nodes.ErrNoRoute)
	)
	for _, r := range t.held() {
		if r.Agents == nil {
			continue
		}
		if v, err = fn(r.Agents); err == nil || !notStarted(err) {
			return v, err
		}
	}
	return v, err
}

func (h *Agents) ExecuteMCPTool(ctx context.Context, req mcpRemote.MCPToolRequest) (*mcpRemote.MCPToolResponse, error) {
	return agentCall(h.t, func(a mcpTools.AgentControl) (*mcpRemote.MCPToolResponse, error) {
		return a.ExecuteMCPTool(ctx, req)
	})
}

func (h *Agents) DiscoverMCPTools(ctx context.Context, req mcpRemote.MCPDiscoveryRequest) (*mcpRemote.MCPDiscoveryResponse, error) {
	return agentCall(h.t, func(a mcpTools.AgentControl) (*mcpRemote.MCPDiscoveryResponse, error) {
		return a.DiscoverMCPTools(ctx, req)
	})
}

var _ nodes.NodeAgentControl = (*Agents)(nil)

// nodeAgents returns the agent RPC of a route that can name a node, or a
// not-started error that moves the call to the next route of the node.
func nodeAgents(r *WorkerRoute) (nodes.NodeAgentControl, error) {
	if na, ok := r.Agents.(nodes.NodeAgentControl); ok {
		return na, nil
	}
	return nil, fmt.Errorf("%w: the %s route cannot name an agent worker", nodes.ErrNoRoute, r.Kind)
}

// ExecuteMCPToolOn runs one MCP tool on the agent worker nodeID, on the routes
// of that node like any other call to a node: the NATS subject of the node on
// the direct route, its tunnel on the tunnel route.
func (h *Agents) ExecuteMCPToolOn(ctx context.Context, nodeID string, req mcpRemote.MCPToolRequest) (*mcpRemote.MCPToolResponse, error) {
	return call(ctx, h.t, nodeID, func(r *WorkerRoute) (*mcpRemote.MCPToolResponse, error) {
		na, err := nodeAgents(r)
		if err != nil {
			return nil, err
		}
		return na.ExecuteMCPToolOn(ctx, nodeID, req)
	})
}

// DiscoverMCPToolsOn asks the agent worker nodeID for the MCP servers and tools
// of a model, on the routes of that node.
func (h *Agents) DiscoverMCPToolsOn(ctx context.Context, nodeID string, req mcpRemote.MCPDiscoveryRequest) (*mcpRemote.MCPDiscoveryResponse, error) {
	return call(ctx, h.t, nodeID, func(r *WorkerRoute) (*mcpRemote.MCPDiscoveryResponse, error) {
		na, err := nodeAgents(r)
		if err != nil {
			return nil, err
		}
		return na.DiscoverMCPToolsOn(ctx, nodeID, req)
	})
}

var _ nodes.ModelFilePrefetcher = (*Commands)(nil)

// PrefetchModelFiles sends model.prefetch on the route of the node. A route
// whose commands cannot send it is a route that does not exist.
func (h *Commands) PrefetchModelFiles(ctx context.Context, nodeID string, req workerctl.ModelPrefetchRequest, timeout time.Duration) (*workerctl.ModelPrefetchReply, error) {
	return call(ctx, h.t, nodeID, func(r *WorkerRoute) (*workerctl.ModelPrefetchReply, error) {
		p, ok := r.Commands.(nodes.ModelFilePrefetcher)
		if !ok {
			return nil, fmt.Errorf("%w: the %s route does not send model.prefetch", nodes.ErrNoRoute, r.Kind)
		}
		return p.PrefetchModelFiles(ctx, nodeID, req, timeout)
	})
}
