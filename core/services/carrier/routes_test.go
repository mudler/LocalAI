package carrier_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/core/services/carrier"
	mcpRemote "github.com/mudler/LocalAI/core/services/mcp"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/tunnel"
	"github.com/mudler/LocalAI/core/services/workerctl"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// holdersCommands is a carrier member that fans a model verb out over the nodes
// that hold the model, as the real sender does. A node that the route cannot
// reach fails with ErrNoRoute and keeps its row, as it does on a real route.
type holdersCommands struct {
	*fakeCommands
	holders []string
	reaches func(nodeID string) bool
}

var (
	_ nodes.NodeControl  = (*holdersCommands)(nil)
	_ nodes.ScopedFanout = (*holdersCommands)(nil)
)

func (f *holdersCommands) fan(verb string, include func(string) bool) error {
	var err error
	for _, id := range f.holders {
		if include != nil && !include(id) {
			continue
		}
		f.rec(verb + ":" + id)
		if !f.reaches(id) {
			err = errors.Join(err, fmt.Errorf("%w: node %s", nodes.ErrNoRoute, id))
		}
	}
	return err
}

func (f *holdersCommands) UnloadRemoteModel(string) error { return f.fan("unload", nil) }
func (f *holdersCommands) UnloadRemoteModelContext(context.Context, string, bool) error {
	return f.fan("unload", nil)
}
func (f *holdersCommands) DeleteModelFiles(string) error { return f.fan("delete", nil) }
func (f *holdersCommands) UnloadRemoteModelScoped(_ context.Context, _ string, _ bool, include func(string) bool) error {
	return f.fan("unload", include)
}
func (f *holdersCommands) DeleteModelFilesScoped(_ string, include func(string) bool) error {
	return f.fan("delete", include)
}

// failingCommands answers PingNode, InstallBackendOp and PrefetchModelFiles with
// the error that the spec sets, and records the node of each call.
type failingCommands struct {
	*fakeCommands
	mu  sync.Mutex
	err error
}

func (f *failingCommands) fail(err error) {
	f.mu.Lock()
	f.err = err
	f.mu.Unlock()
}

func (f *failingCommands) current() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.err
}

func (f *failingCommands) PingNode(nodeID string) error {
	f.rec("PingNode:" + nodeID)
	return f.current()
}

func (f *failingCommands) InstallBackendOp(nodeID, _, _, _ string, _ int, _, _ string, _ time.Duration, _ func(workerctl.BackendInstallProgressEvent), _ ...map[string]string) (*workerctl.BackendInstallReply, error) {
	f.rec("InstallBackendOp:" + nodeID)
	if err := f.current(); err != nil {
		return nil, err
	}
	return &workerctl.BackendInstallReply{Success: true}, nil
}

func (f *failingCommands) PrefetchModelFiles(_ context.Context, nodeID string, _ workerctl.ModelPrefetchRequest, _ time.Duration) (*workerctl.ModelPrefetchReply, error) {
	f.rec("PrefetchModelFiles:" + nodeID)
	if err := f.current(); err != nil {
		return nil, err
	}
	return &workerctl.ModelPrefetchReply{Success: true}, nil
}

// failingAgents answers the agent RPC with the error that the spec sets.
type failingAgents struct {
	recorder
	err error
}

func (f *failingAgents) ExecuteMCPTool(context.Context, mcpRemote.MCPToolRequest) (*mcpRemote.MCPToolResponse, error) {
	f.rec("ExecuteMCPTool")
	if f.err != nil {
		return nil, f.err
	}
	return &mcpRemote.MCPToolResponse{Result: "ok"}, nil
}

func (f *failingAgents) DiscoverMCPTools(context.Context, mcpRemote.MCPDiscoveryRequest) (*mcpRemote.MCPDiscoveryResponse, error) {
	f.rec("DiscoverMCPTools")
	if f.err != nil {
		return nil, f.err
	}
	return &mcpRemote.MCPDiscoveryResponse{}, nil
}

var (
	directOnly = []nodes.Route{{Kind: nodes.RouteDirect}}
	tunnelOnly = []nodes.Route{{Kind: nodes.RouteTunnel, Transport: nodes.TunnelWebSocket}}
	p2pOnly    = []nodes.Route{{Kind: nodes.RouteTunnel, Transport: nodes.TunnelP2P}}
	both       = []nodes.Route{{Kind: nodes.RouteDirect}, {Kind: nodes.RouteTunnel}}
	noRoutes   = []nodes.Route{}
)

var _ = Describe("The route table", func() {
	var (
		ctx            context.Context
		direct, tun    *fakeCarrier
		dCmds, tCmds   *failingCommands
		routesOf       map[string][]nodes.Route
		reads          atomic.Int32
		sourceErr      error
		source         carrier.RouteSource
		table          *carrier.RouteTable
		commands       *carrier.Commands
		directPingSeen func() []string
		tunnelPingSeen func() []string
	)

	BeforeEach(func() {
		ctx = context.Background()
		direct = newFakeCarrier(nodes.RouteDirect)
		tun = newFakeCarrier(nodes.RouteTunnel)
		dCmds = &failingCommands{fakeCommands: direct.commands}
		tCmds = &failingCommands{fakeCommands: tun.commands}
		direct.set.Commands, tun.set.Commands = dCmds, tCmds
		routesOf = map[string][]nodes.Route{
			"nats-only": directOnly, "ws-only": tunnelOnly, "p2p-only": p2pOnly, "both": both, "none": noRoutes,
		}
		reads.Store(0)
		sourceErr = nil
		source = func(_ context.Context, nodeID string) ([]nodes.Route, error) {
			reads.Add(1)
			if sourceErr != nil {
				return nil, sourceErr
			}
			return routesOf[nodeID], nil
		}
		table = carrier.NewRouteTable(source)
		table.Pin(direct.route())
		table.Pin(tun.route())
		commands = carrier.NewCommands(table)
		directPingSeen, tunnelPingSeen = direct.commands.seen, tun.commands.seen
	})

	Describe("the order of the routes", func() {
		It("sends a call to a NATS worker on the direct route and to a websocket or P2P worker on the tunnel", func() {
			Expect(commands.PingNode("nats-only")).To(Succeed())
			Expect(commands.PingNode("ws-only")).To(Succeed())
			Expect(commands.PingNode("p2p-only")).To(Succeed())
			Expect(directPingSeen()).To(Equal([]string{"PingNode:nats-only"}))
			Expect(tunnelPingSeen()).To(Equal([]string{"PingNode:ws-only", "PingNode:p2p-only"}))
		})

		It("prefers the direct route for a node that has both", func() {
			Expect(commands.PingNode("both")).To(Succeed())
			Expect(directPingSeen()).To(Equal([]string{"PingNode:both"}))
			Expect(tunnelPingSeen()).To(BeEmpty())
		})

		It("answers ErrNoRoute for a node with no route, and calls nothing", func() {
			err := commands.PingNode("none")
			Expect(err).To(MatchError(nodes.ErrNoRoute))
			Expect(err.Error()).To(ContainSubstring("no live tunnel"))
			Expect(directPingSeen()).To(BeEmpty())
			Expect(tunnelPingSeen()).To(BeEmpty())
		})

		It("answers ErrNoRoute for a node whose only route this frontend does not hold", func() {
			// A frontend with no NATS server holds the tunnel route only.
			table = carrier.NewRouteTable(source)
			table.Pin(tun.route())
			commands = carrier.NewCommands(table)
			err := commands.PingNode("nats-only")
			Expect(err).To(MatchError(nodes.ErrNoRoute))
			Expect(err.Error()).To(ContainSubstring("holds no route"))
			Expect(tunnelPingSeen()).To(BeEmpty())
		})

		It("tries every route it holds when the routes of the node cannot be read", func() {
			sourceErr = errors.New("the database does not answer")
			dCmds.fail(fmt.Errorf("%w: no responders", nodes.ErrNoRoute))
			Expect(commands.PingNode("unknown")).To(Succeed())
			Expect(directPingSeen()).To(Equal([]string{"PingNode:unknown"}))
			Expect(tunnelPingSeen()).To(Equal([]string{"PingNode:unknown"}))
		})

		It("reads the routes of a node once for a burst of calls", func() {
			for range 20 {
				Expect(commands.PingNode("ws-only")).To(Succeed())
			}
			Expect(reads.Load()).To(BeEquivalentTo(1))
		})

	})

	Describe("moving to the next route", func() {
		It("moves on when the first route proves that nothing was sent", func() {
			for name, notStarted := range map[string]error{
				"no responders on NATS": fmt.Errorf("request: %w: nats: no responders available for request", nodes.ErrNoRoute),
				"no tunnel":             fmt.Errorf("%w: %w", nodes.ErrNoRoute, tunnel.ErrNoRoute),
				"a request not started": fmt.Errorf("asking the replica: %w", tunnel.ErrNotOwner),
			} {
				dCmds.fail(notStarted)
				Expect(commands.PingNode("both")).To(Succeed(), name)
			}
			Expect(tunnelPingSeen()).To(HaveLen(3))
		})

		It("never moves after a timeout, a broken stream or an answer of the worker", func() {
			for name, started := range map[string]error{
				"a timeout":          fmt.Errorf("request: %w", context.DeadlineExceeded),
				"a broken stream":    fmt.Errorf("reading the reply: %w", net.ErrClosed),
				"a worker's refusal": errors.New("disk full"),
				"a slow path":        fmt.Errorf("%w: socket deadline", tunnel.ErrTransport),
			} {
				dCmds.fail(started)
				Expect(commands.PingNode("both")).To(MatchError(started), name)
			}
			Expect(tunnelPingSeen()).To(BeEmpty(), "a call that may have run is never sent again")
		})

		It("keeps the error of the last route when no route reaches the node", func() {
			dCmds.fail(fmt.Errorf("%w: no responders", nodes.ErrNoRoute))
			tCmds.fail(fmt.Errorf("%w: %w", nodes.ErrNoRoute, tunnel.ErrNoRoute))
			err := commands.PingNode("both")
			Expect(err).To(MatchError(nodes.ErrNoRoute))
			Expect(err).To(MatchError(tunnel.ErrNoRoute))
		})

		It("moves a verb with a reply on in the same way", func() {
			dCmds.fail(fmt.Errorf("%w: no responders", nodes.ErrNoRoute))
			reply, err := commands.InstallBackendOp("both", "llama-cpp", "m", "", 0, "op", "load", time.Minute, nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(reply.Success).To(BeTrue())
			Expect(tunnelPingSeen()).To(Equal([]string{"InstallBackendOp:both"}))
		})

		It("sends model.prefetch to the next route when a route cannot send it", func() {
			direct.set.Commands = direct.commands // the plain fake has no model.prefetch
			reply, err := commands.PrefetchModelFiles(ctx, "both", workerctl.ModelPrefetchRequest{}, time.Second)
			Expect(err).ToNot(HaveOccurred())
			Expect(reply.Success).To(BeTrue())
			Expect(tunnelPingSeen()).To(Equal([]string{"PrefetchModelFiles:both"}))

			tun.set.Commands = tun.commands
			_, err = commands.PrefetchModelFiles(ctx, "both", workerctl.ModelPrefetchRequest{}, time.Second)
			Expect(err).To(MatchError(nodes.ErrNoRoute))
		})
	})

	Describe("the sticky route of a node", func() {
		It("keeps the route that last worked, ahead of the default order", func() {
			dCmds.fail(fmt.Errorf("%w: no responders", nodes.ErrNoRoute))
			Expect(commands.PingNode("both")).To(Succeed())
			dCmds.fail(nil)

			Expect(commands.PingNode("both")).To(Succeed())
			Expect(commands.PingNode("both")).To(Succeed())
			Expect(directPingSeen()).To(Equal([]string{"PingNode:both"}), "the direct route is not asked again while the tunnel works")
			Expect(tunnelPingSeen()).To(Equal([]string{"PingNode:both", "PingNode:both", "PingNode:both"}))
		})

		It("leaves the sticky route when it proves that it does not reach the node", func() {
			dCmds.fail(fmt.Errorf("%w: no responders", nodes.ErrNoRoute))
			Expect(commands.PingNode("both")).To(Succeed())
			dCmds.fail(nil)
			tCmds.fail(fmt.Errorf("%w: %w", nodes.ErrNoRoute, tunnel.ErrNoRoute))

			Expect(commands.PingNode("both")).To(Succeed())
			tCmds.fail(nil)
			Expect(commands.PingNode("both")).To(Succeed())
			Expect(directPingSeen()).To(Equal([]string{"PingNode:both", "PingNode:both", "PingNode:both"}), "the direct route worked again and is sticky now")
			Expect(tunnelPingSeen()).To(HaveLen(2))
		})

		It("does not use a sticky route that the node no longer has", func() {
			dCmds.fail(fmt.Errorf("%w: no responders", nodes.ErrNoRoute))
			Expect(commands.PingNode("both")).To(Succeed())
			dCmds.fail(nil)
			routesOf["both"] = directOnly
			table.DropCache()

			Expect(commands.PingNode("both")).To(Succeed())
			Expect(directPingSeen()).To(Equal([]string{"PingNode:both", "PingNode:both"}))
		})

		It("is forgotten with the node, and the routes forget the node too", func() {
			var forgotten []string
			direct.set.ForgetNode = func(id string) { forgotten = append(forgotten, "direct:"+id) }
			tun.set.ForgetNode = func(id string) { forgotten = append(forgotten, "tunnel:"+id) }
			dCmds.fail(fmt.Errorf("%w: no responders", nodes.ErrNoRoute))
			Expect(commands.PingNode("both")).To(Succeed())
			dCmds.fail(nil)

			table.Forget("both")
			Expect(forgotten).To(ConsistOf("direct:both", "tunnel:both"))
			Expect(commands.PingNode("both")).To(Succeed())
			Expect(directPingSeen()).To(Equal([]string{"PingNode:both", "PingNode:both"}), "the default order again")
		})
	})

	Describe("pinned routes", func() {
		It("holds the route pinned for each kind, and the last pin of a kind wins", func() {
			Expect(table.Route(nodes.RouteDirect)).To(BeIdenticalTo(direct.route()))
			Expect(table.Route(nodes.RouteTunnel)).To(BeIdenticalTo(tun.route()))
			other := newFakeCarrier(nodes.RouteTunnel)
			table.Pin(other.route())
			Expect(table.Route(nodes.RouteTunnel)).To(BeIdenticalTo(other.route()))
			Expect(table.Route("pigeon")).To(BeNil())
		})

		It("ignores a nil route and a route of a kind it does not know", func() {
			table.Pin(nil)
			odd := newFakeCarrier(nodes.RouteDirect).route()
			odd.Kind = "pigeon"
			table.Pin(odd)
			Expect(table.Route(nodes.RouteDirect)).To(BeIdenticalTo(direct.route()))
		})
	})

	Describe("the other holders", func() {
		It("stages files on the route of the node and moves on only when nothing was sent", func() {
			files := carrier.NewFiles(table)
			_, err := files.EnsureRemote(ctx, "ws-only", "l", "k")
			Expect(err).ToNot(HaveOccurred())
			Expect(files.FetchRemote(ctx, "nats-only", "r", "d")).To(Succeed())
			Expect(tun.files.seen()).To(Equal([]string{"EnsureRemote"}))
			Expect(direct.files.seen()).To(Equal([]string{"FetchRemote"}))
		})

		It("builds the client of a node on its route, and on the first route for a node with none", func() {
			clients := carrier.NewClients(table)
			_ = clients.NewClient("ws-only", "x:1", false)
			Expect(tun.clients.gotNode).To(Equal("ws-only"))
			_ = clients.NewClient("none", "x:1", false)
			Expect(direct.clients.gotNode).To(Equal("none"), "the first call of the client reports the missing route")
			Expect(carrier.NewClients(carrier.NewRouteTable(nil)).NewClient("n", "x", false)).To(BeNil())
		})

		It("dials on the route of the node, and on the next route when the dial finds no route", func() {
			var dialed []string
			direct.set.Dialer = func(nodeID string) func(context.Context, string, string) (net.Conn, error) {
				return func(context.Context, string, string) (net.Conn, error) {
					dialed = append(dialed, "direct:"+nodeID)
					return nil, fmt.Errorf("%w: not attached", nodes.ErrNoRoute)
				}
			}
			tun.set.Dialer = func(nodeID string) func(context.Context, string, string) (net.Conn, error) {
				return func(context.Context, string, string) (net.Conn, error) {
					dialed = append(dialed, "tunnel:"+nodeID)
					return nil, nil
				}
			}
			dial := carrier.NewRoutedWorkerDialer(table)
			_, err := dial("both")(ctx, "tcp", "x:1")
			Expect(err).ToNot(HaveOccurred())
			_, err = dial("nats-only")(ctx, "tcp", "x:1")
			Expect(err).To(MatchError(nodes.ErrNoRoute))
			Expect(dialed).To(Equal([]string{"direct:both", "tunnel:both", "direct:nats-only"}))
		})

		It("asks the direct route for an agent worker first, then the tunnel, and moves on only on a not-started error", func() {
			dAgents, tAgents := &failingAgents{err: fmt.Errorf("%w: no agent worker on NATS", nodes.ErrNoRoute)}, &failingAgents{}
			direct.set.Agents, tun.set.Agents = dAgents, tAgents
			agents := carrier.NewAgents(table)

			reply, err := agents.ExecuteMCPTool(ctx, mcpRemote.MCPToolRequest{})
			Expect(err).ToNot(HaveOccurred())
			Expect(reply.Result).To(Equal("ok"))
			_, err = agents.DiscoverMCPTools(ctx, mcpRemote.MCPDiscoveryRequest{})
			Expect(err).ToNot(HaveOccurred())
			Expect(dAgents.seen()).To(Equal([]string{"ExecuteMCPTool", "DiscoverMCPTools"}))
			Expect(tAgents.seen()).To(Equal([]string{"ExecuteMCPTool", "DiscoverMCPTools"}))

			dAgents.err = fmt.Errorf("request: %w", context.DeadlineExceeded)
			_, err = agents.ExecuteMCPTool(ctx, mcpRemote.MCPToolRequest{})
			Expect(err).To(MatchError(context.DeadlineExceeded), "a tool that may have run is not sent again")
			Expect(tAgents.seen()).To(HaveLen(2))
		})

		It("skips a route with no agent RPC, and answers ErrNoRoute when no route has one", func() {
			direct.set.Agents = nil
			agents := carrier.NewAgents(table)
			_, err := agents.ExecuteMCPTool(ctx, mcpRemote.MCPToolRequest{})
			Expect(err).ToNot(HaveOccurred())
			Expect(tun.agents.seen()).To(Equal([]string{"ExecuteMCPTool"}))

			tun.set.Agents = nil
			_, err = agents.DiscoverMCPTools(ctx, mcpRemote.MCPDiscoveryRequest{})
			Expect(err).To(MatchError(nodes.ErrNoRoute))
		})

		It("reads the registry and the install bound on the first route, and fails with no route at all", func() {
			has, err := commands.HasRemoteModel(ctx, "m")
			Expect(err).ToNot(HaveOccurred())
			Expect(has).To(BeTrue())
			Expect(commands.InstallTimeout()).To(Equal(time.Minute))
			Expect(directPingSeen()).To(Equal([]string{"HasRemoteModel", "InstallTimeout"}))

			empty := carrier.NewCommands(carrier.NewRouteTable(nil))
			_, err = empty.HasRemoteModel(ctx, "m")
			Expect(err).To(MatchError(nodes.ErrNoRoute))
			Expect(empty.InstallTimeout()).To(BeZero())
			Expect(empty.DeleteModelFiles("m")).To(MatchError(nodes.ErrNoRoute))
		})
	})
})

var _ = Describe("The fan-out verbs through the route table", func() {
	var (
		direct, tun *fakeCarrier
		routesOf    map[string][]nodes.Route
		table       *carrier.RouteTable
		commands    *carrier.Commands
	)

	BeforeEach(func() {
		direct = newFakeCarrier(nodes.RouteDirect)
		tun = newFakeCarrier(nodes.RouteTunnel)
		routesOf = map[string][]nodes.Route{"on-nats": directOnly, "on-ws": tunnelOnly, "on-p2p": p2pOnly}
		holders := []string{"on-nats", "on-ws", "on-p2p"}
		direct.set.Commands = &holdersCommands{fakeCommands: direct.commands, holders: holders,
			reaches: func(id string) bool { return len(routesOf[id]) > 0 && routesOf[id][0].Kind == nodes.RouteDirect }}
		tun.set.Commands = &holdersCommands{fakeCommands: tun.commands, holders: holders,
			reaches: func(id string) bool {
				for _, r := range routesOf[id] {
					if r.Kind == nodes.RouteTunnel {
						return true
					}
				}
				return false
			}}
		table = tableOf(routesOf, direct, tun)
		commands = carrier.NewCommands(table)
	})

	It("unloads a model on a NATS-only node through NATS, and on a tunnel-only node through the tunnel", func() {
		Expect(commands.UnloadRemoteModelContext(context.Background(), "m", true)).To(Succeed())
		Expect(direct.commands.seen()).To(Equal([]string{"unload:on-nats"}))
		Expect(tun.commands.seen()).To(Equal([]string{"unload:on-ws", "unload:on-p2p"}))
	})

	It("does the same for the form of the unload that has no context", func() {
		Expect(commands.UnloadRemoteModel("m")).To(Succeed())
		Expect(direct.commands.seen()).To(Equal([]string{"unload:on-nats"}))
		Expect(tun.commands.seen()).To(Equal([]string{"unload:on-ws", "unload:on-p2p"}))
	})

	It("deletes the files of a model on each node through its own route", func() {
		Expect(commands.DeleteModelFiles("m")).To(Succeed())
		Expect(direct.commands.seen()).To(Equal([]string{"delete:on-nats"}))
		Expect(tun.commands.seen()).To(Equal([]string{"delete:on-ws", "delete:on-p2p"}))
	})

	It("asks the direct route for a node that has both", func() {
		routesOf["on-ws"] = both
		Expect(commands.UnloadRemoteModelContext(context.Background(), "m", false)).To(Succeed())
		Expect(direct.commands.seen()).To(Equal([]string{"unload:on-nats", "unload:on-ws"}))
		Expect(tun.commands.seen()).To(Equal([]string{"unload:on-p2p"}))
	})

	It("gives a node with no route to the first route, whose error reports it", func() {
		routesOf["on-p2p"] = noRoutes
		err := commands.UnloadRemoteModelContext(context.Background(), "m", false)
		Expect(err).To(MatchError(nodes.ErrNoRoute))
		Expect(err.Error()).To(ContainSubstring("on-p2p"))
		Expect(direct.commands.seen()).To(Equal([]string{"unload:on-nats", "unload:on-p2p"}))
	})

	It("runs the plain verb on the only route that the table holds", func() {
		table = tableOf(routesOf, tun)
		commands = carrier.NewCommands(table)
		Expect(commands.DeleteModelFiles("m")).To(MatchError(nodes.ErrNoRoute), "the tunnel reaches no NATS-only node")
		Expect(tun.commands.seen()).To(Equal([]string{"delete:on-nats", "delete:on-ws", "delete:on-p2p"}))
	})

	It("runs the plain verb on the first route when a route has no scoped form", func() {
		tun.set.Commands = tun.commands
		Expect(commands.UnloadRemoteModel("m")).To(MatchError(nodes.ErrNoRoute))
		Expect(direct.commands.seen()).To(Equal([]string{"unload:on-nats", "unload:on-ws", "unload:on-p2p"}))
		Expect(tun.commands.seen()).To(BeEmpty())
	})
})
