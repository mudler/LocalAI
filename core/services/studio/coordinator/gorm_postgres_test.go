package coordinator_test

import (
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/studio/coordinator"
	"github.com/mudler/LocalAI/core/services/studio/coordinator/coordinatortest"
)

// The PostgreSQL run needs a database that tests may wipe. Set
// LOCALAI_TEST_POSTGRES_URL to enable it.
var _ = Describe("the GORM coordinator store on PostgreSQL", func() {
	url := os.Getenv("LOCALAI_TEST_POSTGRES_URL")
	if url == "" {
		It("runs the contract suite", func() {
			Skip("LOCALAI_TEST_POSTGRES_URL is not set")
		})
		return
	}
	coordinatortest.Run(func() coordinator.Store {
		db, err := gorm.Open(postgres.Open(url), &gorm.Config{})
		Expect(err).NotTo(HaveOccurred())
		sqlDB, _ := db.DB()
		DeferCleanup(func() { _ = sqlDB.Close() })
		s, err := coordinator.NewGORM(db)
		Expect(err).NotTo(HaveOccurred())
		Expect(db.Exec("DELETE FROM studio_coordinators").Error).NotTo(HaveOccurred())
		Expect(db.Exec("DELETE FROM studio_coordinator_counters").Error).NotTo(HaveOccurred())
		return s
	})
})
