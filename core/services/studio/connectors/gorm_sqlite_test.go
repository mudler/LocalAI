package connectors_test

import (
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/connectors/connectorstest"
	"github.com/mudler/LocalAI/pkg/sqlitedb"
)

func openSQLite(path string) *gorm.DB {
	db, err := sqlitedb.Open("file:"+path+"?_busy_timeout=5000&_txlock=immediate", &gorm.Config{})
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

var _ = Describe("GORM on SQLite", func() {
	Describe("the store contract", func() {
		connectorstest.Run(func() connectors.Store {
			s, err := connectors.NewGORM(openSQLite(filepath.Join(GinkgoT().TempDir(), "c.db")))
			Expect(err).NotTo(HaveOccurred())
			return s
		})
	})

	It("survives a reopen", func() {
		path := filepath.Join(GinkgoT().TempDir(), "c.db")
		first, _ := connectors.NewGORM(openSQLite(path))
		connectorstest.Seed(first)
		second, err := connectors.NewGORM(openSQLite(path))
		Expect(err).NotTo(HaveOccurred())
		connectorstest.CheckSeed(second)
	})
})
