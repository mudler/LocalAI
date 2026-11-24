package upgraderun_test

import (
	"context"
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/distributed"
	. "github.com/mudler/LocalAI/core/services/upgraderun"
	"github.com/mudler/LocalAI/core/services/upgraderun/upgraderuntest"
)

var _ = Describe("Retention of finished runs", func() {
	var store *distributed.UpgradeRunStore

	BeforeEach(func() {
		db, err := upgraderuntest.OpenSQLite(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		store, err = distributed.NewUpgradeRunStore(db)
		Expect(err).NotTo(HaveOccurred())
	})

	add := func(id, state string, at time.Time) {
		rec := &distributed.UpgradeRunRecord{ID: id, Source: "manual", State: state, CreatedAt: at}
		if state != "running" {
			rec.FinishedAt = &at
		}
		Expect(store.Create(rec, []distributed.UpgradeRunItemRecord{{Key: "gallery:a", Kind: "gallery", Name: "a", Phase: "failed"}})).To(Succeed())
	}
	exists := func(id string) bool {
		_, _, err := store.Get(id)
		return !errors.Is(err, distributed.ErrUpgradeRunNotFound)
	}

	It("prunes by age and by count and leaves a running run", func() {
		now := time.Now()
		add("ancient", "finished", now.Add(-60*24*time.Hour))
		add("old-running", "running", now.Add(-60*24*time.Hour))
		add("a", "finished", now.Add(-3*time.Hour))
		add("b", "cancelled", now.Add(-2*time.Hour))
		add("c", "finished", now.Add(-time.Hour))
		svc, err := New(Options{Store: store, Jobs: upgraderuntest.NewJobs(), Instance: "t",
			Retention: &Retention{MaxAge: 30 * 24 * time.Hour, Keep: 2}})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(svc.Close)

		n, err := svc.Prune(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(Equal(int64(2)))
		Expect(exists("ancient")).To(BeFalse())
		Expect(exists("a")).To(BeFalse())
		Expect(exists("b")).To(BeTrue())
		Expect(exists("c")).To(BeTrue())
		Expect(exists("old-running")).To(BeTrue())
	})

	It("keeps 30 days and 200 runs by default", func() {
		Expect(DefaultRetention).To(Equal(Retention{MaxAge: 30 * 24 * time.Hour, Keep: 200}))
		now := time.Now()
		add("ancient", "finished", now.Add(-31*24*time.Hour))
		add("recent", "finished", now.Add(-29*24*time.Hour))
		svc, err := New(Options{Store: store, Jobs: upgraderuntest.NewJobs(), Instance: "t"})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(svc.Close)
		_, err = svc.Prune(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(exists("ancient")).To(BeFalse())
		Expect(exists("recent")).To(BeTrue())
	})

	It("prunes from the sweep loop", func() {
		add("ancient", "finished", time.Now().Add(-60*24*time.Hour))
		svc, err := New(Options{Store: store, Jobs: upgraderuntest.NewJobs(), Instance: "t",
			SweepInterval: 10 * time.Millisecond, PruneInterval: 10 * time.Millisecond})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(svc.Close)
		ctx, stop := context.WithCancel(context.Background())
		DeferCleanup(stop)
		go svc.Run(ctx)
		Eventually(func() bool { return exists("ancient") }, 5*time.Second, 10*time.Millisecond).Should(BeFalse())
	})
})
