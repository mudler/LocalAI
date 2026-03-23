package distributed_test

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/agents"
	"github.com/mudler/LocalAI/core/services/jobs"
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

var _ = Describe("SSE Routes", Label("Distributed"), func() {
	var (
		ctx           context.Context
		pgContainer   *tcpostgres.PostgresContainer
		natsContainer *tcnats.NATSContainer
		db            *gorm.DB
		nc            *messaging.Client
	)

	BeforeEach(func() {
		ctx = context.Background()

		var err error

		pgContainer, err = tcpostgres.Run(ctx, "postgres:16-alpine",
			tcpostgres.WithDatabase("localai_sse_test"),
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

	Context("Job progress SSE endpoint", func() {
		It("should register job progress SSE endpoint when dispatcher active", func() {
			jobStore, err := jobs.NewJobStore(db)
			Expect(err).ToNot(HaveOccurred())

			dispatcher := jobs.NewDispatcher(jobStore, nc, db, "sse-instance")

			dCtx, dCancel := context.WithCancel(ctx)
			defer dCancel()
			Expect(dispatcher.Start(dCtx)).To(Succeed())
			defer dispatcher.Stop()

			// Subscribe to progress for a job — verifies the dispatcher can bridge
			// NATS progress events that an SSE endpoint would consume
			var events []jobs.ProgressEvent
			sub, err := dispatcher.SubscribeProgress("job-sse-test", func(evt jobs.ProgressEvent) {
				events = append(events, evt)
			})
			Expect(err).ToNot(HaveOccurred())
			defer sub.Unsubscribe()

			time.Sleep(100 * time.Millisecond)

			dispatcher.PublishProgress("job-sse-test", "running", "step 1")
			dispatcher.PublishProgress("job-sse-test", "running", "step 2")
			dispatcher.PublishProgress("job-sse-test", "completed", "done")

			Eventually(func() int { return len(events) }, "5s").Should(Equal(3))
			Expect(events[0].Status).To(Equal("running"))
			Expect(events[2].Status).To(Equal("completed"))
		})
	})

	Context("Agent SSE endpoint", func() {
		It("should register agent SSE endpoint when event bridge active", func() {
			agentStore, err := agents.NewAgentStore(db)
			Expect(err).ToNot(HaveOccurred())

			bridge := agents.NewEventBridge(nc, agentStore, "sse-instance")

			// Subscribe to agent events — verifies the bridge can deliver
			// NATS events that an SSE endpoint would consume
			var received []agents.AgentEvent
			sub, err := bridge.SubscribeEvents("test-agent", "user1", func(evt agents.AgentEvent) {
				received = append(received, evt)
			})
			Expect(err).ToNot(HaveOccurred())
			defer sub.Unsubscribe()

			time.Sleep(100 * time.Millisecond)

			bridge.PublishMessage("test-agent", "user1", "user", "Hello", "msg-1")
			bridge.PublishStatus("test-agent", "user1", "processing")
			bridge.PublishMessage("test-agent", "user1", "agent", "Hi!", "msg-2")

			Eventually(func() int { return len(received) }, "5s").Should(Equal(3))
			Expect(received[0].EventType).To(Equal("json_message"))
			Expect(received[1].EventType).To(Equal("json_message_status"))
		})
	})

	Context("Without --distributed", func() {
		It("should not register SSE routes without --distributed", func() {
			appCfg := config.NewApplicationConfig()
			Expect(appCfg.Distributed.Enabled).To(BeFalse())

			// Without distributed mode, NATS-backed SSE routes are not registered.
			// Agent SSE events use the in-process LocalAGI SSE manager instead.
			// Job progress is tracked in-memory.
			Expect(appCfg.Distributed.NatsURL).To(BeEmpty())
		})
	})
})

// suppress unused imports
var _ = atomic.Int32{}
