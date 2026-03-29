package jobs

import (
	"encoding/json"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/testutil"
)

// publishCall records a single Publish invocation.
type publishCall struct {
	subject string
	data    any
}

// fakeMessagingClient implements messaging.MessagingClient and records published messages.
type fakeMessagingClient struct {
	calls []publishCall
}

func (f *fakeMessagingClient) Publish(subject string, data any) error {
	f.calls = append(f.calls, publishCall{subject: subject, data: data})
	return nil
}

func (f *fakeMessagingClient) Subscribe(string, func([]byte)) (messaging.Subscription, error) {
	return &fakeSub{}, nil
}

func (f *fakeMessagingClient) QueueSubscribe(string, string, func([]byte)) (messaging.Subscription, error) {
	return &fakeSub{}, nil
}

func (f *fakeMessagingClient) QueueSubscribeReply(string, string, func([]byte, func([]byte))) (messaging.Subscription, error) {
	return &fakeSub{}, nil
}

func (f *fakeMessagingClient) SubscribeReply(string, func([]byte, func([]byte))) (messaging.Subscription, error) {
	return &fakeSub{}, nil
}

func (f *fakeMessagingClient) Request(string, []byte, time.Duration) ([]byte, error) {
	return nil, nil
}

func (f *fakeMessagingClient) IsConnected() bool { return true }
func (f *fakeMessagingClient) Close()            {}

// fakeSub implements messaging.Subscription.
type fakeSub struct{}

func (s *fakeSub) Unsubscribe() error { return nil }

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

			disp = NewDispatcher(store, nil, db, "test-instance", 0)
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
	// Enqueue — test NATS subject routing via real Dispatcher.Enqueue()
	// -----------------------------------------------------------------------
	Describe("Enqueue subject routing", func() {
		var (
			store *JobStore
			fake  *fakeMessagingClient
			disp  *Dispatcher
		)

		BeforeEach(func() {
			db := testutil.SetupTestDB()
			var err error
			store, err = NewJobStore(db)
			Expect(err).ToNot(HaveOccurred())
			fake = &fakeMessagingClient{}
			disp = NewDispatcher(store, fake, db, "test-instance", 0)
		})

		It("routes MCP jobs to SubjectMCPCIJobsNew", func() {
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

			Expect(fake.calls).To(HaveLen(1))
			Expect(fake.calls[0].subject).To(Equal(messaging.SubjectMCPCIJobsNew))
		})

		It("routes non-MCP jobs to SubjectJobsNew", func() {
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

			Expect(fake.calls).To(HaveLen(1))
			Expect(fake.calls[0].subject).To(Equal(messaging.SubjectJobsNew))
		})

		It("routes to SubjectJobsNew when model config is not found", func() {
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

			Expect(fake.calls).To(HaveLen(1))
			Expect(fake.calls[0].subject).To(Equal(messaging.SubjectJobsNew))
		})
	})

	// -----------------------------------------------------------------------
	// Enqueue event enrichment — verify the payload published by Enqueue()
	// -----------------------------------------------------------------------
	Describe("Enqueue event enrichment", func() {
		var (
			store *JobStore
			fake  *fakeMessagingClient
			disp  *Dispatcher
		)

		BeforeEach(func() {
			db := testutil.SetupTestDB()
			var err error
			store, err = NewJobStore(db)
			Expect(err).ToNot(HaveOccurred())
			fake = &fakeMessagingClient{}
			disp = NewDispatcher(store, fake, db, "test-instance", 0)
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

			Expect(fake.calls).To(HaveLen(1))
			evt, ok := fake.calls[0].data.(JobEvent)
			Expect(ok).To(BeTrue(), "published data should be a JobEvent")
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

			Expect(fake.calls).To(HaveLen(1))
			evt, ok := fake.calls[0].data.(JobEvent)
			Expect(ok).To(BeTrue())

			data, err := json.Marshal(evt)
			Expect(err).ToNot(HaveOccurred())

			var decoded JobEvent
			Expect(json.Unmarshal(data, &decoded)).To(Succeed())
			Expect(decoded.JobID).To(Equal(job.ID))
			Expect(decoded.TaskID).To(Equal(task.ID))
		})
	})
})
