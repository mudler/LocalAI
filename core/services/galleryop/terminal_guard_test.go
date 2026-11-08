package galleryop_test

import (
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/distributed"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/core/services/testutil"
)

// A finished operation is terminal. A cancel races the handler, which keeps
// running until its context is cancelled and then reports an error or one more
// progress tick. Neither may undo the cancellation.
var _ = Describe("GalleryService terminal operation states", func() {
	var svc *galleryop.GalleryService

	BeforeEach(func() {
		svc = galleryop.NewGalleryService(&config.ApplicationConfig{}, nil)
		svc.StoreCancellation("op", func() {})
		svc.UpdateStatus("op", &galleryop.OpStatus{Message: "downloading", Progress: 10, Cancellable: true})
		Expect(svc.CancelOperation("op")).To(Succeed())
	})

	It("keeps Cancelled when the handler then reports an error", func() {
		svc.UpdateStatus("op", &galleryop.OpStatus{Error: errors.New("context canceled"), Processed: true, Message: "error: context canceled"})

		st := svc.GetStatus("op")
		Expect(st.Cancelled).To(BeTrue())
		Expect(st.Processed).To(BeTrue())
		Expect(st.Error).To(BeNil())
		Expect(st.Message).To(Equal("cancelled"))
	})

	It("ignores late progress after the cancel", func() {
		svc.UpdateStatus("op", &galleryop.OpStatus{Message: "downloading", Progress: 55, Cancellable: true})
		svc.UpdateNodeProgress("op", "node-1", galleryop.NodeProgress{NodeID: "node-1", Percentage: 60})

		st := svc.GetStatus("op")
		Expect(st.Processed).To(BeTrue())
		Expect(st.Cancelled).To(BeTrue())
		Expect(st.Progress).To(BeNumerically("==", 10))
		Expect(st.Nodes).To(BeEmpty())
	})

	It("ignores a peer's late progress broadcast after the cancel", func() {
		svc.MergeStatusForTest("op", &galleryop.OpStatus{Message: "downloading", Progress: 70})

		st := svc.GetStatus("op")
		Expect(st.Processed).To(BeTrue())
		Expect(st.Cancelled).To(BeTrue())
	})

	It("still accepts the handler's own cancelled report", func() {
		svc.UpdateStatus("op", &galleryop.OpStatus{Cancelled: true, Processed: true, Message: "cancelled", GalleryElementName: "m"})

		st := svc.GetStatus("op")
		Expect(st.Cancelled).To(BeTrue())
		Expect(st.GalleryElementName).To(Equal("m"))
	})

	It("does not reopen a completed operation", func() {
		svc.UpdateStatus("done", &galleryop.OpStatus{Message: "installing", Progress: 90})
		svc.UpdateStatus("done", &galleryop.OpStatus{Processed: true, Message: "completed", Progress: 100})
		svc.UpdateStatus("done", &galleryop.OpStatus{Message: "installing", Progress: 95})

		st := svc.GetStatus("done")
		Expect(st.Processed).To(BeTrue())
		Expect(st.Progress).To(BeNumerically("==", 100))
	})

	It("does not persist the handler error over a cancelled row", func() {
		db := testutil.SetupTestDB()
		store, err := distributed.NewGalleryStore(db)
		Expect(err).ToNot(HaveOccurred())
		Expect(store.Create(&distributed.GalleryOperationRecord{
			ID: "op-db", GalleryElementName: "m", OpType: "backend_install", Status: "pending",
		})).To(Succeed())
		svc.SetGalleryStore(store)
		svc.StoreCancellation("op-db", func() {})
		Expect(svc.CancelOperation("op-db")).To(Succeed())

		svc.UpdateStatus("op-db", &galleryop.OpStatus{Error: errors.New("context canceled"), Processed: true})

		rec, err := store.Get("op-db")
		Expect(err).ToNot(HaveOccurred())
		Expect(rec.Status).To(Equal("cancelled"))
	})
})
