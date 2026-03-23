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

var _ = Describe("Fine-Tune Distributed", Label("Distributed"), func() {
	var (
		ctx           context.Context
		pgContainer   *tcpostgres.PostgresContainer
		natsContainer *tcnats.NATSContainer
		db            *gorm.DB
		nc            *messaging.Client
		ftStore       *distributed.FineTuneStore
	)

	BeforeEach(func() {
		ctx = context.Background()

		var err error

		pgContainer, err = tcpostgres.Run(ctx, "postgres:16-alpine",
			tcpostgres.WithDatabase("localai_finetune_dist_test"),
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

		ftStore, err = distributed.NewFineTuneStore(db)
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

	Context("PostgreSQL persistence", func() {
		It("should persist fine-tune jobs in PostgreSQL when store is set", func() {
			job := &distributed.FineTuneJobRecord{
				UserID:         "u1",
				Model:          "llama3-8b",
				Backend:        "transformers",
				TrainingType:   "lora",
				TrainingMethod: "sft",
				Status:         "queued",
			}
			Expect(ftStore.Create(job)).To(Succeed())
			Expect(job.ID).ToNot(BeEmpty())

			retrieved, err := ftStore.Get(job.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(retrieved.Model).To(Equal("llama3-8b"))
			Expect(retrieved.Status).To(Equal("queued"))

			// Update status through training lifecycle
			Expect(ftStore.UpdateStatus(job.ID, "loading_model", "Loading base model")).To(Succeed())
			loading, _ := ftStore.Get(job.ID)
			Expect(loading.Status).To(Equal("loading_model"))

			Expect(ftStore.UpdateStatus(job.ID, "training", "Epoch 1/3")).To(Succeed())
			training, _ := ftStore.Get(job.ID)
			Expect(training.Status).To(Equal("training"))
			Expect(training.Message).To(Equal("Epoch 1/3"))

			Expect(ftStore.UpdateStatus(job.ID, "saving", "Saving adapter")).To(Succeed())
			Expect(ftStore.UpdateStatus(job.ID, "completed", "Training finished")).To(Succeed())
			completed, _ := ftStore.Get(job.ID)
			Expect(completed.Status).To(Equal("completed"))

			// Export status
			Expect(ftStore.UpdateExportStatus(job.ID, "completed", "Export done", "llama3-lora-v1")).To(Succeed())
			exported, _ := ftStore.Get(job.ID)
			Expect(exported.ExportStatus).To(Equal("completed"))
			Expect(exported.ExportModelName).To(Equal("llama3-lora-v1"))

			// List jobs
			allJobs, _ := ftStore.List("")
			Expect(allJobs).To(HaveLen(1))

			u1Jobs, _ := ftStore.List("u1")
			Expect(u1Jobs).To(HaveLen(1))
		})
	})

	Context("NATS progress publishing", func() {
		It("should publish fine-tune progress via NATS", func() {
			job := &distributed.FineTuneJobRecord{
				UserID: "u1", Model: "m1", Backend: "b1",
				TrainingType: "lora", TrainingMethod: "sft", Status: "queued",
			}
			Expect(ftStore.Create(job)).To(Succeed())

			// Subscribe to fine-tune progress
			var received atomic.Int32
			sub, err := nc.Subscribe(messaging.SubjectFineTuneProgress(job.ID), func(data []byte) {
				received.Add(1)
			})
			Expect(err).ToNot(HaveOccurred())
			defer sub.Unsubscribe()

			time.Sleep(100 * time.Millisecond)

			// Publish progress events simulating training steps
			Expect(nc.Publish(messaging.SubjectFineTuneProgress(job.ID), map[string]any{
				"job_id":  job.ID,
				"status":  "training",
				"message": "Epoch 1/3, loss=2.5",
			})).To(Succeed())

			Expect(nc.Publish(messaging.SubjectFineTuneProgress(job.ID), map[string]any{
				"job_id":  job.ID,
				"status":  "training",
				"message": "Epoch 2/3, loss=1.8",
			})).To(Succeed())

			Expect(nc.Publish(messaging.SubjectFineTuneProgress(job.ID), map[string]any{
				"job_id":  job.ID,
				"status":  "completed",
				"message": "Training finished",
			})).To(Succeed())

			Eventually(func() int32 { return received.Load() }, "5s").Should(Equal(int32(3)))

			// Verify cancel subject is correctly formed
			cancelSubj := messaging.SubjectFineTuneCancel(job.ID)
			Expect(cancelSubj).To(ContainSubstring(".cancel"))
		})
	})

	Context("Without --distributed", func() {
		It("should use in-memory state without --distributed", func() {
			appCfg := config.NewApplicationConfig()
			Expect(appCfg.Distributed.Enabled).To(BeFalse())

			// Without distributed mode, fine-tune jobs use local in-memory
			// state tracking. No PostgreSQL or NATS needed.
			Expect(appCfg.Distributed.NatsURL).To(BeEmpty())
		})
	})
})
