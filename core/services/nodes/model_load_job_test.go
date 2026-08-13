package nodes

import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/testutil"
	"gorm.io/gorm"
)

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
			_, claimed, err := registry.ClaimLoadJob(ctx, "slow-model", "replica-a")
			Expect(err).ToNot(HaveOccurred())
			Expect(claimed).To(BeTrue())

			running := make(chan struct{})
			done := make(chan struct{})
			go func() {
				defer GinkgoRecover()
				close(running)
				// Stand in for a multi-GB staging run owned by replica-a.
				time.Sleep(2 * time.Second)
				Expect(registry.DeleteLoadJob(ctx, "slow-model")).To(Succeed())
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

		It("reclaims a job whose owner stopped heartbeating", func() {
			_, claimed, err := registry.ClaimLoadJob(ctx, "orphan", "dead-replica")
			Expect(err).ToNot(HaveOccurred())
			Expect(claimed).To(BeTrue())

			// Backdate the heartbeat past the orphan window, as a replica killed
			// mid-load would leave it.
			stale := time.Now().Add(-2 * loadJobOrphanWindow)
			Expect(db.Model(&ModelLoadJob{}).Where("tracking_key = ?", "orphan").
				Update("last_progress", stale).Error).ToNot(HaveOccurred())

			job, claimed, err := registry.ClaimLoadJob(ctx, "orphan", "live-replica")
			Expect(err).ToNot(HaveOccurred())
			Expect(claimed).To(BeTrue(), "a dead replica must not wedge the model permanently")
			Expect(job.OwnerReplica).To(Equal("live-replica"))
		})
	})

	Describe("lifecycle", func() {
		It("records progress and clears the row on completion", func() {
			_, _, err := registry.ClaimLoadJob(ctx, "m1", "replica-a")
			Expect(err).ToNot(HaveOccurred())

			started := time.Now()
			Expect(registry.UpdateLoadJob(ctx, "m1", LoadJobUpdate{
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

			Expect(registry.DeleteLoadJob(ctx, "m1")).To(Succeed())
			job, err = registry.GetLoadJob(ctx, "m1")
			Expect(err).ToNot(HaveOccurred())
			Expect(job).To(BeNil())
		})

		It("keeps the placement across a byte-less heartbeat", func() {
			_, _, err := registry.ClaimLoadJob(ctx, "m2", "replica-a")
			Expect(err).ToNot(HaveOccurred())
			Expect(registry.UpdateLoadJob(ctx, "m2", LoadJobUpdate{
				State: LoadJobStateStaging, NodeID: "node-1", NodeName: "nvidia-thor",
			})).To(Succeed())

			before, err := registry.GetLoadJob(ctx, "m2")
			Expect(err).ToNot(HaveOccurred())

			// A checkpoint load moves no bytes for minutes; the heartbeat must
			// still tick, and must not erase where the model is loading.
			time.Sleep(10 * time.Millisecond)
			Expect(registry.UpdateLoadJob(ctx, "m2", LoadJobUpdate{State: LoadJobStateLoading})).To(Succeed())

			after, err := registry.GetLoadJob(ctx, "m2")
			Expect(err).ToNot(HaveOccurred())
			Expect(after.NodeName).To(Equal("nvidia-thor"))
			Expect(after.State).To(Equal(LoadJobStateLoading))
			Expect(after.LastProgress.After(before.LastProgress)).To(BeTrue())
		})

		It("records the failure cause for waiters to read", func() {
			_, _, err := registry.ClaimLoadJob(ctx, "m3", "replica-a")
			Expect(err).ToNot(HaveOccurred())
			Expect(registry.FailLoadJob(ctx, "m3", "no available nodes")).To(Succeed())

			job, err := registry.GetLoadJob(ctx, "m3")
			Expect(err).ToNot(HaveOccurred())
			Expect(job.State).To(Equal(LoadJobStateFailed))
			Expect(job.LastError).To(Equal("no available nodes"))
		})

		It("hands a request arriving inside the failure grace the real error", func() {
			_, _, err := registry.ClaimLoadJob(ctx, "m4", "replica-a")
			Expect(err).ToNot(HaveOccurred())
			Expect(registry.FailLoadJob(ctx, "m4", "worker out of VRAM")).To(Succeed())

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
