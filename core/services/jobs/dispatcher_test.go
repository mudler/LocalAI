package jobs

import (
	"context"
	"encoding/json"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/testutil"
)

// enqueueCall records a single Enqueue invocation with the typed payload, so
// a spec can tell a JobEvent from pre-encoded bytes.
type enqueueCall struct {
	kind    messaging.WorkKind
	payload any
}

// fakeWorkQueue implements messaging.WorkQueue and records every Enqueue.
type fakeWorkQueue struct {
	calls []enqueueCall
}

func (f *fakeWorkQueue) Enqueue(_ context.Context, kind messaging.WorkKind, payload any) error {
	f.calls = append(f.calls, enqueueCall{kind: kind, payload: payload})
	return nil
}

// mockConfigLoader implements ModelConfigLoader for testing Enqueue routing.
type mockConfigLoader struct {
	configs map[string]config.ModelConfig
}

func (m *mockConfigLoader) GetModelConfig(name string) (config.ModelConfig, bool) {
	cfg, ok := m.configs[name]
	return cfg, ok
}

var _ = Describe("Dispatcher", func() {

	// -----------------------------------------------------------------------
	// isCronDue — only needs DB, no NATS
	// -----------------------------------------------------------------------
	Describe("isCronDue", func() {
		var (
			store *JobStore
			disp  *Dispatcher
		)

		BeforeEach(func() {
			db := testutil.SetupTestDB()
			var err error
			store, err = NewJobStore(db)
			Expect(err).ToNot(HaveOccurred())

			disp = NewDispatcher(store, nil, nil, db, "test-instance")
		})

		It("returns true when no previous job exists", func() {
			task := TaskRecord{
				ID:      "task-cron-1",
				UserID:  "user-1",
				Cron:    "*/5 * * * *",
				Enabled: true,
			}
			Expect(store.CreateTask(&task)).To(Succeed())

			Expect(disp.isCronDue(task)).To(BeTrue())
		})

		It("returns false when a recent cron job exists", func() {
			task := TaskRecord{
				ID:      "task-cron-2",
				UserID:  "user-1",
				Cron:    "*/5 * * * *",
				Enabled: true,
			}
			Expect(store.CreateTask(&task)).To(Succeed())

			// Create a cron job from 1 minute ago
			job := &JobRecord{
				TaskID:      task.ID,
				UserID:      "user-1",
				Status:      "completed",
				TriggeredBy: "cron",
			}
			Expect(store.CreateJob(job)).To(Succeed())
			// Backdate to 1 minute ago
			store.db.Model(&JobRecord{}).Where("id = ?", job.ID).
				Update("created_at", time.Now().Add(-1*time.Minute))

			Expect(disp.isCronDue(task)).To(BeFalse())
		})

		It("returns true when previous cron job is old enough", func() {
			task := TaskRecord{
				ID:      "task-cron-3",
				UserID:  "user-1",
				Cron:    "*/5 * * * *",
				Enabled: true,
			}
			Expect(store.CreateTask(&task)).To(Succeed())

			// Create a cron job from 10 minutes ago
			job := &JobRecord{
				TaskID:      task.ID,
				UserID:      "user-1",
				Status:      "completed",
				TriggeredBy: "cron",
			}
			Expect(store.CreateJob(job)).To(Succeed())
			store.db.Model(&JobRecord{}).Where("id = ?", job.ID).
				Update("created_at", time.Now().Add(-10*time.Minute))

			Expect(disp.isCronDue(task)).To(BeTrue())
		})

		It("returns false for an invalid cron expression", func() {
			task := TaskRecord{
				ID:      "task-cron-bad",
				UserID:  "user-1",
				Cron:    "not-a-cron",
				Enabled: true,
			}
			Expect(store.CreateTask(&task)).To(Succeed())

			Expect(disp.isCronDue(task)).To(BeFalse())
		})

		It("handles @every descriptor", func() {
			task := TaskRecord{
				ID:      "task-cron-every",
				UserID:  "user-1",
				Cron:    "@every 1h",
				Enabled: true,
			}
			Expect(store.CreateTask(&task)).To(Succeed())

			// No previous job — should be due
			Expect(disp.isCronDue(task)).To(BeTrue())

			// Create a job 30 minutes ago — should not be due (interval is 1h)
			job := &JobRecord{
				TaskID:      task.ID,
				UserID:      "user-1",
				Status:      "completed",
				TriggeredBy: "cron",
			}
			Expect(store.CreateJob(job)).To(Succeed())
			store.db.Model(&JobRecord{}).Where("id = ?", job.ID).
				Update("created_at", time.Now().Add(-30*time.Minute))

			Expect(disp.isCronDue(task)).To(BeFalse())
		})

		It("ignores manually triggered jobs when checking cron due", func() {
			task := TaskRecord{
				ID:      "task-cron-manual",
				UserID:  "user-1",
				Cron:    "*/5 * * * *",
				Enabled: true,
			}
			Expect(store.CreateTask(&task)).To(Succeed())

			// Create a manual job from 1 minute ago (should be ignored)
			manualJob := &JobRecord{
				TaskID:      task.ID,
				UserID:      "user-1",
				Status:      "completed",
				TriggeredBy: "manual",
			}
			Expect(store.CreateJob(manualJob)).To(Succeed())
			store.db.Model(&JobRecord{}).Where("id = ?", manualJob.ID).
				Update("created_at", time.Now().Add(-1*time.Minute))

			// No cron-triggered job exists, so it should still be due
			Expect(disp.isCronDue(task)).To(BeTrue())
		})
	})

	// -----------------------------------------------------------------------
	// Enqueue: the work kind chosen by the real Dispatcher.Enqueue()
	// -----------------------------------------------------------------------
	Describe("Enqueue work kind routing", func() {
		var (
			store *JobStore
			queue *fakeWorkQueue
			bus   *testutil.FakeBus
			disp  *Dispatcher
		)

		BeforeEach(func() {
			db := testutil.SetupTestDB()
			var err error
			store, err = NewJobStore(db)
			Expect(err).ToNot(HaveOccurred())
			queue = &fakeWorkQueue{}
			bus = testutil.NewFakeBus()
			disp = NewDispatcher(store, queue, bus, db, "test-instance")
		})

		It("enqueues MCP jobs as WorkMCPCI", func() {
			task := &TaskRecord{
				UserID:  "user-1",
				Name:    "mcp-task",
				Model:   "mcp-model",
				Enabled: true,
			}
			Expect(store.CreateTask(task)).To(Succeed())

			job := &JobRecord{
				TaskID:      task.ID,
				UserID:      "user-1",
				Status:      "pending",
				TriggeredBy: "manual",
			}
			Expect(store.CreateJob(job)).To(Succeed())

			disp.SetModelConfigLoader(&mockConfigLoader{
				configs: map[string]config.ModelConfig{
					"mcp-model": {
						MCP: config.MCPConfig{
							Servers: "http://mcp-server:8080",
						},
					},
				},
			})

			Expect(disp.Enqueue(job.ID, task.ID, "user-1")).To(Succeed())

			Expect(queue.calls).To(HaveLen(1))
			Expect(queue.calls[0].kind).To(Equal(messaging.WorkMCPCI))
			evt, ok := queue.calls[0].payload.(JobEvent)
			Expect(ok).To(BeTrue(), "the queue must be handed the typed JobEvent")
			Expect(evt.JobID).To(Equal(job.ID))
			Expect(evt.TaskID).To(Equal(task.ID))
		})

		It("enqueues non-MCP jobs as WorkTask", func() {
			task := &TaskRecord{
				UserID:  "user-1",
				Name:    "plain-task",
				Model:   "plain-model",
				Enabled: true,
			}
			Expect(store.CreateTask(task)).To(Succeed())

			job := &JobRecord{
				TaskID:      task.ID,
				UserID:      "user-1",
				Status:      "pending",
				TriggeredBy: "manual",
			}
			Expect(store.CreateJob(job)).To(Succeed())

			disp.SetModelConfigLoader(&mockConfigLoader{
				configs: map[string]config.ModelConfig{
					"plain-model": {},
				},
			})

			Expect(disp.Enqueue(job.ID, task.ID, "user-1")).To(Succeed())

			Expect(queue.calls).To(HaveLen(1))
			Expect(queue.calls[0].kind).To(Equal(messaging.WorkTask))
			evt, ok := queue.calls[0].payload.(JobEvent)
			Expect(ok).To(BeTrue(), "the queue must be handed the typed JobEvent")
			Expect(evt.JobID).To(Equal(job.ID))
		})

		It("enqueues as WorkTask when model config is not found", func() {
			task := &TaskRecord{
				UserID:  "user-1",
				Name:    "unknown-model-task",
				Model:   "unknown-model",
				Enabled: true,
			}
			Expect(store.CreateTask(task)).To(Succeed())

			job := &JobRecord{
				TaskID:      task.ID,
				UserID:      "user-1",
				Status:      "pending",
				TriggeredBy: "manual",
			}
			Expect(store.CreateJob(job)).To(Succeed())

			disp.SetModelConfigLoader(&mockConfigLoader{
				configs: map[string]config.ModelConfig{},
			})

			Expect(disp.Enqueue(job.ID, task.ID, "user-1")).To(Succeed())

			Expect(queue.calls).To(HaveLen(1))
			Expect(queue.calls[0].kind).To(Equal(messaging.WorkTask))
		})

		It("keeps queued work off the fan-out bus", func() {
			task := &TaskRecord{UserID: "user-1", Name: "bus-task", Model: "m", Enabled: true}
			Expect(store.CreateTask(task)).To(Succeed())
			job := &JobRecord{TaskID: task.ID, UserID: "user-1", Status: "pending", TriggeredBy: "manual"}
			Expect(store.CreateJob(job)).To(Succeed())

			Expect(disp.Enqueue(job.ID, task.ID, "user-1")).To(Succeed())

			Expect(queue.calls).To(HaveLen(1))
			Expect(bus.PublishCount(messaging.SubjectJobsNew)).To(BeZero())
			Expect(bus.PublishCount(messaging.SubjectMCPCIJobsNew)).To(BeZero())
		})
	})

	// -----------------------------------------------------------------------
	// Enqueue event enrichment: verify the payload enqueued by Enqueue()
	// -----------------------------------------------------------------------
	Describe("Enqueue event enrichment", func() {
		var (
			store *JobStore
			queue *fakeWorkQueue
			disp  *Dispatcher
		)

		BeforeEach(func() {
			db := testutil.SetupTestDB()
			var err error
			store, err = NewJobStore(db)
			Expect(err).ToNot(HaveOccurred())
			queue = &fakeWorkQueue{}
			disp = NewDispatcher(store, queue, nil, db, "test-instance")
		})

		It("includes full job and task records in the event", func() {
			task := &TaskRecord{
				UserID:      "user-1",
				Name:        "enrich-task",
				Description: "A task for enrichment testing",
				Model:       "test-model",
				Prompt:      "do it",
				Enabled:     true,
			}
			Expect(store.CreateTask(task)).To(Succeed())

			job := &JobRecord{
				TaskID:         task.ID,
				UserID:         "user-1",
				Status:         "pending",
				TriggeredBy:    "manual",
				ParametersJSON: `{"key":"value"}`,
			}
			Expect(store.CreateJob(job)).To(Succeed())

			disp.SetModelConfigLoader(&mockConfigLoader{
				configs: map[string]config.ModelConfig{
					"test-model": {
						MCP: config.MCPConfig{
							Servers: "http://mcp-server:8080",
						},
					},
				},
			})

			Expect(disp.Enqueue(job.ID, task.ID, "user-1")).To(Succeed())

			Expect(queue.calls).To(HaveLen(1))
			evt, ok := queue.calls[0].payload.(JobEvent)
			Expect(ok).To(BeTrue(), "enqueued payload should be a JobEvent")
			Expect(evt.Job).ToNot(BeNil())
			Expect(evt.Job.ID).To(Equal(job.ID))
			Expect(evt.Task).ToNot(BeNil())
			Expect(evt.Task.Name).To(Equal("enrich-task"))
			Expect(evt.ModelConfig).ToNot(BeNil())
			Expect(evt.ModelConfig.MCP.HasMCPServers()).To(BeTrue())
		})

		It("serializes the event to valid JSON", func() {
			task := &TaskRecord{
				UserID:  "user-1",
				Name:    "json-task",
				Model:   "m",
				Enabled: true,
			}
			Expect(store.CreateTask(task)).To(Succeed())

			job := &JobRecord{
				TaskID:      task.ID,
				UserID:      "user-1",
				Status:      "pending",
				TriggeredBy: "manual",
			}
			Expect(store.CreateJob(job)).To(Succeed())

			// No config loader — Enqueue still works, just no model config enrichment.
			Expect(disp.Enqueue(job.ID, task.ID, "user-1")).To(Succeed())

			Expect(queue.calls).To(HaveLen(1))
			evt, ok := queue.calls[0].payload.(JobEvent)
			Expect(ok).To(BeTrue())

			data, err := json.Marshal(evt)
			Expect(err).ToNot(HaveOccurred())

			var decoded JobEvent
			Expect(json.Unmarshal(data, &decoded)).To(Succeed())
			Expect(decoded.JobID).To(Equal(job.ID))
			Expect(decoded.TaskID).To(Equal(task.ID))
		})
	})

	// -----------------------------------------------------------------------
	// The frontend dispatcher only fans out: jobs leave through the WorkQueue
	// and nothing here consumes them, so a Broadcaster is all it may ask for.
	// -----------------------------------------------------------------------
	Describe("on a fan-out-only bus", func() {
		var (
			store *JobStore
			queue *fakeWorkQueue
			bus   *testutil.FakeBus
			disp  *Dispatcher
		)

		BeforeEach(func() {
			db := testutil.SetupTestDB()
			var err error
			store, err = NewJobStore(db)
			Expect(err).ToNot(HaveOccurred())
			queue = &fakeWorkQueue{}
			bus = testutil.NewFakeBus()
			// broadcastOnly hides the queue and request methods, so this
			// compiles only while NewDispatcher asks for no more than it uses.
			disp = NewDispatcher(store, queue, broadcastOnly{bus}, db, "test-instance")
			ctx, cancel := context.WithCancel(context.Background())
			Expect(disp.Start(ctx)).To(Succeed())
			DeferCleanup(func() {
				disp.Stop()
				cancel()
			})
		})

		It("still enqueues jobs and joins no queue group", func() {
			task := &TaskRecord{UserID: "user-1", Name: "fanout-task", Model: "m", Enabled: true}
			Expect(store.CreateTask(task)).To(Succeed())
			job := &JobRecord{TaskID: task.ID, UserID: "user-1", Status: "pending", TriggeredBy: "manual"}
			Expect(store.CreateJob(job)).To(Succeed())

			Expect(disp.Enqueue(job.ID, task.ID, "user-1")).To(Succeed())

			Expect(queue.calls).To(HaveLen(1))
			Expect(queue.calls[0].kind).To(Equal(messaging.WorkTask))
			Expect(bus.QueueGroups()).To(BeEmpty())
		})

		// Dispatcher.Cancel publishes jobs.<id>.cancel, and on the NATS carrier
		// nothing subscribes to it: no worker and no frontend. A cancel of a
		// distributed job therefore ends nothing there, as it has never. The
		// carrier of the claim queue gives the cancel a consumer (AgentDriver),
		// and these specs pin the NATS side so that a change to it is deliberate.
		It("publishes the cancel of a job and subscribes to none", func() {
			// What the dispatcher listens to, read before the spec adds its own.
			for _, subject := range bus.SubscribedSubjects() {
				Expect(messaging.SubjectMatches(subject, messaging.SubjectJobCancel("j1"))).To(BeFalse(), subject)
			}
			var heard []byte
			_, err := bus.Subscribe(messaging.SubjectJobCancel("j1"), func(b []byte) { heard = b })
			Expect(err).ToNot(HaveOccurred())
			Expect(disp.Cancel("j1")).To(Succeed())
			Expect(heard).To(MatchJSON(`{"job_id":"j1"}`))
		})

		It("persists the result a worker publishes", func() {
			task := &TaskRecord{UserID: "user-1", Name: "result-task", Model: "m", Enabled: true}
			Expect(store.CreateTask(task)).To(Succeed())
			job := &JobRecord{TaskID: task.ID, UserID: "user-1", Status: "running", TriggeredBy: "manual"}
			Expect(store.CreateJob(job)).To(Succeed())

			PublishJobResult(bus, job.ID, "completed", "the answer", "")

			stored, err := store.GetJob(job.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(stored.Status).To(Equal("completed"))
			Expect(stored.Result).To(Equal("the answer"))
		})
	})
})

// broadcastOnly narrows a FakeBus to the Broadcaster surface.
type broadcastOnly struct {
	messaging.Broadcaster
}
