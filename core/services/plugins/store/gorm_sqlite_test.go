//go:build auth

package store_test

import (
	"context"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/advisorylock"
	"github.com/mudler/LocalAI/core/services/plugins/store"
	"github.com/mudler/LocalAI/core/services/plugins/store/storetest"
)

func openSQLite(path string) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(path+"?_busy_timeout=5000&_txlock=immediate"), &gorm.Config{})
	Expect(err).NotTo(HaveOccurred())
	sqlDB, err := db.DB()
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = sqlDB.Close() })
	return db
}

var _ = Describe("the GORM plugins store on SQLite", func() {
	storetest.Run(func() store.Store {
		s, err := store.NewGormStore(openSQLite(filepath.Join(GinkgoT().TempDir(), "plugins.db")))
		Expect(err).NotTo(HaveOccurred())
		return s
	})

	It("refuses a nil database", func() {
		_, err := store.NewGormStore(nil)
		Expect(err).To(HaveOccurred())
	})

	// On SQLite the lock is an in-process mutex per key, which a test can hold.
	It("migrates under the schema-migrate lock", func() {
		db := openSQLite(filepath.Join(GinkgoT().TempDir(), "plugins.db"))
		done := make(chan error, 1)
		err := advisorylock.WithLockCtx(context.Background(), db, advisorylock.KeySchemaMigrate, func() error {
			go func() {
				defer GinkgoRecover()
				_, e := store.NewGormStore(db)
				done <- e
			}()
			select {
			case <-done:
				Fail("NewGormStore migrated while the schema-migrate lock was held")
			case <-time.After(300 * time.Millisecond):
			}
			return nil
		})
		Expect(err).NotTo(HaveOccurred())
		Eventually(done, 5*time.Second).Should(Receive(BeNil()))
	})
})
