package galleryop_test

import (
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/galleryop"
)

// Whether a job is a removal or an install is decided once, at admission, and
// never restated. markQueued is the only writer that sets Deletion; every
// status the worker publishes afterwards leaves the field at its zero value.
// Without carry-forward the flag survives only the queued window, which is the
// one window no consumer reads it in.
var _ = Describe("OpStatus.Deletion carry-forward", func() {
	var svc *galleryop.GalleryService

	BeforeEach(func() {
		svc = galleryop.NewGalleryService(&config.ApplicationConfig{}, nil)
	})

	It("keeps the flag set once the worker starts the removal", func() {
		svc.UpdateStatus("op-del", &galleryop.OpStatus{
			Message: galleryop.PhaseQueued, Phase: galleryop.PhaseQueued,
			GalleryElementName: "localai@qwen3-4b", Deletion: true,
		})

		// The worker's first write, verbatim from deleteModel/deleteBackend: a
		// fresh OpStatus that says nothing about what kind of job this is.
		svc.UpdateStatus("op-del", &galleryop.OpStatus{Message: "processing model: localai@qwen3-4b", Cancellable: true})

		Expect(svc.GetStatus("op-del").Deletion).To(BeTrue(), "a running removal must still report as a removal")
	})

	It("keeps the flag set when the removal fails", func() {
		svc.UpdateStatus("op-del", &galleryop.OpStatus{Deletion: true, Phase: galleryop.PhaseQueued})
		svc.UpdateStatus("op-del", &galleryop.OpStatus{Message: "processing model: localai@qwen3-4b", Cancellable: true})

		// The failure path (updateError in Start) publishes only the error.
		svc.UpdateStatus("op-del", &galleryop.OpStatus{
			Error: errors.New("permission denied"), Processed: true, Message: "error: permission denied",
		})

		st := svc.GetStatus("op-del")
		Expect(st.Deletion).To(BeTrue(), "a failed removal must not be mistaken for a failed install: Retry would re-download the model")
		Expect(st.Error).To(HaveOccurred())
	})

	It("never turns an install into a deletion", func() {
		svc.UpdateStatus("op-install", &galleryop.OpStatus{Phase: galleryop.PhaseQueued})
		svc.UpdateStatus("op-install", &galleryop.OpStatus{Progress: 42, Cancellable: true})

		Expect(svc.GetStatus("op-install").Deletion).To(BeFalse())
	})
})
