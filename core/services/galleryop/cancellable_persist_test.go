package galleryop_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/distributed"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/core/services/testutil"
)

// Reproduces "an in-flight install can't be cancelled after a restart". The
// live install path marks OpStatus.Cancellable=true on every progress tick, but
// UpdateStatus persisted progress/status to the gallery store WITHOUT the
// cancellable flag, and Create defaulted it to false. So after a replica
// restart Hydrate rebuilt the op with Cancellable=false, /api/operations
// reported cancellable:false, and the UI hid the cancel button — the orphaned
// op lingered until the 30-minute stale reaper expired it. The cancellable
// state must be persisted so a rehydrated in-flight op stays cancellable.
var _ = Describe("GalleryService cancellable persistence across restart", func() {
	It("rehydrates an in-flight op as still cancellable", func() {
		db := testutil.SetupTestDB()
		store, err := distributed.NewGalleryStore(db)
		Expect(err).ToNot(HaveOccurred())

		svc := galleryop.NewGalleryService(&config.ApplicationConfig{}, nil)
		svc.SetGalleryStore(store)

		// Seed the in-flight op row as the worker goroutine does on admission.
		Expect(store.Create(&distributed.GalleryOperationRecord{
			ID:                 "op-inflight",
			GalleryElementName: "llama-cpp-development",
			OpType:             "backend_install",
			Status:             "pending",
		})).To(Succeed())

		// Simulate a progress tick: the live path always marks installs
		// cancellable while they are downloading/processing.
		svc.UpdateStatus("op-inflight", &galleryop.OpStatus{
			Message:      "downloading",
			Progress:     25,
			Phase:        "downloading",
			CurrentBytes: 123,
			TotalBytes:   456,
			Cancellable:  true,
		})

		// A fresh replica boots and hydrates from the store.
		fresh := galleryop.NewGalleryService(&config.ApplicationConfig{}, nil)
		fresh.SetGalleryStore(store)
		Expect(fresh.Hydrate()).To(Succeed())

		st := fresh.GetStatus("op-inflight")
		Expect(st).ToNot(BeNil(), "the in-flight op must hydrate after a restart")
		Expect(st.Cancellable).To(BeTrue(),
			"a still-active install must rehydrate as cancellable so the admin can dismiss it")
		Expect(st.Phase).To(Equal("downloading"))
		Expect(st.CurrentBytes).To(Equal(int64(123)))
		Expect(st.TotalBytes).To(Equal(int64(456)))
	})
})
