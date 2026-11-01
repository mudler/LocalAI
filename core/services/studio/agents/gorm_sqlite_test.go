package agents_test

import (
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/agents/agentstest"
)

func openSQLite(path string) *gorm.DB {
	db, err := auth.OpenSQLiteFile(path)
	Expect(err).NotTo(HaveOccurred())
	sqlDB, err := db.DB()
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = sqlDB.Close() })
	return db
}

var _ = Describe("the GORM agent store on SQLite", func() {
	agentstest.Run(func() agents.Store {
		s, err := agents.NewGORM(openSQLite(filepath.Join(GinkgoT().TempDir(), "studio.db")))
		Expect(err).NotTo(HaveOccurred())
		return s
	})

	It("survives a reopen", func() {
		path := filepath.Join(GinkgoT().TempDir(), "studio.db")
		first, err := agents.NewGORM(openSQLite(path))
		Expect(err).NotTo(HaveOccurred())
		agentstest.Seed(first)
		second, err := agents.NewGORM(openSQLite(path))
		Expect(err).NotTo(HaveOccurred())
		agentstest.CheckSeed(second)
	})
})
