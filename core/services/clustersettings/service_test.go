package clustersettings_test

import (
	"context"
	"errors"
	"path/filepath"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/clustersettings"
	"github.com/mudler/LocalAI/core/services/testutil"
)

// frontend is one frontend replica: its own config and its own service, on
// the shared bus and database.
type frontend struct {
	mu  sync.Mutex
	cfg *config.ApplicationConfig
	svc *clustersettings.Service
}

func (f *frontend) modelSwap() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cfg.ModelSwapEnabled()
}

func (f *frontend) maxActive() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cfg.MaxActiveBackends
}

func boolp(b bool) *bool { return &b }
func intp(i int) *int    { return &i }

var _ = Describe("Cluster memory settings", func() {
	var (
		ctx   context.Context
		bus   *testutil.FakeBus
		db    *gorm.DB
		store *clustersettings.DBStore
	)

	start := func(env ...config.AppOption) *frontend {
		f := &frontend{cfg: config.NewApplicationConfig(append([]config.AppOption{config.WithMemoryReclaimer(false, 0.95)}, env...)...)}
		svc, err := clustersettings.New(ctx, clustersettings.Options{
			Bus:   bus,
			Store: store,
			Apply: func(rs config.RuntimeSettings) {
				f.mu.Lock()
				defer f.mu.Unlock()
				f.cfg.ApplyRuntimeSettings(&rs)
			},
		})
		Expect(err).ToNot(HaveOccurred())
		_, err = svc.Seed(ctx, config.EnvMemorySettings(f.cfg), "test")
		Expect(err).ToNot(HaveOccurred())
		f.svc = svc
		DeferCleanup(svc.Close)
		return f
	}

	BeforeEach(func() {
		ctx = context.Background()
		bus = testutil.NewFakeBus()
		var err error
		db, err = gorm.Open(sqlite.Open(filepath.Join(GinkgoT().TempDir(), "cluster.db")), &gorm.Config{})
		Expect(err).ToNot(HaveOccurred())
		store, err = clustersettings.NewDBStore(db)
		Expect(err).ToNot(HaveOccurred())
	})

	It("applies a value saved on frontend A to frontend B", func() {
		a := start()
		b := start()

		_, rev, err := a.svc.UpdateMemory(ctx, config.RuntimeSettings{ModelSwap: boolp(false)}, nil, "admin")
		Expect(err).ToNot(HaveOccurred())
		Expect(rev).To(BeNumerically(">", 0))

		Expect(a.modelSwap()).To(BeFalse())
		Expect(b.modelSwap()).To(BeFalse())
		mem, brev := b.svc.Memory()
		Expect(*mem.ModelSwap).To(BeFalse())
		Expect(brev).To(Equal(rev))
	})

	It("keeps the value across a restart of every frontend", func() {
		a := start()
		_, _, err := a.svc.UpdateMemory(ctx, config.RuntimeSettings{MaxActiveBackends: intp(3)}, nil, "admin")
		Expect(err).ToNot(HaveOccurred())
		Expect(a.svc.Close()).To(Succeed())

		bus = testutil.NewFakeBus()
		c := start()

		Expect(c.maxActive()).To(Equal(3))
	})

	It("seeds an empty store from env and then lets the store win over env", func() {
		a := start(config.SetMaxActiveBackends(2))
		Expect(a.maxActive()).To(Equal(2))

		b := start(config.SetMaxActiveBackends(5))

		Expect(b.maxActive()).To(Equal(2), "the stored value wins over the env of a later frontend")
		mem, _ := b.svc.Memory()
		Expect(*mem.MaxActiveBackends).To(Equal(2))
	})

	It("adds the env keys a partial document lacks, and the first writer of a key wins", func() {
		a := start(config.SetMaxActiveBackends(2))
		_, rev := a.svc.Memory()

		b := start(config.SetMaxActiveBackends(5), config.DisableModelSwap)

		mem, brev := b.svc.Memory()
		Expect(brev).To(BeNumerically(">", rev), "b stored the key the document lacked")
		Expect(*mem.MaxActiveBackends).To(Equal(2), "a wrote the limit first")
		Expect(*mem.ModelSwap).To(BeFalse())
		Expect(a.modelSwap()).To(BeFalse(), "a applies the merged document")
	})

	It("stores nothing when the env adds no key", func() {
		a := start(config.SetMaxActiveBackends(2))
		_, rev := a.svc.Memory()

		b := start(config.SetMaxActiveBackends(3))

		_, brev := b.svc.Memory()
		Expect(brev).To(Equal(rev))
	})

	It("merges concurrent partial updates without losing one", func() {
		a := start()
		b := start()

		var wg sync.WaitGroup
		for i := range 10 {
			wg.Add(2)
			go func() {
				defer wg.Done()
				defer GinkgoRecover()
				_, _, err := a.svc.UpdateMemory(ctx, config.RuntimeSettings{ModelSwap: boolp(i%2 == 0)}, nil, "a")
				Expect(err).ToNot(HaveOccurred())
			}()
			go func() {
				defer wg.Done()
				defer GinkgoRecover()
				_, _, err := b.svc.UpdateMemory(ctx, config.RuntimeSettings{MaxActiveBackends: intp(i + 1)}, nil, "b")
				Expect(err).ToNot(HaveOccurred())
			}()
		}
		wg.Wait()

		mem, rev := a.svc.Memory()
		Expect(mem.ModelSwap).ToNot(BeNil())
		Expect(mem.MaxActiveBackends).ToNot(BeNil())
		Expect(rev).To(BeNumerically(">=", 20))
	})

	It("refuses an update made against a stale revision", func() {
		a := start()
		b := start()
		_, rev, err := a.svc.UpdateMemory(ctx, config.RuntimeSettings{ModelSwap: boolp(false)}, nil, "a")
		Expect(err).ToNot(HaveOccurred())
		_, _, err = a.svc.UpdateMemory(ctx, config.RuntimeSettings{ModelSwap: boolp(true)}, &rev, "a")
		Expect(err).ToNot(HaveOccurred())

		_, _, err = b.svc.UpdateMemory(ctx, config.RuntimeSettings{MaxActiveBackends: intp(4)}, &rev, "b")

		Expect(errors.Is(err, clustersettings.ErrStaleRevision)).To(BeTrue(), "got %v", err)
		Expect(b.maxActive()).To(Equal(0))
	})

	It("re-applies the store on resync after a missed delta", func() {
		a := start()
		b := start()
		// A change that reaches the store but not the bus (a missed delta).
		_, _, err := a.svc.UpdateMemory(ctx, config.RuntimeSettings{ModelSwap: boolp(false)}, nil, "a")
		Expect(err).ToNot(HaveOccurred())
		quiet := testutil.NewFakeBus()
		lone, err := clustersettings.New(ctx, clustersettings.Options{Bus: quiet, Store: store, Apply: func(config.RuntimeSettings) {}})
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(lone.Close)
		_, _, err = lone.UpdateMemory(ctx, config.RuntimeSettings{MaxActiveBackends: intp(7)}, nil, "lone")
		Expect(err).ToNot(HaveOccurred())
		Expect(b.maxActive()).To(Equal(0), "b missed the delta")

		Expect(b.svc.Resync(ctx)).To(Succeed())

		Expect(b.maxActive()).To(Equal(7))
		Expect(b.modelSwap()).To(BeFalse())
	})

	It("works alone with no bus and no store", func() {
		var applied config.RuntimeSettings
		svc, err := clustersettings.New(ctx, clustersettings.Options{Apply: func(rs config.RuntimeSettings) { applied = rs }})
		Expect(err).ToNot(HaveOccurred())
		defer svc.Close()

		_, _, err = svc.UpdateMemory(ctx, config.RuntimeSettings{ModelSwap: boolp(false)}, nil, "a")

		Expect(err).ToNot(HaveOccurred())
		Expect(*applied.ModelSwap).To(BeFalse())
	})
})
