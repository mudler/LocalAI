package distributed_test

import (
	"sync/atomic"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/distributed"
	"github.com/mudler/LocalAI/core/services/messaging"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	pgdriver "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var _ = Describe("Gallery Distributed", Label("Distributed"), func() {
	var (
		infra        *TestInfra
		db           *gorm.DB
		galleryStore *distributed.GalleryStore
	)

	BeforeEach(func() {
		infra = SetupInfra("localai_gallery_dist_test")

		var err error
		db, err = gorm.Open(pgdriver.Open(infra.PGURL), &gorm.Config{
			Logger: logger.Default.LogMode(logger.Silent),
		})
		Expect(err).ToNot(HaveOccurred())

		galleryStore, err = distributed.NewGalleryStore(db)
		Expect(err).ToNot(HaveOccurred())
	})

	Context("PostgreSQL gallery operations", func() {
		It("should write gallery operation status to PostgreSQL", func() {
			op := &distributed.GalleryOperationRecord{
				GalleryElementName: "llama3-8b",
				OpType:             "model_install",
				Status:             "downloading",
				Cancellable:        true,
				FrontendID:         "f1",
			}
			Expect(galleryStore.Create(op)).To(Succeed())
			Expect(op.ID).ToNot(BeEmpty())

			retrieved, err := galleryStore.Get(op.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(retrieved.GalleryElementName).To(Equal("llama3-8b"))
			Expect(retrieved.Status).To(Equal("downloading"))
			Expect(retrieved.FrontendID).To(Equal("f1"))

			// Update progress (cancellable: a downloading install can be cancelled)
			Expect(galleryStore.UpdateProgress(op.ID, 0.75, "75% complete", "6GB", true)).To(Succeed())

			updated, _ := galleryStore.Get(op.ID)
			Expect(updated.Progress).To(BeNumerically("~", 0.75, 0.01))
			Expect(updated.Message).To(Equal("75% complete"))
			Expect(updated.Cancellable).To(BeTrue())

			// Complete
			Expect(galleryStore.UpdateStatus(op.ID, "completed", "")).To(Succeed())
			completed, _ := galleryStore.Get(op.ID)
			Expect(completed.Status).To(Equal("completed"))
		})
	})

	// The gallery families ride the broadcast carrier, not NATS.
	//
	// These used to publish and subscribe on a message-bus client, which
	// asserted that the bus delivers to itself and nothing about this
	// deployment: they would have
	// stayed green through the whole migration while the gallery service had
	// already moved. Two carriers on the deployment's own database is the shape
	// a fleet has, and it is the shape that fails when one end moves and the
	// other does not.
	//
	// No flush, unlike the NATS version: pgbus.Subscribe has already issued its
	// LISTEN by the time it returns, and Subscribers() counts only live
	// handlers, so there is no window to wait out.
	Context("gallery progress on the broadcast carrier", func() {
		It("delivers a peer replica's progress updates", func() {
			op := &distributed.GalleryOperationRecord{
				GalleryElementName: "whisper-large",
				OpType:             "model_install",
				Status:             "downloading",
			}
			Expect(galleryStore.Create(op)).To(Succeed())

			publisher, subscriber := infra.Bus(), infra.Bus()

			var received atomic.Int32
			sub, err := subscriber.Subscribe(messaging.SubjectGalleryProgress(op.ID), func([]byte) {
				received.Add(1)
			})
			Expect(err).ToNot(HaveOccurred())
			defer func() { Expect(sub.Unsubscribe()).To(Succeed()) }()

			Expect(publisher.Publish(messaging.SubjectGalleryProgress(op.ID), map[string]any{
				"op_id": op.ID, "progress": 0.25, "message": "25%",
			})).To(Succeed())
			Expect(publisher.Publish(messaging.SubjectGalleryProgress(op.ID), map[string]any{
				"op_id": op.ID, "progress": 0.50, "message": "50%",
			})).To(Succeed())

			Eventually(func() int32 { return received.Load() }, "20s").Should(Equal(int32(2)))
		})
	})

	Context("gallery cancel on the broadcast carrier", func() {
		It("delivers a cancel to the replica holding the operation", func() {
			op := &distributed.GalleryOperationRecord{
				GalleryElementName: "cancel-model",
				OpType:             "model_install",
				Status:             "downloading",
				Cancellable:        true,
			}
			Expect(galleryStore.Create(op)).To(Succeed())

			publisher, subscriber := infra.Bus(), infra.Bus()

			var cancelReceived atomic.Bool
			sub, err := subscriber.Subscribe(messaging.SubjectGalleryCancel(op.ID), func([]byte) {
				cancelReceived.Store(true)
			})
			Expect(err).ToNot(HaveOccurred())
			defer func() { Expect(sub.Unsubscribe()).To(Succeed()) }()

			Expect(publisher.Publish(messaging.SubjectGalleryCancel(op.ID), map[string]string{
				"op_id": op.ID,
			})).To(Succeed())

			Eventually(func() bool { return cancelReceived.Load() }, "20s").Should(BeTrue())

			// The row is what survives a replica that was not listening. The
			// broadcast is the hint to go and look at it.
			Expect(galleryStore.Cancel(op.ID)).To(Succeed())
			updated, _ := galleryStore.Get(op.ID)
			Expect(updated.Status).To(Equal("cancelled"))
		})
	})

	Context("Deduplication", func() {
		It("should deduplicate concurrent downloads of same model", func() {
			op := &distributed.GalleryOperationRecord{
				GalleryElementName: "same-model-v2",
				OpType:             "model_install",
				Status:             "downloading",
			}
			Expect(galleryStore.Create(op)).To(Succeed())

			// Another instance tries to download the same model
			dup, err := galleryStore.FindDuplicate("same-model-v2")
			Expect(err).ToNot(HaveOccurred())
			Expect(dup.ID).To(Equal(op.ID))

			// Completed operations should not be considered duplicates
			Expect(galleryStore.UpdateStatus(op.ID, "completed", "")).To(Succeed())
			_, err = galleryStore.FindDuplicate("same-model-v2")
			Expect(err).To(HaveOccurred()) // no active duplicate
		})
	})

	Context("Without --distributed", func() {
		It("should use in-memory map without --distributed", func() {
			appCfg := config.NewApplicationConfig()
			Expect(appCfg.Distributed.Enabled).To(BeFalse())

			// Without distributed mode, gallery operations use the existing
			// in-memory galleryApplier map. No PostgreSQL needed.
			//
			// The bus-URL half of this assertion went with the field it read;
			// core/config's "broker surface" spec pins its absence.
		})
	})
})
