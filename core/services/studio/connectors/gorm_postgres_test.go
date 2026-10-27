package connectors_test

import (
	"os"
	"testing"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/connectors/connectorstest"
)

// The PostgreSQL run needs a database that tests may wipe. Set
// LOCALAI_TEST_POSTGRES_URL to enable it.
func TestGORMOnPostgres(t *testing.T) {
	url := os.Getenv("LOCALAI_TEST_POSTGRES_URL")
	if url == "" {
		t.Skip("LOCALAI_TEST_POSTGRES_URL is not set")
	}
	connectorstest.Run(t, func(t *testing.T) connectors.Store {
		db, err := gorm.Open(postgres.Open(url), &gorm.Config{})
		if err != nil {
			t.Fatal(err)
		}
		sqlDB, _ := db.DB()
		t.Cleanup(func() { _ = sqlDB.Close() })
		s, err := connectors.NewGORM(db)
		if err != nil {
			t.Fatal(err)
		}
		for _, tbl := range []string{"studio_connector_chats", "studio_connector_senders", "studio_connector_state", "studio_connector_keys", "studio_connectors"} {
			if err := db.Exec("DELETE FROM " + tbl).Error; err != nil {
				t.Fatal(err)
			}
		}
		return s
	})
}
