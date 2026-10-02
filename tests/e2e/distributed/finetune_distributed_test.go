package distributed_test

import (
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/distributed"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	pgdriver "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var _ = Describe("Fine-Tune Distributed", Label("Distributed"), func() {
	var (
		infra   *TestInfra
		db      *gorm.DB
		ftStore *distributed.FineTuneStore
	)

	BeforeEach(func() {
		infra = SetupInfra("localai_finetune_dist_test")

		var err error
		db, err = gorm.Open(pgdriver.Open(infra.PGURL), &gorm.Config{
			Logger: logger.Default.LogMode(logger.Silent),
		})
		Expect(err).ToNot(HaveOccurred())

		ftStore, err = distributed.NewFineTuneStore(db)
		Expect(err).ToNot(HaveOccurred())
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

	// The "NATS progress publishing" Context that stood here is deleted with
	// the two builders it was the only caller of.
	//
	// What it did was subscribe and publish on messaging.SubjectFineTuneProgress
	// through one client and count three deliveries, then assert that
	// SubjectFineTuneCancel contained ".cancel". No production code had
	// published on either subject since fine-tune progress moved to the
	// broadcast carrier, so the round trip pinned the carrier and not this
	// feature, and the string assertion passed for any literal ending in
	// ".cancel".
	//
	// The carrier round trip is core/services/pgbus/bus_test.go, on two
	// separate bus instances. That the surviving builders mint the exact
	// subjects their subscribers filter on is now the literal table in
	// core/services/messaging/subjects_wire_test.go, which is a stronger pin
	// than a round trip through the builder could ever be.

	Context("Without --distributed", func() {
		It("should use in-memory state without --distributed", func() {
			appCfg := config.NewApplicationConfig()
			Expect(appCfg.Distributed.Enabled).To(BeFalse())

			// Without distributed mode, fine-tune jobs use local in-memory
			// state tracking. No PostgreSQL needed.
			//
			// The "and no bus URL" half of this assertion is gone with the
			// field it read: DistributedConfig has nowhere to hold one, which
			// core/config's "broker surface" spec pins for every config rather
			// than for this one.
		})
	})
})
