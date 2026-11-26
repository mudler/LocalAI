package application

import (
	"context"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/distributed"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/core/services/testutil"
	"github.com/mudler/LocalAI/core/services/upgraderun"
	"github.com/mudler/LocalAI/core/services/upgraderun/upgraderuntest"
)

// runApp is the part of an Application the upgrade runs need.
func runApp(ctx context.Context, dataPath, instance string) *Application {
	cfg := &config.ApplicationConfig{Context: ctx, DataPath: dataPath}
	cfg.Distributed.InstanceID = instance
	gs := galleryop.NewGalleryService(cfg, nil)
	return &Application{applicationConfig: cfg, galleryService: gs}
}

var _ = Describe("Upgrade run wiring", func() {
	var ctx context.Context

	BeforeEach(func() {
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(context.Background())
		DeferCleanup(cancel)
	})

	It("starts with a database of its own under the data path on a standalone server, and stops", func() {
		dir := GinkgoT().TempDir()
		a := runApp(ctx, dir, "")
		a.startUpgradeRuns()
		Expect(a.UpgradeRuns()).NotTo(BeNil())
		Expect(a.AddonRunUpgrader()).NotTo(BeNil())
		_, err := os.Stat(filepath.Join(dir, upgradeRunsDBFile))
		Expect(err).NotTo(HaveOccurred())
		runs, err := a.UpgradeRuns().List(ctx, upgraderun.ListOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(runs).To(BeEmpty())
		done := make(chan struct{})
		go func() {
			a.stopUpgradeRuns()
			close(done)
		}()
		Eventually(done, 5*time.Second).Should(BeClosed())
		// A second stop is a no-op, as Shutdown may run twice.
		a.stopUpgradeRuns()
	})

	It("tells its observers the first change of a run it takes over at start", func() {
		dir := GinkgoT().TempDir()
		db, err := upgraderuntest.OpenSQLite(dir)
		Expect(err).NotTo(HaveOccurred())
		store, err := distributed.NewUpgradeRunStore(db)
		Expect(err).NotTo(HaveOccurred())
		// A run that a frontend which went away left running, with an
		// item whose operation has no status any more.
		Expect(store.Create(&distributed.UpgradeRunRecord{ID: "left", Source: "manual", Actor: "admin", State: "running", Runner: "gone", CreatedAt: time.Now()},
			[]distributed.UpgradeRunItemRecord{{Key: "gallery:kokoro", Kind: "gallery", Name: "kokoro", Phase: "running", JobID: "lost-job"}})).To(Succeed())
		a := runApp(ctx, "", "frontend-a")
		a.distributed = &DistributedServices{Broadcaster: testutil.NewFakeBus(), DistStores: &distributed.Stores{UpgradeRuns: store}}
		seen := make(chan upgraderun.Change, 64)
		a.upgradeRunTweak = func(o *upgraderun.Options) {
			o.Jobs = upgraderuntest.NewJobs()
			o.Checkers = []upgraderun.Checker{upgraderuntest.NewChecker(upgraderun.KindGallery)}
			o.Observers = append(o.Observers, upgraderun.ObserverFunc(func(c upgraderun.Change) {
				select {
				case seen <- c:
				default:
				}
			}))
		}
		a.startUpgradeRuns()
		DeferCleanup(a.stopUpgradeRuns)
		var first upgraderun.Change
		Eventually(seen, 5*time.Second).Should(Receive(&first))
		Expect(first.Run.ID).To(Equal("left"))
		Expect(first.Local).To(BeTrue())
		Expect(first.Run.Item("gallery:kokoro").Phase).To(Equal(upgraderun.PhaseUnconfirmed), "the takeover itself is the first change")
	})

	It("publishes the service so that readers on other goroutines never race its start", func() {
		a := runApp(ctx, GinkgoT().TempDir(), "")
		stop := make(chan struct{})
		read := make(chan struct{})
		go func() {
			defer close(read)
			for {
				select {
				case <-stop:
					return
				default:
					_ = a.UpgradeRuns()
				}
			}
		}()
		a.startUpgradeRuns()
		close(stop)
		<-read
		Expect(a.UpgradeRuns()).NotTo(BeNil())
		a.stopUpgradeRuns()
	})

	It("does not start without a database or a data path, and the routes fall back", func() {
		a := runApp(ctx, "", "")
		a.startUpgradeRuns()
		Expect(a.UpgradeRuns()).To(BeNil())
		Expect(a.AddonRunUpgrader()).NotTo(BeNil())
		a.stopUpgradeRuns()
	})

	It("keeps a run cluster-wide on two frontends with the shared store and bus", func() {
		db, err := upgraderuntest.OpenSQLite(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		store, err := distributed.NewUpgradeRunStore(db)
		Expect(err).NotTo(HaveOccurred())
		bus := testutil.NewFakeBus()
		jobs := upgraderuntest.NewJobs()
		starter := upgraderuntest.NewStarter(jobs)
		backends := upgraderuntest.NewChecker(upgraderun.KindGallery)
		backends.Add(upgraderun.Candidate{Kind: upgraderun.KindGallery, Name: "kokoro", To: "v2"})
		frontend := func(instance string) *Application {
			a := runApp(ctx, "", instance)
			a.distributed = &DistributedServices{Broadcaster: bus, DistStores: &distributed.Stores{UpgradeRuns: store}}
			a.upgradeRunTweak = func(o *upgraderun.Options) {
				Expect(o.Bus).To(BeIdenticalTo(bus))
				Expect(o.Store).To(BeIdenticalTo(store))
				o.Jobs = jobs
				o.Starters = map[upgraderun.Kind]upgraderun.Starter{upgraderun.KindGallery: starter}
				o.Checkers = []upgraderun.Checker{backends}
				o.PollInterval, o.SweepInterval = 10*time.Millisecond, 20*time.Millisecond
				// The fake status table keeps A's last status after A stops,
				// as the other frontends keep it when A dies.
				o.RecoveredIdleAfter = 200 * time.Millisecond
			}
			a.startUpgradeRuns()
			Expect(a.UpgradeRuns()).NotTo(BeNil())
			return a
		}
		a, b := frontend("frontend-a"), frontend("frontend-b")
		DeferCleanup(b.stopUpgradeRuns)

		run, err := a.UpgradeRuns().Start(ctx, "admin", upgraderun.StartRequest{Items: []upgraderun.StartItem{{Kind: upgraderun.KindGallery, Name: "kokoro"}}})
		Expect(err).NotTo(HaveOccurred())
		got, err := b.UpgradeRuns().Get(ctx, run.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.ID).To(Equal(run.ID))
		_, err = b.UpgradeRuns().Start(ctx, "admin", upgraderun.StartRequest{Items: []upgraderun.StartItem{{Kind: upgraderun.KindGallery, Name: "kokoro"}}})
		Expect(upgraderun.CodeOf(err)).To(Equal(upgraderun.CodeRunActive))
		Eventually(a.UpgradeRuns().Local).Should(ConsistOf(run.ID))
		Expect(b.UpgradeRuns().Local()).To(BeEmpty())

		// Frontend A stops while the backend item runs, and the update had
		// applied: B's sweep takes the run over, follows the operation, and
		// once its status shows no change its check confirms it.
		Eventually(func() string { return starter.Job("gallery:kokoro") }).ShouldNot(BeEmpty())
		backends.Remove("gallery:kokoro")
		a.stopUpgradeRuns()
		Eventually(func() upgraderun.Phase {
			r, err := b.UpgradeRuns().Get(ctx, run.ID)
			Expect(err).NotTo(HaveOccurred())
			return r.Item("gallery:kokoro").Phase
		}, 5*time.Second, 20*time.Millisecond).Should(Equal(upgraderun.PhaseUpdated))
	})
})
