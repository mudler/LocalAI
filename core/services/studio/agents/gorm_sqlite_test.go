//go:build auth

package agents_test

import (
	"path/filepath"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/agents/agentstest"
)

func openSQLite(t *testing.T, path string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(path+"?_busy_timeout=5000&_txlock=immediate"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

func TestGORMOnSQLite(t *testing.T) {
	agentstest.Run(t, func(t *testing.T) agents.Store {
		s, err := agents.NewGORM(openSQLite(t, filepath.Join(t.TempDir(), "studio.db")))
		if err != nil {
			t.Fatal(err)
		}
		return s
	})
}

func TestGORMSurvivesReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "studio.db")
	first, err := agents.NewGORM(openSQLite(t, path))
	if err != nil {
		t.Fatal(err)
	}
	agentstest.Seed(t, first)
	second, err := agents.NewGORM(openSQLite(t, path))
	if err != nil {
		t.Fatal(err)
	}
	agentstest.CheckSeed(t, second)
}
