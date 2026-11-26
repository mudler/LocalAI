package nodes

import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/advisorylock"
	"github.com/mudler/LocalAI/core/services/testutil"
	"gorm.io/gorm"
)

// advisorylockWith holds the advisory lock key while fn runs.
func advisorylockWith(ctx context.Context, db *gorm.DB, key int64, fn func()) error {
	return advisorylock.WithLockCtx(ctx, db, key, func() error {
		fn()
		return nil
	})
}

// ClaimLoadJob is ClaimLoadJobOn for the job of the empty node: the row an
// older binary wrote before it chose the node. Most job specs do not care
// about the node, and claim this one.
func (r *NodeRegistry) ClaimLoadJob(ctx context.Context, trackingKey, owner string) (*ModelLoadJob, bool, error) {
	return r.ClaimLoadJobOn(ctx, trackingKey, "", owner)
}

var _ = Describe("ModelLoadJob", func() {
	var (
		db       *gorm.DB
		registry *NodeRegistry
		ctx      context.Context
	)

	BeforeEach(func() {
		if runtime.GOOS == "darwin" {
			Skip("testcontainers requires Docker, not available on macOS CI")
		}
		db = testutil.SetupTestDB()
		var err error
		registry, err = NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())
		ctx = context.Background()
	})

	Describe("ClaimLoadJob", func() {
		It("claims a model that has no job yet", func() {
			job, claimed, err := registry.ClaimLoadJob(ctx, "qwen3", "replica-a")
			Expect(err).ToNot(HaveOccurred())
			Expect(claimed).To(BeTrue())
			Expect(job.State).To(Equal(LoadJobStatePending))
			Expect(job.OwnerReplica).To(Equal("replica-a"))
		})

		It("hands a second caller the live job instead of a second claim", func() {
			_, claimed, err := registry.ClaimLoadJob(ctx, "qwen3", "replica-a")
			Expect(err).ToNot(HaveOccurred())
			Expect(claimed).To(BeTrue())

			job, claimed, err := registry.ClaimLoadJob(ctx, "qwen3", "replica-b")
			Expect(err).ToNot(HaveOccurred())
			Expect(claimed).To(BeFalse(), "the second caller must attach as a waiter, not start a duplicate load")
			Expect(job.OwnerReplica).To(Equal("replica-a"))
		})

		It("gives exactly one of many concurrent claimers the job", func() {
			const claimers = 8
			var claims int32
			var wg sync.WaitGroup
			for range claimers {
				wg.Go(func() {
					defer GinkgoRecover()
					_, claimed, err := registry.ClaimLoadJob(ctx, "contended", "replica")
					Expect(err).ToNot(HaveOccurred())
					if claimed {
						atomic.AddInt32(&claims, 1)
					}
				})
			}
			wg.Wait()
			Expect(claims).To(Equal(int32(1)))
		})

		It("returns promptly while another replica's job is running", func() {
			// The whole point of the split: a claim is a decision that takes
			// milliseconds, so a concurrent request never waits behind the
			// minutes-long load the owner is running.
			slow, claimed, err := registry.ClaimLoadJob(ctx, "slow-model", "replica-a")
			Expect(err).ToNot(HaveOccurred())
			Expect(claimed).To(BeTrue())

			running := make(chan struct{})
			done := make(chan struct{})
			go func() {
				defer GinkgoRecover()
				close(running)
				// Stand in for a multi-GB staging run owned by replica-a.
				time.Sleep(2 * time.Second)
				Expect(registry.DeleteLoadJob(ctx, slow.Ref())).To(Succeed())
				close(done)
			}()
			<-running

			start := time.Now()
			_, claimed, err = registry.ClaimLoadJob(ctx, "slow-model", "replica-b")
			Expect(err).ToNot(HaveOccurred())
			Expect(claimed).To(BeFalse())
			Expect(time.Since(start)).To(BeNumerically("<", 100*time.Millisecond),
				"claiming must not block behind the running job")
			<-done
		})

		It("reclaims a job whose owner stopped renewing its lease", func() {
			_, claimed, err := registry.ClaimLoadJob(ctx, "orphan", "dead-replica")
			Expect(err).ToNot(HaveOccurred())
			Expect(claimed).To(BeTrue())

			// Expire the lease on the database clock, as a replica killed
			// mid-load would leave it.
			Expect(db.Exec("UPDATE model_load_jobs SET lease_until = now() - interval '1 second' WHERE tracking_key = 'orphan'").Error).To(Succeed())
			_, claimed, err = registry.ClaimLoadJob(ctx, "orphan", "live-replica")
			Expect(err).ToNot(HaveOccurred())
			Expect(claimed).To(BeFalse(), "the dead owner's remote work may still run, so the stop window applies")

			Expect(db.Exec("UPDATE model_load_jobs SET stop_deadline = now() - interval '1 second' WHERE tracking_key = 'orphan'").Error).To(Succeed())
			job, claimed, err := registry.ClaimLoadJob(ctx, "orphan", "live-replica")
			Expect(err).ToNot(HaveOccurred())
			Expect(claimed).To(BeTrue(), "a dead replica must not wedge the model permanently")
			Expect(job.OwnerReplica).To(Equal("live-replica"))
		})
	})

	Describe("jobs per node", func() {
		It("lets one model load on two nodes at once", func() {
			a, claimed, err := registry.ClaimLoadJobOn(ctx, "model-x", "node-a", "replica-a")
			Expect(err).ToNot(HaveOccurred())
			Expect(claimed).To(BeTrue())
			b, claimed, err := registry.ClaimLoadJobOn(ctx, "model-x", "node-b", "replica-b")
			Expect(err).ToNot(HaveOccurred())
			Expect(claimed).To(BeTrue(), "a load on node-a must not hold the model on node-b")
			Expect(b.Generation).ToNot(Equal(a.Generation))

			jobs, err := registry.ListLoadJobs(ctx, "model-x")
			Expect(err).ToNot(HaveOccurred())
			Expect(jobs).To(HaveLen(2))
			Expect([]string{jobs[0].NodeID, jobs[1].NodeID}).To(Equal([]string{"node-a", "node-b"}))
		})

		It("still gives one node one load", func() {
			_, claimed, err := registry.ClaimLoadJobOn(ctx, "model-x", "node-a", "replica-a")
			Expect(err).ToNot(HaveOccurred())
			Expect(claimed).To(BeTrue())
			job, claimed, err := registry.ClaimLoadJobOn(ctx, "model-x", "node-a", "replica-b")
			Expect(err).ToNot(HaveOccurred())
			Expect(claimed).To(BeFalse())
			Expect(job.OwnerReplica).To(Equal("replica-a"))
		})

		It("does not wait for the claim lock of another node", func() {
			// Hold the claim lock of node-a, as a slow claim would.
			held := make(chan struct{})
			release := make(chan struct{})
			go func() {
				defer GinkgoRecover()
				_ = advisorylockWith(ctx, db, loadJobLockKey("model-x", "node-a"), func() {
					close(held)
					<-release
				})
			}()
			<-held
			defer close(release)

			done := make(chan bool, 1)
			go func() {
				defer GinkgoRecover()
				_, claimed, err := registry.ClaimLoadJobOn(ctx, "model-x", "node-b", "replica-b")
				Expect(err).ToNot(HaveOccurred())
				done <- claimed
			}()
			Eventually(done, 5*time.Second).Should(Receive(BeTrue()))
		})

		It("reads one attempt by its ref, whatever node it is on", func() {
			_, _, err := registry.ClaimLoadJobOn(ctx, "model-x", "node-a", "replica-a")
			Expect(err).ToNot(HaveOccurred())
			b, _, err := registry.ClaimLoadJobOn(ctx, "model-x", "node-b", "replica-b")
			Expect(err).ToNot(HaveOccurred())

			got, err := registry.GetLoadJobByRef(ctx, b.Ref())
			Expect(err).ToNot(HaveOccurred())
			Expect(got.NodeID).To(Equal("node-b"))

			Expect(registry.DeleteLoadJob(ctx, b.Ref())).To(Succeed())
			got, err = registry.GetLoadJobByRef(ctx, b.Ref())
			Expect(err).ToNot(HaveOccurred())
			Expect(got).To(BeNil())
			left, err := registry.ListLoadJobs(ctx, "model-x")
			Expect(err).ToNot(HaveOccurred())
			Expect(left).To(HaveLen(1), "finishing one node's load leaves the other")
		})

		It("reports the load that is furthest along", func() {
			a, _, err := registry.ClaimLoadJobOn(ctx, "model-x", "node-a", "replica-a")
			Expect(err).ToNot(HaveOccurred())
			b, _, err := registry.ClaimLoadJobOn(ctx, "model-x", "node-b", "replica-b")
			Expect(err).ToNot(HaveOccurred())
			Expect(registry.UpdateLoadJob(ctx, a.Ref(), LoadJobUpdate{State: LoadJobStateStaging, BytesSent: 10, TotalBytes: 100})).To(Succeed())
			Expect(registry.UpdateLoadJob(ctx, b.Ref(), LoadJobUpdate{State: LoadJobStateStaging, BytesSent: 60, TotalBytes: 100})).To(Succeed())

			job, err := registry.GetLoadJob(ctx, "model-x")
			Expect(err).ToNot(HaveOccurred())
			Expect(job.NodeID).To(Equal("node-b"))

			Expect(registry.UpdateLoadJob(ctx, a.Ref(), LoadJobUpdate{State: LoadJobStateLoading})).To(Succeed())
			job, err = registry.GetLoadJob(ctx, "model-x")
			Expect(err).ToNot(HaveOccurred())
			Expect(job.NodeID).To(Equal("node-a"), "a later phase is further along")

			Expect(registry.FailLoadJob(ctx, a.Ref(), "boom", false)).To(Succeed())
			job, err = registry.GetLoadJob(ctx, "model-x")
			Expect(err).ToNot(HaveOccurred())
			Expect(job.NodeID).To(Equal("node-b"), "a running load is reported before a failed one")
		})

		It("moves progress_at only when the load moves", func() {
			job, _, err := registry.ClaimLoadJobOn(ctx, "model-x", "node-a", "replica-a")
			Expect(err).ToNot(HaveOccurred())
			Expect(registry.UpdateLoadJob(ctx, job.Ref(), LoadJobUpdate{State: LoadJobStateStaging, BytesSent: 10, TotalBytes: 100})).To(Succeed())
			first, err := registry.GetLoadJobByRef(ctx, job.Ref())
			Expect(err).ToNot(HaveOccurred())
			Expect(first.ProgressAt).ToNot(BeNil())

			time.Sleep(20 * time.Millisecond)
			Expect(registry.UpdateLoadJob(ctx, job.Ref(), LoadJobUpdate{BytesSent: 10, TotalBytes: 100})).To(Succeed())
			still, err := registry.GetLoadJobByRef(ctx, job.Ref())
			Expect(err).ToNot(HaveOccurred())
			Expect(still.ProgressAt.Equal(*first.ProgressAt)).To(BeTrue(), "a heartbeat with no new bytes is not progress")
			Expect(still.LastProgress.After(first.LastProgress)).To(BeTrue(), "the heartbeat still renews")

			time.Sleep(20 * time.Millisecond)
			Expect(registry.UpdateLoadJob(ctx, job.Ref(), LoadJobUpdate{BytesSent: 20, TotalBytes: 100})).To(Succeed())
			moved, err := registry.GetLoadJobByRef(ctx, job.Ref())
			Expect(err).ToNot(HaveOccurred())
			Expect(moved.ProgressAt.After(*first.ProgressAt)).To(BeTrue())
		})

		It("counts the live loads of a model on the given nodes", func() {
			a, _, err := registry.ClaimLoadJobOn(ctx, "model-x", "node-a", "replica-a")
			Expect(err).ToNot(HaveOccurred())
			_, _, err = registry.ClaimLoadJobOn(ctx, "model-x", "node-b", "replica-b")
			Expect(err).ToNot(HaveOccurred())
			Expect(registry.FailLoadJob(ctx, a.Ref(), "boom", false)).To(Succeed())

			n, err := registry.CountLiveLoadJobs(ctx, "model-x", nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(n).To(Equal(1))
			n, err = registry.CountLiveLoadJobs(ctx, "model-x", []string{"node-a"})
			Expect(err).ToNot(HaveOccurred())
			Expect(n).To(BeZero())
		})
	})

	Describe("the key migration", func() {
		It("moves a table keyed by model alone to (model, node) and keeps its rows", func() {
			fresh := testutil.SetupTestDB()
			Expect(fresh.Migrator().CreateTable(&legacyLoadJob{})).To(Succeed())
			Expect(fresh.Exec(`INSERT INTO model_load_jobs (tracking_key, state, owner_replica, node_id, last_progress, created_at, updated_at)
				VALUES ('model-x', 'staging', 'old-frontend', 'node-a', now(), now(), now()),
				       ('model-y', 'pending', 'old-frontend', NULL, now(), now(), now())`).Error).To(Succeed())

			reg, err := NewNodeRegistry(fresh)
			Expect(err).ToNot(HaveOccurred())
			jobs, err := reg.ListLoadJobs(ctx, "model-y")
			Expect(err).ToNot(HaveOccurred())
			Expect(jobs).To(HaveLen(1))
			Expect(jobs[0].NodeID).To(BeEmpty())

			_, claimed, err := reg.ClaimLoadJobOn(ctx, "model-x", "node-b", "replica-b")
			Expect(err).ToNot(HaveOccurred())
			Expect(claimed).To(BeTrue(), "the old key would refuse a second row of model-x")

			// A second start migrates nothing.
			_, err = NewNodeRegistry(fresh)
			Expect(err).ToNot(HaveOccurred())
			all, err := reg.ListLoadJobs(ctx, "model-x")
			Expect(err).ToNot(HaveOccurred())
			Expect(all).To(HaveLen(2))
		})
	})

	Describe("lifecycle", func() {
		It("lists every active job in stable tracking-key order", func() {
			for _, trackingKey := range []string{"zeta-model", "alpha-model", "middle-model"} {
				_, claimed, err := registry.ClaimLoadJob(ctx, trackingKey, "replica-a")
				Expect(err).ToNot(HaveOccurred())
				Expect(claimed).To(BeTrue())
			}

			jobs, err := registry.ListActiveLoadJobs(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(jobs).To(HaveLen(3))
			Expect([]string{jobs[0].TrackingKey, jobs[1].TrackingKey, jobs[2].TrackingKey}).To(Equal(
				[]string{"alpha-model", "middle-model", "zeta-model"},
			))
		})

		It("records progress and clears the row on completion", func() {
			m1Job, _, err := registry.ClaimLoadJob(ctx, "m1", "replica-a")
			Expect(err).ToNot(HaveOccurred())

			started := time.Now()
			Expect(registry.UpdateLoadJob(ctx, m1Job.Ref(), LoadJobUpdate{
				State: LoadJobStateStaging, NodeID: "node-1", NodeName: "nvidia-thor",
				ReplicaIndex: 2, BytesSent: 500, TotalBytes: 1000, FileIndex: 1, TotalFiles: 1,
				StartedAt: started,
			})).To(Succeed())

			job, err := registry.GetLoadJob(ctx, "m1")
			Expect(err).ToNot(HaveOccurred())
			Expect(job.State).To(Equal(LoadJobStateStaging))
			Expect(job.NodeName).To(Equal("nvidia-thor"))
			Expect(job.ReplicaIndex).To(Equal(2))
			Expect(job.Progress()).To(BeNumerically("~", 50, 0.01))

			Expect(registry.DeleteLoadJob(ctx, m1Job.Ref())).To(Succeed())
			job, err = registry.GetLoadJob(ctx, "m1")
			Expect(err).ToNot(HaveOccurred())
			Expect(job).To(BeNil())
		})

		It("keeps the placement across a byte-less heartbeat", func() {
			m2Job, _, err := registry.ClaimLoadJob(ctx, "m2", "replica-a")
			Expect(err).ToNot(HaveOccurred())
			Expect(registry.UpdateLoadJob(ctx, m2Job.Ref(), LoadJobUpdate{
				State: LoadJobStateStaging, NodeID: "node-1", NodeName: "nvidia-thor",
			})).To(Succeed())

			before, err := registry.GetLoadJob(ctx, "m2")
			Expect(err).ToNot(HaveOccurred())

			// A checkpoint load moves no bytes for minutes; the heartbeat must
			// still tick, and must not erase where the model is loading.
			time.Sleep(10 * time.Millisecond)
			Expect(registry.UpdateLoadJob(ctx, m2Job.Ref(), LoadJobUpdate{State: LoadJobStateLoading})).To(Succeed())

			after, err := registry.GetLoadJob(ctx, "m2")
			Expect(err).ToNot(HaveOccurred())
			Expect(after.NodeName).To(Equal("nvidia-thor"))
			Expect(after.State).To(Equal(LoadJobStateLoading))
			Expect(after.LastProgress.After(before.LastProgress)).To(BeTrue())
		})

		It("records the failure cause for waiters to read", func() {
			m3Job, _, err := registry.ClaimLoadJob(ctx, "m3", "replica-a")
			Expect(err).ToNot(HaveOccurred())
			Expect(registry.FailLoadJob(ctx, m3Job.Ref(), "no available nodes", false)).To(Succeed())

			job, err := registry.GetLoadJob(ctx, "m3")
			Expect(err).ToNot(HaveOccurred())
			Expect(job.State).To(Equal(LoadJobStateFailed))
			Expect(job.LastError).To(Equal("no available nodes"))
		})

		It("hands a request arriving inside the failure grace the real error", func() {
			m4Job, _, err := registry.ClaimLoadJob(ctx, "m4", "replica-a")
			Expect(err).ToNot(HaveOccurred())
			Expect(registry.FailLoadJob(ctx, m4Job.Ref(), "worker out of VRAM", false)).To(Succeed())

			job, claimed, err := registry.ClaimLoadJob(ctx, "m4", "replica-b")
			Expect(err).ToNot(HaveOccurred())
			Expect(claimed).To(BeFalse(), "a fresh load must not silently start on top of a just-failed one")
			Expect(job.LastError).To(Equal("worker out of VRAM"))
		})
	})

	Describe("ETA", func() {
		It("is omitted until the observed rate means something", func() {
			job := &ModelLoadJob{State: LoadJobStateStaging, BytesSent: 0, TotalBytes: 1000}
			_, ok := job.ETA(time.Now())
			Expect(ok).To(BeFalse())

			job = &ModelLoadJob{State: LoadJobStateStaging, BytesSent: 10, TotalBytes: 1000, StartedAt: time.Now()}
			_, ok = job.ETA(time.Now())
			Expect(ok).To(BeFalse(), "less than one broadcast interval of data is not a rate")
		})

		It("derives the remaining time from the job's own rate", func() {
			now := time.Now()
			job := &ModelLoadJob{
				State:     LoadJobStateStaging,
				BytesSent: 1000, TotalBytes: 3000,
				StartedAt: now.Add(-10 * time.Second),
			}
			eta, ok := job.ETA(now)
			Expect(ok).To(BeTrue())
			// 100 B/s observed, 2000 B left.
			Expect(eta).To(BeNumerically("~", 20*time.Second, time.Second))
		})
	})
})
