package projects_test

import (
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/studio/projects"
	"github.com/mudler/LocalAI/core/services/studio/projects/projectstest"
)

// The PostgreSQL run needs a database that tests may wipe. Set
// LOCALAI_TEST_POSTGRES_URL to enable it.
var _ = Describe("the GORM project store on PostgreSQL", func() {
	url := os.Getenv("LOCALAI_TEST_POSTGRES_URL")
	if url == "" {
		It("runs the contract suite", func() {
			Skip("LOCALAI_TEST_POSTGRES_URL is not set")
		})
		return
	}
	projectstest.Run(func() projects.Store {
		db, err := gorm.Open(postgres.Open(url), &gorm.Config{})
		Expect(err).NotTo(HaveOccurred())
		sqlDB, _ := db.DB()
		DeferCleanup(func() { _ = sqlDB.Close() })
		s, err := projects.NewGORM(db)
		Expect(err).NotTo(HaveOccurred())
		Expect(db.Exec("DELETE FROM studio_projects").Error).NotTo(HaveOccurred())
		return s
	})
})
