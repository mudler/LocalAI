package agents_test

import (
	"os"
	"testing"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/agents/agentstest"
)

// The PostgreSQL run needs a database that tests may wipe. Set
// LOCALAI_TEST_POSTGRES_URL to enable it.
func TestGORMOnPostgres(t *testing.T) {
	url := os.Getenv("LOCALAI_TEST_POSTGRES_URL")
	if url == "" {
		t.Skip("LOCALAI_TEST_POSTGRES_URL is not set")
	}
	agentstest.Run(t, func(t *testing.T) agents.Store {
		db, err := gorm.Open(postgres.Open(url), &gorm.Config{})
		if err != nil {
			t.Fatal(err)
		}
		sqlDB, _ := db.DB()
		t.Cleanup(func() { _ = sqlDB.Close() })
		s, err := agents.NewGORM(db)
		if err != nil {
			t.Fatal(err)
		}
		if err := db.Exec("DELETE FROM studio_agents").Error; err != nil {
			t.Fatal(err)
		}
		return s
	})
}
