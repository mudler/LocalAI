package distributed_test

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/gorilla/websocket"
	"github.com/libp2p/go-yamux/v5"

	"github.com/mudler/LocalAI/core/services/agents"
	"github.com/mudler/LocalAI/core/services/agentworker"
	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/nodes"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	pgdriver "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var _ = Describe("Phase 3: Agent Conversations & SSE", Label("Distributed"), func() {
	var (
		infra *TestInfra
		db    *gorm.DB
		store *agents.AgentStore
	)

	BeforeEach(func() {
		infra = SetupInfra("localai_agents_test")

		var err error
		db, err = gorm.Open(pgdriver.Open(infra.PGURL), &gorm.Config{
			Logger: logger.Default.LogMode(logger.Silent),
		})
		Expect(err).ToNot(HaveOccurred())

		store, err = agents.NewAgentStore(db)
		Expect(err).ToNot(HaveOccurred())
	})

	Context("Agent Config Store", func() {
		It("should store agent config in PostgreSQL", func() {
			cfg := &agents.AgentConfigRecord{
				UserID:     "user1",
				Name:       "my-agent",
				ConfigJSON: `{"model": "llama3", "actions": ["web_search"]}`,
				Status:     "active",
			}
			Expect(store.SaveConfig(cfg)).To(Succeed())
			Expect(cfg.ID).ToNot(BeEmpty())

			retrieved, err := store.GetConfig("user1", "my-agent")
			Expect(err).ToNot(HaveOccurred())
			Expect(retrieved.Name).To(Equal("my-agent"))
			Expect(retrieved.ConfigJSON).To(ContainSubstring("llama3"))
		})

		It("should list agent configs for a user", func() {
			Expect(store.SaveConfig(&agents.AgentConfigRecord{UserID: "u1", Name: "agent-a", ConfigJSON: "{}", Status: "active"})).To(Succeed())
			Expect(store.SaveConfig(&agents.AgentConfigRecord{UserID: "u1", Name: "agent-b", ConfigJSON: "{}", Status: "active"})).To(Succeed())
			Expect(store.SaveConfig(&agents.AgentConfigRecord{UserID: "u2", Name: "agent-c", ConfigJSON: "{}", Status: "active"})).To(Succeed())

			u1Agents, err := store.ListConfigs("u1")
			Expect(err).ToNot(HaveOccurred())
			Expect(u1Agents).To(HaveLen(2))

			allAgents, err := store.ListConfigs("")
			Expect(err).ToNot(HaveOccurred())
			Expect(allAgents).To(HaveLen(3))
		})

		It("should soft-delete agent config", func() {
			store.SaveConfig(&agents.AgentConfigRecord{UserID: "u1", Name: "deleteme", ConfigJSON: "{}", Status: "active"})

			Expect(store.DeleteConfig("u1", "deleteme")).To(Succeed())

			// Should not appear in list
			configs, _ := store.ListConfigs("u1")
			Expect(configs).To(BeEmpty())

			// But can still be found directly
			cfg, err := store.GetConfig("u1", "deleteme")
			Expect(err).ToNot(HaveOccurred())
			Expect(cfg.Status).To(Equal("deleted"))
		})

		It("should update agent config on re-save", func() {
			store.SaveConfig(&agents.AgentConfigRecord{UserID: "u1", Name: "update-me", ConfigJSON: `{"v":1}`, Status: "active"})
			store.SaveConfig(&agents.AgentConfigRecord{UserID: "u1", Name: "update-me", ConfigJSON: `{"v":2}`, Status: "active"})

			configs, _ := store.ListConfigs("u1")
			Expect(configs).To(HaveLen(1))
			Expect(configs[0].ConfigJSON).To(ContainSubstring(`"v":2`))
		})

		It("should update agent status (pause/resume)", func() {
			store.SaveConfig(&agents.AgentConfigRecord{UserID: "u1", Name: "pausable", ConfigJSON: "{}", Status: "active"})

			Expect(store.UpdateStatus("u1", "pausable", "paused")).To(Succeed())

			cfg, _ := store.GetConfig("u1", "pausable")
			Expect(cfg.Status).To(Equal("paused"))

			Expect(store.UpdateStatus("u1", "pausable", "active")).To(Succeed())
			cfg, _ = store.GetConfig("u1", "pausable")
			Expect(cfg.Status).To(Equal("active"))
		})
	})

	// Conversation history is managed client-side (browser localStorage).
	// No server-side conversation storage tests needed.

	Context("Agent SSE events on the broadcast carrier", func() {
		It("bridges an agent's SSE events from a peer replica's carrier", func() {
			// Two carriers, because the whole point of this bridge is that the
			// user's SSE connection and the replica running the agent are not
			// the same process.
			watcher := agents.NewEventBridge(infra.Bus(), store, "instance-1", nil)
			runner := agents.NewEventBridge(infra.Bus(), store, "instance-2", nil)

			received := make(chan agents.AgentEvent, 16)
			sub, err := watcher.SubscribeEvents("my-agent", "user1", func(evt agents.AgentEvent) {
				received <- evt
			})
			Expect(err).ToNot(HaveOccurred())
			defer sub.Unsubscribe()

			// Published on the OTHER replica, as an agent execution there would.
			Expect(runner.PublishMessage("my-agent", "user1", "user", "What's the weather?", "msg-1")).To(Succeed())
			Expect(runner.PublishStatus("my-agent", "user1", "processing")).To(Succeed())
			Expect(runner.PublishMessage("my-agent", "user1", "agent", "The weather is sunny.", "msg-2")).To(Succeed())
			Expect(runner.PublishStatus("my-agent", "user1", "completed")).To(Succeed())

			var evts []agents.AgentEvent
			for i := 0; i < 4; i++ {
				var evt agents.AgentEvent
				Eventually(received, "20s").Should(Receive(&evt))
				evts = append(evts, evt)
			}
			Expect(evts[0].EventType).To(Equal("json_message"))
			Expect(evts[0].Sender).To(Equal("user"))
			Expect(evts[1].EventType).To(Equal("json_message_status"))
			Expect(evts[2].Sender).To(Equal("agent"))
		})

		// Conversation persistence removed — chat history is browser-only.

		// The whole cancel path, end to end and with nothing doubled: a real
		// agent worker holding a real WebSocket + yamux tunnel, a real
		// connection row deciding which replica owns it, the real selection
		// over the real node rows, and the real control client on top.
		//
		// A cancel does not travel on a carrier any more. Its far end is the
		// agent WORKER, which has no database and so cannot join the carrier
		// the rest of the deployment fans out on, and it holds an outward
		// tunnel instead. This is what that is.
		It("cancels an agent run on a real worker over the tunnel it holds", func() {
			const replica = "instance-1"

			registry, err := nodes.NewNodeRegistry(db)
			Expect(err).ToNot(HaveOccurred())
			clusterReg := cluster.NewRegistry(db)
			Expect(clusterReg.Register(infra.Ctx, replica, "10.0.0.1:8080", "v1")).To(Succeed())
			tunnels := cluster.NewTunnelRegistry(clusterReg, replica)

			// The worker's own bridge, and the run registered on it. This is
			// the state a dispatched agent execution leaves on a worker.
			workerBridge := agents.NewWorkerEventBridge("agent-worker-e2e")
			executor := agents.NewWorkerExecutor(workerBridge, nil, "http://127.0.0.1:1", "token")
			cancelled := make(chan struct{})
			workerBridge.RegisterCancel("msg-e2e", func() { close(cancelled) })

			node := &nodes.BackendNode{Name: "agent-e2e", NodeType: nodes.NodeTypeAgent, Address: "agent-e2e:50051"}
			Expect(registry.Register(infra.Ctx, node, true)).To(Succeed())
			registered, err := registry.GetByName(infra.Ctx, "agent-e2e")
			Expect(err).ToNot(HaveOccurred())

			frontend := newTunnelFrontend()
			rt, err := agentworker.Start(infra.Ctx, agentworker.Options{
				FrontendURL:  frontend.URL(),
				NodeID:       registered.ID,
				TunnelToken:  func() string { return "tunnel-secret" },
				ControlToken: "control-token",
				Handlers:     agentworker.Config{AgentCancel: executor.Cancel},
			})
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(func() { _ = rt.Close() })
			_, err = tunnels.Attach(infra.Ctx, registered.ID, frontend.Session())
			Expect(err).ToNot(HaveOccurred())

			control := nodes.NewControlClient(nodes.WorkerNetDialerFor(func(nodeID string) func(context.Context, string, string) (net.Conn, error) {
				return cluster.NewWorkerDialer(tunnels, nil).DialerFor(nodeID, cluster.StreamTagHTTP)
			}), "control-token")
			agentControl := nodes.NewAgentControlClient(
				nodes.NewAgentSelector(registry, clusterReg, replica, time.Hour), control)

			// Issued through the frontend's own bridge, which is what a cancel
			// request landing on a replica reaches.
			bridge := agents.NewEventBridge(infra.Bus(), store, replica, agentControl)
			Expect(bridge.CancelExecution(infra.Ctx, "my-agent", "user1", "msg-e2e")).To(Succeed())
			Eventually(cancelled, "20s").Should(BeClosed())

			// And the second of the three answers, from the same live fleet:
			// a run no worker holds is NOT reported as cancelled.
			err = bridge.CancelExecution(infra.Ctx, "my-agent", "user1", "msg-nobody-holds")
			Expect(err).To(MatchError(nodes.ErrAgentRunNotOnAnyWorker))
			Expect(err).ToNot(MatchError(nodes.ErrAgentCancelUndelivered))
		})

		// Agent execution is now dispatched via AgentPoolService.dispatchChat(),
		// not via EventBridge.EnqueueExecution(). See agent_pool.go.
	})

	Context("Observables", func() {
		It("should store and retrieve observables", func() {
			store.AppendObservable(&agents.AgentObservableRecord{
				AgentName:   "u1:agent",
				EventType:   "action",
				PayloadJSON: `{"tool": "web_search", "query": "weather"}`,
			})
			store.AppendObservable(&agents.AgentObservableRecord{
				AgentName:   "u1:agent",
				EventType:   "status",
				PayloadJSON: `{"message": "completed"}`,
			})

			obs, err := store.GetObservables("u1:agent", 0)
			Expect(err).ToNot(HaveOccurred())
			Expect(obs).To(HaveLen(2))
		})

		It("should clear observables", func() {
			store.AppendObservable(&agents.AgentObservableRecord{
				AgentName: "u1:agent", EventType: "action", PayloadJSON: "{}",
			})

			Expect(store.ClearObservables("u1:agent")).To(Succeed())

			obs, _ := store.GetObservables("u1:agent", 0)
			Expect(obs).To(BeEmpty())
		})
	})
})

// tunnelFrontend is the far side of a worker's tunnel: the real WebSocket
// upgrade and the real yamux server handshake, with no LocalAI frontend behind
// it. It is what lets these specs put a REAL agent worker on a REAL tunnel
// without starting a whole server.
type tunnelFrontend struct {
	srv      *httptest.Server
	sessions chan *yamux.Session
}

func newTunnelFrontend() *tunnelFrontend {
	GinkgoHelper()
	f := &tunnelFrontend{sessions: make(chan *yamux.Session, 4)}
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

func (f *tunnelFrontend) URL() string { return f.srv.URL }

// Session waits for the worker to dial in and hands back its tunnel session.
// Waited for on a channel rather than slept on: the dial is the worker's own
// and nothing in this process orders it against the next line of the spec.
func (f *tunnelFrontend) Session() *yamux.Session {
	GinkgoHelper()
	var sess *yamux.Session
	Eventually(f.sessions, "20s").Should(Receive(&sess))
	DeferCleanup(func() { _ = sess.Close() })
	return sess
}
