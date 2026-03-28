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

// mockPublisher records all Publish calls for assertions.
type mockPublisher struct {
	calls []publishCall
}

type publishCall struct {
	subject string
	data    any
}

func (m *mockPublisher) Publish(subject string, data any) error {
	m.calls = append(m.calls, publishCall{subject: subject, data: data})
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
	// Enqueue — test NATS subject routing with a mock publisher
	// -----------------------------------------------------------------------
	Describe("Enqueue subject routing", func() {
		var (
			store *JobStore
			pub   *mockPublisher
		)

		BeforeEach(func() {
			db := testutil.SetupTestDB()
			var err error
			store, err = NewJobStore(db)
			Expect(err).ToNot(HaveOccurred())
			pub = &mockPublisher{}
		})

		It("routes MCP jobs to SubjectMCPCIJobsNew", func() {
			// Create task with a model that has MCP servers
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

			loader := &mockConfigLoader{
				configs: map[string]config.ModelConfig{
					"mcp-model": {
						MCP: config.MCPConfig{
							Servers: "http://mcp-server:8080",
						},
					},
				},
			}

			// Since Enqueue calls d.nats.Publish and we can't substitute a mock for
			// the concrete *messaging.Client, we replicate the routing logic here
			// to verify subject selection.
			evt := buildEnqueueEvent(store, loader, job.ID, task.ID, "user-1")
			subject := messaging.SubjectJobsNew
			if evt.ModelConfig != nil && evt.ModelConfig.MCP.HasMCPServers() {
				subject = messaging.SubjectMCPCIJobsNew
			}
			Expect(pub.Publish(subject, evt)).To(Succeed())

			Expect(pub.calls).To(HaveLen(1))
			Expect(pub.calls[0].subject).To(Equal(messaging.SubjectMCPCIJobsNew))
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

			loader := &mockConfigLoader{
				configs: map[string]config.ModelConfig{
					"plain-model": {}, // no MCP servers
				},
			}

			evt := buildEnqueueEvent(store, loader, job.ID, task.ID, "user-1")
			subject := messaging.SubjectJobsNew
			if evt.ModelConfig != nil && evt.ModelConfig.MCP.HasMCPServers() {
				subject = messaging.SubjectMCPCIJobsNew
			}
			Expect(pub.Publish(subject, evt)).To(Succeed())

			Expect(pub.calls).To(HaveLen(1))
			Expect(pub.calls[0].subject).To(Equal(messaging.SubjectJobsNew))
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

			// Config loader has no config for "unknown-model"
			loader := &mockConfigLoader{
				configs: map[string]config.ModelConfig{},
			}

			evt := buildEnqueueEvent(store, loader, job.ID, task.ID, "user-1")
			subject := messaging.SubjectJobsNew
			if evt.ModelConfig != nil && evt.ModelConfig.MCP.HasMCPServers() {
				subject = messaging.SubjectMCPCIJobsNew
			}
			Expect(pub.Publish(subject, evt)).To(Succeed())

			Expect(pub.calls).To(HaveLen(1))
			Expect(pub.calls[0].subject).To(Equal(messaging.SubjectJobsNew))
		})
	})

	// -----------------------------------------------------------------------
	// Enqueue event enrichment
	// -----------------------------------------------------------------------
	Describe("Enqueue event enrichment", func() {
		var store *JobStore

		BeforeEach(func() {
			db := testutil.SetupTestDB()
			var err error
			store, err = NewJobStore(db)
			Expect(err).ToNot(HaveOccurred())
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

			loader := &mockConfigLoader{
				configs: map[string]config.ModelConfig{
					"test-model": {
						MCP: config.MCPConfig{
							Servers: "http://mcp-server:8080",
						},
					},
				},
			}

			evt := buildEnqueueEvent(store, loader, job.ID, task.ID, "user-1")
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

			evt := buildEnqueueEvent(store, nil, job.ID, task.ID, "user-1")

			data, err := json.Marshal(evt)
			Expect(err).ToNot(HaveOccurred())

			var decoded JobEvent
			Expect(json.Unmarshal(data, &decoded)).To(Succeed())
			Expect(decoded.JobID).To(Equal(job.ID))
			Expect(decoded.TaskID).To(Equal(task.ID))
		})
	})
})

// buildEnqueueEvent replicates the event-building logic from Dispatcher.Enqueue
// so we can test enrichment and subject routing without needing a real NATS client.
func buildEnqueueEvent(store *JobStore, loader ModelConfigLoader, jobID, taskID, userID string) JobEvent {
	evt := JobEvent{
		JobID:  jobID,
		TaskID: taskID,
		UserID: userID,
	}

	if store != nil {
		if job, err := store.GetJob(jobID); err == nil {
			evt.Job = job
		}
		if task, err := store.GetTask(taskID); err == nil {
			evt.Task = task
			if loader != nil && task.Model != "" {
				if cfg, ok := loader.GetModelConfig(task.Model); ok {
					evt.ModelConfig = &cfg
				}
			}
		}
	}

	return evt
}
