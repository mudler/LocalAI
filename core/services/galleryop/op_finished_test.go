package galleryop_test

import (
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/galleryop"
)

// The notification center hears when an operation reaches a terminal state.
var _ = Describe("GalleryService OnOpFinished", func() {
	var (
		svc  *galleryop.GalleryService
		seen chan galleryop.OpResult
	)

	BeforeEach(func() {
		svc = galleryop.NewGalleryService(&config.ApplicationConfig{}, nil)
		seen = make(chan galleryop.OpResult, 4)
		svc.SetOnOpFinished(func(r galleryop.OpResult) { seen <- r })
	})

	It("reports a completed operation", func() {
		svc.UpdateStatus("op", &galleryop.OpStatus{Processed: true, Message: "completed", Progress: 100})
		svc.AnnounceOpForTest("op", "qwen3-8b", false, false, false, nil)
		Eventually(seen).Should(Receive(Equal(galleryop.OpResult{ID: "op", Name: "qwen3-8b", Outcome: galleryop.OpCompleted})))
	})

	It("reports a model removed with files left over as completed", func() {
		svc.UpdateStatus("op", &galleryop.OpStatus{Processed: true, Message: "completed", Deletion: true})
		svc.AnnounceOpForTest("op", "m", false, true, false, errors.New("some files stay"))
		Eventually(seen).Should(Receive(HaveField("Outcome", galleryop.OpCompleted)))
	})

	It("reports a failure with its reason", func() {
		boom := errors.New("download interrupted")
		svc.UpdateStatus("op", &galleryop.OpStatus{Error: boom, Processed: true, Message: "error: download interrupted"})
		svc.AnnounceOpForTest("op", "whisper", true, false, true, boom)
		Eventually(seen).Should(Receive(Equal(galleryop.OpResult{ID: "op", Name: "whisper", Backend: true, Upgrade: true, Outcome: galleryop.OpFailed, Err: "download interrupted"})))
	})

	It("reports a cancel as cancelled, whatever error the handler returned", func() {
		svc.StoreCancellation("op", func() {})
		svc.UpdateStatus("op", &galleryop.OpStatus{Message: "downloading", Cancellable: true})
		Expect(svc.CancelOperation("op")).To(Succeed())
		svc.AnnounceOpForTest("op", "m", false, false, false, errors.New("context canceled"))
		Eventually(seen).Should(Receive(HaveField("Outcome", galleryop.OpCancelled)))
	})

	It("says nothing without an ID, and nothing when another frontend runs the same element", func() {
		svc.AnnounceOpForTest("", "llama-cpp", true, false, true, nil)
		svc.UpdateStatus("dup", &galleryop.OpStatus{Processed: true, Message: "already being processed by another instance (op x)"})
		svc.AnnounceOpForTest("dup", "m", false, false, false, nil)
		Consistently(seen, "200ms").ShouldNot(Receive())
	})
})
