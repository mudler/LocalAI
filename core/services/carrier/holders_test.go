package carrier_test

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mudler/LocalAI/core/services/carrier"
	mcpRemote "github.com/mudler/LocalAI/core/services/mcp"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/workerctl"
	grpc "github.com/mudler/LocalAI/pkg/grpc"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// callCase calls one method of a holder.
type callCase[H any] struct {
	method string
	call   func(H)
}

var _ = Describe("WorkerRoute", func() {
	It("accepts a complete route", func() {
		Expect(newFakeCarrier(nodes.RouteDirect).set.Validate()).To(Succeed())
	})

	It("names the first member that is missing", func() {
		for name, mutate := range map[string]func(*carrier.WorkerRoute){
			"Commands": func(r *carrier.WorkerRoute) { r.Commands = nil },
			"Files":    func(r *carrier.WorkerRoute) { r.Files = nil },
			"Clients":  func(r *carrier.WorkerRoute) { r.Clients = nil },
			"Dialer":   func(r *carrier.WorkerRoute) { r.Dialer = nil },
		} {
			r := newFakeCarrier(nodes.RouteDirect).set
			mutate(r)
			Expect(r.Validate()).To(MatchError(ContainSubstring(name)), name)
		}
	})

	It("accepts a route with no agent RPC", func() {
		r := newFakeCarrier(nodes.RouteTunnel).set
		r.Agents = nil
		Expect(r.Validate()).To(Succeed())
	})

	It("refuses a route of a kind the table does not know", func() {
		r := newFakeCarrier(nodes.RouteDirect).route()
		r.Kind = "pigeon"
		Expect(r.Validate()).To(MatchError(ContainSubstring("unknown kind")))
		Expect((*carrier.WorkerRoute)(nil).Validate()).To(HaveOccurred())
	})
})

var _ = Describe("Forwarding holders", func() {
	var (
		a, b  *fakeCarrier
		cur   *carrier.RouteTable
		onlyB atomic.Bool
	)

	// moveToB leaves b as the only route of every node, as when a worker loses
	// its NATS route.
	moveToB := func() {
		onlyB.Store(true)
		cur.DropCache()
	}

	BeforeEach(func() {
		a = newFakeCarrier(nodes.RouteDirect)
		b = newFakeCarrier(nodes.RouteTunnel)
		onlyB.Store(false)
		cur = tableFrom(func(context.Context, string) ([]nodes.Route, error) {
			if onlyB.Load() {
				return []nodes.Route{{Kind: nodes.RouteTunnel}}, nil
			}
			return []nodes.Route{{Kind: nodes.RouteDirect}, {Kind: nodes.RouteTunnel}}, nil
		}, a, b)
	})

	ctx := context.Background()

	Describe("Commands", func() {
		var progress = func(workerctl.BackendInstallProgressEvent) {}

		It("passes the environment snapshot of an install on to the carrier", func() {
			var got []map[string]string
			a.set.Commands = &envCommands{fakeCommands: a.commands, got: &got}
			h := carrier.NewCommands(cur)
			_, err := h.InstallBackend("n", "t", "m", "g", "", "", "", 0, "op", progress, map[string]string{"A": "1"})
			Expect(err).ToNot(HaveOccurred())
			_, err = h.InstallBackendOp("n", "t", "m", "g", 0, "op", "load-1", time.Minute, progress, map[string]string{})
			Expect(err).ToNot(HaveOccurred())
			Expect(got).To(Equal([]map[string]string{{"A": "1"}, {}}), "a nil snapshot and an empty one mean different things to the worker")
		})
		cases := []callCase[*carrier.Commands]{
			{"InstallBackend", func(h *carrier.Commands) {
				_, _ = h.InstallBackend("n", "t", "m", "g", "u", "name", "alias", 1, "op", progress)
			}},
			{"UpgradeBackend", func(h *carrier.Commands) {
				_, _ = h.UpgradeBackend("n", "t", "g", "u", "name", "alias", 1, "op", progress)
			}},
			{"DeleteBackend", func(h *carrier.Commands) { _, _ = h.DeleteBackend("n", "b") }},
			{"ListBackends", func(h *carrier.Commands) { _, _ = h.ListBackends("n") }},
			{"StopBackend", func(h *carrier.Commands) { _ = h.StopBackend("n", "b") }},
			{"UnloadModelOnNode", func(h *carrier.Commands) { _ = h.UnloadModelOnNode("n", "m") }},
			{"PingNode", func(h *carrier.Commands) { _ = h.PingNode("n") }},
			{"InstallBackendOp", func(h *carrier.Commands) {
				_, _ = h.InstallBackendOp("n", "t", "m", "g", 1, "op", "oid", time.Second, progress)
			}},
			{"StopLoadOperation", func(h *carrier.Commands) {
				_, _ = h.StopLoadOperation(ctx, "n", workerctl.ModelStopRequest{})
			}},
			{"OperationControl", func(h *carrier.Commands) { _, _ = h.OperationControl("n", workerctl.OperationRequest{}) }},
			{"UnloadReplica", func(h *carrier.Commands) { _ = h.UnloadReplica("n", nodes.NodeModel{}) }},
			{"StopModelReplica", func(h *carrier.Commands) {
				_, _ = h.StopModelReplica(ctx, "n", nodes.NodeModel{}, true)
			}},
			{"ListRunningModels", func(h *carrier.Commands) { _, _ = h.ListRunningModels("n") }},
			{"UnloadRemoteModel", func(h *carrier.Commands) { _ = h.UnloadRemoteModel("m") }},
			{"UnloadRemoteModelContext", func(h *carrier.Commands) { _ = h.UnloadRemoteModelContext(ctx, "m", true) }},
			{"HasRemoteModel", func(h *carrier.Commands) { _, _ = h.HasRemoteModel(ctx, "m") }},
			{"InstallTimeout", func(h *carrier.Commands) { _ = h.InstallTimeout() }},
			{"DeleteModelFiles", func(h *carrier.Commands) { _ = h.DeleteModelFiles("m") }},
		}

		It("covers every method of nodes.NodeControl", func() {
			// The compile-time assertion proves the holder has each method; this
			// table proves each one is forwarded to the method of the same name.
			Expect(cases).To(HaveLen(18))
		})

		// The verbs that name no node (the fan-outs, and the reads of the
		// registry and of the install bound) run on the first route that the
		// table holds, so they reach b only on a table that holds b alone.
		nodeless := map[string]bool{
			"DeleteModelFiles": true, "HasRemoteModel": true, "InstallTimeout": true,
			"UnloadRemoteModel": true, "UnloadRemoteModelContext": true,
		}

		for _, tc := range cases {
			It("forwards "+tc.method+" to the first route of the node", func() {
				h := carrier.NewCommands(cur)
				tc.call(h)
				Expect(a.commands.seen()).To(Equal([]string{tc.method}))
				Expect(b.commands.seen()).To(BeEmpty())

				moveToB()
				if nodeless[tc.method] {
					h = carrier.NewCommands(tableFrom(nil, b))
				}
				tc.call(h)
				Expect(a.commands.seen()).To(Equal([]string{tc.method}))
				Expect(b.commands.seen()).To(Equal([]string{tc.method}))
			})
		}

		It("passes the arguments and the reply through untouched", func() {
			var got []string
			fake := &argCommands{fakeCommands: a.commands, got: &got}
			a.set.Commands = fake
			h := carrier.NewCommands(cur)

			reply, err := h.OperationControl("node-7", workerctl.OperationRequest{Renew: []string{"op-9"}})
			Expect(err).ToNot(HaveOccurred())
			Expect(reply.Renewed).To(Equal([]string{"op-9"}))
			Expect(got).To(Equal([]string{"node-7/op-9"}))
		})
	})

	Describe("Files", func() {
		cases := []callCase[*carrier.Files]{
			{"EnsureRemote", func(h *carrier.Files) { _, _ = h.EnsureRemote(ctx, "n", "l", "k") }},
			{"FetchRemote", func(h *carrier.Files) { _ = h.FetchRemote(ctx, "n", "r", "d") }},
			{"FetchRemoteByKey", func(h *carrier.Files) { _ = h.FetchRemoteByKey(ctx, "n", "k", "d") }},
			{"AllocRemoteTemp", func(h *carrier.Files) { _, _ = h.AllocRemoteTemp(ctx, "n") }},
			{"StageRemoteToStore", func(h *carrier.Files) { _ = h.StageRemoteToStore(ctx, "n", "r", "k") }},
			{"ReleaseRemote", func(h *carrier.Files) { _ = h.ReleaseRemote(ctx, "n", "k") }},
			{"ListRemoteDir", func(h *carrier.Files) { _, _ = h.ListRemoteDir(ctx, "n", "p") }},
			{"ReleaseRemoteRequest", func(h *carrier.Files) { _ = h.ReleaseRemoteRequest(ctx, "n", "req", []string{"k"}) }},
		}

		for _, tc := range cases {
			It("forwards "+tc.method+" to the first route of the node", func() {
				h := carrier.NewFiles(cur)
				tc.call(h)
				Expect(a.files.seen()).To(Equal([]string{tc.method}))
				moveToB()
				tc.call(h)
				Expect(b.files.seen()).To(Equal([]string{tc.method}))
				Expect(a.files.seen()).To(HaveLen(1))
			})
		}

		It("is the batch release the file staging client looks for", func() {
			var stager nodes.FileStager = carrier.NewFiles(cur)
			_, ok := stager.(nodes.RequestFileReleaser)
			Expect(ok).To(BeTrue(), "a holder that hid the extension would silently turn batch release into one call per key")
		})
	})

	Describe("Clients", func() {
		It("builds the client with the factory of the first route of the node", func() {
			h := carrier.NewClients(cur)
			_ = h.NewClient("node-1", "10.0.0.1:50051", true)
			Expect(a.clients.gotNode).To(Equal("node-1"))
			Expect(a.clients.gotAddr).To(Equal("10.0.0.1:50051"))
			Expect(a.clients.gotParallel).To(BeTrue())

			moveToB()
			_ = h.NewClient("node-2", "x", false)
			Expect(a.clients.seen()).To(HaveLen(1))
			Expect(b.clients.gotNode).To(Equal("node-2"))
		})
	})

	Describe("WorkerDialer", func() {
		It("dials on the route of the node at the moment of each dial", func() {
			dialFor := carrier.NewRoutedWorkerDialer(cur)
			dial := dialFor("node-1") // a client keeps this function for good

			_, _ = dial(ctx, "tcp", "x:1")
			moveToB()
			_, _ = dial(ctx, "tcp", "x:1")

			Expect(a.dialed.seen()).To(Equal([]string{"dial:node-1"}))
			Expect(b.dialed.seen()).To(Equal([]string{"dial:node-1"}))
		})
	})

	Describe("Agents", func() {
		It("forwards both verbs to the first route that carries the agent RPC", func() {
			h := carrier.NewAgents(cur)
			_, _ = h.ExecuteMCPTool(ctx, mcpRemote.MCPToolRequest{})
			_, _ = h.DiscoverMCPTools(ctx, mcpRemote.MCPDiscoveryRequest{})
			// A call that names no worker goes to the first route that carries
			// the agent RPC: b, on a table that holds b alone.
			h = carrier.NewAgents(tableFrom(nil, b))
			_, _ = h.ExecuteMCPTool(ctx, mcpRemote.MCPToolRequest{})
			Expect(a.agents.seen()).To(Equal([]string{"ExecuteMCPTool", "DiscoverMCPTools"}))
			Expect(b.agents.seen()).To(Equal([]string{"ExecuteMCPTool"}))
		})

		It("sends a call that names an agent worker on the routes of that worker", func() {
			nats := newFakeCarrier(nodes.RouteDirect)
			tun := newFakeCarrier(nodes.RouteTunnel)
			nats.agents.absent = map[string]bool{"both": true}
			t := tableOf(map[string][]nodes.Route{
				"on-nats":   {{Kind: nodes.RouteDirect}},
				"on-tunnel": {{Kind: nodes.RouteTunnel}},
				"both":      {{Kind: nodes.RouteDirect}, {Kind: nodes.RouteTunnel}},
				"nowhere":   nil,
			}, nats, tun)
			h := carrier.NewAgents(t)

			reply, err := h.ExecuteMCPToolOn(ctx, "on-nats", mcpRemote.MCPToolRequest{})
			Expect(err).ToNot(HaveOccurred())
			Expect(reply.Result).To(Equal("ran on on-nats"))
			_, err = h.DiscoverMCPToolsOn(ctx, "on-tunnel", mcpRemote.MCPDiscoveryRequest{})
			Expect(err).ToNot(HaveOccurred())
			_, err = h.ExecuteMCPToolOn(ctx, "both", mcpRemote.MCPToolRequest{})
			Expect(err).ToNot(HaveOccurred(), "no responder on the subject of the node moves the call to its tunnel")
			_, err = h.ExecuteMCPToolOn(ctx, "nowhere", mcpRemote.MCPToolRequest{})
			Expect(err).To(MatchError(nodes.ErrNoRoute))

			Expect(nats.agents.seen()).To(Equal([]string{"ExecuteMCPToolOn:on-nats", "ExecuteMCPToolOn:both"}))
			Expect(tun.agents.seen()).To(Equal([]string{"DiscoverMCPToolsOn:on-tunnel", "ExecuteMCPToolOn:both"}))
		})
	})

	Describe("a call in flight", func() {
		It("finishes on the route it started on, and a new call goes to the next route", func() {
			gate := make(chan struct{})
			entered := make(chan struct{}, 1)
			a.set.Commands = &gatedCommands{fakeCommands: a.commands, gate: gate, entered: entered}
			h := carrier.NewCommands(cur)

			done := make(chan error, 1)
			go func() {
				_, err := h.InstallBackendOp("n", "t", "m", "g", 0, "op", "oid", time.Minute, nil)
				done <- err
			}()
			Eventually(entered).Should(Receive())

			moveToB()
			Expect(h.PingNode("n")).To(Succeed())
			Expect(b.commands.seen()).To(Equal([]string{"PingNode"}))

			close(gate)
			Eventually(done, time.Second).Should(Receive(BeNil()))
			Expect(a.commands.seen()).To(Equal([]string{"InstallBackendOp"}))
		})
	})

	Describe("hot path", func() {
		It("adds no allocation to a forwarded call", func() {
			cur = carrier.NewRouteTable(nil)
			cur.Pin(newQuietCarrier().route())
			h := carrier.NewCommands(cur)
			cl := carrier.NewClients(cur)
			dial := carrier.NewRoutedWorkerDialer(cur)("node-1")

			Expect(testing.AllocsPerRun(1000, func() { _ = h.PingNode("node-1") })).To(BeZero())
			Expect(testing.AllocsPerRun(1000, func() { _ = cl.NewClient("node-1", "x:1", false) })).To(BeZero())
			Expect(testing.AllocsPerRun(1000, func() { _, _ = dial(ctx, "tcp", "x:1") })).To(BeZero())
		})

		It("adds no allocation when the routes of the node come from the cache", func() {
			routes := []nodes.Route{{Kind: nodes.RouteDirect}}
			cur = carrier.NewRouteTable(func(context.Context, string) ([]nodes.Route, error) { return routes, nil })
			cur.Pin(newQuietCarrier().route())
			h := carrier.NewCommands(cur)
			Expect(h.PingNode("node-1")).To(Succeed())
			Expect(testing.AllocsPerRun(1000, func() { _ = h.PingNode("node-1") })).To(BeZero())
		})
	})
})

// envCommands records the environment snapshot that an install carries.
type envCommands struct {
	*fakeCommands
	got *[]map[string]string
}

func (c *envCommands) InstallBackend(_, _, _, _, _, _, _ string, _ int, _ string, _ func(workerctl.BackendInstallProgressEvent), env ...map[string]string) (*workerctl.BackendInstallReply, error) {
	*c.got = append(*c.got, env...)
	return &workerctl.BackendInstallReply{Success: true}, nil
}

func (c *envCommands) InstallBackendOp(_, _, _, _ string, _ int, _, _ string, _ time.Duration, _ func(workerctl.BackendInstallProgressEvent), env ...map[string]string) (*workerctl.BackendInstallReply, error) {
	*c.got = append(*c.got, env...)
	return &workerctl.BackendInstallReply{Success: true}, nil
}

// argCommands answers OperationControl with a reply that records its arguments.
type argCommands struct {
	*fakeCommands
	got *[]string
}

func (c *argCommands) OperationControl(nodeID string, req workerctl.OperationRequest) (*workerctl.OperationReply, error) {
	*c.got = append(*c.got, nodeID+"/"+req.Renew[0])
	return &workerctl.OperationReply{Renewed: req.Renew}, nil
}

// gatedCommands blocks InstallBackendOp until gate closes.
type gatedCommands struct {
	*fakeCommands
	gate    chan struct{}
	entered chan struct{}
}

func (c *gatedCommands) InstallBackendOp(nodeID, backendType, modelID, galleriesJSON string, replicaIndex int, opID, operationID string, deadline time.Duration, onProgress func(workerctl.BackendInstallProgressEvent), env ...map[string]string) (*workerctl.BackendInstallReply, error) {
	c.entered <- struct{}{}
	<-c.gate
	return c.fakeCommands.InstallBackendOp(nodeID, backendType, modelID, galleriesJSON, replicaIndex, opID, operationID, deadline, onProgress, env...)
}

// quietCommands and quietClients answer without recording, so a
// spec can count the allocations the holder itself adds.
type quietCommands struct{ *fakeCommands }

func (quietCommands) PingNode(string) error { return nil }

type quietClients struct{}

func (quietClients) NewClient(string, string, bool) grpc.Backend { return nil }

func newQuietCarrier() *fakeCarrier {
	f := newFakeCarrier(nodes.RouteDirect)
	f.set.Commands = quietCommands{f.commands}
	f.set.Clients = quietClients{}
	f.set.Dialer = func(string) func(context.Context, string, string) (net.Conn, error) { return quietDial }
	return f
}

func quietDial(context.Context, string, string) (net.Conn, error) { return nil, nil }
