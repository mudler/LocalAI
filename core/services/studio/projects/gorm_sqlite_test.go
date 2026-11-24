package projects_test

import (
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/studio/projects"
	"github.com/mudler/LocalAI/core/services/studio/projects/projectstest"
)

func openSQLite(path string) *gorm.DB {
	db, err := auth.OpenSQLiteFile(path)
	Expect(err).NotTo(HaveOccurred())
	sqlDB, err := db.DB()
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = sqlDB.Close() })
	return db
}

var _ = Describe("the GORM project store on SQLite", func() {
	projectstest.Run(func() projects.Store {
		s, err := projects.NewGORM(openSQLite(filepath.Join(GinkgoT().TempDir(), "p.db")))
		Expect(err).NotTo(HaveOccurred())
		return s
	})

	It("survives a reopen", func() {
		path := filepath.Join(GinkgoT().TempDir(), "p.db")
		first, _ := projects.NewGORM(openSQLite(path))
		projectstest.Seed(first)
		second, err := projects.NewGORM(openSQLite(path))
		Expect(err).NotTo(HaveOccurred())
		projectstest.CheckSeed(second)
	})
})
