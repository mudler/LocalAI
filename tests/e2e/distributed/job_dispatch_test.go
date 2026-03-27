package distributed_test

import (
	"context"
	"sync/atomic"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/jobs"
	"github.com/mudler/LocalAI/core/services/messaging"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	pgdriver "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var _ = Describe("Job Dispatch", Label("Distributed"), func() {
	var (
		infra *TestInfra
		db    *gorm.DB
		store *jobs.JobStore
	)

	BeforeEach(func() {
		infra = SetupInfra("localai_dispatch_test")

		var err error
		db, err = gorm.Open(pgdriver.Open(infra.PGURL), &gorm.Config{
			Logger: logger.Default.LogMode(logger.Silent),
		})
		Expect(err).ToNot(HaveOccurred())

		store, err = jobs.NewJobStore(db)
		Expect(err).ToNot(HaveOccurred())
	})

	Context("NATS job dispatch", func() {
		It("should enqueue job via NATS when dispatcher is set", func() {
			dispatcher := jobs.NewDispatcher(store, infra.NC, db, "dispatch-instance")
			var processed atomic.Int32
			dispatcher.SetWorkerFunc(func(ctx context.Context, job *jobs.JobRecord, task *jobs.TaskRecord) error {
				processed.Add(1)
				store.UpdateJobStatus(job.ID, "completed", "done", "")
				return nil
			})

			dCtx, dCancel := context.WithCancel(infra.Ctx)
			defer dCancel()
			Expect(dispatcher.Start(dCtx)).To(Succeed())
			defer dispatcher.Stop()

			task := &jobs.TaskRecord{UserID: "u1", Name: "dispatch-task", Model: "m1", Prompt: "p1"}
			store.CreateTask(task)
			job := &jobs.JobRecord{TaskID: task.ID, UserID: "u1", Status: "pending", TriggeredBy: "api"}
			store.CreateJob(job)

			Expect(dispatcher.Enqueue(job.ID, task.ID, "u1")).To(Succeed())

			Eventually(func() int32 { return processed.Load() }, "10s").Should(Equal(int32(1)))

			updated, _ := store.GetJob(job.ID)
			Expect(updated.Status).To(Equal("completed"))
		})
	})

	Context("PostgreSQL job persistence", func() {
		It("should persist job state in PostgreSQL via JobStore", func() {
			task := &jobs.TaskRecord{UserID: "u1", Name: "persist-task", Model: "m1", Prompt: "run something"}
			Expect(store.CreateTask(task)).To(Succeed())
			Expect(task.ID).ToNot(BeEmpty())

			job := &jobs.JobRecord{
				TaskID:      task.ID,
				UserID:      "u1",
				Status:      "pending",
				TriggeredBy: "api",
			}
			Expect(store.CreateJob(job)).To(Succeed())
			Expect(job.ID).ToNot(BeEmpty())

			// Verify retrieval
			retrieved, err := store.GetJob(job.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(retrieved.TaskID).To(Equal(task.ID))
			Expect(retrieved.Status).To(Equal("pending"))

			// Update status
			Expect(store.UpdateJobStatus(job.ID, "running", "", "")).To(Succeed())
			running, _ := store.GetJob(job.ID)
			Expect(running.Status).To(Equal("running"))
			Expect(running.StartedAt).ToNot(BeNil())

			// Complete
			Expect(store.UpdateJobStatus(job.ID, "completed", "output data", "")).To(Succeed())
			completed, _ := store.GetJob(job.ID)
			Expect(completed.Status).To(Equal("completed"))
			Expect(completed.Result).To(Equal("output data"))
			Expect(completed.CompletedAt).ToNot(BeNil())
		})
	})

	Context("NATS job cancellation", func() {
		It("should cancel running job via NATS cancel subject", func() {
			dispatcher := jobs.NewDispatcher(store, infra.NC, db, "cancel-instance")
			jobStarted := make(chan struct{})
			dispatcher.SetWorkerFunc(func(ctx context.Context, job *jobs.JobRecord, task *jobs.TaskRecord) error {
				close(jobStarted)
				<-ctx.Done()
				return ctx.Err()
			})

			dCtx, dCancel := context.WithCancel(infra.Ctx)
			defer dCancel()
			Expect(dispatcher.Start(dCtx)).To(Succeed())
			defer dispatcher.Stop()

			task := &jobs.TaskRecord{UserID: "u1", Name: "cancel-task", Model: "m1", Prompt: "p1"}
			store.CreateTask(task)
			job := &jobs.JobRecord{TaskID: task.ID, UserID: "u1", Status: "pending", TriggeredBy: "api"}
			store.CreateJob(job)

			dispatcher.Enqueue(job.ID, task.ID, "u1")

			Eventually(jobStarted, "10s").Should(BeClosed())

			Expect(dispatcher.Cancel(job.ID)).To(Succeed())

			Eventually(func() string {
				j, _ := store.GetJob(job.ID)
				if j == nil {
					return ""
				}
				return j.Status
			}, "10s").Should(Equal("cancelled"))
		})
	})

	Context("Cron leader election", func() {
		It("should elect one cron leader via advisory lock", func() {
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

	Context("Without --distributed", func() {
		It("should use local channel without --distributed", func() {
			appCfg := config.NewApplicationConfig()
			Expect(appCfg.Distributed.Enabled).To(BeFalse())

			// Without distributed mode, jobs use local in-process dispatch.
			// The JobStore can still be used standalone with SQLite or in-memory.
			Expect(appCfg.Distributed.NatsURL).To(BeEmpty())
		})
	})
})
