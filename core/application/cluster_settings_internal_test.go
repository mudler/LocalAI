package application

import (
	"context"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/clustersettings"
	"github.com/mudler/LocalAI/core/services/testutil"
	"github.com/mudler/LocalAI/pkg/system"
)

// Every frontend of a cluster must run the same memory settings: the cluster
// document, then the built-in default. Neither the runtime_settings.json of a
// frontend nor its env may make one frontend differ from another.
var _ = Describe("Cluster memory settings on several frontends", func() {
	var (
		ctx   context.Context
		bus   *testutil.FakeBus
		store *clustersettings.DBStore
	)

	BeforeEach(func() {
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(context.Background())
		DeferCleanup(cancel)
		bus = testutil.NewFakeBus()
		db, err := gorm.Open(sqlite.Open(filepath.Join(GinkgoT().TempDir(), "cluster.db")), &gorm.Config{})
		Expect(err).ToNot(HaveOccurred())
		store, err = clustersettings.NewDBStore(db)
		Expect(err).ToNot(HaveOccurred())
	})

	// newFrontend starts a frontend whose runtime_settings.json holds file and
	// whose env sets env.
	newFrontend := func(file string, env ...config.AppOption) *Application {
		dir := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(dir, "runtime_settings.json"), []byte(file), 0o600)).To(Succeed())
		st, err := system.GetSystemState(
			system.WithModelPath(filepath.Join(dir, "models")),
			system.WithBackendPath(filepath.Join(dir, "backends")),
		)
		Expect(err).ToNot(HaveOccurred())
		opts := append([]config.AppOption{
			config.WithContext(ctx), config.WithSystemState(st), config.WithDynamicConfigDir(dir),
		}, env...)
		app, err := New(opts...)
		Expect(err).ToNot(HaveOccurred())
		Expect(app.StartClusterSettings(ctx, bus, store)).To(Succeed())
		DeferCleanup(func() { _ = app.ClusterSettings().Close() })
		return app
	}

	It("gives the same effective policy on frontends with different files and env", func() {
		a := newFrontend(`{"size_aware_eviction": true, "max_active_backends": 7}`,
			config.SetMaxActiveBackends(2))
		b := newFrontend(`{"force_eviction_when_busy": true, "lru_eviction_max_retries": 3}`,
			config.SetMaxActiveBackends(5), config.DisableModelSwap)

		pa, pb := a.ClusterMemoryPolicy(), b.ClusterMemoryPolicy()

		Expect(pa).To(Equal(pb))
		Expect(pa.MaxActiveBackends).To(Equal(2), "the first frontend seeded the limit")
		Expect(pa.ModelSwap).To(BeFalse(), "a key the document lacked comes from the env of a later frontend")
		Expect(pa.SizeAwareEviction).To(BeFalse(), "a frontend's file does not apply in distributed mode")
		Expect(pa.ForceEvictionWhenBusy).To(BeFalse(), "a frontend's file does not apply in distributed mode")
		Expect(pa.LRUMaxRetries).To(Equal(config.DefaultRuntimeBaseline().LRUEvictionMaxRetries))
	})

	It("resolves a key the document lacks to the default, not to a local value", func() {
		a := newFrontend(`{}`, config.SetMaxActiveBackends(2))
		b := newFrontend(`{}`)
		// A local value that is neither in the document nor in the env of
		// the frontend at its start (a stale apply of an old document).
		b.ApplicationConfig().SizeAwareEviction = true

		_, _, err := a.ClusterSettings().UpdateMemory(ctx, config.RuntimeSettings{ModelSwap: new(false)}, nil, "admin")
		Expect(err).ToNot(HaveOccurred())

		Expect(b.ClusterMemoryPolicy()).To(Equal(a.ClusterMemoryPolicy()))
		Expect(b.ApplicationConfig().SizeAwareEviction).To(BeFalse())
	})
})

var _ = Describe("Startup settings in distributed mode", func() {
	It("does not take the memory settings from runtime_settings.json", func() {
		dir := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(dir, "runtime_settings.json"),
			[]byte(`{"max_active_backends": 4, "model_swap": false, "threads": 3}`), 0o600)).To(Succeed())
		opts := config.NewApplicationConfig(config.WithDynamicConfigDir(dir))
		opts.Distributed.Enabled = true

		loadRuntimeSettingsFromFile(opts)

		Expect(opts.MaxActiveBackends).To(Equal(0))
		Expect(opts.ModelSwapEnabled()).To(BeTrue())
		Expect(opts.Threads).To(Equal(3), "the other settings still load from the file")
	})
})
