package distributed_test

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"time"

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

var _ = Describe("Phase 2: Jobs & Tasks", Label("Distributed"), func() {
	var (
		ctx           context.Context
		pgContainer   *tcpostgres.PostgresContainer
		natsContainer *tcnats.NATSContainer
		db            *gorm.DB
		nc            *messaging.Client
		store         *jobs.JobStore
	)

	BeforeEach(func() {
		ctx = context.Background()

		var err error

		pgContainer, err = tcpostgres.Run(ctx, "postgres:16-alpine",
			tcpostgres.WithDatabase("localai_jobs_test"),
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

		store, err = jobs.NewJobStore(db)
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

	Context("Job Store (PostgreSQL)", func() {
		It("should create and retrieve a task", func() {
			task := &jobs.TaskRecord{
				UserID:  "user1",
				Name:    "test-task",
				Model:   "test-model",
				Prompt:  "Hello {{.name}}",
				Enabled: true,
			}
			Expect(store.CreateTask(task)).To(Succeed())
			Expect(task.ID).ToNot(BeEmpty())

			retrieved, err := store.GetTask(task.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(retrieved.Name).To(Equal("test-task"))
			Expect(retrieved.Model).To(Equal("test-model"))
		})

		It("should list tasks for a user", func() {
			store.CreateTask(&jobs.TaskRecord{UserID: "u1", Name: "t1", Model: "m1", Prompt: "p1"})
			store.CreateTask(&jobs.TaskRecord{UserID: "u1", Name: "t2", Model: "m2", Prompt: "p2"})
			store.CreateTask(&jobs.TaskRecord{UserID: "u2", Name: "t3", Model: "m3", Prompt: "p3"})

			tasks, err := store.ListTasks("u1")
			Expect(err).ToNot(HaveOccurred())
			Expect(tasks).To(HaveLen(2))

			allTasks, err := store.ListTasks("")
			Expect(err).ToNot(HaveOccurred())
			Expect(allTasks).To(HaveLen(3))
		})

		It("should create and retrieve a job", func() {
			task := &jobs.TaskRecord{UserID: "u1", Name: "t1", Model: "m1", Prompt: "p1"}
			store.CreateTask(task)

			job := &jobs.JobRecord{
				TaskID:      task.ID,
				UserID:      "u1",
				Status:      "pending",
				TriggeredBy: "manual",
			}
			Expect(store.CreateJob(job)).To(Succeed())
			Expect(job.ID).ToNot(BeEmpty())

			retrieved, err := store.GetJob(job.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(retrieved.TaskID).To(Equal(task.ID))
			Expect(retrieved.Status).To(Equal("pending"))
		})

		It("should update job status", func() {
			task := &jobs.TaskRecord{UserID: "u1", Name: "t1", Model: "m1", Prompt: "p1"}
			store.CreateTask(task)

			job := &jobs.JobRecord{TaskID: task.ID, UserID: "u1", Status: "pending", TriggeredBy: "api"}
			store.CreateJob(job)

			Expect(store.UpdateJobStatus(job.ID, "running", "", "")).To(Succeed())

			updated, _ := store.GetJob(job.ID)
			Expect(updated.Status).To(Equal("running"))
			Expect(updated.StartedAt).ToNot(BeNil())

			Expect(store.UpdateJobStatus(job.ID, "completed", "result text", "")).To(Succeed())

			completed, _ := store.GetJob(job.ID)
			Expect(completed.Status).To(Equal("completed"))
			Expect(completed.Result).To(Equal("result text"))
			Expect(completed.CompletedAt).ToNot(BeNil())
		})

		It("should list jobs with filters", func() {
			task := &jobs.TaskRecord{UserID: "u1", Name: "t1", Model: "m1", Prompt: "p1"}
			store.CreateTask(task)

			store.CreateJob(&jobs.JobRecord{TaskID: task.ID, UserID: "u1", Status: "completed", TriggeredBy: "manual"})
			store.CreateJob(&jobs.JobRecord{TaskID: task.ID, UserID: "u1", Status: "failed", TriggeredBy: "cron"})
			store.CreateJob(&jobs.JobRecord{TaskID: task.ID, UserID: "u2", Status: "pending", TriggeredBy: "api"})

			u1Jobs, _ := store.ListJobs("u1", "", "", 0)
			Expect(u1Jobs).To(HaveLen(2))

			failedJobs, _ := store.ListJobs("", "", "failed", 0)
			Expect(failedJobs).To(HaveLen(1))

			limitedJobs, _ := store.ListJobs("", "", "", 2)
			Expect(limitedJobs).To(HaveLen(2))
		})

		It("should cleanup old jobs", func() {
			task := &jobs.TaskRecord{UserID: "u1", Name: "t1", Model: "m1", Prompt: "p1"}
			store.CreateTask(task)

			// Create an old job
			job := &jobs.JobRecord{TaskID: task.ID, UserID: "u1", Status: "completed", TriggeredBy: "manual"}
			store.CreateJob(job)
			db.Model(&jobs.JobRecord{}).Where("id = ?", job.ID).
				Update("created_at", time.Now().Add(-60*24*time.Hour))

			// Create a recent job
			recentJob := &jobs.JobRecord{TaskID: task.ID, UserID: "u1", Status: "completed", TriggeredBy: "manual"}
			store.CreateJob(recentJob)

			deleted, err := store.CleanupOldJobs(30 * 24 * time.Hour)
			Expect(err).ToNot(HaveOccurred())
			Expect(deleted).To(Equal(int64(1)))

			remaining, _ := store.ListJobs("", "", "", 0)
			Expect(remaining).To(HaveLen(1))
		})

		It("should list cron tasks", func() {
			store.CreateTask(&jobs.TaskRecord{UserID: "u1", Name: "cron-task", Model: "m1", Prompt: "p1", Enabled: true, Cron: "*/5 * * * *"})

			// Create disabled task and explicitly set enabled=false after creation
			disabledTask := &jobs.TaskRecord{UserID: "u1", Name: "disabled-cron", Model: "m1", Prompt: "p1", Enabled: true, Cron: "*/5 * * * *"}
			store.CreateTask(disabledTask)
			db.Model(&jobs.TaskRecord{}).Where("id = ?", disabledTask.ID).Update("enabled", false)

			store.CreateTask(&jobs.TaskRecord{UserID: "u1", Name: "no-cron", Model: "m1", Prompt: "p1", Enabled: true})

			cronTasks, err := store.ListCronTasks()
			Expect(err).ToNot(HaveOccurred())
			Expect(cronTasks).To(HaveLen(1))
			Expect(cronTasks[0].Name).To(Equal("cron-task"))
		})
	})

	Context("Job Distribution via NATS", func() {
		It("should enqueue job via NATS and worker picks it up", func() {
			dispatcher := jobs.NewDispatcher(store, nc, db, "test-instance")
			var processed atomic.Int32
			dispatcher.SetWorkerFunc(func(ctx context.Context, job *jobs.JobRecord, task *jobs.TaskRecord) error {
				processed.Add(1)
				store.UpdateJobStatus(job.ID, "completed", "done", "")
				return nil
			})

			dCtx, dCancel := context.WithCancel(ctx)
			defer dCancel()
			Expect(dispatcher.Start(dCtx)).To(Succeed())
			defer dispatcher.Stop()

			// Create task and job
			task := &jobs.TaskRecord{UserID: "u1", Name: "dispatch-test", Model: "m1", Prompt: "p1"}
			store.CreateTask(task)
			job := &jobs.JobRecord{TaskID: task.ID, UserID: "u1", Status: "pending", TriggeredBy: "api"}
			store.CreateJob(job)

			// Enqueue
			Expect(dispatcher.Enqueue(job.ID, task.ID, "u1")).To(Succeed())

			// Wait for processing
			Eventually(func() int32 { return processed.Load() }, "10s").Should(Equal(int32(1)))

			// Verify status updated
			updated, _ := store.GetJob(job.ID)
			Expect(updated.Status).To(Equal("completed"))
		})

		It("should cancel running job via NATS", func() {
			dispatcher := jobs.NewDispatcher(store, nc, db, "test-instance")
			jobStarted := make(chan struct{})
			dispatcher.SetWorkerFunc(func(ctx context.Context, job *jobs.JobRecord, task *jobs.TaskRecord) error {
				close(jobStarted)
				// Simulate long work — wait for cancellation
				<-ctx.Done()
				return ctx.Err()
			})

			dCtx, dCancel := context.WithCancel(ctx)
			defer dCancel()
			Expect(dispatcher.Start(dCtx)).To(Succeed())
			defer dispatcher.Stop()

			task := &jobs.TaskRecord{UserID: "u1", Name: "cancel-test", Model: "m1", Prompt: "p1"}
			store.CreateTask(task)
			job := &jobs.JobRecord{TaskID: task.ID, UserID: "u1", Status: "pending", TriggeredBy: "api"}
			store.CreateJob(job)

			dispatcher.Enqueue(job.ID, task.ID, "u1")

			// Wait for job to start
			Eventually(jobStarted, "10s").Should(BeClosed())

			// Cancel via NATS
			Expect(dispatcher.Cancel(job.ID)).To(Succeed())

			// Wait for cancellation
			Eventually(func() string {
				j, _ := store.GetJob(job.ID)
				if j == nil {
					return ""
				}
				return j.Status
			}, "10s").Should(Equal("cancelled"))
		})

		It("should report job progress via NATS", func() {
			dispatcher := jobs.NewDispatcher(store, nc, db, "test-instance")
			dispatcher.SetWorkerFunc(func(ctx context.Context, job *jobs.JobRecord, task *jobs.TaskRecord) error {
				dispatcher.PublishProgress(job.ID, "running", "step 1")
				time.Sleep(50 * time.Millisecond)
				dispatcher.PublishProgress(job.ID, "running", "step 2")
				store.UpdateJobStatus(job.ID, "completed", "done", "")
				return nil
			})

			dCtx, dCancel := context.WithCancel(ctx)
			defer dCancel()
			Expect(dispatcher.Start(dCtx)).To(Succeed())
			defer dispatcher.Stop()

			task := &jobs.TaskRecord{UserID: "u1", Name: "progress-test", Model: "m1", Prompt: "p1"}
			store.CreateTask(task)
			job := &jobs.JobRecord{TaskID: task.ID, UserID: "u1", Status: "pending", TriggeredBy: "api"}
			store.CreateJob(job)

			// Subscribe to progress before enqueuing
			var progressEvents []jobs.ProgressEvent
			sub, err := dispatcher.SubscribeProgress(job.ID, func(evt jobs.ProgressEvent) {
				progressEvents = append(progressEvents, evt)
			})
			Expect(err).ToNot(HaveOccurred())
			defer sub.Unsubscribe()

			dispatcher.Enqueue(job.ID, task.ID, "u1")

			// Wait for completion
			Eventually(func() int { return len(progressEvents) }, "10s").Should(BeNumerically(">=", 3))

			// Should have received progress events
			Expect(progressEvents[0].Status).To(Equal("running"))
		})
	})

	Context("Cron Coordination", func() {
		It("should elect one cron leader via advisory lock", func() {
			// Use two dedicated connections to simulate two instances
			sqlDB, err := db.DB()
			Expect(err).ToNot(HaveOccurred())

			conn1, err := sqlDB.Conn(context.Background())
			Expect(err).ToNot(HaveOccurred())
			defer conn1.Close()

			conn2, err := sqlDB.Conn(context.Background())
			Expect(err).ToNot(HaveOccurred())
			defer conn2.Close()

			// Instance 1 acquires the cron leader lock
			var acquired1 bool
			conn1.QueryRowContext(context.Background(),
				"SELECT pg_try_advisory_lock($1)", messaging.AdvisoryLockCronScheduler).Scan(&acquired1)
			Expect(acquired1).To(BeTrue())

			// Instance 2 cannot acquire
			var acquired2 bool
			conn2.QueryRowContext(context.Background(),
				"SELECT pg_try_advisory_lock($1)", messaging.AdvisoryLockCronScheduler).Scan(&acquired2)
			Expect(acquired2).To(BeFalse())

			// Instance 1 releases
			conn1.ExecContext(context.Background(),
				"SELECT pg_advisory_unlock($1)", messaging.AdvisoryLockCronScheduler)

			// Now instance 2 can acquire
			conn2.QueryRowContext(context.Background(),
				"SELECT pg_try_advisory_lock($1)", messaging.AdvisoryLockCronScheduler).Scan(&acquired2)
			Expect(acquired2).To(BeTrue())
			conn2.ExecContext(context.Background(),
				"SELECT pg_advisory_unlock($1)", messaging.AdvisoryLockCronScheduler)
		})
	})

	Context("Progress Streaming (NATS → SSE bridge)", func() {
		It("should bridge NATS progress events", func() {
			dispatcher := jobs.NewDispatcher(store, nc, db, "test-instance")

			dCtx, dCancel := context.WithCancel(ctx)
			defer dCancel()
			Expect(dispatcher.Start(dCtx)).To(Succeed())
			defer dispatcher.Stop()

			// Subscribe to a job's progress
			var events []jobs.ProgressEvent
			sub, err := dispatcher.SubscribeProgress("job-123", func(evt jobs.ProgressEvent) {
				events = append(events, evt)
			})
			Expect(err).ToNot(HaveOccurred())
			defer sub.Unsubscribe()

			time.Sleep(100 * time.Millisecond)

			// Publish progress events
			dispatcher.PublishProgress("job-123", "running", "processing")
			dispatcher.PublishProgress("job-123", "running", "almost done")
			dispatcher.PublishProgress("job-123", "completed", "finished")

			Eventually(func() int { return len(events) }, "5s").Should(Equal(3))
			Expect(events[0].Status).To(Equal("running"))
			Expect(events[2].Status).To(Equal("completed"))
		})

		It("should filter SSE events by job ID", func() {
			dispatcher := jobs.NewDispatcher(store, nc, db, "test-instance")

			dCtx, dCancel := context.WithCancel(ctx)
			defer dCancel()
			Expect(dispatcher.Start(dCtx)).To(Succeed())
			defer dispatcher.Stop()

			// Subscribe to job-A only
			var eventsA []jobs.ProgressEvent
			subA, _ := dispatcher.SubscribeProgress("job-A", func(evt jobs.ProgressEvent) {
				eventsA = append(eventsA, evt)
			})
			defer subA.Unsubscribe()

			time.Sleep(100 * time.Millisecond)

			// Publish to both job-A and job-B
			dispatcher.PublishProgress("job-A", "running", "A progress")
			dispatcher.PublishProgress("job-B", "running", "B progress")
			dispatcher.PublishProgress("job-A", "completed", "A done")

			Eventually(func() int { return len(eventsA) }, "5s").Should(Equal(2))
			// Should only have job-A events
			for _, evt := range eventsA {
				Expect(evt.JobID).To(Equal("job-A"))
			}
		})
	})

	Context("Enriched Job Payload (DB-free worker)", func() {
		It("should enrich JobEvent with full Job and Task data", func() {
			dispatcher := jobs.NewDispatcher(store, nc, db, "enrichment-test")

			dCtx, dCancel := context.WithCancel(ctx)
			defer dCancel()
			Expect(dispatcher.Start(dCtx)).To(Succeed())
			defer dispatcher.Stop()

			// Create task and job
			task := &jobs.TaskRecord{UserID: "u1", Name: "enrich-task", Model: "m1", Prompt: "hello {{.name}}"}
			store.CreateTask(task)
			job := &jobs.JobRecord{TaskID: task.ID, UserID: "u1", Status: "pending", TriggeredBy: "api"}
			store.CreateJob(job)

			// Capture the raw NATS event
			var capturedEvt jobs.JobEvent
			var captured atomic.Int32
			sub, err := nc.Subscribe(messaging.SubjectJobsNew, func(data []byte) {
				var evt jobs.JobEvent
				if json.Unmarshal(data, &evt) == nil {
					capturedEvt = evt
					captured.Add(1)
				}
			})
			Expect(err).ToNot(HaveOccurred())
			defer sub.Unsubscribe()

			time.Sleep(100 * time.Millisecond)

			// Enqueue — this should embed Job+Task in the event
			Expect(dispatcher.Enqueue(job.ID, task.ID, "u1")).To(Succeed())

			Eventually(func() int32 { return captured.Load() }, "5s").Should(BeNumerically(">=", 1))

			// Verify enriched payload
			Expect(capturedEvt.Job).ToNot(BeNil(), "JobEvent should contain embedded Job")
			Expect(capturedEvt.Task).ToNot(BeNil(), "JobEvent should contain embedded Task")
			Expect(capturedEvt.Job.ID).To(Equal(job.ID))
			Expect(capturedEvt.Task.Name).To(Equal("enrich-task"))
			Expect(capturedEvt.Task.Prompt).To(Equal("hello {{.name}}"))
		})

		It("should process job from enriched payload without DB access", func() {
			// Create a worker-side dispatcher with NO store (simulating DB-free worker)
			workerDispatcher := jobs.NewDispatcher(nil, nc, nil, "worker-no-db")

			var receivedJob *jobs.JobRecord
			var receivedTask *jobs.TaskRecord
			processed := make(chan struct{})

			workerDispatcher.SetWorkerFunc(func(ctx context.Context, job *jobs.JobRecord, task *jobs.TaskRecord) error {
				receivedJob = job
				receivedTask = task
				job.Result = "processed without DB"
				close(processed)
				return nil
			})

			dCtx, dCancel := context.WithCancel(ctx)
			defer dCancel()
			Expect(workerDispatcher.Start(dCtx)).To(Succeed())
			defer workerDispatcher.Stop()

			time.Sleep(100 * time.Millisecond)

			// Publish an enriched event directly (simulating what the frontend does)
			evt := jobs.JobEvent{
				JobID:  "test-job-123",
				TaskID: "test-task-456",
				UserID: "u1",
				Job: &jobs.JobRecord{
					ID:          "test-job-123",
					TaskID:      "test-task-456",
					UserID:      "u1",
					Status:      "pending",
					TriggeredBy: "api",
				},
				Task: &jobs.TaskRecord{
					ID:     "test-task-456",
					Name:   "embedded-task",
					Model:  "test-model",
					Prompt: "do something",
				},
			}
			Expect(nc.Publish(messaging.SubjectJobsNew, evt)).To(Succeed())

			Eventually(processed, "10s").Should(BeClosed())

			// Verify the worker received data from the payload, not from DB
			Expect(receivedJob).ToNot(BeNil())
			Expect(receivedJob.ID).To(Equal("test-job-123"))
			Expect(receivedTask).ToNot(BeNil())
			Expect(receivedTask.Name).To(Equal("embedded-task"))
			Expect(receivedTask.Model).To(Equal("test-model"))
		})

		It("should publish job result via NATS on completion", func() {
			dispatcher := jobs.NewDispatcher(store, nc, db, "result-test")
			dispatcher.SetWorkerFunc(func(ctx context.Context, job *jobs.JobRecord, task *jobs.TaskRecord) error {
				job.Result = "job finished successfully"
				return nil
			})

			dCtx, dCancel := context.WithCancel(ctx)
			defer dCancel()
			Expect(dispatcher.Start(dCtx)).To(Succeed())
			defer dispatcher.Stop()

			task := &jobs.TaskRecord{UserID: "u1", Name: "result-task", Model: "m1", Prompt: "p1"}
			store.CreateTask(task)
			job := &jobs.JobRecord{TaskID: task.ID, UserID: "u1", Status: "pending", TriggeredBy: "api"}
			store.CreateJob(job)

			// Subscribe to result events
			var resultEvt jobs.JobResultEvent
			var received atomic.Int32
			sub, err := nc.Subscribe(messaging.SubjectJobResult(job.ID), func(data []byte) {
				json.Unmarshal(data, &resultEvt)
				received.Add(1)
			})
			Expect(err).ToNot(HaveOccurred())
			defer sub.Unsubscribe()

			time.Sleep(100 * time.Millisecond)
			dispatcher.Enqueue(job.ID, task.ID, "u1")

			Eventually(func() int32 { return received.Load() }, "10s").Should(BeNumerically(">=", 1))
			Expect(resultEvt.JobID).To(Equal(job.ID))
			Expect(resultEvt.Status).To(Equal("completed"))
		})

		It("should stream traces via NATS progress events", func() {
			dispatcher := jobs.NewDispatcher(store, nc, db, "trace-test")
			dispatcher.SetWorkerFunc(func(ctx context.Context, job *jobs.JobRecord, task *jobs.TaskRecord) error {
				dispatcher.PublishTrace(job.ID, "reasoning", "thinking about the problem")
				dispatcher.PublishTrace(job.ID, "tool_call", "calling search tool")
				return nil
			})

			dCtx, dCancel := context.WithCancel(ctx)
			defer dCancel()
			Expect(dispatcher.Start(dCtx)).To(Succeed())
			defer dispatcher.Stop()

			task := &jobs.TaskRecord{UserID: "u1", Name: "trace-task", Model: "m1", Prompt: "p1"}
			store.CreateTask(task)
			job := &jobs.JobRecord{TaskID: task.ID, UserID: "u1", Status: "pending", TriggeredBy: "api"}
			store.CreateJob(job)

			// Subscribe to progress events
			var traceEvents []jobs.ProgressEvent
			sub, err := dispatcher.SubscribeProgress(job.ID, func(evt jobs.ProgressEvent) {
				if evt.TraceType != "" {
					traceEvents = append(traceEvents, evt)
				}
			})
			Expect(err).ToNot(HaveOccurred())
			defer sub.Unsubscribe()

			dispatcher.Enqueue(job.ID, task.ID, "u1")

			Eventually(func() int { return len(traceEvents) }, "10s").Should(BeNumerically(">=", 2))
			Expect(traceEvents[0].TraceType).To(Equal("reasoning"))
			Expect(traceEvents[0].TraceContent).To(Equal("thinking about the problem"))
			Expect(traceEvents[1].TraceType).To(Equal("tool_call"))
		})

		It("should append traces incrementally to job record", func() {
			task := &jobs.TaskRecord{UserID: "u1", Name: "trace-store-task", Model: "m1", Prompt: "p1"}
			store.CreateTask(task)
			job := &jobs.JobRecord{TaskID: task.ID, UserID: "u1", Status: "running", TriggeredBy: "api"}
			store.CreateJob(job)

			Expect(store.AppendJobTrace(job.ID, "reasoning", "step 1")).To(Succeed())
			Expect(store.AppendJobTrace(job.ID, "tool_call", "step 2")).To(Succeed())
			Expect(store.AppendJobTrace(job.ID, "tool_result", "step 3")).To(Succeed())

			updated, err := store.GetJob(job.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(updated.TracesJSON).ToNot(BeEmpty())

			var traces []map[string]string
			Expect(json.Unmarshal([]byte(updated.TracesJSON), &traces)).To(Succeed())
			Expect(traces).To(HaveLen(3))
			Expect(traces[0]["type"]).To(Equal("reasoning"))
			Expect(traces[0]["content"]).To(Equal("step 1"))
			Expect(traces[1]["type"]).To(Equal("tool_call"))
			Expect(traces[2]["type"]).To(Equal("tool_result"))
		})
	})

	Context("JSON helpers", func() {
		It("should marshal and unmarshal JSON fields", func() {
			params := map[string]string{"key": "value", "foo": "bar"}
			encoded := jobs.MarshalJSON(params)
			Expect(encoded).ToNot(BeEmpty())

			var decoded map[string]string
			Expect(jobs.UnmarshalJSON(encoded, &decoded)).To(Succeed())
			Expect(decoded).To(HaveKeyWithValue("key", "value"))
			Expect(decoded).To(HaveKeyWithValue("foo", "bar"))
		})

		It("should handle empty/nil JSON gracefully", func() {
			Expect(jobs.MarshalJSON(nil)).To(BeEmpty())
			Expect(jobs.MarshalJSON([]string{})).To(BeEmpty())

			var result map[string]string
			Expect(jobs.UnmarshalJSON("", &result)).To(Succeed())
			Expect(result).To(BeNil())
		})
	})
})

// suppress unused import
var _ = json.Marshal
