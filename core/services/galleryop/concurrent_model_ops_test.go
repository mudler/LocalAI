package galleryop_test

import (
	"context"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/galleryop"
)

// gatedUpgrader parks every model upgrade until it is released or its context
// ends. It records how many ran at once and whether one model ran twice at once.
type gatedUpgrader struct {
	mu         sync.Mutex
	gates      map[string]chan struct{}
	started    []string
	running    int
	maxRunning int
	perName    map[string]int
	overlapped bool
	cancelled  []string
}

func newGatedUpgrader() *gatedUpgrader {
	return &gatedUpgrader{gates: map[string]chan struct{}{}, perName: map[string]int{}}
}

func (u *gatedUpgrader) gate(name string) chan struct{} {
	u.mu.Lock()
	defer u.mu.Unlock()
	g, ok := u.gates[name]
	if !ok {
		g = make(chan struct{})
		u.gates[name] = g
	}
	return g
}

func (u *gatedUpgrader) Release(name string) { close(u.gate(name)) }

func (u *gatedUpgrader) Started() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]string(nil), u.started...)
}

func (u *gatedUpgrader) MaxRunning() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.maxRunning
}

func (u *gatedUpgrader) Overlapped() bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.overlapped
}

func (u *gatedUpgrader) Cancelled() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]string(nil), u.cancelled...)
}

func (u *gatedUpgrader) UpgradeModel(ctx context.Context, _, name string, _ galleryop.ModelUpgradeOptions, _ func(*galleryop.OpStatus)) error {
	g := u.gate(name)
	u.mu.Lock()
	u.started = append(u.started, name)
	u.running++
	u.perName[name]++
	if u.perName[name] > 1 {
		u.overlapped = true
	}
	if u.running > u.maxRunning {
		u.maxRunning = u.running
	}
	u.mu.Unlock()

	var err error
	select {
	case <-g:
	case <-ctx.Done():
		err = context.Cause(ctx)
		u.mu.Lock()
		u.cancelled = append(u.cancelled, name)
		u.mu.Unlock()
	}

	u.mu.Lock()
	u.running--
	u.perName[name]--
	u.mu.Unlock()
	return err
}

var _ = Describe("concurrent model operations", func() {
	var (
		up  *gatedUpgrader
		svc *galleryop.GalleryService
	)

	start := func(limit int) {
		cfg := config.NewApplicationConfig(config.WithMaxConcurrentModelOperations(limit))
		up = newGatedUpgrader()
		svc = galleryop.NewGalleryService(cfg, nil)
		svc.SetModelUpgrader(up)
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		Expect(svc.Start(ctx, nil, nil)).To(Succeed())
	}

	processed := func(id string) func() bool {
		return func() bool {
			st := svc.GetStatus(id)
			return st != nil && st.Processed
		}
	}

	names := []string{"alpha", "beta", "gamma", "delta", "epsilon"}

	It("returns from every enqueue at once and reports the jobs as queued", func() {
		start(1)
		ids := make([]string, 0, len(names))
		for _, n := range names {
			id, err := svc.EnqueueModelUpgrade(n, galleryop.ModelUpgradeOptions{})
			Expect(err).ToNot(HaveOccurred())
			ids = append(ids, id)
			Expect(svc.GetStatus(id)).ToNot(BeNil())
		}
		Eventually(up.Started, "5s", "10ms").Should(HaveLen(1))
		queued := 0
		for _, id := range ids {
			if svc.GetStatus(id).IsQueued() {
				queued++
			}
		}
		Expect(queued).To(Equal(len(names) - 1))
		for _, n := range names {
			up.Release(n)
		}
	})

	It("runs upgrades of different models in parallel up to the limit and no more", func() {
		start(3)
		ids := map[string]string{}
		for _, n := range names {
			id, err := svc.EnqueueModelUpgrade(n, galleryop.ModelUpgradeOptions{})
			Expect(err).ToNot(HaveOccurred())
			ids[n] = id
		}
		Eventually(up.Started, "5s", "10ms").Should(HaveLen(3))
		Consistently(up.Started, "300ms", "10ms").Should(HaveLen(3))
		for _, n := range names {
			up.Release(n)
		}
		for _, n := range names {
			Eventually(processed(ids[n]), "5s", "10ms").Should(BeTrue())
		}
		Expect(up.MaxRunning()).To(Equal(3))
	})

	It("keeps one model serialized while others run", func() {
		start(4)
		// Admission is asynchronous: wait for each start so the order is known.
		first, _ := svc.EnqueueModelUpgrade("alpha", galleryop.ModelUpgradeOptions{})
		Eventually(up.Started, "5s", "10ms").Should(HaveLen(1))
		second, _ := svc.EnqueueModelUpgrade("alpha", galleryop.ModelUpgradeOptions{})
		Eventually(func() bool { return svc.GetStatus(second).IsQueued() }, "5s", "10ms").Should(BeTrue())
		other, _ := svc.EnqueueModelUpgrade("beta", galleryop.ModelUpgradeOptions{})

		Eventually(up.Started, "5s", "10ms").Should(ConsistOf("alpha", "beta"))
		Consistently(up.Started, "300ms", "10ms").Should(HaveLen(2))
		Expect(svc.GetStatus(second).IsQueued()).To(BeTrue())

		up.Release("alpha")
		up.Release("beta")
		Eventually(processed(first), "5s", "10ms").Should(BeTrue())
		Eventually(processed(second), "5s", "10ms").Should(BeTrue())
		Eventually(processed(other), "5s", "10ms").Should(BeTrue())
		Expect(up.Overlapped()).To(BeFalse())
	})

	It("cancels a queued upgrade without running it and a running one through its context", func() {
		start(1)
		running, _ := svc.EnqueueModelUpgrade("alpha", galleryop.ModelUpgradeOptions{})
		// Admission is asynchronous, so wait for the first before the second.
		Eventually(up.Started, "5s", "10ms").Should(HaveLen(1))
		queued, _ := svc.EnqueueModelUpgrade("beta", galleryop.ModelUpgradeOptions{})
		Eventually(func() bool { return svc.GetStatus(queued).IsQueued() }, "5s", "10ms").Should(BeTrue())

		svc.CancelOperation(queued)
		Eventually(processed(queued), "5s", "10ms").Should(BeTrue())
		Expect(svc.GetStatus(queued).Cancelled).To(BeTrue())
		Expect(up.Started()).To(Equal([]string{"alpha"}))

		svc.CancelOperation(running)
		Eventually(processed(running), "5s", "10ms").Should(BeTrue())
		Eventually(up.Cancelled, "5s", "10ms").Should(ConsistOf("alpha"))
		// The freed slot is not taken by the cancelled one.
		Consistently(up.Started, "200ms", "10ms").Should(Equal([]string{"alpha"}))
	})

	It("starts a waiting upgrade when a running one ends", func() {
		start(1)
		svc.EnqueueModelUpgrade("alpha", galleryop.ModelUpgradeOptions{})
		Eventually(up.Started, "5s", "10ms").Should(HaveLen(1))
		last, _ := svc.EnqueueModelUpgrade("beta", galleryop.ModelUpgradeOptions{})
		up.Release("alpha")
		Eventually(up.Started, "5s", "10ms").Should(HaveLen(2))
		up.Release("beta")
		Eventually(processed(last), "5s", "10ms").Should(BeTrue())
	})
})

var _ = Describe("the model operation limit setting", func() {
	It("defaults to two and never goes below one", func() {
		Expect(config.NewApplicationConfig().ModelOperationLimit()).To(Equal(config.DefaultMaxConcurrentModelOperations))
		cfg := config.NewApplicationConfig(config.WithMaxConcurrentModelOperations(0))
		Expect(cfg.ModelOperationLimit()).To(Equal(1))
		Expect((&config.ApplicationConfig{}).ModelOperationLimit()).To(Equal(1))
	})

	It("follows a runtime settings change", func() {
		cfg := config.NewApplicationConfig()
		n := 5
		cfg.ApplyRuntimeSettings(&config.RuntimeSettings{MaxConcurrentModelOperations: &n})
		Expect(cfg.ModelOperationLimit()).To(Equal(5))
	})
})
