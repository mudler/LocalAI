package jobs

import (
	"encoding/json"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/testutil"
	"gorm.io/gorm"
)

// publishCall records a single Publish invocation.
type publishCall struct {
	subject string
	data    any
}

// recordingBus is a messaging.Broadcaster that records what was published.
//
// A Broadcaster and NOT a MessagingClient, which is the point of the type
// rather than tidiness: the dispatcher may only fan out, and a double that
// still offered Request or QueueSubscribe would let a spec exercise a surface
// the production type can no longer reach.
type recordingBus struct {
	mu    sync.Mutex
	calls []publishCall
}

func (f *recordingBus) Publish(subject string, data any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, publishCall{subject: subject, data: data})
	return nil
}

func (f *recordingBus) Subscribe(string, func([]byte)) (messaging.Subscription, error) {
	return &fakeSub{}, nil
}

func (f *recordingBus) published() []publishCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]publishCall(nil), f.calls...)
}

var _ messaging.Broadcaster = (*recordingBus)(nil)

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

			disp = NewDispatcher(store, nil, db, "test-instance")
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
	// Enqueue: the kind of claim row Dispatcher.Enqueue() writes.
	//
	// It used to choose between two NATS subjects. The choice is the same
	// choice; what it now decides is the KIND stored on the row, which is what
	// the dispatch loop maps onto a control verb. Asserted against the row
	// rather than a publish because a publish to a queue group nobody joined
	// succeeds, and that is the failure this change exists to remove.
	// -----------------------------------------------------------------------
	Describe("Enqueue claim kind", func() {
		var (
			store *JobStore
			fake  *recordingBus
			disp  *Dispatcher
			db    *gorm.DB
		)

		BeforeEach(func() {
			db = testutil.SetupTestDB()
			var err error
			store, err = NewJobStore(db)
			Expect(err).ToNot(HaveOccurred())
			fake = &recordingBus{}
			disp = NewDispatcher(store, fake, db, "test-instance")
		})

		It("writes an mcp-ci claim for a model with MCP servers", func() {
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

			Expect(onlyClaim(db).Kind).To(Equal(string(ClaimKindMCPCI)))
			Expect(fake.published()).To(BeEmpty(), "enqueueing must not publish: a queue subject nobody joined swallows the job")
		})

		It("writes a plain task claim for a model without MCP servers", func() {
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

			Expect(onlyClaim(db).Kind).To(Equal(string(ClaimKindTask)))
			Expect(fake.published()).To(BeEmpty(), "enqueueing must not publish: a queue subject nobody joined swallows the job")
		})

		It("writes a plain task claim when the model config is not found", func() {
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

			Expect(onlyClaim(db).Kind).To(Equal(string(ClaimKindTask)))
			Expect(fake.published()).To(BeEmpty(), "enqueueing must not publish: a queue subject nobody joined swallows the job")
		})
	})

	// -----------------------------------------------------------------------
	// Enqueue event enrichment: the payload stored on the claim row
	// -----------------------------------------------------------------------
	Describe("Enqueue event enrichment", func() {
		var (
			store *JobStore
			fake  *recordingBus
			disp  *Dispatcher
			db    *gorm.DB
		)

		BeforeEach(func() {
			db = testutil.SetupTestDB()
			var err error
			store, err = NewJobStore(db)
			Expect(err).ToNot(HaveOccurred())
			fake = &recordingBus{}
			disp = NewDispatcher(store, fake, db, "test-instance")
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

			evt := claimEvent(onlyClaim(db))
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

			evt := claimEvent(onlyClaim(db))

			data, err := json.Marshal(evt)
			Expect(err).ToNot(HaveOccurred())

			var decoded JobEvent
			Expect(json.Unmarshal(data, &decoded)).To(Succeed())
			Expect(decoded.JobID).To(Equal(job.ID))
			Expect(decoded.TaskID).To(Equal(task.ID))
		})
	})
})

// onlyClaim returns the single claim row Enqueue wrote, failing the spec if it
// wrote any other number.
func onlyClaim(db *gorm.DB) WorkClaim {
	GinkgoHelper()
	var rows []WorkClaim
	Expect(db.Find(&rows).Error).To(Succeed())
	Expect(rows).To(HaveLen(1), "expected exactly one claim row")
	return rows[0]
}

// claimEvent decodes the JobEvent a claim row carries.
func claimEvent(claim WorkClaim) JobEvent {
	GinkgoHelper()
	var evt JobEvent
	Expect(json.Unmarshal(claim.Payload, &evt)).To(Succeed())
	return evt
}
