package galleryop_test

import (
	"context"
	"errors"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/galleryop"
)

var _ = Describe("plugin changes in the operation queue", func() {
	var svc *galleryop.GalleryService
	status := func(id string) func() *galleryop.OpStatus {
		return func() *galleryop.OpStatus { return svc.GetStatus(id) }
	}
	BeforeEach(func() {
		svc = galleryop.NewGalleryService(&config.ApplicationConfig{}, nil)
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		Expect(svc.Start(ctx, nil, nil)).To(Succeed())
	})

	It("keys an operation by its plugin ID", func() {
		Expect(galleryop.PluginOpKey("fmt@tools")).To(Equal("plugin:fmt@tools"))
		id, ok := galleryop.ParsePluginOpKey("plugin:fmt@tools")
		Expect(ok).To(BeTrue())
		Expect(id).To(Equal("fmt@tools"))
		_, ok = galleryop.ParsePluginOpKey("addon:fmt")
		Expect(ok).To(BeFalse())
	})

	It("runs a change and marks it completed, or failed with its error", func() {
		svc.EnqueuePluginOp(galleryop.PluginOp{ID: "p1", PluginID: "fmt@tools", Run: func(context.Context, func(*galleryop.OpStatus)) error { return nil }})
		Eventually(status("p1")).Should(Satisfy(func(s *galleryop.OpStatus) bool {
			return s != nil && s.Processed && s.Message == galleryop.MessageCompleted && s.GalleryElementName == "fmt@tools"
		}))
		svc.EnqueuePluginOp(galleryop.PluginOp{ID: "p2", PluginID: "fmt@tools", Update: true, Run: func(context.Context, func(*galleryop.OpStatus)) error {
			return errors.New("digest mismatch")
		}})
		Eventually(status("p2")).Should(Satisfy(func(s *galleryop.OpStatus) bool {
			return s != nil && s.Processed && s.Error != nil && s.Upgrade
		}))
	})

	It("reports the end of a change with the person who started it", func() {
		got := make(chan galleryop.OpResult, 1)
		svc.SetOnOpFinished(func(r galleryop.OpResult) { got <- r })
		svc.EnqueuePluginOp(galleryop.PluginOp{ID: "p3", PluginID: "fmt@tools", By: "alice", Run: func(context.Context, func(*galleryop.OpStatus)) error {
			return errors.New("digest mismatch")
		}})
		var r galleryop.OpResult
		Eventually(got).Should(Receive(&r))
		Expect(r).To(SatisfyAll(HaveField("ID", "p3"), HaveField("Name", "fmt@tools"), HaveField("Plugin", true),
			HaveField("By", "alice"), HaveField("Outcome", galleryop.OpFailed), HaveField("Err", ContainSubstring("digest mismatch"))))
	})

	It("drops a change cancelled while queued without running it", func() {
		release := make(chan struct{})
		var mu sync.Mutex
		ran := map[string]bool{}
		abandoned := false
		run := func(id string) func(context.Context, func(*galleryop.OpStatus)) error {
			return func(ctx context.Context, _ func(*galleryop.OpStatus)) error {
				mu.Lock()
				ran[id] = true
				mu.Unlock()
				select {
				case <-release:
				case <-ctx.Done():
				}
				return ctx.Err()
			}
		}
		svc.EnqueuePluginOp(galleryop.PluginOp{ID: "a", PluginID: "fmt@tools", Run: run("a")})
		Eventually(func() bool { mu.Lock(); defer mu.Unlock(); return ran["a"] }).Should(BeTrue())
		svc.EnqueuePluginOp(galleryop.PluginOp{ID: "b", PluginID: "fmt@tools", Run: run("b"), Abandon: func() {
			mu.Lock()
			defer mu.Unlock()
			abandoned = true
		}})
		Eventually(status("b")).Should(Satisfy(func(s *galleryop.OpStatus) bool { return s.IsQueued() }))
		Expect(svc.CancelOperation("b")).To(Succeed())
		close(release)
		Eventually(func() bool { mu.Lock(); defer mu.Unlock(); return abandoned }).Should(BeTrue())
		Consistently(func() bool { mu.Lock(); defer mu.Unlock(); return ran["b"] }, "200ms").Should(BeFalse())
	})
})
