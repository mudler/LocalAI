// SPDX-License-Identifier: MIT
package nodes

import (
	"context"
	"errors"
	"runtime"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/testutil"
)

// flakyJobRegistry fails lease renewals on demand, the way a database that is
// briefly unreachable would.
type flakyJobRegistry struct {
	*NodeRegistry
	failRenewals atomic.Bool
}

func (f *flakyJobRegistry) UpdateLoadJob(ctx context.Context, ref LoadJobRef, u LoadJobUpdate) error {
	if f.failRenewals.Load() {
		return errors.New("database unreachable")
	}
	return f.NodeRegistry.UpdateLoadJob(ctx, ref, u)
}

// These specs move time by editing the stored deadlines relative to the
// database clock, because the lease is decided by the database and never by the
// frontend clock.
var _ = Describe("Load job lease and reclaim", func() {
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

	// shift sets a deadline column to the database's now() plus secs.
	shift := func(key, column string, secs int) {
		Expect(db.Exec("UPDATE model_load_jobs SET "+column+" = now() + make_interval(secs => ?) WHERE tracking_key = ?", secs, key).Error).To(Succeed())
	}
	// dbSecondsUntil reads how far a deadline is ahead of the database clock.
	dbSecondsUntil := func(key, column string) float64 {
		var secs float64
		Expect(db.Raw("SELECT EXTRACT(EPOCH FROM ("+column+" - now())) FROM model_load_jobs WHERE tracking_key = ?", key).Scan(&secs).Error).To(Succeed())
		return secs
	}
	loadJob := func(key string) *ModelLoadJob {
		job, err := registry.GetLoadJob(ctx, key)
		Expect(err).ToNot(HaveOccurred())
		return job
	}

	Describe("an owner that dies", func() {
		It("is released by the reconciler alone, and the next request then loads", func() {
			node := &BackendNode{Name: "n1", NodeType: NodeTypeBackend, Address: "10.0.0.1:50051"}
			Expect(registry.Register(ctx, node, true)).To(Succeed())
			rc := NewReplicaReconciler(ReplicaReconcilerOptions{Registry: registry, DB: db})

			dead, claimed, err := registry.ClaimLoadJob(ctx, "crashed", "frontend-a")
			Expect(err).ToNot(HaveOccurred())
			Expect(claimed).To(BeTrue())
			Expect(dbSecondsUntil("crashed", "lease_until")).To(BeNumerically("~", loadJobLeaseTTL.Seconds(), 3))
			// The dead owner had published a staging replica before it died.
			Expect(registry.SetNodeModel(withLoadOwnership(ctx, dead.Ref()), node.ID, "crashed", 0, "staging", "10.0.0.1:9001", 0)).To(Succeed())

			// While the lease is live nobody may take the model, and the sweep
			// leaves everything alone.
			_, claimed, err = registry.ClaimLoadJob(ctx, "crashed", "frontend-b")
			Expect(err).ToNot(HaveOccurred())
			Expect(claimed).To(BeFalse())
			rc.reclaimAbandonedLoads(ctx)
			Expect(loadJob("crashed").State).To(Equal(LoadJobStatePending))

			// The owner never renews. The lease runs out; no request arrives.
			shift("crashed", "lease_until", -1)
			rc.reclaimAbandonedLoads(ctx)
			failed := loadJob("crashed")
			Expect(failed).ToNot(BeNil())
			Expect(failed.State).To(Equal(LoadJobStateFailed))
			Expect(failed.LastError).To(ContainSubstring("lease"))
			Expect(dbSecondsUntil("crashed", "stop_deadline")).To(BeNumerically("~", loadJobStopWindow.Seconds(), 3),
				"the slot is held for the stop window, not for ever")

			// A request inside the stop window is told the cause and is not
			// started a second load on top of work that may still run.
			held, claimed, err := registry.ClaimLoadJob(ctx, "crashed", "frontend-b")
			Expect(err).ToNot(HaveOccurred())
			Expect(claimed).To(BeFalse())
			Expect(held.State).To(Equal(LoadJobStateFailed))
			var rows int64
			Expect(db.Model(&NodeModel{}).Where("model_name = ?", "crashed").Count(&rows).Error).To(Succeed())
			Expect(rows).To(Equal(int64(1)), "the replica slot stays reserved until the job is released")

			// The stop window passes. The sweep releases the job and its replica.
			shift("crashed", "stop_deadline", -1)
			rc.reclaimAbandonedLoads(ctx)
			Expect(loadJob("crashed")).To(BeNil())
			Expect(db.Model(&NodeModel{}).Where("model_name = ?", "crashed").Count(&rows).Error).To(Succeed())
			Expect(rows).To(BeZero())

			next, claimed, err := registry.ClaimLoadJob(ctx, "crashed", "frontend-b")
			Expect(err).ToNot(HaveOccurred())
			Expect(claimed).To(BeTrue())
			Expect(next.Generation).ToNot(Equal(dead.Generation))
		})

		It("is reclaimed by a request alone when no reconciler runs", func() {
			dead, _, err := registry.ClaimLoadJob(ctx, "lazy", "frontend-a")
			Expect(err).ToNot(HaveOccurred())
			shift("lazy", "lease_until", -1)

			first, claimed, err := registry.ClaimLoadJob(ctx, "lazy", "frontend-b")
			Expect(err).ToNot(HaveOccurred())
			Expect(claimed).To(BeFalse())
			Expect(first.State).To(Equal(LoadJobStateFailed))

			shift("lazy", "stop_deadline", -1)
			next, claimed, err := registry.ClaimLoadJob(ctx, "lazy", "frontend-b")
			Expect(err).ToNot(HaveOccurred())
			Expect(claimed).To(BeTrue())
			Expect(next.Generation).ToNot(Equal(dead.Generation))
			Expect(registry.UpdateLoadJob(ctx, dead.Ref(), LoadJobUpdate{})).To(MatchError(ErrStaleLoadJob))
		})
	})

	Describe("clock skew", func() {
		It("lets the database clock decide the lease, not the frontend clock", func() {
			for _, skew := range []time.Duration{10 * time.Minute, -10 * time.Minute} {
				key := "skew-" + skew.String()
				registry.clock = func() time.Time { return time.Now().Add(skew) }

				live, claimed, err := registry.ClaimLoadJob(ctx, key, "frontend-a")
				Expect(err).ToNot(HaveOccurred())
				Expect(claimed).To(BeTrue())

				// A live lease is not expired by a frontend clock that runs ahead.
				other, claimed, err := registry.ClaimLoadJob(ctx, key, "frontend-b")
				Expect(err).ToNot(HaveOccurred())
				Expect(claimed).To(BeFalse())
				Expect(other.State).ToNot(Equal(LoadJobStateFailed))

				// Renewal extends the lease by the database's time.
				Expect(registry.UpdateLoadJob(ctx, live.Ref(), LoadJobUpdate{})).To(Succeed())
				Expect(dbSecondsUntil(key, "lease_until")).To(BeNumerically("~", loadJobLeaseTTL.Seconds(), 3))

				// A dead lease is not kept alive by a frontend clock that runs behind.
				shift(key, "lease_until", -1)
				expired, claimed, err := registry.ClaimLoadJob(ctx, key, "frontend-b")
				Expect(err).ToNot(HaveOccurred())
				Expect(claimed).To(BeFalse())
				Expect(expired.State).To(Equal(LoadJobStateFailed))
			}
		})
	})

	Describe("lease renewal in the owner loop", func() {
		var (
			flaky  *flakyJobRegistry
			router *SmartRouter
		)
		BeforeEach(func() {
			flaky = &flakyJobRegistry{NodeRegistry: registry}
			registry.leaseTTL = 4 * time.Second
			router = NewSmartRouter(flaky, SmartRouterOptions{DB: db})
			router.leaseTTL = registry.leaseTTL
		})

		It("renews the lease with every heartbeat", func() {
			job, _, err := registry.ClaimLoadJob(ctx, "renewing", "frontend-a")
			Expect(err).ToNot(HaveOccurred())
			shift("renewing", "lease_until", 1)

			release := make(chan struct{})
			done := make(chan error, 1)
			go func() {
				defer GinkgoRecover()
				done <- router.runLoadOwner(ctx, job.Ref(), func(context.Context) error { <-release; return nil })
			}()
			Eventually(func() float64 { return dbSecondsUntil("renewing", "lease_until") }, 5*time.Second, 200*time.Millisecond).
				Should(BeNumerically(">", 2), "a live owner must push its lease forward")
			close(release)
			Eventually(done, 5*time.Second).Should(Receive(BeNil()))
		})

		It("keeps working through a short database failure", func() {
			job, _, err := registry.ClaimLoadJob(ctx, "blip", "frontend-a")
			Expect(err).ToNot(HaveOccurred())

			started := make(chan context.Context, 1)
			release := make(chan struct{})
			go func() {
				defer GinkgoRecover()
				_ = router.runLoadOwner(ctx, job.Ref(), func(c context.Context) error {
					started <- c
					select {
					case <-c.Done():
						return c.Err()
					case <-release:
						return nil
					}
				})
			}()
			var workCtx context.Context
			Eventually(started).Should(Receive(&workCtx))

			flaky.failRenewals.Store(true)
			time.Sleep(2 * time.Second) // under the TTL
			flaky.failRenewals.Store(false)
			Consistently(workCtx.Done(), 2*time.Second).ShouldNot(BeClosed())
			close(release)
		})

		It("stops its own work when it can no longer extend the lease", func() {
			job, _, err := registry.ClaimLoadJob(ctx, "cut-off", "frontend-a")
			Expect(err).ToNot(HaveOccurred())

			flaky.failRenewals.Store(true)
			result := make(chan error, 1)
			go func() {
				defer GinkgoRecover()
				result <- router.runLoadOwner(ctx, job.Ref(), func(c context.Context) error {
					<-c.Done()
					return c.Err()
				})
			}()

			var ownerErr error
			Eventually(result, 15*time.Second).Should(Receive(&ownerErr))
			Expect(errors.Is(ownerErr, ErrLoadLeaseExpired)).To(BeTrue(), "an owner must not outlive a lease it cannot extend")
			// The database came back, so the owner could record the failure.
			Expect(loadJob("cut-off").State).To(Equal(LoadJobStateFailed))
		})
	})

	Describe("failed jobs", func() {
		It("clears a failure the backend answered after the report window, with no SQL", func() {
			job, _, err := registry.ClaimLoadJob(ctx, "answered", "frontend-a")
			Expect(err).ToNot(HaveOccurred())
			Expect(registry.FailLoadJob(ctx, job.Ref(), "unsupported architecture", false)).To(Succeed())
			Expect(dbSecondsUntil("answered", "stop_deadline")).To(BeNumerically("~", loadJobFailureReport.Seconds(), 3))

			_, claimed, err := registry.ClaimLoadJob(ctx, "answered", "frontend-b")
			Expect(err).ToNot(HaveOccurred())
			Expect(claimed).To(BeFalse())

			shift("answered", "stop_deadline", -1)
			restarted, err := NewNodeRegistry(db)
			Expect(err).ToNot(HaveOccurred())
			next, claimed, err := restarted.ClaimLoadJob(ctx, "answered", "frontend-b")
			Expect(err).ToNot(HaveOccurred())
			Expect(claimed).To(BeTrue())
			Expect(next.Generation).ToNot(Equal(job.Generation))
		})

		It("holds the slot for the stop window when remote work may still run", func() {
			job, _, err := registry.ClaimLoadJob(ctx, "maybe-running", "frontend-a")
			Expect(err).ToNot(HaveOccurred())
			Expect(registry.FailLoadJob(ctx, job.Ref(), "context deadline exceeded", true)).To(Succeed())
			Expect(dbSecondsUntil("maybe-running", "stop_deadline")).To(BeNumerically("~", loadJobStopWindow.Seconds(), 3))
		})
	})

	Describe("legacy rows", func() {
		It("treats a row from before the lease as expired and releases it after the stop window", func() {
			legacyDB := testutil.SetupTestDB()
			Expect(legacyDB.Exec(`CREATE TABLE model_load_jobs (
				tracking_key varchar(255) PRIMARY KEY, state varchar(16) NOT NULL, owner_replica varchar(64),
				node_id varchar(36), node_name varchar(255), replica_index bigint, bytes_sent bigint,
				total_bytes bigint, file_index bigint, total_files bigint, last_error text,
				started_at timestamptz, created_at timestamptz, updated_at timestamptz, last_progress timestamptz)`).Error).To(Succeed())
			Expect(legacyDB.Exec(`INSERT INTO model_load_jobs (tracking_key, state, owner_replica, last_progress, created_at, updated_at)
				VALUES ('old-load', 'staging', 'old-frontend', now(), now(), now())`).Error).To(Succeed())

			migrated, err := NewNodeRegistry(legacyDB)
			Expect(err).ToNot(HaveOccurred())

			held, claimed, err := migrated.ClaimLoadJob(ctx, "old-load", "new-frontend")
			Expect(err).ToNot(HaveOccurred())
			Expect(claimed).To(BeFalse())
			Expect(held.State).To(Equal(LoadJobStateFailed), "an old binary never renews a lease, so its row is expired")

			Expect(legacyDB.Exec("UPDATE model_load_jobs SET stop_deadline = now() - interval '1 second'").Error).To(Succeed())
			next, claimed, err := migrated.ClaimLoadJob(ctx, "old-load", "new-frontend")
			Expect(err).ToNot(HaveOccurred())
			Expect(claimed).To(BeTrue())
			Expect(next.Generation).ToNot(BeEmpty())
		})

		It("gives a failed row from an old binary a stop window instead of releasing it at once", func() {
			Expect(db.Exec(`INSERT INTO model_load_jobs (tracking_key, state, owner_replica, last_error, last_progress, created_at, updated_at)
				VALUES ('old-failed', 'failed', 'old-frontend', 'boom', now(), now(), now())`).Error).To(Succeed())
			held, claimed, err := registry.ClaimLoadJob(ctx, "old-failed", "new-frontend")
			Expect(err).ToNot(HaveOccurred())
			Expect(claimed).To(BeFalse())
			Expect(held.LastError).To(Equal("boom"))
			Expect(dbSecondsUntil("old-failed", "stop_deadline")).To(BeNumerically("~", loadJobStopWindow.Seconds(), 3))
		})
	})

	Describe("replicas of an abandoned attempt", func() {
		It("reaps by generation and leaves the current attempt's replica alone", func() {
			a := &BackendNode{Name: "n-a", NodeType: NodeTypeBackend, Address: "10.0.0.1:50051"}
			b := &BackendNode{Name: "n-b", NodeType: NodeTypeBackend, Address: "10.0.0.2:50051"}
			Expect(registry.Register(ctx, a, true)).To(Succeed())
			Expect(registry.Register(ctx, b, true)).To(Succeed())
			rc := NewReplicaReconciler(ReplicaReconcilerOptions{Registry: registry, DB: db})

			old, _, err := registry.ClaimLoadJob(ctx, "generations", "frontend-a")
			Expect(err).ToNot(HaveOccurred())
			Expect(registry.SetNodeModel(withLoadOwnership(ctx, old.Ref()), a.ID, "generations", 0, "staging", "x:1", 0)).To(Succeed())
			// The owner dies, the job is released and another attempt takes over.
			shift("generations", "lease_until", -1)
			rc.reclaimAbandonedLoads(ctx)
			shift("generations", "stop_deadline", -1)
			rc.reclaimAbandonedLoads(ctx)
			current, claimed, err := registry.ClaimLoadJob(ctx, "generations", "frontend-b")
			Expect(err).ToNot(HaveOccurred())
			Expect(claimed).To(BeTrue())
			Expect(registry.SetNodeModel(withLoadOwnership(ctx, current.Ref()), b.ID, "generations", 0, "staging", "y:1", 0)).To(Succeed())
			// A row of the dead attempt that survived (a late write before the
			// release) belongs to a generation that no longer has a job.
			Expect(db.Create(&NodeModel{ID: "straggler", NodeID: a.ID, ModelName: "generations", ReplicaIndex: 1,
				State: "staging", LoadGeneration: old.Generation}).Error).To(Succeed())

			rc.reclaimAbandonedLoads(ctx)

			var left []NodeModel
			Expect(db.Where("model_name = ?", "generations").Find(&left).Error).To(Succeed())
			Expect(left).To(HaveLen(1))
			Expect(left[0].LoadGeneration).To(Equal(current.Generation))
		})
	})
})
