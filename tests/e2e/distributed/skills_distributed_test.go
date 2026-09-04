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

	// The "NATS cache invalidation" Context that stood here is deleted with the
	// two builders it was the only caller of.
	//
	// Its two Its published on messaging.SubjectCacheInvalidateSkills and
	// messaging.SubjectCacheInvalidateCollection through one client and counted
	// their own deliveries back. Their own comment admitted the publish was
	// simulated ("in production this is done by the service layer"), and no
	// service layer published on either subject: the two subjects had no
	// production publisher and no production subscriber, on any carrier. Only
	// the model and backend caches have cross-replica invalidation (see
	// galleryop.Service), and they are untouched here.
	//
	// So what these Its actually pinned was the carrier round trip, which
	// core/services/pgbus/bus_test.go pins on two instances, and the subject
	// literals, which core/services/messaging/subjects_wire_test.go now pins
	// directly for every subject that survives.

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
