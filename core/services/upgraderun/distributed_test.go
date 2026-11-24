package upgraderun_test

import (
	"context"
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/distributed"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/core/services/testutil"
	. "github.com/mudler/LocalAI/core/services/upgraderun"
)

// Two frontends of one cluster: the same database, the same bus, and the
// same operation statuses (the bus replicates them between real frontends).
func twoFrontends(open func() *gorm.DB) {
	ctx := context.Background()

	It("answer for the same run, pass a cancel to the runner, and take over the run of a dead frontend", func() {
		db := open()
		bus := testutil.NewFakeBus()
		jobs := newFakeJobs()
		a := newHarness(harnessOpts{db: db, bus: bus, jobs: jobs, instance: "frontend-a"})
		b := newHarness(harnessOpts{db: db, bus: bus, jobs: jobs, instance: "frontend-b",
			starter: a.starter, backends: a.backends, models: a.models, addons: a.addons})

		a.backends.add(Candidate{Kind: KindGallery, Name: "kokoro"})
		a.backends.add(Candidate{Kind: KindGallery, Name: "whisper"})
		a.models.add(Candidate{Kind: KindModel, Name: "m"})
		r := a.start(si(KindGallery, "kokoro"), si(KindGallery, "whisper"), si(KindModel, "m"))

		// B answers for the run A runs, with the live progress of the
		// operation.
		kokoro := a.jobOf("gallery:kokoro")
		whisper := a.jobOf("gallery:whisper")
		jobs.set(kokoro, galleryop.OpStatus{Phase: "fetching", Progress: 55, Message: "downloading", Cancellable: true})
		Eventually(func() int { return b.item(r.ID, "gallery:kokoro").Progress }).Should(Equal(55))
		Expect(b.item(r.ID, "gallery:kokoro").Message).To(Equal("downloading"))

		// B's observers hear of A's changes over the bus.
		Eventually(func() bool {
			select {
			case c := <-b.changes:
				return !c.Local && c.Run.ID == r.ID
			default:
				return false
			}
		}).Should(BeTrue())

		// A start through B is refused while A's run runs: one run at a time
		// in the cluster.
		_, err := b.svc.Start(ctx, "admin", StartRequest{Items: []StartItem{si(KindGallery, "kokoro")}})
		Expect(CodeOf(err)).To(Equal(CodeRunActive))

		// A cancel through B reaches the runner on A.
		Eventually(func() bool { return b.item(r.ID, "gallery:kokoro").Cancellable }).Should(BeTrue())
		Expect(b.svc.CancelItem(ctx, r.ID, "gallery:kokoro")).To(Succeed())
		Eventually(jobs.cancelled).Should(Equal([]string{kokoro}))
		Eventually(func() Phase { return b.phase(r.ID, "gallery:kokoro") }).Should(Equal(PhaseCancelled))

		// B does not take a run A still holds.
		b.svc.Sweep(ctx)
		Consistently(func() int { return len(b.svc.Local()) }, 50*time.Millisecond).Should(BeZero())

		// A dies with whisper running. Its operation died with it, but the
		// update had applied.
		a.svc.Close()
		a.backends.remove("gallery:whisper")
		jobs.drop(whisper)

		b.svc.Sweep(ctx)
		Eventually(b.svc.Local).Should(ConsistOf(r.ID))
		Eventually(func() Phase { return b.phase(r.ID, "gallery:whisper") }).Should(Equal(PhaseUpdated))

		// B goes on with the run: the model starts after the backends.
		Eventually(a.starter.started).Should(ContainElement("model:m"))
		m := a.jobOf("model:m")
		a.models.remove("model:m")
		jobs.complete(m)
		Eventually(func() RunState { return b.state(r.ID) }).Should(Equal(RunFinished))
		rec, _, err := b.store.Get(r.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(rec.Runner).To(Equal("frontend-b"))
	})
}

// A frontend that loses a run while its operation goes on.
func liveHandover(open func() *gorm.DB) {
	It("follows an operation that is still live when it takes the run over, and never fails it as interrupted", func() {
		db := open()
		bus := testutil.NewFakeBus()
		jobs := newFakeJobs()
		a := newHarness(harnessOpts{db: db, bus: bus, jobs: jobs, instance: "frontend-a"})
		b := newHarness(harnessOpts{db: db, bus: bus, jobs: jobs, instance: "frontend-b",
			starter: a.starter, backends: a.backends, models: a.models, addons: a.addons})
		a.backends.add(Candidate{Kind: KindGallery, Name: "kokoro"})
		r := a.start(si(KindGallery, "kokoro"))
		kokoro := a.jobOf("gallery:kokoro")
		jobs.set(kokoro, galleryop.OpStatus{Phase: "fetching", Progress: 40, Cancellable: true})
		Eventually(func() int { return a.item(r.ID, "gallery:kokoro").Progress }).Should(Equal(40))

		// A stops running the run; the operation goes on (a lost lock, not
		// a dead process).
		a.svc.Close()
		b.svc.Sweep(context.Background())
		Eventually(b.svc.Local).Should(ConsistOf(r.ID))
		Consistently(func() Phase { return b.phase(r.ID, "gallery:kokoro") }, 200*time.Millisecond).Should(Equal(PhaseRunning))
		Expect(b.item(r.ID, "gallery:kokoro").JobID).To(Equal(kokoro))

		// The download ends later: B follows it to the end.
		a.backends.remove("gallery:kokoro")
		jobs.complete(kokoro)
		Eventually(func() Phase { return b.phase(r.ID, "gallery:kokoro") }).Should(Equal(PhaseUpdated))
		Expect(b.state(r.ID)).To(Equal(RunFinished))
	})
}

var _ = Describe("A run taken over with a live operation", func() {
	liveHandover(openSQLite)

	It("checks an item whose operation shows no change after the takeover: its frontend is gone", func() {
		db := openSQLite()
		jobs := newFakeJobs()
		a := newHarness(harnessOpts{db: db, jobs: jobs, instance: "frontend-a"})
		b := newHarness(harnessOpts{db: db, jobs: jobs, instance: "frontend-b", recoveredIdle: 200 * time.Millisecond,
			starter: a.starter, backends: a.backends, models: a.models, addons: a.addons})
		a.backends.add(Candidate{Kind: KindGallery, Name: "kokoro"})
		r := a.start(si(KindGallery, "kokoro"))
		kokoro := a.jobOf("gallery:kokoro")
		jobs.set(kokoro, galleryop.OpStatus{Phase: "fetching", Progress: 40})
		Eventually(func() int { return a.item(r.ID, "gallery:kokoro").Progress }).Should(Equal(40))
		// A dies; the status it last wrote stays on the others. The update
		// had applied.
		a.svc.Close()
		a.backends.remove("gallery:kokoro")
		b.svc.Sweep(context.Background())
		Eventually(b.svc.Local).Should(ConsistOf(r.ID))
		Eventually(func() Phase { return b.phase(r.ID, "gallery:kokoro") }, 5*time.Second).Should(Equal(PhaseUpdated))
	})
})

var _ = Describe("Two frontends on SQLite", func() {
	twoFrontends(openSQLite)
})

// On PostgreSQL the locks are real session locks: the lock of a frontend
// that closes is freed on the server, and the other frontend takes it.
var _ = Describe("Two frontends on PostgreSQL", func() {
	url := os.Getenv("LOCALAI_TEST_POSTGRES_URL")
	if url == "" {
		It("take over a run", func() {
			Skip("set LOCALAI_TEST_POSTGRES_URL to run the two-frontend spec against PostgreSQL")
		})
		return
	}
	// A schema of its own: the distributed package runs its store specs on
	// the same database at the same time, and their running runs made the
	// start here answer run_active.
	twoFrontends(func() *gorm.DB {
		db := testutil.OpenPostgresSchema(url, "upgrade_runs")
		Expect(db.AutoMigrate(&distributed.UpgradeRunRecord{}, &distributed.UpgradeRunItemRecord{})).To(Succeed())
		return db
	})
})

var _ = Describe("The stale operation reaper", func() {
	It("does not fail the operations of a live run, and still fails an orphan", func() {
		db := openSQLite()
		gallery, err := distributed.NewGalleryStore(db)
		Expect(err).NotTo(HaveOccurred())
		h := newHarness(harnessOpts{db: db, toucher: gallery})
		h.backends.add(Candidate{Kind: KindGallery, Name: "kokoro"})
		r := h.start(si(KindGallery, "kokoro"))
		job := h.jobOf("gallery:kokoro")
		Eventually(func() string { return h.item(r.ID, "gallery:kokoro").JobID }).Should(Equal(job))

		// The operation waits in the gallery queue behind a long download,
		// so nothing ticks it; an orphan of a dead frontend sits beside it.
		for _, id := range []string{job, "orphan"} {
			Expect(gallery.Create(&distributed.GalleryOperationRecord{ID: id, GalleryElementName: id, OpType: "backend_upgrade", Status: "pending"})).To(Succeed())
		}
		old := time.Now().Add(-time.Hour)
		Expect(db.Model(&distributed.GalleryOperationRecord{}).Where("1 = 1").Update("updated_at", old).Error).To(Succeed())

		Eventually(func() bool {
			rec, err := gallery.Get(job)
			return err == nil && rec.UpdatedAt.After(old.Add(time.Minute))
		}).Should(BeTrue(), "the runner touches the operations it follows")

		n, err := gallery.CleanStale(30 * time.Minute)
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(Equal(int64(1)))
		live, err := gallery.Get(job)
		Expect(err).NotTo(HaveOccurred())
		Expect(live.Status).To(Equal("pending"))
		orphan, err := gallery.Get("orphan")
		Expect(err).NotTo(HaveOccurred())
		Expect(orphan.Status).To(Equal("failed"))
		Expect(h.phase(r.ID, "gallery:kokoro")).To(Equal(PhaseRunning))
	})
})
