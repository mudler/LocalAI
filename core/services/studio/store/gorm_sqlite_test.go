//go:build auth

package store_test

import (
	"path/filepath"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/store/storetest"
)

// openSQLite opens a file database with the settings the auth database uses in
// production: a busy timeout, and the write lock taken at BEGIN.
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
	storetest.Run(t, func(t *testing.T) store.Store {
		s, err := store.NewGORM(openSQLite(t, filepath.Join(t.TempDir(), "studio.db")))
		if err != nil {
			t.Fatal(err)
		}
		return s
	})
}

// A second handle on the same database sees what the first wrote.
func TestGORMSurvivesReopenOnSQLite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "studio.db")
	open := func() store.Store {
		s, err := store.NewGORM(openSQLite(t, path))
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	storetest.Seed(t, open())
	storetest.CheckSeed(t, open())
}
