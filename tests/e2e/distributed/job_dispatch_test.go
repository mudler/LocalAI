package distributed_test

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/advisorylock"
	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/jobs"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/nodes"

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

	Context("Claim-queue dispatch", func() {
		// The whole path, against a real PostgreSQL: Enqueue writes a claim
		// row, a frontend replica takes it with SELECT ... FOR UPDATE SKIP
		// LOCKED, drives it as a streaming control RPC, and persists the
		// worker's terminal line before it releases the claim.
		It("enqueues a claim, drives it on a worker, and persists what the worker answered", func() {
			Expect(cluster.Migrate(infra.Ctx, db)).To(Succeed())
			const owner = "dispatch-instance"
			// A replica that is not registered may not claim: its claims could
			// not be told from ones a dead replica left.
			Expect(cluster.NewRegistry(db).Register(infra.Ctx, owner, "127.0.0.1:8080", "v1")).To(Succeed())

			dispatcher := jobs.NewDispatcher(store, infra.Bus(), db, owner)

			task := &jobs.TaskRecord{UserID: "u1", Name: "dispatch-task", Model: "m1", Prompt: "p1"}
			store.CreateTask(task)
			job := &jobs.JobRecord{TaskID: task.ID, UserID: "u1", Status: "pending", TriggeredBy: "api"}
			store.CreateJob(job)

			Expect(dispatcher.Enqueue(job.ID, task.ID, "u1")).To(Succeed())

			var claimed int64
			Expect(db.Model(&jobs.WorkClaim{}).Count(&claimed).Error).To(Succeed())
			Expect(claimed).To(Equal(int64(1)), "the enqueue must leave a row, not a publish nobody may be listening for")

			worker := &scriptedWorker{reply: jobs.ClaimReply{JobID: job.ID, Status: "completed", Result: "done"}}
			loop, err := jobs.NewDispatchLoop(jobs.DispatchConfig{
				DB:       db,
				Owner:    owner,
				Selector: fixedAgent{},
				Control:  worker,
				Store:    store,
				Liveness: time.Minute,
			})
			Expect(err).ToNot(HaveOccurred())

			// The claim kind is "task" here (no MCP servers on the model), and
			// no worker serves it, so the loop closes the job out with a reason
			// rather than leaving it running for ever. Give the row a kind a
			// worker DOES serve, so this spec exercises the dispatch path.
			Expect(db.Model(&jobs.WorkClaim{}).Where("kind = ?", string(jobs.ClaimKindTask)).
				Update("kind", string(jobs.ClaimKindMCPCI)).Error).To(Succeed())

			Expect(loop.DispatchOnce(infra.Ctx)).To(Succeed())

			Expect(worker.calls.Load()).To(Equal(int32(1)))
			updated, _ := store.GetJob(job.ID)
			Expect(updated.Status).To(Equal("completed"))
			Expect(updated.Result).To(Equal("done"))

			Expect(db.Model(&jobs.WorkClaim{}).Count(&claimed).Error).To(Succeed())
			Expect(claimed).To(BeZero(), "an answered claim must not be able to run again")
		})

		It("leaves a plain task job failed with a reason, since no worker in this deployment serves that kind", func() {
			Expect(cluster.Migrate(infra.Ctx, db)).To(Succeed())
			const owner = "plain-instance"
			Expect(cluster.NewRegistry(db).Register(infra.Ctx, owner, "127.0.0.1:8081", "v1")).To(Succeed())

			dispatcher := jobs.NewDispatcher(store, infra.Bus(), db, owner)
			task := &jobs.TaskRecord{UserID: "u1", Name: "plain-task", Model: "m1", Prompt: "p1"}
			Expect(store.CreateTask(task)).To(Succeed())
			job := &jobs.JobRecord{TaskID: task.ID, UserID: "u1", Status: "pending", TriggeredBy: "api"}
			Expect(store.CreateJob(job)).To(Succeed())
			Expect(dispatcher.Enqueue(job.ID, task.ID, "u1")).To(Succeed())

			worker := &scriptedWorker{}
			loop, err := jobs.NewDispatchLoop(jobs.DispatchConfig{
				DB: db, Owner: owner, Selector: fixedAgent{}, Control: worker,
				Store: store, Liveness: time.Minute,
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(loop.DispatchOnce(infra.Ctx)).To(Succeed())

			Expect(worker.calls.Load()).To(BeZero(), "nothing serves this kind, so nothing may be asked of a worker")
			updated, _ := store.GetJob(job.ID)
			Expect(updated.Status).To(Equal("failed"))
			Expect(updated.Error).To(ContainSubstring("plain task jobs"))
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

	Context("job cancellation", func() {
		// Cancellation stays a BROADCAST and is not part of the claim queue: the
		// replica holding a run is not the one an API cancel lands on, so the
		// signal has to reach every replica and every worker.
		//
		// Asserted across TWO carriers, because one carrier hearing itself
		// proves nothing about the replica that actually holds the execution.
		// A cancel that does not arrive is not a cancel that was refused, so
		// what is pinned here is arrival and never the publisher's error.
		It("broadcasts a cancel for a job on the job's own cancel subject", func() {
			publisher, listener := infra.Bus(), infra.Bus()
			dispatcher := jobs.NewDispatcher(store, publisher, db, "cancel-instance")

			task := &jobs.TaskRecord{UserID: "u1", Name: "cancel-task", Model: "m1", Prompt: "p1"}
			store.CreateTask(task)
			job := &jobs.JobRecord{TaskID: task.ID, UserID: "u1", Status: "pending", TriggeredBy: "api"}
			store.CreateJob(job)

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

			Expect(dispatcher.Cancel(job.ID)).To(Succeed())
			Eventually(seen, "10s").Should(Receive(Equal(job.ID)))
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

	Context("Without --distributed", func() {
		It("should use local channel without --distributed", func() {
			appCfg := config.NewApplicationConfig()
			Expect(appCfg.Distributed.Enabled).To(BeFalse())

			// Without distributed mode, jobs use local in-process dispatch.
			// The JobStore can still be used standalone with SQLite or in-memory.
			//
			// The bus-URL half of this assertion went with the field it read;
			// core/config's "broker surface" spec pins its absence.
		})
	})
})

// fixedAgent is a selection that always names one connected agent worker. The
// selection itself is pinned against real connection rows in
// core/services/nodes; what this suite drives is the dispatch that follows it.
type fixedAgent struct{}

func (fixedAgent) PickConnected(context.Context) (string, string, error) {
	return "agent-node-1", nodes.NodeTypeAgent, nil
}

// scriptedWorker answers a streaming control RPC with a scripted reply, so this
// suite exercises the claim, the persist and the settle against a real database
// without needing a worker process.
type scriptedWorker struct {
	calls atomic.Int32
	reply jobs.ClaimReply
	err   error
}

func (w *scriptedWorker) CallStreaming(_ context.Context, _, _ string, _, reply any,
	_ func(string, json.RawMessage)) error {
	w.calls.Add(1)
	if w.err != nil {
		return w.err
	}
	if out, ok := reply.(*jobs.ClaimReply); ok {
		*out = w.reply
	}
	return nil
}
