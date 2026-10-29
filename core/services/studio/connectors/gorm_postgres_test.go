package connectors_test

import (
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/connectors/connectorstest"
)

// The PostgreSQL run needs a database that tests may wipe. Set
// LOCALAI_TEST_POSTGRES_URL to enable it.
var _ = Describe("GORM on PostgreSQL", func() {
	url := os.Getenv("LOCALAI_TEST_POSTGRES_URL")
	if url == "" {
		It("runs the store contract", func() {
			Skip("LOCALAI_TEST_POSTGRES_URL is not set")
		})
		return
	}
	connectorstest.Run(func() connectors.Store {
		db, err := gorm.Open(postgres.Open(url), &gorm.Config{})
		Expect(err).NotTo(HaveOccurred())
		sqlDB, _ := db.DB()
		DeferCleanup(func() { _ = sqlDB.Close() })
		s, err := connectors.NewGORM(db)
		Expect(err).NotTo(HaveOccurred())
		for _, tbl := range []string{"studio_connector_chats", "studio_connector_senders", "studio_connector_state", "studio_connector_keys", "studio_connectors"} {
			Expect(db.Exec("DELETE FROM " + tbl).Error).To(Succeed())
		}
		return s
	})
})
