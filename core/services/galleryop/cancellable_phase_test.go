package galleryop_test

import (
	"context"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/services/galleryop"
)

// gatedModelManager records the element name of every operation the worker
// actually starts and then parks inside the handler until the gate is released.
// Parking is what makes the running phase observable at all: without it the
// handler-entry status is overwritten by the terminal write before a spec can
// read it.
//
// The gate CHANNEL is under the mutex, not only the recorded names. A spec that
// frees the worker replaces the channel so the next operation parks on a fresh
// one, and the worker goroutine reads the field to park on it; the two happen
// on different goroutines and raced. It was a race in this double and not in
// anything it stands for, which is exactly why it survived: the suite passed
// every run and only -race said so.
type gatedModelManager struct {
	mu      sync.Mutex
	started []string
	gate    chan struct{}
}

func newGatedModelManager() *gatedModelManager {
	return &gatedModelManager{gate: make(chan struct{})}
}

func (m *gatedModelManager) record(name string) {
	m.mu.Lock()
	m.started = append(m.started, name)
	m.mu.Unlock()
}

func (m *gatedModelManager) Started() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.started...)
}

// gateCh reads the current gate, for a handler about to park on it.
func (m *gatedModelManager) gateCh() chan struct{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.gate
}

// releaseGate frees every handler parked on the current gate and arms a fresh
// one, so the next operation parks again.
func (m *gatedModelManager) releaseGate() {
	m.mu.Lock()
	defer m.mu.Unlock()
	close(m.gate)
	m.gate = make(chan struct{})
}

// closeGate frees every parked handler without arming another. It is what
// cleanup does, and it is idempotent so a spec that already released can be
// cleaned up after.
func (m *gatedModelManager) closeGate() {
	m.mu.Lock()
	defer m.mu.Unlock()
	select {
	case <-m.gate:
	default:
		close(m.gate)
	}
}

func (m *gatedModelManager) InstallModel(_ context.Context, op *galleryop.ManagementOp[gallery.GalleryModel, gallery.ModelConfig], _ galleryop.ProgressCallback) error {
	m.record(op.GalleryElementName)
	<-m.gateCh()
	return nil
}

func (m *gatedModelManager) DeleteModel(name string) error {
	m.record(name)
	<-m.gateCh()
	return nil
}

// An operation is cancellable for exactly as long as cancelling it can still
// change the outcome, and that window is not the one the shape of the UI
// suggests. A queued operation of ANY kind is cancellable: the delivery
// goroutine is released by the operation context and abandons the op before the
// worker ever sees it, so nothing has happened yet and nothing needs undoing. A
// running removal is not: DeleteModel and DeleteBackend take no context, so once
// the worker has entered the handler the files are going away regardless.
// Reporting these backwards gave the admin a Cancel button on the removal that
// could not honour it, and hid the button on the queued removal that could.
var _ = Describe("operation cancellability by phase", func() {
	var svc *galleryop.GalleryService

	BeforeEach(func() {
		svc = galleryop.NewGalleryService(&config.ApplicationConfig{}, nil)
	})

	// Nothing consumes the channel in this context, which is exactly the state
	// of an operation admitted while the serial worker is busy elsewhere.
	Context("while the operation is still queued", func() {
		It("reports a queued removal as cancellable", func() {
			svc.EnqueueModelOp(galleryop.ManagementOp[gallery.GalleryModel, gallery.ModelConfig]{
				ID:                 "job-queued-removal",
				GalleryElementName: "localai@qwen3-4b",
				Delete:             true,
			})

			Eventually(func() *galleryop.OpStatus {
				return svc.GetStatus("job-queued-removal")
			}, "2s", "10ms").ShouldNot(BeNil())

			st := svc.GetStatus("job-queued-removal")
			Expect(st.Deletion).To(BeTrue())
			Expect(st.Cancellable).To(BeTrue(),
				"a removal that has not reached the worker can still be called off with no trace")
		})

		It("reports a queued install as cancellable", func() {
			svc.EnqueueModelOp(galleryop.ManagementOp[gallery.GalleryModel, gallery.ModelConfig]{
				ID:                 "job-queued-install",
				GalleryElementName: "localai@qwen3-4b",
			})

			Eventually(func() *galleryop.OpStatus {
				return svc.GetStatus("job-queued-install")
			}, "2s", "10ms").ShouldNot(BeNil())

			Expect(svc.GetStatus("job-queued-install").Cancellable).To(BeTrue())
		})

		It("reports a queued backend removal as cancellable", func() {
			svc.EnqueueBackendOp(galleryop.ManagementOp[gallery.GalleryBackend, any]{
				ID:                 "job-queued-backend-removal",
				GalleryElementName: "vllm",
				Delete:             true,
			})

			Eventually(func() *galleryop.OpStatus {
				return svc.GetStatus("job-queued-backend-removal")
			}, "2s", "10ms").ShouldNot(BeNil())

			Expect(svc.GetStatus("job-queued-backend-removal").Cancellable).To(BeTrue())
		})
	})

	Context("once the worker has started the operation", func() {
		var manager *gatedModelManager

		BeforeEach(func() {
			manager = newGatedModelManager()
			DeferCleanup(manager.closeGate)

			svc.SetModelManager(manager)
			ctx, cancel := context.WithCancel(context.Background())
			DeferCleanup(cancel)
			Expect(svc.Start(ctx, nil, nil)).To(Succeed())
		})

		It("reports a running removal as not cancellable", func() {
			svc.EnqueueModelOp(galleryop.ManagementOp[gallery.GalleryModel, gallery.ModelConfig]{
				ID:                 "job-running-removal",
				GalleryElementName: "localai@qwen3-4b",
				Delete:             true,
			})

			Eventually(manager.Started, "5s", "20ms").Should(ContainElement("localai@qwen3-4b"))

			st := svc.GetStatus("job-running-removal")
			Expect(st).ToNot(BeNil())
			Expect(st.Cancellable).To(BeFalse(),
				"DeleteModel takes no context, so a Cancel button here cannot stop anything")
		})

		It("reports a running install as cancellable", func() {
			svc.EnqueueModelOp(galleryop.ManagementOp[gallery.GalleryModel, gallery.ModelConfig]{
				ID:                 "job-running-install",
				GalleryElementName: "localai@qwen3-4b",
			})

			Eventually(manager.Started, "5s", "20ms").Should(ContainElement("localai@qwen3-4b"))

			st := svc.GetStatus("job-running-install")
			Expect(st).ToNot(BeNil())
			Expect(st.Cancellable).To(BeTrue(),
				"an in-flight download is cancelled through the operation context")
		})

		// The behaviour the whole asymmetry rests on: if cancelling a queued
		// removal did not actually stop it, reporting it as cancellable would be
		// the same lie in the other direction.
		It("keeps a cancelled queued removal from ever reaching the worker", func() {
			svc.EnqueueModelOp(galleryop.ManagementOp[gallery.GalleryModel, gallery.ModelConfig]{
				ID:                 "job-occupying-worker",
				GalleryElementName: "localai@long-install",
			})
			Eventually(manager.Started, "5s", "20ms").Should(ContainElement("localai@long-install"))

			opCtx, cancelOp := context.WithCancel(context.Background())
			svc.EnqueueModelOp(galleryop.ManagementOp[gallery.GalleryModel, gallery.ModelConfig]{
				ID:                 "job-cancelled-while-queued",
				GalleryElementName: "localai@doomed-removal",
				Delete:             true,
				Context:            opCtx,
				CancelFunc:         cancelOp,
			})
			Eventually(func() *galleryop.OpStatus {
				return svc.GetStatus("job-cancelled-while-queued")
			}, "2s", "10ms").ShouldNot(BeNil())

			cancelOp()

			Eventually(func() bool {
				st := svc.GetStatus("job-cancelled-while-queued")
				return st != nil && st.Processed
			}, "2s", "10ms").Should(BeTrue(), "abandonQueued must retire the op the worker never took")

			// Free the worker: it loops back to an empty channel because the
			// delivery goroutine gave up on the send.
			manager.releaseGate()

			Consistently(manager.Started, "500ms", "20ms").ShouldNot(
				ContainElement("localai@doomed-removal"),
				"a removal cancelled while queued must never delete anything")
		})
	})
})
