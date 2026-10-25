// SPDX-License-Identifier: MIT
package nodes

import (
	"context"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// legacyLoadJob is the job table as a binary without the generation column made
// it.
type legacyLoadJob struct {
	TrackingKey  string `gorm:"primaryKey;size:255"`
	State        string `gorm:"size:16;not null;index"`
	OwnerReplica string `gorm:"size:64"`
	NodeID       string `gorm:"size:36"`
	NodeName     string `gorm:"size:255"`
	ReplicaIndex int
	BytesSent    int64
	TotalBytes   int64
	FileIndex    int
	TotalFiles   int
	LastError    string `gorm:"type:text"`
	StartedAt    time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
	LastProgress time.Time `gorm:"index"`
}

func (legacyLoadJob) TableName() string { return "model_load_jobs" }

// A single-process deployment keeps its registry in SQLite. The migration and
// the job rules must hold there too, not only on PostgreSQL. These specs need
// no container.
var _ = Describe("Load jobs on SQLite", func() {
	var (
		db  *gorm.DB
		ctx context.Context
	)

	BeforeEach(func() {
		ctx = context.Background()
		var err error
		db, err = gorm.Open(sqlite.Open(filepath.Join(GinkgoT().TempDir(), "nodes.db")), &gorm.Config{})
		Expect(err).ToNot(HaveOccurred())
	})

	It("gives legacy rows a generation of their own and fences them", func() {
		Expect(db.Migrator().CreateTable(&legacyLoadJob{})).To(Succeed())
		for _, key := range []string{"legacy-a", "legacy-b"} {
			Expect(db.Exec(`INSERT INTO model_load_jobs (tracking_key, state, owner_replica, last_progress, created_at, updated_at)
				VALUES (?, 'staging', 'old-frontend', ?, ?, ?)`, key, time.Now(), time.Now(), time.Now()).Error).To(Succeed())
		}

		registry, err := NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())

		var jobs []ModelLoadJob
		Expect(db.Find(&jobs).Error).To(Succeed())
		Expect(jobs).To(HaveLen(2))
		Expect(jobs[0].Generation).ToNot(BeEmpty())
		Expect(jobs[1].Generation).ToNot(BeEmpty())
		Expect(jobs[0].Generation).ToNot(Equal(jobs[1].Generation))

		// Running it again changes nothing.
		before := jobs[0].Generation
		_, err = NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())
		again, err := registry.GetLoadJob(ctx, jobs[0].TrackingKey)
		Expect(err).ToNot(HaveOccurred())
		Expect(again.Generation).To(Equal(before))
	})

	It("claims, heartbeats and releases a job, and fences a replaced attempt", func() {
		registry, err := NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())

		a, claimed, err := registry.ClaimLoadJob(ctx, "sqlite-model", "frontend-a")
		Expect(err).ToNot(HaveOccurred())
		Expect(claimed).To(BeTrue())
		Expect(registry.UpdateLoadJob(ctx, a.Ref(), LoadJobUpdate{State: LoadJobStateLoading})).To(Succeed())
		Expect(registry.DeleteLoadJob(ctx, a.Ref())).To(Succeed())

		b, claimed, err := registry.ClaimLoadJob(ctx, "sqlite-model", "frontend-b")
		Expect(err).ToNot(HaveOccurred())
		Expect(claimed).To(BeTrue())
		Expect(registry.UpdateLoadJob(ctx, a.Ref(), LoadJobUpdate{})).To(MatchError(ErrStaleLoadJob))
		Expect(registry.FailLoadJob(ctx, b.Ref(), "boom", false)).To(Succeed())
		Expect(registry.DeleteLoadJob(ctx, b.Ref())).To(MatchError(ErrStaleLoadJob))
	})

	// The lease rules read a clock that moves, so these specs move the stored
	// deadlines instead of waiting.
	It("applies the lease and stop window rules", func() {
		registry, err := NewNodeRegistry(db)
		Expect(err).ToNot(HaveOccurred())
		past := time.Now().Add(-time.Second)

		dead, claimed, err := registry.ClaimLoadJob(ctx, "sqlite-lease", "frontend-a")
		Expect(err).ToNot(HaveOccurred())
		Expect(claimed).To(BeTrue())
		other, claimed, err := registry.ClaimLoadJob(ctx, "sqlite-lease", "frontend-b")
		Expect(err).ToNot(HaveOccurred())
		Expect(claimed).To(BeFalse(), "a live lease holds the model")
		Expect(other.State).ToNot(Equal(LoadJobStateFailed))

		// The owner stops renewing: the claim fails the job and holds the model.
		Expect(db.Exec("UPDATE model_load_jobs SET lease_until = ?", past).Error).To(Succeed())
		held, claimed, err := registry.ClaimLoadJob(ctx, "sqlite-lease", "frontend-b")
		Expect(err).ToNot(HaveOccurred())
		Expect(claimed).To(BeFalse())
		Expect(held.State).To(Equal(LoadJobStateFailed))
		Expect(held.StopDeadline).ToNot(BeNil())
		Expect(held.StopDeadline.After(time.Now().Add(loadJobStopWindow - 10*time.Second))).To(BeTrue())

		// The stop window ends: the sweep releases it, and the next claim wins.
		Expect(db.Exec("UPDATE model_load_jobs SET stop_deadline = ?", past).Error).To(Succeed())
		sweep, err := registry.SweepLoadJobs(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(sweep.Released).To(HaveLen(1))
		next, claimed, err := registry.ClaimLoadJob(ctx, "sqlite-lease", "frontend-b")
		Expect(err).ToNot(HaveOccurred())
		Expect(claimed).To(BeTrue())
		Expect(next.Generation).ToNot(Equal(dead.Generation))

		// A heartbeat renews the lease.
		Expect(db.Exec("UPDATE model_load_jobs SET lease_until = ?", past).Error).To(Succeed())
		Expect(registry.UpdateLoadJob(ctx, next.Ref(), LoadJobUpdate{})).To(Succeed())
		live, err := registry.GetLoadJob(ctx, "sqlite-lease")
		Expect(err).ToNot(HaveOccurred())
		Expect(live.LeaseUntil).ToNot(BeNil())
		Expect(live.LeaseUntil.After(time.Now())).To(BeTrue())
	})
})
