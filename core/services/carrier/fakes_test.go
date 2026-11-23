package carrier_test

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/core/services/carrier"
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
}

var _ nodes.NodeControl = (*fakeCommands)(nil)

func (f *fakeCommands) InstallBackend(string, string, string, string, string, string, string, int, string, func(workerctl.BackendInstallProgressEvent), ...map[string]string) (*workerctl.BackendInstallReply, error) {
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
	f.rec("PingNode")
	return nil
}
func (f *fakeCommands) InstallBackendOp(string, string, string, string, int, string, string, time.Duration, func(workerctl.BackendInstallProgressEvent), ...map[string]string) (*workerctl.BackendInstallReply, error) {
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

type fakeAgents struct {
	recorder
	// absent are the nodes this route does not reach.
	absent map[string]bool
}

func (f *fakeAgents) ExecuteMCPToolOn(_ context.Context, nodeID string, _ mcpRemote.MCPToolRequest) (*mcpRemote.MCPToolResponse, error) {
	f.rec("ExecuteMCPToolOn:" + nodeID)
	if f.absent[nodeID] {
		return nil, nodes.ErrNoRoute
	}
	return &mcpRemote.MCPToolResponse{Result: "ran on " + nodeID}, nil
}

func (f *fakeAgents) DiscoverMCPToolsOn(_ context.Context, nodeID string, _ mcpRemote.MCPDiscoveryRequest) (*mcpRemote.MCPDiscoveryResponse, error) {
	f.rec("DiscoverMCPToolsOn:" + nodeID)
	if f.absent[nodeID] {
		return nil, nodes.ErrNoRoute
	}
	return &mcpRemote.MCPDiscoveryResponse{}, nil
}

func (f *fakeAgents) ExecuteMCPTool(context.Context, mcpRemote.MCPToolRequest) (*mcpRemote.MCPToolResponse, error) {
	f.rec("ExecuteMCPTool")
	return &mcpRemote.MCPToolResponse{}, nil
}

func (f *fakeAgents) DiscoverMCPTools(context.Context, mcpRemote.MCPDiscoveryRequest) (*mcpRemote.MCPDiscoveryResponse, error) {
	f.rec("DiscoverMCPTools")
	return &mcpRemote.MCPDiscoveryResponse{}, nil
}

// fakeCarrier is one complete worker route with every member recorded.
type fakeCarrier struct {
	set      *carrier.WorkerRoute
	bus      *testutil.FakeBus
	commands *fakeCommands
	files    *fakeFiles
	clients  *fakeClients
	agents   *fakeAgents
	dialed   *recorder
}

// newFakeCarrier builds a worker route of the given kind.
func newFakeCarrier(kind nodes.RouteKind) *fakeCarrier {
	f := &fakeCarrier{
		bus:      testutil.NewFakeBus(),
		commands: &fakeCommands{},
		files:    &fakeFiles{},
		clients:  &fakeClients{},
		agents:   &fakeAgents{},
		dialed:   &recorder{},
	}
	f.set = &carrier.WorkerRoute{
		Kind:     kind,
		Commands: f.commands,
		Files:    f.files,
		Clients:  f.clients,
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

// route returns the worker route of the fake carrier.
func (f *fakeCarrier) route() *carrier.WorkerRoute { return f.set }

// tableOf returns a route table over the routes of the fake carriers. routes
// gives the routes of each node; a node that it does not name has every route.
func tableOf(routes map[string][]nodes.Route, carriers ...*fakeCarrier) *carrier.RouteTable {
	var of carrier.RouteSource
	if routes != nil {
		of = func(_ context.Context, nodeID string) ([]nodes.Route, error) {
			r, ok := routes[nodeID]
			if !ok {
				return []nodes.Route{{Kind: nodes.RouteDirect}, {Kind: nodes.RouteTunnel}}, nil
			}
			return r, nil
		}
	}
	return tableFrom(of, carriers...)
}

// tableFrom returns a route table that reads the routes of each node from of
// and holds the routes of the fake carriers.
func tableFrom(of carrier.RouteSource, carriers ...*fakeCarrier) *carrier.RouteTable {
	t := carrier.NewRouteTable(of)
	for _, c := range carriers {
		t.Pin(c.route())
	}
	return t
}

// holder returns a broadcaster holder over a floor of its own and no primary
// bus.
func holder() *carrier.Broadcaster {
	h, err := carrier.NewBroadcaster(carrier.BroadcasterOptions{Floor: fakeFloor(testutil.NewFakeBus())})
	if err != nil {
		panic(err)
	}
	return h
}

// fakeFloor is a PostgreSQL bus over a fake.
func fakeFloor(bus *testutil.FakeBus) *carrier.Bus {
	return &carrier.Bus{Name: carrier.BusPostgres, Broadcaster: bus, OnReconnect: bus.OnReconnect}
}

// errNATSDown is what a gated bus answers to a publish while it is down.
var errNATSDown = errors.New("nats: connection is down")

// gatedBus is the view that one process has of a shared NATS server: while the
// process does not reach the server it neither publishes nor hears anything on
// it. The server itself (bus) keeps working for the other processes.
type gatedBus struct {
	bus *testutil.FakeBus
	up  atomic.Bool
	mu  sync.Mutex
	cbs []func()
}

func newGatedBus(bus *testutil.FakeBus) *gatedBus {
	g := &gatedBus{bus: bus}
	g.up.Store(true)
	return g
}

func (g *gatedBus) Publish(subject string, data any) error {
	if !g.up.Load() {
		return errNATSDown
	}
	return g.bus.Publish(subject, data)
}

func (g *gatedBus) Subscribe(subject string, handler func([]byte)) (messaging.Subscription, error) {
	return g.bus.Subscribe(subject, func(b []byte) {
		if g.up.Load() {
			handler(b)
		}
	})
}

// OnReconnect registers cb to run when the process reaches the server again.
func (g *gatedBus) OnReconnect(cb func()) {
	g.mu.Lock()
	g.cbs = append(g.cbs, cb)
	g.mu.Unlock()
}

// down makes the process lose the server.
func (g *gatedBus) down() { g.up.Store(false) }

// back makes the process reach the server again and runs its reconnect
// callbacks, as the NATS client does.
func (g *gatedBus) back() {
	g.up.Store(true)
	g.mu.Lock()
	cbs := append([]func(){}, g.cbs...)
	g.mu.Unlock()
	for _, cb := range cbs {
		cb()
	}
}

// natsBus is the NATS bus of a process over its view of the server.
func (g *gatedBus) natsBus() *carrier.Bus {
	return &carrier.Bus{Name: carrier.BusNATS, Broadcaster: g, OnReconnect: g.OnReconnect, Connected: g.up.Load}
}

// frontend is one process of a two-frontend spec: its holder over a shared
// PostgreSQL bus and its own view of a shared NATS server.
type frontend struct {
	h    *carrier.Broadcaster
	nats *gatedBus
}

func newFrontend(pg, nats *testutil.FakeBus) *frontend {
	f := &frontend{}
	var primary *carrier.Bus
	if nats != nil {
		f.nats = newGatedBus(nats)
		primary = f.nats.natsBus()
	}
	h, err := carrier.NewBroadcaster(carrier.BroadcasterOptions{Floor: fakeFloor(pg), Primary: primary})
	if err != nil {
		panic(err)
	}
	f.h = h
	return f
}
