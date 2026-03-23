package distributed_test

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/distributed"
	"github.com/mudler/LocalAI/core/services/messaging"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/testcontainers/testcontainers-go"
	tcnats "github.com/testcontainers/testcontainers-go/modules/nats"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	pgdriver "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var _ = Describe("Gallery Distributed", Label("Distributed"), func() {
	var (
		ctx           context.Context
		pgContainer   *tcpostgres.PostgresContainer
		natsContainer *tcnats.NATSContainer
		db            *gorm.DB
		nc            *messaging.Client
		galleryStore  *distributed.GalleryStore
	)

	BeforeEach(func() {
		ctx = context.Background()

		var err error

		pgContainer, err = tcpostgres.Run(ctx, "postgres:16-alpine",
			tcpostgres.WithDatabase("localai_gallery_dist_test"),
			tcpostgres.WithUsername("test"),
			tcpostgres.WithPassword("test"),
			testcontainers.WithWaitStrategy(
				wait.ForLog("database system is ready to accept connections").
					WithOccurrence(2).
					WithStartupTimeout(30*time.Second),
			),
		)
		Expect(err).ToNot(HaveOccurred())

		pgURL, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
		Expect(err).ToNot(HaveOccurred())

		db, err = gorm.Open(pgdriver.Open(pgURL), &gorm.Config{
			Logger: logger.Default.LogMode(logger.Silent),
		})
		Expect(err).ToNot(HaveOccurred())

		natsContainer, err = tcnats.Run(ctx, "nats:2-alpine")
		Expect(err).ToNot(HaveOccurred())

		natsURL, err := natsContainer.ConnectionString(ctx)
		Expect(err).ToNot(HaveOccurred())

		nc, err = messaging.New(natsURL)
		Expect(err).ToNot(HaveOccurred())

		galleryStore, err = distributed.NewGalleryStore(db)
		Expect(err).ToNot(HaveOccurred())
	})

	AfterEach(func() {
		if nc != nil {
			nc.Close()
		}
		if pgContainer != nil {
			pgContainer.Terminate(ctx)
		}
		if natsContainer != nil {
			natsContainer.Terminate(ctx)
		}
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

			// Update progress
			Expect(galleryStore.UpdateProgress(op.ID, 0.75, "75% complete", "6GB")).To(Succeed())

			updated, _ := galleryStore.Get(op.ID)
			Expect(updated.Progress).To(BeNumerically("~", 0.75, 0.01))
			Expect(updated.Message).To(Equal("75% complete"))

			// Complete
			Expect(galleryStore.UpdateStatus(op.ID, "completed", "")).To(Succeed())
			completed, _ := galleryStore.Get(op.ID)
			Expect(completed.Status).To(Equal("completed"))
		})
	})

	Context("NATS progress updates", func() {
		It("should publish progress updates via NATS", func() {
			op := &distributed.GalleryOperationRecord{
				GalleryElementName: "whisper-large",
				OpType:             "model_install",
				Status:             "downloading",
			}
			Expect(galleryStore.Create(op)).To(Succeed())

			// Subscribe to gallery progress
			var received atomic.Int32
			sub, err := nc.Subscribe(messaging.SubjectGalleryProgress(op.ID), func(data []byte) {
				received.Add(1)
			})
			Expect(err).ToNot(HaveOccurred())
			defer sub.Unsubscribe()

			time.Sleep(100 * time.Millisecond)

			// Publish progress events
			Expect(nc.Publish(messaging.SubjectGalleryProgress(op.ID), map[string]any{
				"op_id":    op.ID,
				"progress": 0.25,
				"message":  "25%",
			})).To(Succeed())

			Expect(nc.Publish(messaging.SubjectGalleryProgress(op.ID), map[string]any{
				"op_id":    op.ID,
				"progress": 0.50,
				"message":  "50%",
			})).To(Succeed())

			Eventually(func() int32 { return received.Load() }, "5s").Should(Equal(int32(2)))
		})
	})

	Context("NATS cancel across instances", func() {
		It("should cancel operation across instances via NATS", func() {
			op := &distributed.GalleryOperationRecord{
				GalleryElementName: "cancel-model",
				OpType:             "model_install",
				Status:             "downloading",
				Cancellable:        true,
			}
			Expect(galleryStore.Create(op)).To(Succeed())

			// Simulate another instance listening for cancel
			var cancelReceived atomic.Bool
			sub, err := nc.Subscribe(messaging.SubjectGalleryCancel(op.ID), func(data []byte) {
				cancelReceived.Store(true)
			})
			Expect(err).ToNot(HaveOccurred())
			defer sub.Unsubscribe()

			time.Sleep(100 * time.Millisecond)

			// Send cancel from this instance
			Expect(nc.Publish(messaging.SubjectGalleryCancel(op.ID), map[string]string{
				"op_id": op.ID,
			})).To(Succeed())

			Eventually(func() bool { return cancelReceived.Load() }, "5s").Should(BeTrue())

			// Mark cancelled in the store
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
			Expect(appCfg.Distributed.NatsURL).To(BeEmpty())
		})
	})
})
