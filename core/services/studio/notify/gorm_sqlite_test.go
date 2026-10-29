package notify_test

import (
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/studio/notify"
	"github.com/mudler/LocalAI/core/services/studio/notify/notifytest"
)

func openSQLite(path string) *gorm.DB {
	db, err := auth.OpenSQLiteFile(path)
	Expect(err).NotTo(HaveOccurred())
	sqlDB, err := db.DB()
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = sqlDB.Close() })
	return db
}

var _ = Describe("GORM on SQLite", func() {
	Describe("the notification store contract", func() {
		notifytest.Run(func() notify.Store {
			s, err := notify.NewGORM(openSQLite(filepath.Join(GinkgoT().TempDir(), "studio.db")))
			Expect(err).NotTo(HaveOccurred())
			return s
		})
	})

	It("keeps notifications across a reopen", func() {
		path := filepath.Join(GinkgoT().TempDir(), "studio.db")
		first, err := notify.NewGORM(openSQLite(path))
		Expect(err).NotTo(HaveOccurred())
		notifytest.Seed(first)
		second, err := notify.NewGORM(openSQLite(path))
		Expect(err).NotTo(HaveOccurred())
		notifytest.CheckSeed(second)
	})

	Describe("the push store contract", func() {
		notifytest.RunPush(func() notify.PushStore {
			s, err := notify.NewGORMPush(openSQLite(filepath.Join(GinkgoT().TempDir(), "push.db")))
			Expect(err).NotTo(HaveOccurred())
			return s
		})
	})

	It("keeps push subscriptions across a reopen", func() {
		path := filepath.Join(GinkgoT().TempDir(), "push.db")
		first, err := notify.NewGORMPush(openSQLite(path))
		Expect(err).NotTo(HaveOccurred())
		notifytest.SeedPush(first)
		second, err := notify.NewGORMPush(openSQLite(path))
		Expect(err).NotTo(HaveOccurred())
		notifytest.CheckPushSeed(second)
	})
})
