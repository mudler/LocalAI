package studio_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/gormstore"
)

func newFiles(t *testing.T) *files.Service {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "ws.db") + "?_busy_timeout=5000&_txlock=immediate"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	blobs, err := blob.NewFS(filepath.Join(t.TempDir(), "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	ws, err := gormstore.New(db, blobs, gormstore.Options{Admin: files.AdminPrincipal})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if raw, err := db.DB(); err == nil {
			_ = raw.Close()
		}
	})
	return files.New(ws)
}

func newServiceWithFiles(t *testing.T, h *studiotest.GateHarness, f *files.Service) *studio.Service {
	t.Helper()
	st, err := store.NewJSON("")
	if err != nil {
		t.Fatal(err)
	}
	svc, err := studio.New(studio.Deps{Store: st, Provider: provider.New(), StateDir: t.TempDir(), Harness: h, Files: f})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close(context.Background()) })
	return svc
}

func TestAChatGivesItsUserAHomeFolderWhenFilesAreOn(t *testing.T) {
	f := newFiles(t)
	h := studiotest.NewGateHarness()
	svc := newServiceWithFiles(t, h, f)
	id := create(t, svc, alice)
	sent(t, svc, alice, id, "hi")
	until(t, "the turn to start", func() bool { return len(h.Sent()) == 1 })
	h.Gate <- struct{}{}
	idle(t, svc, alice, id)

	if _, err := f.Store().Stat(context.Background(), workspace.Actor{Principal: alice}, session.HomePath(alice)); err != nil {
		t.Fatalf("the user's home does not exist after a chat: %v", err)
	}
	if _, err := f.Store().Stat(context.Background(), workspace.Actor{Principal: "bob"}, session.HomePath(alice)); !errors.Is(err, workspace.ErrNotFound) {
		t.Fatalf("another user sees the home: %v", err)
	}
}

func TestAChatWorksWithoutFiles(t *testing.T) {
	h := studiotest.NewGateHarness()
	svc := newServiceWithFiles(t, h, files.Disabled("off for this test"))
	id := create(t, svc, alice)
	sent(t, svc, alice, id, "hi")
	until(t, "the turn to start", func() bool { return len(h.Sent()) == 1 })
	h.Gate <- struct{}{}
	idle(t, svc, alice, id)
	if got := turn(t, svc, alice, id).Last; got != store.TurnDone {
		t.Fatalf("turn ended %q, want done", got)
	}
	if svc.Files().Available() {
		t.Fatal("the service reports files as available")
	}
}
