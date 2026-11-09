package coordinator_test

import (
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/studio/coordinator"
	"github.com/mudler/LocalAI/core/services/studio/coordinator/coordinatortest"
)

func openSQLite(path string) *gorm.DB {
	db, err := auth.OpenSQLiteFile(path)
	Expect(err).NotTo(HaveOccurred())
	sqlDB, err := db.DB()
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = sqlDB.Close() })
	return db
}

var _ = Describe("the GORM coordinator store on SQLite", func() {
	coordinatortest.Run(func() coordinator.Store {
		s, err := coordinator.NewGORM(openSQLite(filepath.Join(GinkgoT().TempDir(), "studio.db")))
		Expect(err).NotTo(HaveOccurred())
		return s
	})

	It("survives a reopen", func() {
		path := filepath.Join(GinkgoT().TempDir(), "studio.db")
		first, err := coordinator.NewGORM(openSQLite(path))
		Expect(err).NotTo(HaveOccurred())
		c := coordinatortest.Sample("a1", "u1")
		Expect(first.Put(GinkgoT().Context(), c)).To(Succeed())
		second, err := coordinator.NewGORM(openSQLite(path))
		Expect(err).NotTo(HaveOccurred())
		got, err := second.Get(GinkgoT().Context(), "a1")
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal(c))
	})
})
