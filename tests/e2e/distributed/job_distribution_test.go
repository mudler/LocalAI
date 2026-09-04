package distributed_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/advisorylock"
	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/dbutil"
	"github.com/mudler/LocalAI/core/services/jobs"
	"github.com/mudler/LocalAI/core/services/messaging"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	pgdriver "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var _ = Describe("Phase 2: Jobs & Tasks", Label("Distributed"), func() {
	var (
		infra *TestInfra
		db    *gorm.DB
		store *jobs.JobStore
	)

	BeforeEach(func() {
		infra = SetupInfra("localai_jobs_test")

		var err error
		db, err = gorm.Open(pgdriver.Open(infra.PGURL), &gorm.Config{
			Logger: logger.Default.LogMode(logger.Silent),
		})
		Expect(err).ToNot(HaveOccurred())

		store, err = jobs.NewJobStore(db)
		Expect(err).ToNot(HaveOccurred())
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

	Context("Job distribution through the claim queue", func() {
		It("enqueues a claim and drives it on a worker, persisting what the worker answered", func() {
			Expect(cluster.Migrate(infra.Ctx, db)).To(Succeed())
			const owner = "test-instance"
			Expect(cluster.NewRegistry(db).Register(infra.Ctx, owner, "127.0.0.1:8090", "v1")).To(Succeed())

			dispatcher := jobs.NewDispatcher(store, infra.Bus(), db, owner)

			task := &jobs.TaskRecord{UserID: "u1", Name: "dispatch-test", Model: "m1", Prompt: "p1"}
			store.CreateTask(task)
			job := &jobs.JobRecord{TaskID: task.ID, UserID: "u1", Status: "pending", TriggeredBy: "api"}
			store.CreateJob(job)

			Expect(dispatcher.Enqueue(job.ID, task.ID, "u1")).To(Succeed())
			Expect(db.Model(&jobs.WorkClaim{}).Where("kind = ?", string(jobs.ClaimKindTask)).
				Update("kind", string(jobs.ClaimKindMCPCI)).Error).To(Succeed())

			worker := &scriptedWorker{reply: jobs.ClaimReply{JobID: job.ID, Status: "completed", Result: "done"}}
			loop, err := jobs.NewDispatchLoop(jobs.DispatchConfig{
				DB: db, Owner: owner, Selector: fixedAgent{}, Control: worker,
				Store: store, Liveness: time.Minute,
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(loop.DispatchOnce(infra.Ctx)).To(Succeed())

			updated, _ := store.GetJob(job.ID)
			Expect(updated.Status).To(Equal("completed"))
		})

		It("returns a claim to the pool when the dispatch obtained no answer, rather than losing the work", func() {
			Expect(cluster.Migrate(infra.Ctx, db)).To(Succeed())
			const owner = "lossy-instance"
			Expect(cluster.NewRegistry(db).Register(infra.Ctx, owner, "127.0.0.1:8091", "v1")).To(Succeed())

			dispatcher := jobs.NewDispatcher(store, infra.Bus(), db, owner)
			task := &jobs.TaskRecord{UserID: "u1", Name: "lossy-test", Model: "m1", Prompt: "p1"}
			store.CreateTask(task)
			job := &jobs.JobRecord{TaskID: task.ID, UserID: "u1", Status: "pending", TriggeredBy: "api"}
			store.CreateJob(job)
			Expect(dispatcher.Enqueue(job.ID, task.ID, "u1")).To(Succeed())
			Expect(db.Model(&jobs.WorkClaim{}).Where("kind = ?", string(jobs.ClaimKindTask)).
				Update("kind", string(jobs.ClaimKindMCPCI)).Error).To(Succeed())

			worker := &scriptedWorker{err: errors.New("the tunnel died mid-verb")}
			loop, err := jobs.NewDispatchLoop(jobs.DispatchConfig{
				DB: db, Owner: owner, Selector: fixedAgent{}, Control: worker,
				Store: store, Liveness: time.Minute,
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(loop.DispatchOnce(infra.Ctx)).To(HaveOccurred())

			var rows []jobs.WorkClaim
			Expect(db.Find(&rows).Error).To(Succeed())
			Expect(rows).To(HaveLen(1), "a transport failure must not complete or discard the work")
			Expect(rows[0].ClaimedAt).To(BeNil())
			Expect(rows[0].Attempts).To(Equal(1))
			updated, _ := store.GetJob(job.ID)
			Expect(updated.Status).ToNot(Equal("completed"), "nothing was learned, so nothing may be written about the job")
		})

		It("broadcasts a cancel on the job's own cancel subject", func() {
			// Two carriers, because the replica that holds the execution is
			// never the one an API cancel lands on. A cancel that does not
			// arrive is not a cancel that was refused, so this pins ARRIVAL.
			publisher, listener := infra.Bus(), infra.Bus()
			dispatcher := jobs.NewDispatcher(store, publisher, db, "test-instance")
			seen := make(chan string, 1)
			sub, err := listener.Subscribe(messaging.SubjectJobCancelWildcard, func(data []byte) {
				var evt jobs.CancelEvent
				if json.Unmarshal(data, &evt) == nil {
					select {
					case seen <- evt.JobID:
					default:
					}
				}
			})
			Expect(err).ToNot(HaveOccurred())
			defer func() { _ = sub.Unsubscribe() }()

			Expect(dispatcher.Cancel("job-to-cancel")).To(Succeed())
			Eventually(seen, "10s").Should(Receive(Equal("job-to-cancel")))
		})

		It("reports job progress on the job's own progress subject", func() {
			dispatcher := jobs.NewDispatcher(store, infra.Bus(), db, "test-instance")

			var progressEvents []jobs.ProgressEvent
			var mu sync.Mutex
			sub, err := dispatcher.SubscribeProgress("progress-job", func(evt jobs.ProgressEvent) {
				mu.Lock()
				progressEvents = append(progressEvents, evt)
				mu.Unlock()
			})
			Expect(err).ToNot(HaveOccurred())
			defer func() { _ = sub.Unsubscribe() }()

			Expect(dispatcher.PublishProgress("progress-job", "running", "step 1")).To(Succeed())
			Expect(dispatcher.PublishProgress("progress-job", "running", "step 2")).To(Succeed())
			Expect(dispatcher.PublishProgress("progress-job", "completed", "done")).To(Succeed())

			Eventually(func() int {
				mu.Lock()
				defer mu.Unlock()
				return len(progressEvents)
			}, "10s").Should(BeNumerically(">=", 3))
			mu.Lock()
			defer mu.Unlock()
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
				"SELECT pg_try_advisory_lock($1)", advisorylock.KeyCronScheduler).Scan(&acquired1)
			Expect(acquired1).To(BeTrue())

			// Instance 2 cannot acquire
			var acquired2 bool
			conn2.QueryRowContext(context.Background(),
				"SELECT pg_try_advisory_lock($1)", advisorylock.KeyCronScheduler).Scan(&acquired2)
			Expect(acquired2).To(BeFalse())

			// Instance 1 releases
			conn1.ExecContext(context.Background(),
				"SELECT pg_advisory_unlock($1)", advisorylock.KeyCronScheduler)

			// Now instance 2 can acquire
			conn2.QueryRowContext(context.Background(),
				"SELECT pg_try_advisory_lock($1)", advisorylock.KeyCronScheduler).Scan(&acquired2)
			Expect(acquired2).To(BeTrue())
			conn2.ExecContext(context.Background(),
				"SELECT pg_advisory_unlock($1)", advisorylock.KeyCronScheduler)
		})
	})

	Context("Progress streaming to the SSE bridge", func() {
		It("bridges progress events to a per-job subscription", func() {
			dispatcher := jobs.NewDispatcher(store, infra.Bus(), db, "test-instance")

			dCtx, dCancel := context.WithCancel(infra.Ctx)
			defer dCancel()
			Expect(dispatcher.Start(dCtx)).To(Succeed())
			defer dispatcher.Stop()

			// Subscribe to a job's progress
			var events []jobs.ProgressEvent
			sub, err := dispatcher.SubscribeProgress("job-123", func(evt jobs.ProgressEvent) {
				events = append(events, evt)
			})
			Expect(err).ToNot(HaveOccurred())
			defer func() { _ = sub.Unsubscribe() }()

			// Publish progress events
			dispatcher.PublishProgress("job-123", "running", "processing")
			dispatcher.PublishProgress("job-123", "running", "almost done")
			dispatcher.PublishProgress("job-123", "completed", "finished")

			Eventually(func() int { return len(events) }, "5s").Should(Equal(3))
			Expect(events[0].Status).To(Equal("running"))
			Expect(events[2].Status).To(Equal("completed"))
		})

		It("should filter SSE events by job ID", func() {
			dispatcher := jobs.NewDispatcher(store, infra.Bus(), db, "test-instance")

			dCtx, dCancel := context.WithCancel(infra.Ctx)
			defer dCancel()
			Expect(dispatcher.Start(dCtx)).To(Succeed())
			defer dispatcher.Stop()

			// Subscribe to job-A only
			var eventsA []jobs.ProgressEvent
			subA, _ := dispatcher.SubscribeProgress("job-A", func(evt jobs.ProgressEvent) {
				eventsA = append(eventsA, evt)
			})
			defer subA.Unsubscribe()

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

	Context("Enriched claim payload (DB-free worker)", func() {
		It("stores the full Job and Task on the claim row, so the worker needs no database", func() {
			dispatcher := jobs.NewDispatcher(store, infra.Bus(), db, "enrichment-test")

			task := &jobs.TaskRecord{UserID: "u1", Name: "enrich-task", Model: "m1", Prompt: "hello {{.name}}"}
			store.CreateTask(task)
			job := &jobs.JobRecord{TaskID: task.ID, UserID: "u1", Status: "pending", TriggeredBy: "api"}
			store.CreateJob(job)

			Expect(dispatcher.Enqueue(job.ID, task.ID, "u1")).To(Succeed())

			var rows []jobs.WorkClaim
			Expect(db.Find(&rows).Error).To(Succeed())
			Expect(rows).To(HaveLen(1))

			var evt jobs.JobEvent
			Expect(json.Unmarshal(rows[0].Payload, &evt)).To(Succeed())
			Expect(evt.Job).ToNot(BeNil(), "the claim payload should contain the embedded Job")
			Expect(evt.Task).ToNot(BeNil(), "the claim payload should contain the embedded Task")
			Expect(evt.Job.ID).To(Equal(job.ID))
			Expect(evt.Task.Name).To(Equal("enrich-task"))
			Expect(evt.Task.Prompt).To(Equal("hello {{.name}}"))
		})

		// The two subscriptions the dispatcher KEEPS. A worker's result and
		// trace lines are re-broadcast by the replica that claimed the work, and
		// every replica persists them, because the SSE stream a user is watching
		// may be open on a replica that claimed nothing.
		It("persists a result a worker's re-broadcast carried, whichever replica reads it", func() {
			dispatcher := jobs.NewDispatcher(store, infra.Bus(), db, "result-test")
			peer := infra.Bus()
			dCtx, dCancel := context.WithCancel(infra.Ctx)
			defer dCancel()
			Expect(dispatcher.Start(dCtx)).To(Succeed())
			defer dispatcher.Stop()

			task := &jobs.TaskRecord{UserID: "u1", Name: "result-task", Model: "m1", Prompt: "p1"}
			store.CreateTask(task)
			job := &jobs.JobRecord{TaskID: task.ID, UserID: "u1", Status: "running", TriggeredBy: "api"}
			store.CreateJob(job)

			// Published by a PEER replica's carrier: this is the fan-out copy of
			// a terminal line the claiming replica already persisted, and the
			// replica asserted on here claimed nothing.
			jobs.PublishJobResult(peer, job.ID, "completed", "job finished successfully", "")

			Eventually(func() string {
				j, _ := store.GetJob(job.ID)
				if j == nil {
					return ""
				}
				return j.Status
			}, "10s").Should(Equal("completed"))
		})

		It("appends a trace a worker's re-broadcast carried", func() {
			dispatcher := jobs.NewDispatcher(store, infra.Bus(), db, "trace-test")
			dCtx, dCancel := context.WithCancel(infra.Ctx)
			defer dCancel()
			Expect(dispatcher.Start(dCtx)).To(Succeed())
			defer dispatcher.Stop()

			task := &jobs.TaskRecord{UserID: "u1", Name: "trace-task", Model: "m1", Prompt: "p1"}
			store.CreateTask(task)
			job := &jobs.JobRecord{TaskID: task.ID, UserID: "u1", Status: "running", TriggeredBy: "api"}
			store.CreateJob(job)

			Expect(dispatcher.PublishTrace(job.ID, "reasoning", "thinking about the problem")).To(Succeed())
			Expect(dispatcher.PublishTrace(job.ID, "tool_call", "calling search tool")).To(Succeed())

			Eventually(func() int {
				j, _ := store.GetJob(job.ID)
				if j == nil || j.TracesJSON == "" {
					return 0
				}
				var traces []map[string]string
				if json.Unmarshal([]byte(j.TracesJSON), &traces) != nil {
					return 0
				}
				return len(traces)
			}, "10s").Should(BeNumerically(">=", 2))
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
			encoded := dbutil.MarshalJSON(params)
			Expect(encoded).ToNot(BeEmpty())

			var decoded map[string]string
			Expect(dbutil.UnmarshalJSON(encoded, &decoded)).To(Succeed())
			Expect(decoded).To(HaveKeyWithValue("key", "value"))
			Expect(decoded).To(HaveKeyWithValue("foo", "bar"))
		})

		It("should handle empty/nil JSON gracefully", func() {
			Expect(dbutil.MarshalJSON(nil)).To(BeEmpty())
			Expect(dbutil.MarshalJSON([]string{})).To(BeEmpty())

			var result map[string]string
			Expect(dbutil.UnmarshalJSON("", &result)).To(Succeed())
			Expect(result).To(BeNil())
		})
	})
})
