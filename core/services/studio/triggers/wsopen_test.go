package triggers_test

import (
	"path/filepath"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/gormstore"
	"github.com/mudler/LocalAI/core/services/studio/workspace/wstest"
	"github.com/mudler/LocalAI/pkg/sqlitedb"
)

// openWorkspace returns a workspace store on a private SQLite file. now, when not
// nil, is the clock of its change sets.
func openWorkspace(t *testing.T, now func() time.Time) workspace.Store {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "ws.db") + "?_busy_timeout=5000&_txlock=immediate"
	db, err := sqlitedb.Open(dsn, &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	blobs, err := blob.NewFS(filepath.Join(t.TempDir(), "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	ws, err := gormstore.New(db, blobs, gormstore.Options{Admin: wstest.Admin, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return ws
}
