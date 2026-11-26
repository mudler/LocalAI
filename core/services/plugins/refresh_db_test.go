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

var _ = Describe("Installs with two frontends and one database", func() {
	It("gives one install for two concurrent installs, and shows it in the other frontend's turn view", func() {
		ctx := context.Background()
		db := openSQLite(filepath.Join(GinkgoT().TempDir(), "plugins.db"))
		ff := newFakeFetcher()
		repo := &pluginRepo{versions: map[string]map[string]string{shaA: pluginFiles(textPlugin, "v1")}}
		ff.plugins["fmt"] = repo.fetch
		ff.refs["https://github.com/o/fmt.git#"] = shaA
		ff.setMarketplace("owner/team", marketplaceJSON("team", githubEntry("fmt", "o/fmt", "")), shaA, nil)
		clk := &testClock{now: time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC)}
		ws := newWorkspace(clk.Now)
		frontend := func() (*plugins.Service, *recordQueue) {
			st, err := store.NewGormStore(db)
			Expect(err).NotTo(HaveOccurred())
			q := &recordQueue{errs: map[string]error{}}
			s, err := plugins.New(plugins.Options{Store: st, DB: db, Fetcher: ff, Queue: q, Now: clk.Now, Workspace: ws, Root: "/plugins"})
			Expect(err).NotTo(HaveOccurred())
			return s, q
		}
		a, qa := frontend()
		b, qb := frontend()
		_, err := a.AddMarketplace(ctx, admin, plugins.AddMarketplaceRequest{Source: "owner/team"})
		Expect(err).NotTo(HaveOccurred())

		pa, err := a.Inspect(ctx, admin, "fmt@team")
		Expect(err).NotTo(HaveOccurred())
		pb, err := b.Inspect(ctx, admin, "fmt@team")
		Expect(err).NotTo(HaveOccurred())

		var wg sync.WaitGroup
		jobs := make([]string, 2)
		for i, c := range []struct {
			s   *plugins.Service
			tok string
		}{{a, pa.Token}, {b, pb.Token}} {
			wg.Add(1)
			go func() {
				defer GinkgoRecover()
				defer wg.Done()
				// Both pass the early check; the lock and the transaction
				// decide.
				jobs[i], _ = c.s.Install(ctx, admin, c.tok, store.ScopeInstance)
			}()
		}
		wg.Wait()
		var failed int
		for i, q := range []*recordQueue{qa, qb} {
			if jobs[i] == "" || q.err(jobs[i]) != nil {
				failed++
			}
		}
		Expect(failed).To(Equal(1))
		st, err := store.NewGormStore(db)
		Expect(err).NotTo(HaveOccurred())
		list, err := st.Installs(ctx, store.InstallFilter{})
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(1))

		Expect(st.SaveUserState(ctx, store.UserState{Principal: user.ID, InstallID: list[0].ID, Enabled: true})).To(Succeed())
		for _, s := range []*plugins.Service{a, b} {
			view, err := s.TurnView(ctx, user.ID, plugins.ChatRef{})
			Expect(err).NotTo(HaveOccurred())
			Expect(view).To(HaveLen(1))
			Expect(view[0].PluginID).To(Equal("fmt@team"))
		}
	})
})
