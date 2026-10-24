package galleryop_test

import (
	"context"
	"path/filepath"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/services/distributed"
	"github.com/mudler/LocalAI/core/services/galleryop"
)

// gatedBackendManager parks every operation until its backend is released. It
// records how many operations ran at once, and per backend, so a spec can tell
// a pool that really runs in parallel from one that only looks like it.
type gatedBackendManager struct {
	mu         sync.Mutex
	gates      map[string]chan struct{}
	started    []string
	running    int
	maxRunning int
	perName    map[string]int
	overlapped bool
}

func newGatedBackendManager() *gatedBackendManager {
	return &gatedBackendManager{gates: map[string]chan struct{}{}, perName: map[string]int{}}
}

func (m *gatedBackendManager) gate(name string) chan struct{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	g, ok := m.gates[name]
	if !ok {
		g = make(chan struct{})
		m.gates[name] = g
	}
	return g
}

// Release lets every operation on name finish.
func (m *gatedBackendManager) Release(name string) { close(m.gate(name)) }

func (m *gatedBackendManager) Started() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.started...)
}

func (m *gatedBackendManager) MaxRunning() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.maxRunning
}

func (m *gatedBackendManager) Overlapped() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.overlapped
}

func (m *gatedBackendManager) run(name string) {
	g := m.gate(name)
	m.mu.Lock()
	m.started = append(m.started, name)
	m.running++
	m.perName[name]++
	if m.perName[name] > 1 {
		m.overlapped = true
	}
	if m.running > m.maxRunning {
		m.maxRunning = m.running
	}
	m.mu.Unlock()

	<-g

	m.mu.Lock()
	m.running--
	m.perName[name]--
	m.mu.Unlock()
}

func (m *gatedBackendManager) InstallBackend(_ context.Context, op *galleryop.ManagementOp[gallery.GalleryBackend, any], _ galleryop.ProgressCallback) error {
	m.run(op.GalleryElementName)
	return nil
}

func (m *gatedBackendManager) UpgradeBackend(_ context.Context, op *galleryop.ManagementOp[gallery.GalleryBackend, any], _ galleryop.ProgressCallback) error {
	m.run(op.GalleryElementName)
	return nil
}

func (m *gatedBackendManager) DeleteBackend(name string) error {
	m.run(name)
	return nil
}

func (*gatedBackendManager) ListBackends() (gallery.SystemBackends, error) {
	return gallery.SystemBackends{}, nil
}

func (*gatedBackendManager) CheckUpgrades(context.Context) (map[string]gallery.UpgradeInfo, error) {
	return nil, nil
}

func (*gatedBackendManager) IsDistributed() bool { return false }

func (*gatedBackendManager) BackendAvailable(string) (bool, error) { return false, nil }

var _ = Describe("concurrent backend operations", func() {
	var (
		mgr *gatedBackendManager
		svc *galleryop.GalleryService
		cfg *config.ApplicationConfig
		ctx context.Context
	)

	start := func(limit int) {
		cfg = config.NewApplicationConfig(config.WithMaxConcurrentBackendOperations(limit))
		mgr = newGatedBackendManager()
		svc = galleryop.NewGalleryService(cfg, nil)
		svc.SetBackendManager(mgr)
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(context.Background())
		DeferCleanup(cancel)
		Expect(svc.Start(ctx, nil, nil)).To(Succeed())
	}

	enqueue := func(id, name string, opts ...func(*galleryop.ManagementOp[gallery.GalleryBackend, any])) {
		op := galleryop.ManagementOp[gallery.GalleryBackend, any]{ID: id, GalleryElementName: name}
		for _, o := range opts {
			o(&op)
		}
		svc.EnqueueBackendOp(op)
	}

	processed := func(id string) func() bool {
		return func() bool {
			st := svc.GetStatus(id)
			return st != nil && st.Processed
		}
	}

	It("runs one operation at a time by default", func() {
		start(0)
		enqueue("a", "alpha")
		enqueue("b", "beta")

		Eventually(mgr.Started, "5s", "10ms").Should(HaveLen(1))
		Consistently(mgr.Started, "300ms", "10ms").Should(HaveLen(1))

		mgr.Release("alpha")
		mgr.Release("beta")
		Eventually(processed("a"), "5s", "10ms").Should(BeTrue())
		Eventually(processed("b"), "5s", "10ms").Should(BeTrue())
		Expect(mgr.MaxRunning()).To(Equal(1))
	})

	It("reaches the configured number of parallel operations and no more", func() {
		start(3)
		for _, n := range []string{"alpha", "beta", "gamma", "delta", "epsilon"} {
			enqueue("op-"+n, n)
		}

		Eventually(mgr.Started, "5s", "10ms").Should(HaveLen(3))
		Consistently(mgr.Started, "300ms", "10ms").Should(HaveLen(3))
		Expect(mgr.MaxRunning()).To(Equal(3))

		for _, n := range []string{"alpha", "beta", "gamma", "delta", "epsilon"} {
			mgr.Release(n)
		}
		for _, n := range []string{"alpha", "beta", "gamma", "delta", "epsilon"} {
			Eventually(processed("op-"+n), "5s", "10ms").Should(BeTrue())
		}
		Expect(mgr.MaxRunning()).To(Equal(3))
	})

	It("keeps operations on one backend serialized, whatever their kind", func() {
		start(4)
		enqueue("install", "alpha")
		enqueue("upgrade", "alpha", func(o *galleryop.ManagementOp[gallery.GalleryBackend, any]) { o.Upgrade = true })
		enqueue("delete", "gallery@alpha", func(o *galleryop.ManagementOp[gallery.GalleryBackend, any]) { o.Delete = true })

		Eventually(mgr.Started, "5s", "10ms").Should(HaveLen(1))
		Consistently(mgr.Started, "300ms", "10ms").Should(HaveLen(1))
		// The ones that wait are still queued, not failed.
		Eventually(func() int {
			queued := 0
			for _, id := range []string{"install", "upgrade", "delete"} {
				if svc.GetStatus(id).IsQueued() {
					queued++
				}
			}
			return queued
		}, "2s", "10ms").Should(Equal(2))

		mgr.Release("alpha")
		mgr.Release("gallery@alpha") // DeleteBackend receives the name as given
		for _, id := range []string{"install", "upgrade", "delete"} {
			Eventually(processed(id), "5s", "10ms").Should(BeTrue())
		}
		Expect(mgr.Overlapped()).To(BeFalse())
		Expect(mgr.Started()).To(HaveLen(3))
	})

	It("lets an independent operation pass a slow one", func() {
		start(2)
		enqueue("slow", "alpha")
		Eventually(mgr.Started, "5s", "10ms").Should(HaveLen(1))
		enqueue("fast", "beta")
		Eventually(mgr.Started, "5s", "10ms").Should(HaveLen(2))

		mgr.Release("beta")
		Eventually(processed("fast"), "5s", "10ms").Should(BeTrue())
		Expect(processed("slow")()).To(BeFalse())

		mgr.Release("alpha")
		Eventually(processed("slow"), "5s", "10ms").Should(BeTrue())
	})

	It("does not let a same-backend op that waits block an independent one", func() {
		start(2)
		enqueue("first", "alpha")
		Eventually(mgr.Started, "5s", "10ms").Should(HaveLen(1))
		enqueue("second", "alpha")
		enqueue("third", "beta")

		Eventually(mgr.Started, "5s", "10ms").Should(ConsistOf("alpha", "beta"))
		mgr.Release("alpha")
		mgr.Release("beta")
		for _, id := range []string{"first", "second", "third"} {
			Eventually(processed(id), "5s", "10ms").Should(BeTrue())
		}
	})

	It("does not hold model operations behind a slow backend operation", func() {
		start(1)
		models := newGatedModelManager()
		svc.SetModelManager(models)
		close(models.gate)

		enqueue("slow", "alpha")
		Eventually(mgr.Started, "5s", "10ms").Should(HaveLen(1))

		svc.EnqueueModelOp(galleryop.ManagementOp[gallery.GalleryModel, gallery.ModelConfig]{ID: "m", GalleryElementName: "some-model"})
		Eventually(processed("m"), "5s", "10ms").Should(BeTrue())
		Expect(processed("slow")()).To(BeFalse())

		mgr.Release("alpha")
	})

	It("cancels a queued operation without running it", func() {
		start(1)
		enqueue("running", "alpha")
		Eventually(mgr.Started, "5s", "10ms").Should(HaveLen(1))

		cctx, cancel, pause := galleryop.NewUserCancellableContext(context.Background())
		enqueue("queued", "beta", func(o *galleryop.ManagementOp[gallery.GalleryBackend, any]) {
			o.Context, o.CancelFunc, o.PauseFunc = cctx, cancel, pause
		})
		svc.StoreCancellationActions("queued", cancel, pause)
		Eventually(func() bool { return svc.GetStatus("queued").IsQueued() }, "2s", "10ms").Should(BeTrue())

		// A second queued op for the same name goes through the service API,
		// with a context the service creates.
		enqueue("queued-by-id", "gamma")
		Eventually(func() bool { return svc.GetStatus("queued-by-id").IsQueued() }, "2s", "10ms").Should(BeTrue())

		Expect(svc.CancelOperation("queued")).To(Succeed())
		Eventually(processed("queued"), "5s", "10ms").Should(BeTrue())
		Expect(svc.GetStatus("queued").Cancelled).To(BeTrue())

		// The service stores the cancel func of this one when it takes the op
		// off the channel, which is a moment after admission.
		Eventually(func() error { return svc.CancelOperation("queued-by-id") }, "2s", "10ms").Should(Succeed())
		Eventually(processed("queued-by-id"), "5s", "10ms").Should(BeTrue())

		mgr.Release("alpha")
		mgr.Release("beta")
		mgr.Release("gamma")
		Eventually(processed("running"), "5s", "10ms").Should(BeTrue())
		Consistently(mgr.Started, "300ms", "10ms").Should(Equal([]string{"alpha"}))
	})

	It("follows a raised limit while operations wait", func() {
		start(1)
		enqueue("a", "alpha")
		enqueue("b", "beta")
		Eventually(mgr.Started, "5s", "10ms").Should(HaveLen(1))
		Consistently(mgr.Started, "300ms", "10ms").Should(HaveLen(1))

		applied := 2
		cfg.ApplyRuntimeSettings(&config.RuntimeSettings{MaxConcurrentBackendOperations: &applied})
		Eventually(mgr.Started, "5s", "50ms").Should(HaveLen(2))

		mgr.Release("alpha")
		mgr.Release("beta")
	})

	It("clamps a runtime value below 1 to 1", func() {
		cfg = config.NewApplicationConfig(config.WithMaxConcurrentBackendOperations(4))
		zero := 0
		cfg.ApplyRuntimeSettings(&config.RuntimeSettings{MaxConcurrentBackendOperations: &zero})
		Expect(cfg.MaxConcurrentBackendOperations).To(Equal(1))
	})

	It("persists one row per concurrently running operation", func() {
		start(2)
		db, err := gorm.Open(sqlite.Open(filepath.Join(GinkgoT().TempDir(), "ops.db")+"?_busy_timeout=5000"), &gorm.Config{})
		Expect(err).ToNot(HaveOccurred())
		store, err := distributed.NewGalleryStore(db)
		Expect(err).ToNot(HaveOccurred())
		svc.SetGalleryStore(store)

		enqueue("row-a", "alpha")
		enqueue("row-b", "beta")
		Eventually(mgr.Started, "5s", "10ms").Should(HaveLen(2))
		for _, id := range []string{"row-a", "row-b"} {
			Eventually(func() error { _, err := store.Get(id); return err }, "5s", "20ms").Should(Succeed())
		}

		mgr.Release("alpha")
		mgr.Release("beta")
		for _, id := range []string{"row-a", "row-b"} {
			Eventually(func() string {
				rec, err := store.Get(id)
				if err != nil {
					return ""
				}
				return rec.Status
			}, "5s", "20ms").Should(Equal("completed"))
		}
	})
})
