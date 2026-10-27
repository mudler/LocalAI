//go:build auth

package store_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/studio/session"
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

// ReapTurns frees the chat and restores its messages in one transaction: when
// the restore fails the chat must stay claimed, or the lost messages could
// never be given back.
func TestGORMReapTurnsIsAtomic(t *testing.T) {
	ctx := context.Background()
	db := openSQLite(t, filepath.Join(t.TempDir(), "studio.db"))
	s, err := store.NewGORM(db)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := s.CreateChat(ctx, session.ChatRecord{ID: "a", Principal: "alice", Created: now, Updated: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddPending(ctx, "alice", "a", "m"); err != nil {
		t.Fatal(err)
	}
	if err := s.ClaimTurn(ctx, "alice", "a", "r1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.TakePending(ctx, "alice", "a"); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TRIGGER no_restore BEFORE UPDATE ON studio_pending
		BEGIN SELECT RAISE(ABORT, 'forced'); END`).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReapTurns(ctx, time.Now().Add(time.Hour)); err == nil {
		t.Fatal("ReapTurns should fail when the restore fails")
	}
	info, err := s.Turn(ctx, "alice", "a")
	if err != nil || !info.Running {
		t.Fatalf("turn after failed reap = %+v, %v; want still running", info, err)
	}
}
