package distributed_test

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/core/services/agents"
	"github.com/mudler/LocalAI/core/services/messaging"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/testcontainers/testcontainers-go"
	tcnats "github.com/testcontainers/testcontainers-go/modules/nats"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	pgdriver "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var _ = Describe("Phase 3: Agent Conversations & SSE", Label("Distributed"), func() {
	var (
		ctx           context.Context
		pgContainer   *tcpostgres.PostgresContainer
		natsContainer *tcnats.NATSContainer
		db            *gorm.DB
		nc            *messaging.Client
		store         *agents.AgentStore
	)

	BeforeEach(func() {
		ctx = context.Background()

		var err error

		pgContainer, err = tcpostgres.Run(ctx, "postgres:16-alpine",
			tcpostgres.WithDatabase("localai_agents_test"),
			tcpostgres.WithUsername("test"),
			tcpostgres.WithPassword("test"),
			testcontainers.WithWaitStrategy(
				wait.ForLog("database system is ready to accept connections").
					WithOccurrence(2).
					WithStartupTimeout(30*time.Second),
			),
		)
		Expect(err).ToNot(HaveOccurred())

		pgURL, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
		Expect(err).ToNot(HaveOccurred())

		db, err = gorm.Open(pgdriver.Open(pgURL), &gorm.Config{
			Logger: logger.Default.LogMode(logger.Silent),
		})
		Expect(err).ToNot(HaveOccurred())

		natsContainer, err = tcnats.Run(ctx, "nats:2-alpine")
		Expect(err).ToNot(HaveOccurred())

		natsURL, err := natsContainer.ConnectionString(ctx)
		Expect(err).ToNot(HaveOccurred())

		nc, err = messaging.New(natsURL)
		Expect(err).ToNot(HaveOccurred())

		store, err = agents.NewAgentStore(db)
		Expect(err).ToNot(HaveOccurred())
	})

	AfterEach(func() {
		if nc != nil {
			nc.Close()
		}
		if pgContainer != nil {
			pgContainer.Terminate(ctx)
		}
		if natsContainer != nil {
			natsContainer.Terminate(ctx)
		}
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
			store.SaveConfig(&agents.AgentConfigRecord{UserID: "u1", Name: "agent-a", ConfigJSON: "{}", Status: "active"})
			store.SaveConfig(&agents.AgentConfigRecord{UserID: "u1", Name: "agent-b", ConfigJSON: "{}", Status: "active"})
			store.SaveConfig(&agents.AgentConfigRecord{UserID: "u2", Name: "agent-c", ConfigJSON: "{}", Status: "active"})

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

	Context("Agent SSE Events via NATS", func() {
		It("should bridge agent SSE events via NATS", func() {
			bridge := agents.NewEventBridge(nc, store, "instance-1")

			var received []agents.AgentEvent
			sub, err := bridge.SubscribeEvents("my-agent", "user1", func(evt agents.AgentEvent) {
				received = append(received, evt)
			})
			Expect(err).ToNot(HaveOccurred())
			defer sub.Unsubscribe()

			time.Sleep(100 * time.Millisecond)

			// Publish events (simulating agent execution on another instance)
			bridge.PublishMessage("my-agent", "user1", "user", "What's the weather?", "msg-1")
			bridge.PublishStatus("my-agent", "user1", "processing")
			bridge.PublishMessage("my-agent", "user1", "agent", "The weather is sunny.", "msg-2")
			bridge.PublishStatus("my-agent", "user1", "completed")

			Eventually(func() int { return len(received) }, "5s").Should(Equal(4))
			Expect(received[0].EventType).To(Equal("json_message"))
			Expect(received[0].Sender).To(Equal("user"))
			Expect(received[1].EventType).To(Equal("json_message_status"))
			Expect(received[2].Sender).To(Equal("agent"))
		})

		// Conversation persistence removed — chat history is browser-only.

		It("should cancel running agent via NATS", func() {
			bridge := agents.NewEventBridge(nc, store, "instance-1")

			// Start cancel listener
			cancelSub, err := bridge.StartCancelListener()
			Expect(err).ToNot(HaveOccurred())
			defer cancelSub.Unsubscribe()

			// Register a cancellable context
			_, cancel := context.WithCancel(ctx)
			var cancelled atomic.Bool
			wrappedCancel := context.CancelFunc(func() {
				cancelled.Store(true)
				cancel()
			})
			bridge.RegisterCancel("my-agent", "user1", wrappedCancel)

			time.Sleep(100 * time.Millisecond)

			// Cancel via NATS
			Expect(bridge.CancelExecution("my-agent", "user1")).To(Succeed())

			Eventually(func() bool { return cancelled.Load() }, "5s").Should(BeTrue())
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
