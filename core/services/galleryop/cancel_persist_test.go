package galleryop_test

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/distributed"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/core/services/testutil"
)

// Reproduces "a cancelled/orphaned op resurrects as 'processing' after a pod
// restart". CancelOperation flipped the in-memory status to cancelled and
// broadcast a NATS event, but never persisted the terminal status to the
// gallery store. On the next replica restart the still-"pending" row hydrated
// straight back into processingBackends and the UI spun again. CancelOperation
// must persist the cancellation so it survives a restart.
var _ = Describe("GalleryService.CancelOperation persistence", func() {
	It("persists the cancelled status to the gallery store", func() {
		db := testutil.SetupTestDB()
		store, err := distributed.NewGalleryStore(db)
		Expect(err).ToNot(HaveOccurred())

		// Seed an in-flight op as if a replica was mid-install.
		Expect(store.Create(&distributed.GalleryOperationRecord{
			ID:                 "op-cancel",
			GalleryElementName: "llama-cpp-development",
			OpType:             "backend_install",
			Status:             "pending",
			Progress:           0,
		})).To(Succeed())

		svc := galleryop.NewGalleryService(&config.ApplicationConfig{}, nil)
		svc.SetGalleryStore(store)
		// Make the op locally cancellable so CancelOperation proceeds.
		svc.StoreCancellation("op-cancel", context.CancelFunc(func() {}))

		Expect(svc.CancelOperation("op-cancel")).To(Succeed())

		// The persisted row must now be terminal — otherwise it re-hydrates as
		// pending on the next restart.
		rec, err := store.Get("op-cancel")
		Expect(err).ToNot(HaveOccurred())
		Expect(rec.Status).To(Equal("cancelled"))

		// And a fresh service hydrating from the store must NOT see it as active.
		fresh := galleryop.NewGalleryService(&config.ApplicationConfig{}, nil)
		fresh.SetGalleryStore(store)
		Expect(fresh.Hydrate()).To(Succeed())
		Expect(fresh.GetStatus("op-cancel")).To(BeNil(),
			"a cancelled op must not hydrate back as active after a restart")
	})
})

// Reproduces "an op orphaned by a replica that died mid-flight stays 'pending'
// forever". CleanStale (which marks abandoned active ops failed) only ran once
// on startup, so an op orphaned AFTER startup was never reaped until the next
// restart. The service must reap stale ops on an interval, not just at boot.
var _ = Describe("GalleryService.ReapStaleOperations", func() {
	It("marks abandoned active ops terminal once they pass the age cutoff", func() {
		db := testutil.SetupTestDB()
		store, err := distributed.NewGalleryStore(db)
		Expect(err).ToNot(HaveOccurred())

		Expect(store.Create(&distributed.GalleryOperationRecord{
			ID:                 "orphan-op",
			GalleryElementName: "llama-cpp-development",
			OpType:             "backend_install",
			Status:             "pending",
			Progress:           0,
		})).To(Succeed())
		// Force the row's updated_at into the past so it is older than the cutoff.
		Expect(db.Exec(
			"UPDATE gallery_operations SET updated_at = ? WHERE id = ?",
			time.Now().Add(-1*time.Hour), "orphan-op",
		).Error).To(Succeed())

		// A fresh, still-progressing op must NOT be reaped.
		Expect(store.Create(&distributed.GalleryOperationRecord{
			ID:                 "live-op",
			GalleryElementName: "vllm-development",
			OpType:             "backend_install",
			Status:             "downloading",
			Progress:           50,
		})).To(Succeed())

		svc := galleryop.NewGalleryService(&config.ApplicationConfig{}, nil)
		svc.SetGalleryStore(store)

		reaped, err := svc.ReapStaleOperations(30 * time.Minute)
		Expect(err).ToNot(HaveOccurred())
		Expect(reaped).To(Equal(int64(1)))

		orphan, err := store.Get("orphan-op")
		Expect(err).ToNot(HaveOccurred())
		Expect(orphan.Status).To(Equal("failed"))

		live, err := store.Get("live-op")
		Expect(err).ToNot(HaveOccurred())
		Expect(live.Status).To(Equal("downloading"), "a recently-updated op must not be reaped")
	})
})
