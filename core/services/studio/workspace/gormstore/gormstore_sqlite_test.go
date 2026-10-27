package gormstore_test

import (
	"path/filepath"
	"testing"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/gormstore"
	"github.com/mudler/LocalAI/core/services/studio/workspace/wstest"
)

// sqliteStore opens a store on a SQLite file the way LocalAI does: the pure-Go
// driver, a busy timeout and immediate write transactions.
func sqliteStore(t *testing.T, o gormstore.Options) workspace.Store {
	t.Helper()
	db, err := auth.OpenSQLiteFile(filepath.Join(t.TempDir(), "ws.db"))
	if err != nil {
		t.Fatal(err)
	}
	blobs, err := blob.NewFS(filepath.Join(t.TempDir(), "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	o.Admin = wstest.Admin
	s, err := gormstore.New(db, blobs, o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if raw, err := db.DB(); err == nil {
			_ = raw.Close()
		}
	})
	return s
}

func TestSQLiteStoreMeetsTheContract(t *testing.T) {
	wstest.RunStoreSuite(t, func(t *testing.T) workspace.Store { return sqliteStore(t, gormstore.Options{}) })
}
