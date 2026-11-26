//go:build auth

package store_test

import (
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/plugins/store"
	"github.com/mudler/LocalAI/core/services/plugins/store/storetest"
)

// The PostgreSQL run needs a database that tests may wipe. Set
// LOCALAI_TEST_POSTGRES_URL to enable it.
var _ = Describe("the GORM plugins store on PostgreSQL", func() {
	url := os.Getenv("LOCALAI_TEST_POSTGRES_URL")
	if url == "" {
		It("runs the contract suite", func() {
			Skip("LOCALAI_TEST_POSTGRES_URL is not set")
		})
		return
	}
	storetest.Run(func() store.Store {
		db, err := gorm.Open(postgres.Open(url), &gorm.Config{})
		Expect(err).NotTo(HaveOccurred())
		sqlDB, _ := db.DB()
		DeferCleanup(func() { _ = sqlDB.Close() })
		s, err := store.NewGormStore(db)
		Expect(err).NotTo(HaveOccurred())
		for _, tbl := range []string{"plugin_user_state", "plugin_installs", "plugin_packages", "plugin_marketplace_snapshots", "plugin_marketplaces"} {
			Expect(db.Exec("DELETE FROM " + tbl).Error).NotTo(HaveOccurred())
		}
		return s
	})
})
