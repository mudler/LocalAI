package connectors_test

import (
	"path/filepath"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/connectors/connectorstest"
)

func openSQLite(t *testing.T, path string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+path+"?_busy_timeout=5000&_txlock=immediate"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

func TestGORMOnSQLite(t *testing.T) {
	connectorstest.Run(t, func(t *testing.T) connectors.Store {
		s, err := connectors.NewGORM(openSQLite(t, filepath.Join(t.TempDir(), "c.db")))
		if err != nil {
			t.Fatal(err)
		}
		return s
	})
}

func TestGORMSurvivesReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.db")
	first, _ := connectors.NewGORM(openSQLite(t, path))
	connectorstest.Seed(t, first)
	second, err := connectors.NewGORM(openSQLite(t, path))
	if err != nil {
		t.Fatal(err)
	}
	connectorstest.CheckSeed(t, second)
}
