// SPDX-License-Identifier: MIT

package nodes_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/libp2p/go-yamux/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/agentworker"
	"github.com/mudler/LocalAI/core/services/cluster"
	mcpremote "github.com/mudler/LocalAI/core/services/mcp"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/testutil"
	"github.com/mudler/LocalAI/core/services/worker"
)

// This file drives the WHOLE path a distributed MCP call takes: a real agent
// worker holding a real tunnel, a real connection row deciding which replica
// owns it, a real relay when the owner is a peer, and the real control client
// on top. Nothing here is a double of a transport.
//
// That matters more than usual for this task. The selection it proves is a
// query against the same rows the scheduler reads absence from, and a fake
// registry that never dials would prove nothing at all about a relayed pick:
// every interesting failure (a stream a worker refuses, a tunnel a peer holds,
// a candidate whose owner is dead) exists only on the wire.

const agentControlToken = "agent-control-token"

// agentReconnectGrace is the window these specs read a lost tunnel as
// reconnecting rather than gone. Long enough that a worker registered in this
// spec and never connected is never GONE, which is what makes the undelivered
// answer reachable here.
const agentReconnectGrace = time.Hour

// fakeFrontend is the far side of a worker's tunnel: the real WebSocket
// upgrade and the real yamux server handshake.
type fakeFrontend struct {
	srv      *httptest.Server
	sessions chan *yamux.Session
}

func newFakeFrontend() *fakeFrontend {
	GinkgoHelper()
	f := &fakeFrontend{sessions: make(chan *yamux.Session, 4)}
	upgrader := websocket.Upgrader{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != cluster.ConnectPath {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		sess, err := yamux.Server(cluster.WebsocketConn(ws), nil, nil)
		if err != nil {
			_ = ws.Close()
			return
		}
		select {
		case f.sessions <- sess:
		default:
			_ = sess.Close()
		}
	}))
	DeferCleanup(f.srv.Close)
	return f
}

// session waits for the worker to dial in and hands back its tunnel session.
//
// Waited for on a channel rather than slept on: the dial is the worker's own
// and nothing in this process orders it against the next line of the spec.
func (f *fakeFrontend) session() *yamux.Session {
	GinkgoHelper()
	var sess *yamux.Session
	Eventually(f.sessions, "20s").Should(Receive(&sess))
	DeferCleanup(func() { _ = sess.Close() })
	return sess
}

// yamuxPair is the peer link between two frontend replicas, over an in-process
// pipe: the client half is what the dialling replica opens relay streams on,
// the server half is what the owning replica accepts them from.
func yamuxPair() (client, server *yamux.Session) {
	GinkgoHelper()
	a, b := net.Pipe()
	var err error
	server, err = yamux.Server(a, nil, nil)
	Expect(err).ToNot(HaveOccurred())
	client, err = yamux.Client(b, nil, nil)
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})
	return client, server
}

// peerLinks is the outbound half of the peer mesh, standing in for
// cluster.PeerPool. It opens a real yamux stream onto the owning replica's
// accepted session, which is what the relay reads its request frame from.
type peerLinks struct{ sessions map[string]*yamux.Session }

func (p *peerLinks) Open(ctx context.Context, peerID string) (net.Conn, error) {
	sess, ok := p.sessions[peerID]
	if !ok {
		return nil, cluster.ErrPeerUnreachable
	}
	return sess.OpenStream(ctx)
}

var _ = Describe("AgentControlClient", func() {
	const (
		selfInstance  = "replica-me"
		peerInstance  = "replica-peer"
		toolCallReply = "Weather in London: 15C, cloudy"
	)

	var (
		ctx        context.Context
		cancel     context.CancelFunc
		db         *gorm.DB
		registry   *nodes.NodeRegistry
		clusterReg *cluster.Registry
		mine       *cluster.TunnelRegistry
		theirs     *cluster.TunnelRegistry
		peers      *peerLinks
		control    *nodes.ControlClient
	)

	BeforeEach(func() {
		if runtime.GOOS == "darwin" {
			Skip("testcontainers requires Docker, not available on macOS CI")
		}
		ctx, cancel = context.WithCancel(context.Background())
		DeferCleanup(cancel)

		db = testutil.SetupTestDB()
		var err error
		registry, err = nodes.NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())
		clusterReg = cluster.NewRegistry(db)
		Expect(clusterReg.Register(ctx, selfInstance, "10.0.0.1:8080", "v1")).To(Succeed())
		Expect(clusterReg.Register(ctx, peerInstance, "10.0.0.2:8080", "v1")).To(Succeed())

		mine = cluster.NewTunnelRegistry(clusterReg, selfInstance)
		theirs = cluster.NewTunnelRegistry(clusterReg, peerInstance)

		// The owning replica's inbound half, with the relay installed, and this
		// replica's outbound half pointed at it. Together they are the path a
		// request takes when the worker's tunnel landed somewhere else.
		relayed := cluster.NewSessionStore(cluster.NewRelay(theirs).Stream)
		DeferCleanup(relayed.CloseAll)
		client, server := yamuxPair()
		relayed.Accept(selfInstance, server)
		peers = &peerLinks{sessions: map[string]*yamux.Session{peerInstance: client}}

		dialer := cluster.NewWorkerDialer(mine, peers)
		control = nodes.NewControlClient(nodes.WorkerNetDialerFor(func(nodeID string) func(context.Context, string, string) (net.Conn, error) {
			return dialer.DialerFor(nodeID, cluster.StreamTagHTTP)
		}), agentControlToken)
	})

	// registerAgent puts an approved agent node in the registry and returns its
	// assigned id, which is the id the connection row and the tunnel both use.
	registerAgent := func(name string) string {
		GinkgoHelper()
		Expect(registry.Register(ctx, &nodes.BackendNode{
			Name: name, NodeType: nodes.NodeTypeAgent, Address: name + ":50051",
		}, true)).To(Succeed())
		node, err := registry.GetByName(ctx, name)
		Expect(err).ToNot(HaveOccurred())
		return node.ID
	}

	// startAgent runs a REAL agent worker serving cfg, dialling a tunnel that
	// lands on holder, and returns its node id.
	startAgent := func(name string, holder *cluster.TunnelRegistry, cfg agentworker.Config) string {
		GinkgoHelper()
		nodeID := registerAgent(name)
		frontend := newFakeFrontend()
		rt, err := agentworker.Start(ctx, agentworker.Options{
			FrontendURL:  frontend.srv.URL,
			NodeID:       nodeID,
			TunnelToken:  func() string { return "tunnel-secret" },
			ControlToken: agentControlToken,
			Handlers:     cfg,
		})
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { _ = rt.Close() })
		_, err = holder.Attach(ctx, nodeID, frontend.session())
		Expect(err).ToNot(HaveOccurred())
		return nodeID
	}

	// startRefusingAgent runs a worker whose tunnel is real and whose local
	// service will not open, which is what a worker with a dead control server
	// looks like from the frontend: the worker itself writes the refusal, and
	// the frontend reads cluster.ErrStreamTargetUnavailable off the wire.
	startRefusingAgent := func(name string, holder *cluster.TunnelRegistry, refusals *atomic.Int32) string {
		GinkgoHelper()
		nodeID := registerAgent(name)
		frontend := newFakeFrontend()
		tunnel, err := worker.StartTunnel(ctx, worker.TunnelConfig{
			FrontendURL: frontend.srv.URL,
			NodeID:      nodeID,
			Token:       func() string { return "tunnel-secret" },
			Services: map[string]worker.LocalService{
				cluster.StreamTagHTTP: func(context.Context, string) (net.Conn, error) {
					refusals.Add(1)
					return nil, errors.New("this worker's control server is not listening")
				},
			},
		})
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { _ = tunnel.Close() })
		_, err = holder.Attach(ctx, nodeID, frontend.session())
		Expect(err).ToNot(HaveOccurred())
		return nodeID
	}

	// recordingTool answers every tool call with reply and counts its calls.
	recordingTool := func(calls *atomic.Int32, reply mcpremote.MCPToolResponse) agentworker.UnaryHandler {
		return func(_ context.Context, raw json.RawMessage) (json.RawMessage, error) {
			calls.Add(1)
			var req mcpremote.MCPToolRequest
			Expect(json.Unmarshal(raw, &req)).To(Succeed())
			out, err := json.Marshal(reply)
			Expect(err).ToNot(HaveOccurred())
			return out, nil
		}
	}

	agentControl := func() *nodes.AgentControlClient {
		return nodes.NewAgentControlClient(
			nodes.NewAgentSelector(registry, clusterReg, selfInstance, agentReconnectGrace), control)
	}

	toolRequest := mcpremote.MCPToolRequest{
		ModelName: "test-model",
		ToolName:  "weather",
		Arguments: map[string]any{"city": "London"},
	}

	It("carries a tool call over the tunnel of an agent THIS replica holds", func() {
		var calls atomic.Int32
		startAgent("agent-local", mine, agentworker.Config{
			MCPTool: recordingTool(&calls, mcpremote.MCPToolResponse{Result: toolCallReply}),
		})

		resp, err := agentControl().ExecuteMCPTool(ctx, toolRequest)
		Expect(err).ToNot(HaveOccurred())
		Expect(resp.Result).To(Equal(toolCallReply))
		Expect(calls.Load()).To(Equal(int32(1)))
	})

	It("reaches an agent whose tunnel a PEER holds, by relaying through that peer", func() {
		// The hop the queue group used to hide. This replica holds nothing, so
		// the only way the request reaches the worker is the connection row
		// naming the peer and the peer's relay splicing the stream onto the
		// tunnel it holds.
		var calls atomic.Int32
		startAgent("agent-remote", theirs, agentworker.Config{
			MCPTool: recordingTool(&calls, mcpremote.MCPToolResponse{Result: toolCallReply}),
		})
		Expect(mine.Held()).To(BeEmpty(), "this spec is only about the relayed path")

		resp, err := agentControl().ExecuteMCPTool(ctx, toolRequest)
		Expect(err).ToNot(HaveOccurred())
		Expect(resp.Result).To(Equal(toolCallReply))
		Expect(calls.Load()).To(Equal(int32(1)))
	})

	It("answers discovery from the agent worker's own reply", func() {
		startAgent("agent-disco", mine, agentworker.Config{
			MCPDiscovery: func(context.Context, json.RawMessage) (json.RawMessage, error) {
				return json.Marshal(mcpremote.MCPDiscoveryResponse{
					Servers: []mcpremote.MCPServerInfo{{Name: "weather", Type: "remote", Tools: []string{"get_weather"}}},
				})
			},
		})

		resp, err := agentControl().DiscoverMCPTools(ctx, mcpremote.MCPDiscoveryRequest{ModelName: "test-model"})
		Expect(err).ToNot(HaveOccurred())
		Expect(resp.Servers).To(HaveLen(1))
		Expect(resp.Servers[0].Name).To(Equal("weather"))
	})

	It("returns a worker's own error answer and never offers the tool to a second worker", func() {
		// The one thing the retry may not do. "This MCP server rejected your
		// arguments" retried elsewhere becomes "the fleet is broken", and a
		// tool with a side effect would run twice.
		//
		// Both workers answer the same refusal, so which one the random
		// tie-break picks does not matter: what is asserted is that exactly ONE
		// handler ran in total.
		var first, second atomic.Int32
		refusal := mcpremote.MCPToolResponse{Error: "tool 'weather' is not configured"}
		startAgent("agent-a", mine, agentworker.Config{MCPTool: recordingTool(&first, refusal)})
		startAgent("agent-b", mine, agentworker.Config{MCPTool: recordingTool(&second, refusal)})

		_, err := agentControl().ExecuteMCPTool(ctx, toolRequest)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("tool 'weather' is not configured"))
		Expect(first.Load()+second.Load()).To(Equal(int32(1)),
			"the worker's own answer must not be offered to another worker")
	})

	It("retries against another agent when one refuses the stream, and the second answers", func() {
		// Deterministic without touching the tie-break: the refusing worker is
		// the one THIS replica holds, which the selector prefers, and the
		// worker that answers is peer-held, which is where the retry has to
		// fall back to. So the first pick always refuses, the second always
		// relays, and both halves of the rule are exercised on the wire.
		var refusals atomic.Int32
		var calls atomic.Int32
		startRefusingAgent("agent-dead", mine, &refusals)
		startAgent("agent-alive", theirs, agentworker.Config{
			MCPTool: recordingTool(&calls, mcpremote.MCPToolResponse{Result: toolCallReply}),
		})

		resp, err := agentControl().ExecuteMCPTool(ctx, toolRequest)
		Expect(err).ToNot(HaveOccurred())
		Expect(resp.Result).To(Equal(toolCallReply))
		Expect(refusals.Load()).To(Equal(int32(1)), "the first pick must have been the refusing worker")
		Expect(calls.Load()).To(Equal(int32(1)))
	})

	It("gives up after three picks and returns the last refusal unchanged", func() {
		// The bound, and the taxonomy. Returning the last error UNWRAPPED is
		// what keeps cluster.IsWorkerAnswer able to see what the worker
		// actually said; wrapping it in the unroutable umbrella here would make
		// a fleet that is refusing look like a frontend with no route.
		var refusals atomic.Int32
		for _, name := range []string{"agent-1", "agent-2", "agent-3", "agent-4"} {
			startRefusingAgent(name, mine, &refusals)
		}

		_, err := agentControl().ExecuteMCPTool(ctx, toolRequest)
		Expect(err).To(MatchError(cluster.ErrStreamTargetUnavailable))
		Expect(cluster.IsWorkerAnswer(err)).To(BeTrue())
		Expect(errors.Is(err, nodes.ErrWorkerUnroutable)).To(BeFalse())
		Expect(errors.Is(err, nodes.ErrNoAgentWorker)).To(BeFalse(),
			"three workers refused; that is not an empty fleet")
		Expect(refusals.Load()).To(Equal(int32(3)), "exactly three picks, never a fourth")
	})

	It("reports an empty fleet as neither a route verdict nor a worker answer", func() {
		registerAgent("agent-registered-but-offline")

		_, err := agentControl().ExecuteMCPTool(ctx, toolRequest)
		Expect(err).To(MatchError(nodes.ErrNoAgentWorker))
		Expect(errors.Is(err, nodes.ErrWorkerUnroutable)).To(BeFalse())
		Expect(cluster.IsWorkerAnswer(err)).To(BeFalse())
	})

	// The cancel, over the same real transport as everything above it.
	//
	// A cancel is a FAN-OUT and not a pick: no row in this deployment records
	// which worker holds a given execution, so the cancel is offered to every
	// worker a live replica can reach and each answers only for itself. What
	// these pin is that the three answers stay apart, because a caller told any
	// one of them in place of another acts on something that did not happen.
	Describe("cancelling one agent run", func() {
		// cancelHandler answers the cancel verb the way a worker does: true
		// when it holds the named run, false when it does not.
		cancelHandler := func(mine string, seen *atomic.Int32) agentworker.UnaryHandler {
			return func(_ context.Context, raw json.RawMessage) (json.RawMessage, error) {
				seen.Add(1)
				var req messaging.AgentCancelRequest
				Expect(json.Unmarshal(raw, &req)).To(Succeed())
				return json.Marshal(messaging.AgentCancelReply{Cancelled: req.MessageID == mine})
			}
		}

		cancelOf := func(messageID string) messaging.AgentCancelRequest {
			return messaging.AgentCancelRequest{AgentName: "a1", UserID: "u1", MessageID: messageID}
		}

		It("reaches the worker holding the run over the tunnel THIS replica holds", func() {
			var asked atomic.Int32
			startAgent("agent-holder", mine, agentworker.Config{
				AgentCancel: cancelHandler("msg-1", &asked),
			})

			Expect(agentControl().CancelAgentRun(ctx, cancelOf("msg-1"))).To(Succeed())
			Expect(asked.Load()).To(Equal(int32(1)))
		})

		It("reaches a worker whose tunnel a PEER holds, by relaying through that peer", func() {
			// The hop the broadcast used to hide. This replica holds nothing,
			// so the only way the cancel arrives is the connection row naming
			// the peer and the peer's relay splicing the stream onto the tunnel
			// it holds.
			var asked atomic.Int32
			startAgent("agent-remote", theirs, agentworker.Config{
				AgentCancel: cancelHandler("msg-1", &asked),
			})
			Expect(mine.Held()).To(BeEmpty(), "this spec is only about the relayed path")

			Expect(agentControl().CancelAgentRun(ctx, cancelOf("msg-1"))).To(Succeed())
			Expect(asked.Load()).To(Equal(int32(1)))
		})

		It("asks EVERY reachable worker, because nothing records which one holds the run", func() {
			// A pick would ask one and, four times in five, report a run that
			// is running as one no worker is running.
			var first, second atomic.Int32
			startAgent("agent-a", mine, agentworker.Config{AgentCancel: cancelHandler("nothing-here", &first)})
			startAgent("agent-b", theirs, agentworker.Config{AgentCancel: cancelHandler("msg-1", &second)})

			Expect(agentControl().CancelAgentRun(ctx, cancelOf("msg-1"))).To(Succeed())
			Expect(first.Load()).To(Equal(int32(1)))
			Expect(second.Load()).To(Equal(int32(1)))
		})

		It("reports a run no reachable worker holds as exactly that, and never as undelivered", func() {
			var asked atomic.Int32
			startAgent("agent-a", mine, agentworker.Config{AgentCancel: cancelHandler("some-other-run", &asked)})

			err := agentControl().CancelAgentRun(ctx, cancelOf("msg-1"))
			Expect(err).To(MatchError(nodes.ErrAgentRunNotOnAnyWorker))
			Expect(err).ToNot(MatchError(nodes.ErrAgentCancelUndelivered))
			Expect(asked.Load()).To(Equal(int32(1)))
		})

		It("reports a worker it could not reach as UNDELIVERED and never as a run that does not exist", func() {
			// The refusing worker's tunnel is real and its control server will
			// not open, which is what a worker with a dead control plane looks
			// like from here. Nothing was learned about the run, so the answer
			// may not be "no worker is running it".
			var refusals atomic.Int32
			startRefusingAgent("agent-dead", mine, &refusals)

			err := agentControl().CancelAgentRun(ctx, cancelOf("msg-1"))
			Expect(err).To(MatchError(nodes.ErrAgentCancelUndelivered))
			Expect(err).ToNot(MatchError(nodes.ErrAgentRunNotOnAnyWorker))
			Expect(refusals.Load()).To(Equal(int32(1)))
		})

		It("counts a RECONNECTING worker as undelivered, which is the decision this task made", func() {
			// A registered, approved agent worker with no live tunnel and a
			// departure inside the grace. Nobody may act on that condition, so
			// the cancel is reported as one that may not have arrived rather
			// than retried here, queued, or folded into "no worker holds it".
			var asked atomic.Int32
			startAgent("agent-alive", mine, agentworker.Config{AgentCancel: cancelHandler("some-other-run", &asked)})
			registerAgent("agent-reconnecting")

			err := agentControl().CancelAgentRun(ctx, cancelOf("msg-1"))
			Expect(err).To(MatchError(nodes.ErrAgentCancelUndelivered))
			Expect(err).ToNot(MatchError(nodes.ErrAgentRunNotOnAnyWorker))
			Expect(asked.Load()).To(Equal(int32(1)), "the worker that WAS reachable must still have been asked")
		})

		It("reports a worker's cancellation even when another worker could not be reached", func() {
			// There is one execution and one worker cancelled it. Reporting
			// undelivered here would tell the caller nothing happened when
			// something did.
			var refusals, asked atomic.Int32
			startRefusingAgent("agent-dead", mine, &refusals)
			startAgent("agent-holder", theirs, agentworker.Config{AgentCancel: cancelHandler("msg-1", &asked)})

			Expect(agentControl().CancelAgentRun(ctx, cancelOf("msg-1"))).To(Succeed())
			Expect(refusals.Load()).To(Equal(int32(1)))
			Expect(asked.Load()).To(Equal(int32(1)))
		})

		It("reports an empty fleet as neither a route verdict nor a worker answer", func() {
			err := agentControl().CancelAgentRun(ctx, cancelOf("msg-1"))
			Expect(err).To(MatchError(nodes.ErrNoAgentWorker))
			Expect(errors.Is(err, nodes.ErrWorkerUnroutable)).To(BeFalse())
			Expect(cluster.IsWorkerAnswer(err)).To(BeFalse())
		})

		It("refuses on a nil client rather than panicking inside a request", func() {
			var missing *nodes.AgentControlClient
			Expect(missing.CancelAgentRun(ctx, cancelOf("msg-1"))).To(MatchError(nodes.ErrNoAgentControl))
		})

		// The reap guard's two predicates, asserted on the errors a REAL
		// fan-out over a real tunnel actually returns.
		//
		// A cancel is offered to many workers and its answer is assembled from
		// all of them, so no answer it produces speaks about any one node. The
		// guard that reaps, demotes and evicts decides on exactly these two
		// predicates, and a cancel that satisfied either would let one worker
		// whose control server happened to be down take a node out of the
		// deployment.
		//
		// The edit that breaks this is small and reads like an improvement:
		// wrapping the per-worker Call failure with %w instead of naming the
		// nodes with %v. cluster.ErrStreamTargetUnavailable then travels out of
		// the fan-out, every spec above still passes, and an undelivered cancel
		// has become a routing verdict about a node.
		DescribeTable("produces no answer a reap guard may act on, whichever of the three it is",
			func(fleet func(), sentinel error) {
				fleet()
				err := agentControl().CancelAgentRun(ctx, cancelOf("msg-1"))
				Expect(err).To(MatchError(sentinel))
				Expect(errors.Is(err, nodes.ErrWorkerUnroutable)).To(BeFalse(),
					"a cancel answer was readable as a routing verdict about a worker, which the scheduler may act on")
				Expect(cluster.IsWorkerAnswer(err)).To(BeFalse(),
					"a cancel answer was readable as a worker's own answer, which a reap guard may act on")
			},
			Entry("a worker that could not be asked", func() {
				var refusals atomic.Int32
				startRefusingAgent("agent-dead", mine, &refusals)
			}, nodes.ErrAgentCancelUndelivered),
			Entry("a worker that is reconnecting", func() {
				registerAgent("agent-reconnecting")
			}, nodes.ErrAgentCancelUndelivered),
			Entry("every reachable worker answering that it does not hold the run", func() {
				var asked atomic.Int32
				startAgent("agent-a", mine, agentworker.Config{AgentCancel: cancelHandler("some-other-run", &asked)})
			}, nodes.ErrAgentRunNotOnAnyWorker),
			Entry("a deployment with no agent worker at all", func() {}, nodes.ErrNoAgentWorker),
		)
	})

	It("refuses on a nil client rather than panicking inside a request", func() {
		// The interface a caller holds this through is satisfied by a typed nil
		// pointer, which is not an untyped nil and passes every `!= nil` check
		// a call site makes.
		var missing *nodes.AgentControlClient
		_, err := missing.ExecuteMCPTool(ctx, toolRequest)
		Expect(err).To(MatchError(nodes.ErrNoAgentControl))
		_, err = missing.DiscoverMCPTools(ctx, mcpremote.MCPDiscoveryRequest{})
		Expect(err).To(MatchError(nodes.ErrNoAgentControl))
	})
})
