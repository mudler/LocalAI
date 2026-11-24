package galleryop_test

import (
	"context"
	"errors"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/galleryop"
)

// addonRun is a fake install: it reports one download step, then waits for
// release or for its context.
type addonRun struct {
	mu       sync.Mutex
	started  []string
	release  map[string]chan error
	abandons []string
}

func newAddonRun() *addonRun { return &addonRun{release: map[string]chan error{}} }

func (a *addonRun) op(id, addon string) galleryop.AddonOp {
	a.mu.Lock()
	a.release[id] = make(chan error, 1)
	ch := a.release[id]
	a.mu.Unlock()
	return galleryop.AddonOp{
		ID: id, AddonID: addon,
		Run: func(ctx context.Context, progress func(*galleryop.OpStatus)) error {
			a.mu.Lock()
			a.started = append(a.started, id)
			a.mu.Unlock()
			progress(&galleryop.OpStatus{Phase: "downloading", Message: "downloading", CurrentBytes: 5, TotalBytes: 10, Progress: 50, Cancellable: true})
			select {
			case err := <-ch:
				if err == nil {
					progress(&galleryop.OpStatus{Phase: "committing", Message: "committing"})
				}
				return err
			case <-ctx.Done():
				return ctx.Err()
			}
		},
		Abandon: func() {
			a.mu.Lock()
			defer a.mu.Unlock()
			a.abandons = append(a.abandons, id)
		},
	}
}

func (a *addonRun) finish(id string, err error) {
	a.mu.Lock()
	ch := a.release[id]
	a.mu.Unlock()
	ch <- err
}

func (a *addonRun) startedIDs() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.started...)
}

func (a *addonRun) abandoned() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.abandons...)
}

var _ = Describe("add-on installs in the operation queue", func() {
	var (
		svc     *galleryop.GalleryService
		run     *addonRun
		cancel  context.CancelFunc
		results chan galleryop.OpResult
	)
	status := func(id string) func() *galleryop.OpStatus {
		return func() *galleryop.OpStatus { return svc.GetStatus(id) }
	}

	BeforeEach(func() {
		svc = galleryop.NewGalleryService(&config.ApplicationConfig{}, nil)
		results = make(chan galleryop.OpResult, 8)
		svc.SetOnOpFinished(func(r galleryop.OpResult) { results <- r })
		run = newAddonRun()
		var ctx context.Context
		ctx, cancel = context.WithCancel(context.Background())
		DeferCleanup(func() { cancel() })
		Expect(svc.Start(ctx, nil, nil)).To(Succeed())
	})

	It("runs installs in the order they were queued within the operation limit, with progress", func() {
		svc.EnqueueAddonOp(run.op("j1", "calendar"))
		Eventually(run.startedIDs).Should(Equal([]string{"j1"}))
		svc.EnqueueAddonOp(run.op("j2", "notes"))
		svc.EnqueueAddonOp(run.op("j3", "tables"))
		Eventually(status("j2")).Should(Satisfy(func(s *galleryop.OpStatus) bool { return s.IsQueued() }))
		Eventually(status("j1")).Should(Satisfy(func(s *galleryop.OpStatus) bool {
			return s.Phase == "downloading" && s.CurrentBytes == 5 && s.TotalBytes == 10 && s.GalleryElementName == "calendar"
		}))
		Consistently(run.startedIDs, "200ms").Should(Equal([]string{"j1"}))

		run.finish("j1", nil)
		Eventually(run.startedIDs).Should(Equal([]string{"j1", "j2"}))
		run.finish("j2", nil)
		Eventually(run.startedIDs).Should(Equal([]string{"j1", "j2", "j3"}))
		run.finish("j3", nil)

		Eventually(status("j1")).Should(Satisfy(func(s *galleryop.OpStatus) bool {
			return s.Processed && s.Message == galleryop.MessageCompleted && s.Progress == 100
		}))
		var got []galleryop.OpResult
		Eventually(results).Should(Receive())
		Eventually(func() int { return len(results) }).Should(Equal(2))
		for len(results) > 0 {
			got = append(got, <-results)
		}
		for _, r := range got {
			Expect(r.Addon).To(BeTrue())
			Expect(r.Outcome).To(Equal(galleryop.OpCompleted))
		}
	})

	It("shares the backend operation pool: two add-ons run side by side, the same add-on never twice", func() {
		pooled := galleryop.NewGalleryService(&config.ApplicationConfig{MaxConcurrentBackendOperations: 2}, nil)
		ctx, stop := context.WithCancel(context.Background())
		DeferCleanup(stop)
		Expect(pooled.Start(ctx, nil, nil)).To(Succeed())
		pooled.EnqueueAddonOp(run.op("j1", "calendar"))
		pooled.EnqueueAddonOp(run.op("j2", "calendar"))
		pooled.EnqueueAddonOp(run.op("j3", "notes"))
		Eventually(run.startedIDs).Should(ConsistOf("j1", "j3"))
		Consistently(run.startedIDs, "200ms").Should(HaveLen(2))
		Expect(pooled.GetStatus("j2").IsQueued()).To(BeTrue())
		run.finish("j1", nil)
		Eventually(run.startedIDs).Should(ContainElement("j2"))
		run.finish("j2", nil)
		run.finish("j3", nil)
	})

	It("cancels a queued install: it never runs and the owner's record is released", func() {
		svc.EnqueueAddonOp(run.op("j1", "calendar"))
		Eventually(run.startedIDs).Should(Equal([]string{"j1"}))
		svc.EnqueueAddonOp(run.op("j2", "notes"))
		Eventually(status("j2")).ShouldNot(BeNil())
		Expect(svc.CancelOperation("j2")).To(Succeed())
		Eventually(run.abandoned).Should(Equal([]string{"j2"}))

		run.finish("j1", nil)
		Consistently(run.startedIDs, "300ms").Should(Equal([]string{"j1"}))
		Expect(svc.GetStatus("j2").Cancelled).To(BeTrue())
	})

	It("cancels a running install through its context and reports it cancelled", func() {
		svc.EnqueueAddonOp(run.op("j1", "calendar"))
		Eventually(status("j1")).Should(Satisfy(func(s *galleryop.OpStatus) bool { return s.Phase == "downloading" }))
		Expect(svc.CancelOperation("j1")).To(Succeed())
		Eventually(status("j1")).Should(Satisfy(func(s *galleryop.OpStatus) bool { return s.Processed && s.Cancelled }))
		var r galleryop.OpResult
		Eventually(results).Should(Receive(&r))
		Expect(r.Outcome).To(Equal(galleryop.OpCancelled))
		Expect(r.Addon).To(BeTrue())
		Expect(run.abandoned()).To(BeEmpty(), "a running install is stopped by its context, not dropped")
	})

	It("refuses to cancel an install that says it can no longer stop", func() {
		op := run.op("j1", "calendar")
		inner := op.Run
		committing := make(chan struct{})
		op.Run = func(ctx context.Context, progress func(*galleryop.OpStatus)) error {
			progress(&galleryop.OpStatus{Phase: "committing", Message: "committing", Cancellable: false})
			close(committing)
			return inner(ctx, func(*galleryop.OpStatus) {})
		}
		svc.EnqueueAddonOp(op)
		Eventually(committing).Should(BeClosed())
		Expect(errors.Is(svc.CancelOperation("j1"), galleryop.ErrNotCancellable)).To(BeTrue())
		run.finish("j1", nil)
		Eventually(status("j1")).Should(Satisfy(func(s *galleryop.OpStatus) bool { return s.Processed && !s.Cancelled }))
	})

	It("reports a failed install, and one that needs a new review, to the notification hook", func() {
		svc.EnqueueAddonOp(run.op("j1", "calendar"))
		Eventually(run.startedIDs).Should(Equal([]string{"j1"}))
		run.finish("j1", &galleryop.AddonAttentionError{Reason: "review it"})
		var r galleryop.OpResult
		Eventually(results, time.Second).Should(Receive(&r))
		Expect(r.Outcome).To(Equal(galleryop.OpFailed))
		Expect(r.NeedsApproval).To(BeTrue())
		Expect(svc.GetStatus("j1").Error).To(HaveOccurred())

		svc.EnqueueAddonOp(run.op("j2", "notes"))
		Eventually(run.startedIDs).Should(HaveLen(2))
		run.finish("j2", errors.New("the install failed (package_fetch_failed)"))
		Eventually(results, time.Second).Should(Receive(&r))
		Expect(r.Outcome).To(Equal(galleryop.OpFailed))
		Expect(r.NeedsApproval).To(BeFalse())
	})

	It("queues an add-on upgrade as an upgrade, and reports it as one", func() {
		op := run.op("j1", "calendar")
		op.Upgrade = true
		svc.EnqueueAddonOp(op)
		Eventually(run.startedIDs).Should(Equal([]string{"j1"}))
		Expect(svc.GetStatus("j1").Upgrade).To(BeTrue())
		run.finish("j1", nil)
		var r galleryop.OpResult
		Eventually(results).Should(Receive(&r))
		Expect(r.Addon).To(BeTrue())
		Expect(r.Upgrade).To(BeTrue())
		Expect(r.Outcome).To(Equal(galleryop.OpCompleted))
	})

	It("names an add-on install by its add-on in the operation keys", func() {
		key := galleryop.AddonOpKey("calendar")
		id, ok := galleryop.ParseAddonOpKey(key)
		Expect(ok).To(BeTrue())
		Expect(id).To(Equal("calendar"))
		_, ok = galleryop.ParseAddonOpKey("localai@calendar")
		Expect(ok).To(BeFalse())
	})

	It("tells an install that needs a new review, also from the text a peer replicated", func() {
		err := &galleryop.AddonAttentionError{Reason: "review it"}
		Expect(galleryop.IsAddonAttention(err)).To(BeTrue())
		Expect(galleryop.IsAddonAttention(errors.New(err.Error()))).To(BeTrue())
		Expect(galleryop.IsAddonAttention(errors.New("the install failed"))).To(BeFalse())
		Expect(galleryop.IsAddonAttention(nil)).To(BeFalse())
	})
})
