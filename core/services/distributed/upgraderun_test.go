package distributed_test

import (
	"errors"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/mudler/LocalAI/core/services/distributed"
	"github.com/mudler/LocalAI/core/services/testutil"
	"github.com/mudler/LocalAI/pkg/sqlitedb"
)

func openUpgradeRunSQLite() *gorm.DB {
	path := filepath.Join(GinkgoT().TempDir(), "runs.db")
	db, err := sqlitedb.Open("file:"+path+"?_busy_timeout=5000&_txlock=immediate", &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

// upgradeRunStoreContract is what the upgrade run store does on every
// database.
func upgradeRunStoreContract(open func() *gorm.DB) {
	var s *distributed.UpgradeRunStore

	BeforeEach(func() {
		var err error
		s, err = distributed.NewUpgradeRunStore(open())
		Expect(err).NotTo(HaveOccurred())
	})

	newRun := func(id string, created time.Time) {
		Expect(s.Create(&distributed.UpgradeRunRecord{ID: id, Source: "manual", Actor: "admin", State: "running", CreatedAt: created},
			[]distributed.UpgradeRunItemRecord{
				{Key: "gallery:b", Kind: "gallery", Name: "b", Phase: "queued"},
				{Key: "gallery:a", Kind: "gallery", Name: "a", Phase: "queued"},
			})).To(Succeed())
	}

	It("creates a run and returns it with its items in run order", func() {
		newRun("r1", time.Now())
		run, items, err := s.Get("r1")
		Expect(err).NotTo(HaveOccurred())
		Expect(run.State).To(Equal("running"))
		Expect(run.Actor).To(Equal("admin"))
		Expect(items).To(HaveLen(2))
		Expect(items[0].Key).To(Equal("gallery:b"))
		Expect(items[1].Position).To(Equal(1))
	})

	It("returns ErrUpgradeRunNotFound for an unknown run", func() {
		_, _, err := s.Get("nope")
		Expect(errors.Is(err, distributed.ErrUpgradeRunNotFound)).To(BeTrue())
	})

	It("saves the runner's columns and keeps a cancel flag another frontend set", func() {
		newRun("r1", time.Now())
		Expect(s.RequestCancelItem("r1", "gallery:a")).To(Succeed())
		Expect(s.RequestCancel("r1")).To(Succeed())

		// The runner saves the state it had before it saw the flags.
		now := time.Now()
		Expect(s.Save(&distributed.UpgradeRunRecord{ID: "r1", State: "running", Runner: "frontend-a"},
			[]distributed.UpgradeRunItemRecord{
				{Key: "gallery:b", Kind: "gallery", Name: "b", Phase: "running", JobID: "job-b", Message: "starting"},
				{Key: "gallery:a", Kind: "gallery", Name: "a", Phase: "queued", QueuedReason: "backend_limit"},
			})).To(Succeed())
		run, items, err := s.Get("r1")
		Expect(err).NotTo(HaveOccurred())
		Expect(run.CancelRequested).To(BeTrue())
		Expect(run.Runner).To(Equal("frontend-a"))
		Expect(items[0].JobID).To(Equal("job-b"))
		Expect(items[1].QueuedReason).To(Equal("backend_limit"))
		Expect(items[1].CancelRequested).To(BeTrue())

		Expect(s.Save(&distributed.UpgradeRunRecord{ID: "r1", State: "cancelled", FinishedAt: &now, Runner: "frontend-a"}, items)).To(Succeed())
		run, _, err = s.Get("r1")
		Expect(err).NotTo(HaveOccurred())
		Expect(run.State).To(Equal("cancelled"))
		Expect(run.FinishedAt).NotTo(BeNil())
	})

	It("refuses a save of a runner that another frontend took the run from", func() {
		newRun("r1", time.Now())
		items := []distributed.UpgradeRunItemRecord{
			{Key: "gallery:b", Kind: "gallery", Name: "b", Phase: "running", JobID: "job-b"},
			{Key: "gallery:a", Kind: "gallery", Name: "a", Phase: "queued"},
		}
		Expect(s.Save(&distributed.UpgradeRunRecord{ID: "r1", State: "running", Runner: "frontend-a"}, items)).To(Succeed())
		// Frontend B takes the run over.
		Expect(s.Claim("r1", "frontend-b")).To(Succeed())
		// A, which lost its lock and did not notice yet, writes no more.
		items[0].Phase = "failed"
		err := s.Save(&distributed.UpgradeRunRecord{ID: "r1", State: "running", Runner: "frontend-a"}, items)
		Expect(errors.Is(err, distributed.ErrUpgradeRunFenced)).To(BeTrue(), "%v", err)
		run, got, err := s.Get("r1")
		Expect(err).NotTo(HaveOccurred())
		Expect(run.Runner).To(Equal("frontend-b"))
		Expect(got[0].Phase).To(Equal("running"))
		// B writes.
		Expect(s.Save(&distributed.UpgradeRunRecord{ID: "r1", State: "running", Runner: "frontend-b"}, items)).To(Succeed())
		// A claim of a run that is over changes nothing.
		now := time.Now()
		Expect(s.Save(&distributed.UpgradeRunRecord{ID: "r1", State: "finished", FinishedAt: &now, Runner: "frontend-b"}, items)).To(Succeed())
		Expect(errors.Is(s.Claim("r1", "frontend-c"), distributed.ErrUpgradeRunState)).To(BeTrue())
	})

	It("refuses a save of an unknown run", func() {
		err := s.Save(&distributed.UpgradeRunRecord{ID: "ghost", State: "running"}, nil)
		Expect(errors.Is(err, distributed.ErrUpgradeRunNotFound)).To(BeTrue())
	})

	It("lists running runs first, then the newest, and hides dismissed ones", func() {
		base := time.Now().Add(-time.Hour)
		newRun("old-done", base)
		newRun("new-done", base.Add(2*time.Minute))
		newRun("running", base.Add(time.Minute))
		done := time.Now()
		for _, id := range []string{"old-done", "new-done"} {
			Expect(s.Save(&distributed.UpgradeRunRecord{ID: id, State: "finished", FinishedAt: &done}, nil)).To(Succeed())
		}
		runs, items, err := s.List(0, false)
		Expect(err).NotTo(HaveOccurred())
		ids := []string{}
		for _, r := range runs {
			ids = append(ids, r.ID)
		}
		Expect(ids).To(Equal([]string{"running", "new-done", "old-done"}))
		Expect(items["running"]).To(HaveLen(2))

		Expect(s.Dismiss("old-done")).To(Succeed())
		runs, _, err = s.List(0, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(runs).To(HaveLen(2))
		runs, _, err = s.List(0, true)
		Expect(err).NotTo(HaveOccurred())
		Expect(runs).To(HaveLen(3))
		runs, _, err = s.List(1, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(runs).To(HaveLen(1))

		active, err := s.ListActive()
		Expect(err).NotTo(HaveOccurred())
		Expect(active).To(Equal([]string{"running"}))
	})

	It("refuses a dismiss of a running run and a cancel of a finished one", func() {
		newRun("r1", time.Now())
		Expect(errors.Is(s.Dismiss("r1"), distributed.ErrUpgradeRunState)).To(BeTrue())
		done := time.Now()
		Expect(s.Save(&distributed.UpgradeRunRecord{ID: "r1", State: "finished", FinishedAt: &done}, nil)).To(Succeed())
		Expect(errors.Is(s.RequestCancel("r1"), distributed.ErrUpgradeRunState)).To(BeTrue())
		Expect(errors.Is(s.RequestCancelItem("r1", "gallery:a"), distributed.ErrUpgradeRunState)).To(BeTrue())
		Expect(errors.Is(s.RequestCancel("nope"), distributed.ErrUpgradeRunNotFound)).To(BeTrue())
		Expect(errors.Is(s.Dismiss("nope"), distributed.ErrUpgradeRunNotFound)).To(BeTrue())
	})

	It("removes runs that ended before the retention", func() {
		newRun("r1", time.Now())
		newRun("r2", time.Now())
		long := time.Now().Add(-48 * time.Hour)
		Expect(s.Save(&distributed.UpgradeRunRecord{ID: "r1", State: "finished", FinishedAt: &long}, nil)).To(Succeed())
		_, err := s.Prune(24*time.Hour, -1)
		Expect(err).NotTo(HaveOccurred())
		_, _, err = s.Get("r1")
		Expect(errors.Is(err, distributed.ErrUpgradeRunNotFound)).To(BeTrue())
		_, _, err = s.Get("r2")
		Expect(err).NotTo(HaveOccurred())
	})

	// finish ends a run at the given time.
	finish := func(id string, at time.Time) {
		Expect(s.Save(&distributed.UpgradeRunRecord{ID: id, State: "finished", FinishedAt: &at}, nil)).To(Succeed())
	}
	exists := func(id string) bool {
		_, _, err := s.Get(id)
		if errors.Is(err, distributed.ErrUpgradeRunNotFound) {
			return false
		}
		Expect(err).NotTo(HaveOccurred())
		return true
	}

	It("prunes finished runs by age with their items, and never a running run", func() {
		old := time.Now().Add(-40 * 24 * time.Hour)
		newRun("old-done", old)
		newRun("old-running", old)
		newRun("recent", time.Now())
		finish("old-done", old)
		finish("recent", time.Now())

		n, err := s.Prune(30*24*time.Hour, -1)
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(Equal(int64(1)))
		Expect(exists("old-done")).To(BeFalse())
		Expect(exists("old-running")).To(BeTrue(), "a running run is never removed")
		Expect(exists("recent")).To(BeTrue())
		var orphans int64
		Expect(s.DB().Model(&distributed.UpgradeRunItemRecord{}).Where("run_id = ?", "old-done").Count(&orphans).Error).To(Succeed())
		Expect(orphans).To(BeZero())

		n, err = s.Prune(30*24*time.Hour, -1)
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(BeZero(), "pruning again removes nothing")
	})

	It("keeps only the newest finished runs, and never counts or removes a running run", func() {
		base := time.Now().Add(-time.Hour)
		for i, id := range []string{"f1", "f2", "f3", "f4"} {
			newRun(id, base.Add(time.Duration(i)*time.Minute))
			finish(id, base.Add(time.Duration(i)*time.Minute))
		}
		newRun("running", base.Add(-time.Hour))

		n, err := s.Prune(0, 2)
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(Equal(int64(2)))
		Expect(exists("f1")).To(BeFalse())
		Expect(exists("f2")).To(BeFalse())
		Expect(exists("f3")).To(BeTrue())
		Expect(exists("f4")).To(BeTrue())
		Expect(exists("running")).To(BeTrue())

		n, err = s.Prune(0, -1)
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(BeZero(), "no age and no count limit keeps every run")
	})
}

// galleryTouchContract checks TouchActive on every database.
func galleryTouchContract(open func() *gorm.DB) {
	It("keeps a touched operation from being reaped and leaves the others to the reaper", func() {
		db := open()
		g, err := distributed.NewGalleryStore(db)
		Expect(err).NotTo(HaveOccurred())
		for _, id := range []string{"live", "orphan", "done"} {
			Expect(g.Create(&distributed.GalleryOperationRecord{ID: id, GalleryElementName: id, OpType: "backend_upgrade", Status: "pending"})).To(Succeed())
		}
		Expect(g.UpdateStatus("done", "completed", "")).To(Succeed())
		old := time.Now().Add(-time.Hour)
		Expect(db.Model(&distributed.GalleryOperationRecord{}).Where("1 = 1").Update("updated_at", old).Error).To(Succeed())

		Expect(g.TouchActive([]string{"live", "done"})).To(Succeed())
		n, err := g.CleanStale(30 * time.Minute)
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(Equal(int64(1)))
		live, err := g.Get("live")
		Expect(err).NotTo(HaveOccurred())
		Expect(live.Status).To(Equal("pending"))
		orphan, err := g.Get("orphan")
		Expect(err).NotTo(HaveOccurred())
		Expect(orphan.Status).To(Equal("failed"))
		done, err := g.Get("done")
		Expect(err).NotTo(HaveOccurred())
		Expect(done.UpdatedAt.Before(time.Now().Add(-50*time.Minute))).To(BeTrue(), "a finished operation is not touched")
		Expect(g.TouchActive(nil)).To(Succeed())
	})
}

var _ = Describe("UpgradeRunStore on SQLite", func() {
	upgradeRunStoreContract(openUpgradeRunSQLite)
	galleryTouchContract(openUpgradeRunSQLite)
})

var _ = Describe("UpgradeRunStore on PostgreSQL", func() {
	url := os.Getenv("LOCALAI_TEST_POSTGRES_URL")
	if url == "" {
		It("runs the store contract", func() {
			Skip("set LOCALAI_TEST_POSTGRES_URL to run the upgrade run store against PostgreSQL")
		})
		return
	}
	// Each spec has a schema of its own: the upgraderun package runs its
	// specs on the same database at the same time, and a running run of one
	// package made a start of the other answer run_active.
	open := func() *gorm.DB {
		db := testutil.OpenPostgresSchema(url, "upgrade_store")
		Expect(db.AutoMigrate(&distributed.UpgradeRunRecord{}, &distributed.UpgradeRunItemRecord{}, &distributed.GalleryOperationRecord{})).To(Succeed())
		return db
	}
	upgradeRunStoreContract(open)
	galleryTouchContract(open)
})
