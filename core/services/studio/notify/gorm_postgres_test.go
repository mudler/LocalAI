package notify_test

import (
	"os"
	"testing"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/studio/notify"
	"github.com/mudler/LocalAI/core/services/studio/notify/notifytest"
)

// The PostgreSQL run needs a database that tests may wipe. Set
// LOCALAI_TEST_POSTGRES_URL to enable it.
func TestGORMOnPostgres(t *testing.T) {
	url := os.Getenv("LOCALAI_TEST_POSTGRES_URL")
	if url == "" {
		t.Skip("LOCALAI_TEST_POSTGRES_URL is not set")
	}
	notifytest.Run(t, func(t *testing.T) notify.Store {
		db, err := gorm.Open(postgres.Open(url), &gorm.Config{})
		if err != nil {
			t.Fatal(err)
		}
		sqlDB, _ := db.DB()
		t.Cleanup(func() { _ = sqlDB.Close() })
		s, err := notify.NewGORM(db)
		if err != nil {
			t.Fatal(err)
		}
		if err := db.Exec("DELETE FROM studio_notifications").Error; err != nil {
			t.Fatal(err)
		}
		return s
	})
}

func TestGORMPushOnPostgres(t *testing.T) {
	url := os.Getenv("LOCALAI_TEST_POSTGRES_URL")
	if url == "" {
		t.Skip("LOCALAI_TEST_POSTGRES_URL is not set")
	}
	notifytest.RunPush(t, func(t *testing.T) notify.PushStore {
		db, err := gorm.Open(postgres.Open(url), &gorm.Config{})
		if err != nil {
			t.Fatal(err)
		}
		sqlDB, _ := db.DB()
		t.Cleanup(func() { _ = sqlDB.Close() })
		s, err := notify.NewGORMPush(db)
		if err != nil {
			t.Fatal(err)
		}
		for _, tbl := range []string{"studio_push_subscriptions", "studio_push_keys"} {
			if err := db.Exec("DELETE FROM " + tbl).Error; err != nil {
				t.Fatal(err)
			}
		}
		return s
	})
}
