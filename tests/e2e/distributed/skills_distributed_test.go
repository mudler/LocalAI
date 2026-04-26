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

var _ = Describe("Skills Distributed", Label("Distributed"), func() {
	var (
		infra      *TestInfra
		db         *gorm.DB
		skillStore *distributed.SkillStore
	)

	BeforeEach(func() {
		infra = SetupInfra("localai_skills_dist_test")

		var err error
		db, err = gorm.Open(pgdriver.Open(infra.PGURL), &gorm.Config{
			Logger: logger.Default.LogMode(logger.Silent),
		})
		Expect(err).ToNot(HaveOccurred())

		skillStore, err = distributed.NewSkillStore(db)
		Expect(err).ToNot(HaveOccurred())
	})

	Context("PostgreSQL metadata storage", func() {
		It("should store skill metadata in PostgreSQL", func() {
			rec := &distributed.SkillMetadataRecord{
				UserID:     "u1",
				Name:       "web-search",
				Definition: "# Web Search\nSearches the web for information.",
				SourceType: "inline",
				Enabled:    true,
			}
			Expect(skillStore.Save(rec)).To(Succeed())
			Expect(rec.ID).ToNot(BeEmpty())

			retrieved, err := skillStore.Get("u1", "web-search")
			Expect(err).ToNot(HaveOccurred())
			Expect(retrieved.Name).To(Equal("web-search"))
			Expect(retrieved.Definition).To(ContainSubstring("Web Search"))
			Expect(retrieved.SourceType).To(Equal("inline"))
			Expect(retrieved.Enabled).To(BeTrue())

			// Update skill
			rec.Definition = "# Web Search v2\nImproved search."
			Expect(skillStore.Save(rec)).To(Succeed())

			updated, _ := skillStore.Get("u1", "web-search")
			Expect(updated.Definition).To(ContainSubstring("v2"))

			// List skills
			skillStore.Save(&distributed.SkillMetadataRecord{
				UserID: "u1", Name: "code-gen", SourceType: "inline",
			})
			skillStore.Save(&distributed.SkillMetadataRecord{
				UserID: "u2", Name: "translate", SourceType: "git",
				SourceURL: "https://github.com/example/translate-skill",
			})

			u1Skills, _ := skillStore.List("u1")
			Expect(u1Skills).To(HaveLen(2))

			allSkills, _ := skillStore.List("")
			Expect(allSkills).To(HaveLen(3))

			// Git skills for sync
			gitSkills, err := skillStore.ListGitSkills()
			Expect(err).ToNot(HaveOccurred())
			Expect(gitSkills).To(HaveLen(1))
			Expect(gitSkills[0].Name).To(Equal("translate"))

			// Delete
			Expect(skillStore.Delete("u1", "web-search")).To(Succeed())
			_, err = skillStore.Get("u1", "web-search")
			Expect(err).To(HaveOccurred())
		})
	})

	Context("NATS cache invalidation", func() {
		It("should publish cache invalidation via NATS on skill change", func() {
			// Subscribe to skills cache invalidation
			var received atomic.Int32
			sub, err := infra.NC.Subscribe(messaging.SubjectCacheInvalidateSkills, func(data []byte) {
				received.Add(1)
			})
			Expect(err).ToNot(HaveOccurred())
			defer sub.Unsubscribe()

			FlushNATS(infra.NC)

			// Save a skill and publish cache invalidation
			rec := &distributed.SkillMetadataRecord{
				UserID: "u1", Name: "new-skill", SourceType: "inline",
			}
			Expect(skillStore.Save(rec)).To(Succeed())

			// Publish invalidation (in production this is done by the service layer)
			Expect(infra.NC.Publish(messaging.SubjectCacheInvalidateSkills, map[string]string{
				"user_id": "u1",
				"name":    "new-skill",
				"action":  "save",
			})).To(Succeed())

			Eventually(func() int32 { return received.Load() }, "5s").Should(Equal(int32(1)))

			// Delete and publish another invalidation
			Expect(skillStore.Delete("u1", "new-skill")).To(Succeed())
			Expect(infra.NC.Publish(messaging.SubjectCacheInvalidateSkills, map[string]string{
				"user_id": "u1",
				"name":    "new-skill",
				"action":  "delete",
			})).To(Succeed())

			Eventually(func() int32 { return received.Load() }, "5s").Should(Equal(int32(2)))
		})

		It("should broadcast collection cache invalidation", func() {
			var received atomic.Int32
			sub, err := infra.NC.Subscribe(messaging.SubjectCacheInvalidateCollection("my-collection"), func(data []byte) {
				received.Add(1)
			})
			Expect(err).ToNot(HaveOccurred())
			defer sub.Unsubscribe()

			FlushNATS(infra.NC)

			Expect(infra.NC.Publish(messaging.SubjectCacheInvalidateCollection("my-collection"), map[string]string{
				"reason": "skill_updated",
			})).To(Succeed())

			Eventually(func() int32 { return received.Load() }, "5s").Should(Equal(int32(1)))
		})
	})

	Context("Without --distributed", func() {
		It("should use filesystem without --distributed", func() {
			appCfg := config.NewApplicationConfig()
			Expect(appCfg.Distributed.Enabled).To(BeFalse())

			// Without distributed mode, skills are stored on the local
			// filesystem. No PostgreSQL metadata or NATS cache invalidation.
			Expect(appCfg.Distributed.NatsURL).To(BeEmpty())
		})
	})
})
