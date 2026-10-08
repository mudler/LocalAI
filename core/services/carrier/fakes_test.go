package carrier_test

import (
	"context"
	"net"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/carrier"
	"github.com/mudler/LocalAI/core/services/cluster"
	mcpRemote "github.com/mudler/LocalAI/core/services/mcp"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/testutil"
	"github.com/mudler/LocalAI/core/services/workerctl"
	grpc "github.com/mudler/LocalAI/pkg/grpc"
)

// recorder remembers which method a fake carrier member received.
type recorder struct {
	mu    sync.Mutex
	calls []string
}

func (r *recorder) rec(name string) {
	r.mu.Lock()
	r.calls = append(r.calls, name)
	r.mu.Unlock()
}

func (r *recorder) seen() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}

// fakeCommands is a nodes.NodeControl that only records the call.
type fakeCommands struct {
	recorder
	// closed, when set, makes the calls that a spec hammers fail once the set is closed.
	closed interface{ Load() bool }
}

var _ nodes.NodeControl = (*fakeCommands)(nil)

func (f *fakeCommands) InstallBackend(string, string, string, string, string, string, string, int, string, func(workerctl.BackendInstallProgressEvent)) (*workerctl.BackendInstallReply, error) {
	f.rec("InstallBackend")
	return &workerctl.BackendInstallReply{}, nil
}

func (f *fakeCommands) UpgradeBackend(string, string, string, string, string, string, int, string, func(workerctl.BackendInstallProgressEvent)) (*workerctl.BackendUpgradeReply, error) {
	f.rec("UpgradeBackend")
	return &workerctl.BackendUpgradeReply{}, nil
}

func (f *fakeCommands) DeleteBackend(string, string) (*workerctl.BackendDeleteReply, error) {
	f.rec("DeleteBackend")
	return &workerctl.BackendDeleteReply{}, nil
}

func (f *fakeCommands) ListBackends(string) (*workerctl.BackendListReply, error) {
	f.rec("ListBackends")
	return &workerctl.BackendListReply{}, nil
}
func (f *fakeCommands) StopBackend(string, string) error { f.rec("StopBackend"); return nil }
func (f *fakeCommands) UnloadModelOnNode(string, string) error {
	f.rec("UnloadModelOnNode")
	return nil
}
func (f *fakeCommands) PingNode(string) error {
	if f.closed != nil && f.closed.Load() {
		return errUsedAfterClose
	}
	f.rec("PingNode")
	return nil
}
func (f *fakeCommands) InstallBackendOp(string, string, string, string, int, string, string, time.Duration, func(workerctl.BackendInstallProgressEvent)) (*workerctl.BackendInstallReply, error) {
	f.rec("InstallBackendOp")
	return &workerctl.BackendInstallReply{}, nil
}

func (f *fakeCommands) StopLoadOperation(context.Context, string, workerctl.ModelStopRequest) (workerctl.ModelStopReply, error) {
	f.rec("StopLoadOperation")
	return workerctl.ModelStopReply{}, nil
}

func (f *fakeCommands) OperationControl(string, workerctl.OperationRequest) (*workerctl.OperationReply, error) {
	f.rec("OperationControl")
	return &workerctl.OperationReply{}, nil
}
func (f *fakeCommands) UnloadReplica(string, nodes.NodeModel) error {
	f.rec("UnloadReplica")
	return nil
}

func (f *fakeCommands) StopModelReplica(context.Context, string, nodes.NodeModel, bool) (workerctl.ModelStopReply, error) {
	f.rec("StopModelReplica")
	return workerctl.ModelStopReply{}, nil
}

func (f *fakeCommands) ListRunningModels(string) (*workerctl.ModelsRunningReply, error) {
	f.rec("ListRunningModels")
	return &workerctl.ModelsRunningReply{}, nil
}
func (f *fakeCommands) UnloadRemoteModel(string) error { f.rec("UnloadRemoteModel"); return nil }
func (f *fakeCommands) UnloadRemoteModelContext(context.Context, string, bool) error {
	f.rec("UnloadRemoteModelContext")
	return nil
}

func (f *fakeCommands) HasRemoteModel(context.Context, string) (bool, error) {
	f.rec("HasRemoteModel")
	return true, nil
}
func (f *fakeCommands) InstallTimeout() time.Duration { f.rec("InstallTimeout"); return time.Minute }
func (f *fakeCommands) DeleteModelFiles(string) error { f.rec("DeleteModelFiles"); return nil }
func (f *fakeCommands) InstallBackendForce(string, string, string, string, string, string, int, string, func(workerctl.BackendInstallProgressEvent)) (*workerctl.BackendInstallReply, error) {
	f.rec("InstallBackendForce")
	return &workerctl.BackendInstallReply{}, nil
}

// fakeFiles is a carrier.FileCarrier that only records the call.
type fakeFiles struct{ recorder }

var _ carrier.FileCarrier = (*fakeFiles)(nil)

func (f *fakeFiles) EnsureRemote(context.Context, string, string, string) (string, error) {
	f.rec("EnsureRemote")
	return "", nil
}

func (f *fakeFiles) FetchRemote(context.Context, string, string, string) error {
	f.rec("FetchRemote")
	return nil
}

func (f *fakeFiles) FetchRemoteByKey(context.Context, string, string, string) error {
	f.rec("FetchRemoteByKey")
	return nil
}

func (f *fakeFiles) AllocRemoteTemp(context.Context, string) (string, error) {
	f.rec("AllocRemoteTemp")
	return "", nil
}

func (f *fakeFiles) StageRemoteToStore(context.Context, string, string, string) error {
	f.rec("StageRemoteToStore")
	return nil
}

func (f *fakeFiles) ReleaseRemote(context.Context, string, string) error {
	f.rec("ReleaseRemote")
	return nil
}

func (f *fakeFiles) ListRemoteDir(context.Context, string, string) ([]string, error) {
	f.rec("ListRemoteDir")
	return nil, nil
}

func (f *fakeFiles) ReleaseRemoteRequest(context.Context, string, string, []string) error {
	f.rec("ReleaseRemoteRequest")
	return nil
}

type fakeQueue struct{ recorder }

func (f *fakeQueue) Enqueue(context.Context, messaging.WorkKind, any) error {
	f.rec("Enqueue")
	return nil
}

type fakeClients struct {
	recorder
	gotNode, gotAddr string
	gotParallel      bool
}

func (f *fakeClients) NewClient(nodeID, address string, parallel bool) grpc.Backend {
	f.rec("NewClient")
	f.gotNode, f.gotAddr, f.gotParallel = nodeID, address, parallel
	return nil
}

type fakeAgents struct{ recorder }

func (f *fakeAgents) ExecuteMCPTool(context.Context, mcpRemote.MCPToolRequest) (*mcpRemote.MCPToolResponse, error) {
	f.rec("ExecuteMCPTool")
	return &mcpRemote.MCPToolResponse{}, nil
}

func (f *fakeAgents) DiscoverMCPTools(context.Context, mcpRemote.MCPDiscoveryRequest) (*mcpRemote.MCPDiscoveryResponse, error) {
	f.rec("DiscoverMCPTools")
	return &mcpRemote.MCPDiscoveryResponse{}, nil
}

// fakeCarrier is one complete carrier set with every member recorded.
type fakeCarrier struct {
	set      *carrier.Set
	bus      *testutil.FakeBus
	queue    *fakeQueue
	commands *fakeCommands
	files    *fakeFiles
	clients  *fakeClients
	agents   *fakeAgents
	dialed   *recorder
}

// newFakeCarrier builds a set named name. The two sets of a spec share a
// carrier name only when the spec says so.
func newFakeCarrier(name cluster.Carrier, epoch int64) *fakeCarrier {
	f := &fakeCarrier{
		bus:      testutil.NewFakeBus(),
		queue:    &fakeQueue{},
		commands: &fakeCommands{},
		files:    &fakeFiles{},
		clients:  &fakeClients{},
		agents:   &fakeAgents{},
		dialed:   &recorder{},
	}
	f.set = &carrier.Set{
		Name:        name,
		Epoch:       epoch,
		Broadcaster: f.bus,
		OnReconnect: f.bus.OnReconnect,
		WorkQueue:   f.queue,
		Commands:    f.commands,
		Files:       f.files,
		Clients:     f.clients,
		Dialer: func(nodeID string) func(context.Context, string, string) (net.Conn, error) {
			return func(context.Context, string, string) (net.Conn, error) {
				f.dialed.rec("dial:" + nodeID)
				return nil, nil
			}
		},
		Agents: f.agents,
	}
	return f
}
