//go:build auth

package jobs_test

import (
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/studio/jobs"
	"github.com/mudler/LocalAI/core/services/studio/jobs/jobstest"
)

func openSQLite(path string) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(path+"?_busy_timeout=5000&_txlock=immediate"), &gorm.Config{})
	Expect(err).NotTo(HaveOccurred())
	sqlDB, err := db.DB()
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = sqlDB.Close() })
	return db
}

var _ = Describe("the GORM jobs store on SQLite", func() {
	jobstest.Run(func() jobs.Store {
		s, err := jobs.NewGORM(openSQLite(filepath.Join(GinkgoT().TempDir(), "studio.db")))
		Expect(err).NotTo(HaveOccurred())
		return s
	})
})
