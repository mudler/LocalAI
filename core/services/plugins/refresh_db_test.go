//go:build auth

package plugins_test

import (
	"context"
	"path/filepath"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/advisorylock"
	"github.com/mudler/LocalAI/core/services/plugins"
	"github.com/mudler/LocalAI/core/services/plugins/content"
	"github.com/mudler/LocalAI/core/services/plugins/store"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/jsonstore"
)

func openSQLite(path string) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(path+"?_busy_timeout=5000&_txlock=immediate"), &gorm.Config{})
	Expect(err).NotTo(HaveOccurred())
	sqlDB, err := db.DB()
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = sqlDB.Close() })
	return db
}

func newWorkspace(now func() time.Time) content.Workspace {
	blobs, err := blob.NewFS(filepath.Join(GinkgoT().TempDir(), "blobs"))
	Expect(err).NotTo(HaveOccurred())
	ws, err := jsonstore.New("", blobs, jsonstore.Options{Admin: "admin", Now: now})
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = ws.Close() })
	return content.Workspace{Files: ws, Actor: workspace.Actor{Principal: "admin"}}
}

var _ = Describe("Refresh with two frontends and one database", func() {
	var (
		ctx  context.Context
		ff   *fakeFetcher
		clk  *testClock
		a, b *plugins.Service
		db   *gorm.DB
	)
	BeforeEach(func() {
		ctx = context.Background()
		db = openSQLite(filepath.Join(GinkgoT().TempDir(), "plugins.db"))
		ff = newFakeFetcher()
		clk = &testClock{now: time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC)}
		ws := newWorkspace(clk.Now)
		frontend := func() *plugins.Service {
			// Each frontend opens its own store over the shared database.
			st, err := store.NewGormStore(db)
			Expect(err).NotTo(HaveOccurred())
			s, err := plugins.New(plugins.Options{Store: st, DB: db, Fetcher: ff, Now: clk.Now, Workspace: ws, Root: "/plugins"})
			Expect(err).NotTo(HaveOccurred())
			return s
		}
		a, b = frontend(), frontend()
		ff.setMarketplace(curated, marketplaceJSON("localai"), "", nil)
		Expect(a.Seed(ctx)).To(Succeed())
		Expect(b.Seed(ctx)).To(Succeed())
	})

	It("seeds once and shows the same marketplaces on both", func() {
		la, err := a.ListMarketplaces(ctx)
		Expect(err).NotTo(HaveOccurred())
		lb, err := b.ListMarketplaces(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(la).To(HaveLen(1))
		Expect(lb).To(Equal(la))
	})

	It("refreshes once per tick when both tick at the same time", func() {
		ff.delay = 50 * time.Millisecond
		var wg sync.WaitGroup
		results := make([]plugins.TickResult, 2)
		for i, s := range []*plugins.Service{a, b} {
			wg.Add(1)
			go func() {
				defer GinkgoRecover()
				defer wg.Done()
				var err error
				results[i], err = s.RefreshTick(ctx)
				Expect(err).NotTo(HaveOccurred())
			}()
		}
		wg.Wait()
		Expect(ff.count(curated)).To(Equal(1))
		Expect(len(results[0].Refreshed) + len(results[1].Refreshed)).To(Equal(1))
	})

	It("refreshes once per tick when they tick one after the other", func() {
		_, err := a.RefreshTick(ctx)
		Expect(err).NotTo(HaveOccurred())
		res, err := b.RefreshTick(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Ran).To(BeTrue())
		Expect(res.Refreshed).To(BeEmpty())
		Expect(ff.count(curated)).To(Equal(1))

		clk.Advance(plugins.DefaultRefreshInterval)
		res, err = b.RefreshTick(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Refreshed).To(Equal([]string{"localai"}))
		Expect(ff.count(curated)).To(Equal(2))
	})

	It("skips the tick while another frontend holds the refresh lock", func() {
		_, err := advisorylock.TryWithLockCtx(ctx, db, advisorylock.KeyPluginsRefresh, func() error {
			res, err := b.RefreshTick(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(res.Ran).To(BeFalse())
			return nil
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(ff.count(curated)).To(Equal(0))
	})

	It("shows a refresh through one frontend on the other", func() {
		_, err := a.RefreshMarketplace(ctx, admin, "localai")
		Expect(err).NotTo(HaveOccurred())
		m, err := b.Marketplace(ctx, "localai")
		Expect(err).NotTo(HaveOccurred())
		Expect(m.Name).To(Equal("localai"))
		Expect(m.RefreshedAt).NotTo(BeZero())
	})
})
