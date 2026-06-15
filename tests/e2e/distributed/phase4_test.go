package distributed_test

import (
	"github.com/mudler/LocalAI/core/services/distributed"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	pgdriver "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var _ = Describe("Phase 4: MCP, Skills, Gallery, Fine-Tuning", Label("Distributed"), func() {
	var (
		infra  *TestInfra
		db     *gorm.DB
		stores *distributed.Stores
	)

	BeforeEach(func() {
		infra = SetupInfra("localai_phase4_test")

		var err error
		db, err = gorm.Open(pgdriver.Open(infra.PGURL), &gorm.Config{
			Logger: logger.Default.LogMode(logger.Silent),
		})
		Expect(err).ToNot(HaveOccurred())

		stores, err = distributed.InitStores(db)
		Expect(err).ToNot(HaveOccurred())
	})

	Context("Skills Metadata", func() {
		It("should store skills metadata in PostgreSQL", func() {
			rec := &distributed.SkillMetadataRecord{
				UserID:     "u1",
				Name:       "web-search",
				Definition: "# Web Search\nSearches the web.",
				SourceType: "inline",
				Enabled:    true,
			}
			Expect(stores.Skills.Save(rec)).To(Succeed())
			Expect(rec.ID).ToNot(BeEmpty())

			retrieved, err := stores.Skills.Get("u1", "web-search")
			Expect(err).ToNot(HaveOccurred())
			Expect(retrieved.Definition).To(ContainSubstring("Web Search"))
		})

		It("should list skills for a user", func() {
			stores.Skills.Save(&distributed.SkillMetadataRecord{UserID: "u1", Name: "skill-a", SourceType: "inline"})
			stores.Skills.Save(&distributed.SkillMetadataRecord{UserID: "u1", Name: "skill-b", SourceType: "git", SourceURL: "https://github.com/example/skill"})
			stores.Skills.Save(&distributed.SkillMetadataRecord{UserID: "u2", Name: "skill-c", SourceType: "inline"})

			u1Skills, _ := stores.Skills.List("u1")
			Expect(u1Skills).To(HaveLen(2))

			allSkills, _ := stores.Skills.List("")
			Expect(allSkills).To(HaveLen(3))
		})

		It("should list git skills for sync", func() {
			stores.Skills.Save(&distributed.SkillMetadataRecord{UserID: "u1", Name: "git-skill", SourceType: "git", SourceURL: "https://github.com/example", Enabled: true})
			stores.Skills.Save(&distributed.SkillMetadataRecord{UserID: "u1", Name: "inline-skill", SourceType: "inline", Enabled: true})

			gitSkills, err := stores.Skills.ListGitSkills()
			Expect(err).ToNot(HaveOccurred())
			Expect(gitSkills).To(HaveLen(1))
			Expect(gitSkills[0].Name).To(Equal("git-skill"))
		})

		It("should delete skill", func() {
			stores.Skills.Save(&distributed.SkillMetadataRecord{UserID: "u1", Name: "deleteme", SourceType: "inline"})

			Expect(stores.Skills.Delete("u1", "deleteme")).To(Succeed())

			_, err := stores.Skills.Get("u1", "deleteme")
			Expect(err).To(HaveOccurred())
		})
	})

	Context("Gallery Operations", func() {
		It("should track gallery operations in PostgreSQL", func() {
			op := &distributed.GalleryOperationRecord{
				GalleryElementName: "llama3-8b",
				OpType:             "model_install",
				Status:             "downloading",
				Cancellable:        true,
				FrontendID:         "f1",
			}
			Expect(stores.Gallery.Create(op)).To(Succeed())
			Expect(op.ID).ToNot(BeEmpty())

			retrieved, err := stores.Gallery.Get(op.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(retrieved.GalleryElementName).To(Equal("llama3-8b"))
			Expect(retrieved.Status).To(Equal("downloading"))
		})

		It("should update download progress", func() {
			op := &distributed.GalleryOperationRecord{
				GalleryElementName: "model1", OpType: "model_install", Status: "downloading",
			}
			stores.Gallery.Create(op)

			Expect(stores.Gallery.UpdateProgress(op.ID, 0.5, "50% complete", "2GB")).To(Succeed())

			updated, _ := stores.Gallery.Get(op.ID)
			Expect(updated.Progress).To(BeNumerically("~", 0.5, 0.01))
			Expect(updated.Message).To(Equal("50% complete"))
		})

		It("should deduplicate concurrent downloads", func() {
			op := &distributed.GalleryOperationRecord{
				GalleryElementName: "same-model", OpType: "model_install", Status: "downloading",
			}
			stores.Gallery.Create(op)

			dup, err := stores.Gallery.FindDuplicate("same-model")
			Expect(err).ToNot(HaveOccurred())
			Expect(dup.ID).To(Equal(op.ID))
		})

		It("should cancel download", func() {
			op := &distributed.GalleryOperationRecord{
				GalleryElementName: "cancel-me", OpType: "model_install", Status: "downloading",
			}
			stores.Gallery.Create(op)

			Expect(stores.Gallery.Cancel(op.ID)).To(Succeed())

			updated, _ := stores.Gallery.Get(op.ID)
			Expect(updated.Status).To(Equal("cancelled"))
		})

		It("should list operations by status", func() {
			stores.Gallery.Create(&distributed.GalleryOperationRecord{GalleryElementName: "m1", OpType: "model_install", Status: "completed"})
			stores.Gallery.Create(&distributed.GalleryOperationRecord{GalleryElementName: "m2", OpType: "model_install", Status: "downloading"})

			downloading, _ := stores.Gallery.List("downloading")
			Expect(downloading).To(HaveLen(1))
			Expect(downloading[0].GalleryElementName).To(Equal("m2"))

			all, _ := stores.Gallery.List("")
			Expect(all).To(HaveLen(2))
		})
	})

	Context("Fine-Tune Jobs", func() {
		It("should track fine-tune jobs in PostgreSQL", func() {
			job := &distributed.FineTuneJobRecord{
				UserID:         "u1",
				Model:          "llama3-8b",
				Backend:        "transformers",
				TrainingType:   "lora",
				TrainingMethod: "sft",
				Status:         "queued",
			}
			Expect(stores.FineTune.Create(job)).To(Succeed())
			Expect(job.ID).ToNot(BeEmpty())

			retrieved, err := stores.FineTune.Get(job.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(retrieved.Model).To(Equal("llama3-8b"))
			Expect(retrieved.Status).To(Equal("queued"))
		})

		It("should update fine-tune job status", func() {
			job := &distributed.FineTuneJobRecord{
				UserID: "u1", Model: "m1", Backend: "b1",
				TrainingType: "lora", TrainingMethod: "sft", Status: "queued",
			}
			stores.FineTune.Create(job)

			Expect(stores.FineTune.UpdateStatus(job.ID, "training", "Epoch 1/3")).To(Succeed())

			updated, _ := stores.FineTune.Get(job.ID)
			Expect(updated.Status).To(Equal("training"))
			Expect(updated.Message).To(Equal("Epoch 1/3"))
		})

		It("should update export status", func() {
			job := &distributed.FineTuneJobRecord{
				UserID: "u1", Model: "m1", Backend: "b1",
				TrainingType: "lora", TrainingMethod: "sft", Status: "completed",
			}
			stores.FineTune.Create(job)

			Expect(stores.FineTune.UpdateExportStatus(job.ID, "completed", "Export done", "llama3-lora-v1")).To(Succeed())

			updated, _ := stores.FineTune.Get(job.ID)
			Expect(updated.ExportStatus).To(Equal("completed"))
			Expect(updated.ExportModelName).To(Equal("llama3-lora-v1"))
		})

		It("should list fine-tune jobs for user", func() {
			stores.FineTune.Create(&distributed.FineTuneJobRecord{UserID: "u1", Model: "m1", Backend: "b1", TrainingType: "lora", TrainingMethod: "sft", Status: "completed"})
			stores.FineTune.Create(&distributed.FineTuneJobRecord{UserID: "u1", Model: "m2", Backend: "b1", TrainingType: "full", TrainingMethod: "dpo", Status: "training"})
			stores.FineTune.Create(&distributed.FineTuneJobRecord{UserID: "u2", Model: "m3", Backend: "b1", TrainingType: "lora", TrainingMethod: "sft", Status: "queued"})

			u1Jobs, _ := stores.FineTune.List("u1")
			Expect(u1Jobs).To(HaveLen(2))

			allJobs, _ := stores.FineTune.List("")
			Expect(allJobs).To(HaveLen(3))
		})

		It("should delete fine-tune job", func() {
			job := &distributed.FineTuneJobRecord{
				UserID: "u1", Model: "m1", Backend: "b1",
				TrainingType: "lora", TrainingMethod: "sft", Status: "failed",
			}
			stores.FineTune.Create(job)

			Expect(stores.FineTune.Delete(job.ID)).To(Succeed())

			_, err := stores.FineTune.Get(job.ID)
			Expect(err).To(HaveOccurred())
		})
	})
})
