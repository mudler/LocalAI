package mcpconns_test

import (
	"os"
	"path/filepath"
	"testing"

	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/studio/mcpconns"
	"github.com/mudler/LocalAI/core/services/studio/mcpconns/mcpconnstest"
	"github.com/mudler/LocalAI/pkg/sqlitedb"
)

func openSQLite(t *testing.T, path string) *gorm.DB {
	t.Helper()
	db, err := sqlitedb.Open("file:"+path+"?_busy_timeout=5000&_txlock=immediate", &gorm.Config{})
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

func TestJSONInMemory(t *testing.T) {
	mcpconnstest.Run(t, func(t *testing.T) mcpconns.Store {
		s, err := mcpconns.NewJSON("")
		if err != nil {
			t.Fatal(err)
		}
		return s
	})
}

func TestJSONOnDisk(t *testing.T) {
	mcpconnstest.Run(t, func(t *testing.T) mcpconns.Store {
		s, err := mcpconns.NewJSON(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		return s
	})
}

func TestJSONSurvivesReopenAndKeepsItsFilePrivate(t *testing.T) {
	dir := t.TempDir()
	first, _ := mcpconns.NewJSON(dir)
	mcpconnstest.Seed(t, first)
	second, err := mcpconns.NewJSON(dir)
	if err != nil {
		t.Fatal(err)
	}
	mcpconnstest.CheckSeed(t, second)
	st, err := os.Stat(filepath.Join(dir, "mcp-connectors.json"))
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("%v %v", st, err)
	}
}

func TestGORMOnSQLite(t *testing.T) {
	mcpconnstest.Run(t, func(t *testing.T) mcpconns.Store {
		s, err := mcpconns.NewGORM(openSQLite(t, filepath.Join(t.TempDir(), "c.db")))
		if err != nil {
			t.Fatal(err)
		}
		return s
	})
}

func TestGORMSurvivesReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.db")
	first, _ := mcpconns.NewGORM(openSQLite(t, path))
	mcpconnstest.Seed(t, first)
	second, err := mcpconns.NewGORM(openSQLite(t, path))
	if err != nil {
		t.Fatal(err)
	}
	mcpconnstest.CheckSeed(t, second)
}
