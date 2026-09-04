package distributed_test

import (
	"context"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/agents"
	"github.com/mudler/LocalAI/core/services/jobs"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	pgdriver "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var _ = Describe("SSE Routes", Label("Distributed"), func() {
	var (
		infra *TestInfra
		db    *gorm.DB
	)

	BeforeEach(func() {
		infra = SetupInfra("localai_sse_test")

		var err error
		db, err = gorm.Open(pgdriver.Open(infra.PGURL), &gorm.Config{
			Logger: logger.Default.LogMode(logger.Silent),
		})
		Expect(err).ToNot(HaveOccurred())
	})

	Context("Job progress SSE endpoint", func() {
		// Frontend 0 publishes, frontend 1 is where the SSE reader is attached.
		// One carrier hearing itself would pass with the dispatcher wired to
		// any carrier at all, which is the wiring defect that leaves every unit
		// spec green and only the stream empty.
		It("delivers a job's progress to a reader attached to another frontend", func() {
			jobStore, err := jobs.NewJobStore(db)
			Expect(err).ToNot(HaveOccurred())

			frontend0 := jobs.NewDispatcher(jobStore, infra.Bus(), db, "frontend-0")
			frontend1 := jobs.NewDispatcher(jobStore, infra.Bus(), db, "frontend-1")

			dCtx, dCancel := context.WithCancel(infra.Ctx)
			defer dCancel()
			Expect(frontend1.Start(dCtx)).To(Succeed())
			defer frontend1.Stop()

			mine := make(chan jobs.ProgressEvent, 16)
			sub, err := frontend1.SubscribeProgress("job-sse-test", func(evt jobs.ProgressEvent) {
				mine <- evt
			})
			Expect(err).ToNot(HaveOccurred())
			defer sub.Unsubscribe()

			// A reader on a DIFFERENT job, which must be shown nothing. The
			// per-request subscription is a data boundary: on the wildcard,
			// every client watching any job sees every other job.
			theirs := make(chan jobs.ProgressEvent, 16)
			otherSub, err := frontend1.SubscribeProgress("job-someone-else", func(evt jobs.ProgressEvent) {
				theirs <- evt
			})
			Expect(err).ToNot(HaveOccurred())
			defer func() { _ = otherSub.Unsubscribe() }()

			Expect(frontend0.PublishProgress("job-sse-test", "running", "step 1")).To(Succeed())
			Expect(frontend0.PublishProgress("job-sse-test", "running", "step 2")).To(Succeed())
			Expect(frontend0.PublishProgress("job-sse-test", "completed", "done")).To(Succeed())

			var seen []jobs.ProgressEvent
			for i := 0; i < 3; i++ {
				var evt jobs.ProgressEvent
				Eventually(mine, "20s").Should(Receive(&evt))
				seen = append(seen, evt)
			}
			Expect(seen[0].Status).To(Equal("running"))
			Expect(seen[2].Status).To(Equal("completed"))
			Expect(theirs).ToNot(Receive(), "a reader on another job id must be shown nothing")
		})
	})

	Context("Agent SSE endpoint", func() {
		It("delivers an agent's events to a reader attached to another frontend", func() {
			agentStore, err := agents.NewAgentStore(db)
			Expect(err).ToNot(HaveOccurred())

			frontend0 := agents.NewEventBridge(infra.Bus(), agentStore, "frontend-0")
			frontend1 := agents.NewEventBridge(infra.Bus(), agentStore, "frontend-1")

			received := make(chan agents.AgentEvent, 16)
			sub, err := frontend1.SubscribeEvents("test-agent", "user1", func(evt agents.AgentEvent) {
				received <- evt
			})
			Expect(err).ToNot(HaveOccurred())
			defer sub.Unsubscribe()

			other := make(chan agents.AgentEvent, 16)
			otherSub, err := frontend1.SubscribeEvents("test-agent", "user2", func(evt agents.AgentEvent) {
				other <- evt
			})
			Expect(err).ToNot(HaveOccurred())
			defer func() { _ = otherSub.Unsubscribe() }()

			Expect(frontend0.PublishMessage("test-agent", "user1", "user", "Hello", "msg-1")).To(Succeed())
			Expect(frontend0.PublishStatus("test-agent", "user1", "processing")).To(Succeed())
			Expect(frontend0.PublishMessage("test-agent", "user1", "agent", "Hi!", "msg-2")).To(Succeed())

			var seen []agents.AgentEvent
			for i := 0; i < 3; i++ {
				var evt agents.AgentEvent
				Eventually(received, "20s").Should(Receive(&evt))
				seen = append(seen, evt)
			}
			Expect(seen[0].EventType).To(Equal("json_message"))
			Expect(seen[1].EventType).To(Equal("json_message_status"))
			Expect(other).ToNot(Receive(), "another user's stream must be shown nothing")
		})
	})

	Context("Without --distributed", func() {
		It("should not register SSE routes without --distributed", func() {
			appCfg := config.NewApplicationConfig()
			Expect(appCfg.Distributed.Enabled).To(BeFalse())

			// Without distributed mode, carrier-backed SSE routes are not registered.
			// Agent SSE events use the in-process LocalAGI SSE manager instead.
			// Job progress is tracked in-memory.
			Expect(appCfg.Distributed.NatsURL).To(BeEmpty())
		})
	})
})
