package agents_test

import (
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/agents/agentstest"
)

// The PostgreSQL run needs a database that tests may wipe. Set
// LOCALAI_TEST_POSTGRES_URL to enable it.
var _ = Describe("the GORM agent store on PostgreSQL", func() {
	url := os.Getenv("LOCALAI_TEST_POSTGRES_URL")
	if url == "" {
		It("runs the contract suite", func() {
			Skip("LOCALAI_TEST_POSTGRES_URL is not set")
		})
		return
	}
	agentstest.Run(func() agents.Store {
		db, err := gorm.Open(postgres.Open(url), &gorm.Config{})
		Expect(err).NotTo(HaveOccurred())
		sqlDB, _ := db.DB()
		DeferCleanup(func() { _ = sqlDB.Close() })
		s, err := agents.NewGORM(db)
		Expect(err).NotTo(HaveOccurred())
		Expect(db.Exec("DELETE FROM studio_agents").Error).NotTo(HaveOccurred())
		return s
	})
})
