package notify_test

import (
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/studio/notify"
	"github.com/mudler/LocalAI/core/services/studio/notify/notifytest"
)

// The PostgreSQL run needs a database that tests may wipe. Set
// LOCALAI_TEST_POSTGRES_URL to enable it.
var postgresURL = os.Getenv("LOCALAI_TEST_POSTGRES_URL")

func openPostgres() *gorm.DB {
	db, err := gorm.Open(postgres.Open(postgresURL), &gorm.Config{})
	Expect(err).NotTo(HaveOccurred())
	sqlDB, _ := db.DB()
	DeferCleanup(func() { _ = sqlDB.Close() })
	return db
}

var _ = Describe("GORM on PostgreSQL", func() {
	Describe("the notification store contract", func() {
		if postgresURL == "" {
			It("runs the contract", func() { Skip("LOCALAI_TEST_POSTGRES_URL is not set") })
			return
		}
		notifytest.Run(func() notify.Store {
			db := openPostgres()
			s, err := notify.NewGORM(db)
			Expect(err).NotTo(HaveOccurred())
			Expect(db.Exec("DELETE FROM studio_notifications").Error).To(Succeed())
			return s
		})
	})

	Describe("the push store contract", func() {
		if postgresURL == "" {
			It("runs the contract", func() { Skip("LOCALAI_TEST_POSTGRES_URL is not set") })
			return
		}
		notifytest.RunPush(func() notify.PushStore {
			db := openPostgres()
			s, err := notify.NewGORMPush(db)
			Expect(err).NotTo(HaveOccurred())
			for _, tbl := range []string{"studio_push_subscriptions", "studio_push_keys"} {
				Expect(db.Exec("DELETE FROM " + tbl).Error).To(Succeed())
			}
			return s
		})
	})
})
