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
		Expect(registry.FailLoadJob(ctx, b.Ref(), "boom")).To(Succeed())
		Expect(registry.DeleteLoadJob(ctx, b.Ref())).To(MatchError(ErrStaleLoadJob))
	})
})
