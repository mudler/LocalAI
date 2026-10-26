package files_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/session"
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

func TestEachUserGetsAHomeOfTheirOwn(t *testing.T) {
	ctx := context.Background()
	f := newFiles(t)
	for _, u := range []string{"alice", "bob"} {
		if err := f.EnsureHome(ctx, u); err != nil {
			t.Fatalf("EnsureHome(%s): %v", u, err)
		}
	}
	alice, bob := workspace.Actor{Principal: "alice"}, workspace.Actor{Principal: "bob"}
	if _, err := f.Store().Put(ctx, alice, session.HomePath("alice")+"/notes.txt", strings.NewReader("mine"), workspace.PutOptions{}); err != nil {
		t.Fatalf("alice cannot write in her home: %v", err)
	}
	if _, err := f.Store().Stat(ctx, alice, session.ChatsPath("alice")); err != nil {
		t.Fatalf("alice has no Chats folder: %v", err)
	}
	// Bob cannot see Alice's home, its files, or even that it exists.
	for _, p := range []string{session.HomePath("alice"), session.HomePath("alice") + "/notes.txt", session.UsersRoot} {
		if _, err := f.Store().Stat(ctx, bob, p); !errors.Is(err, workspace.ErrNotFound) {
			t.Errorf("bob Stat(%s) = %v, want ErrNotFound", p, err)
		}
	}
	if _, err := f.Store().Put(ctx, bob, session.HomePath("alice")+"/x.txt", strings.NewReader("x"), workspace.PutOptions{}); !errors.Is(err, workspace.ErrNotFound) {
		t.Errorf("bob Put into alice's home = %v, want ErrNotFound", err)
	}
	// Asking again changes nothing and does not fail.
	if err := f.EnsureHome(ctx, "alice"); err != nil {
		t.Fatalf("second EnsureHome: %v", err)
	}
}

func TestNobodyCanTakeTheAdminPrincipalOrAnUnsafeOne(t *testing.T) {
	f := newFiles(t)
	for _, p := range []string{files.AdminPrincipal, "", "a/b", "..", "a\x00b"} {
		if err := f.EnsureHome(context.Background(), p); err == nil {
			t.Errorf("EnsureHome(%q) succeeded, want an error", p)
		}
	}
}

func TestWithoutAWorkspaceTheServiceSaysWhy(t *testing.T) {
	f := files.Disabled("There is no data path, so files are off.")
	if f.Available() || f.Unavailable() == "" || f.Store() != nil {
		t.Fatalf("a disabled service = available %v, reason %q, store %v", f.Available(), f.Unavailable(), f.Store())
	}
	if err := f.EnsureHome(context.Background(), "alice"); !errors.Is(err, files.ErrUnavailable) {
		t.Fatalf("EnsureHome on a disabled service = %v, want ErrUnavailable", err)
	}
}

func TestPathsAreRelativeToTheUsersHomeAndCannotEscapeIt(t *testing.T) {
	f := newFiles(t)
	abs, err := f.Abs("alice", "/Reports/q1.txt")
	if err != nil || abs != session.HomePath("alice")+"/Reports/q1.txt" {
		t.Fatalf("Abs = %q, %v", abs, err)
	}
	if got := f.Rel("alice", abs); got != "/Reports/q1.txt" {
		t.Fatalf("Rel = %q", got)
	}
	if got, _ := f.Abs("alice", "/"); got != session.HomePath("alice") {
		t.Fatalf("Abs(/) = %q, want the home", got)
	}
	for _, bad := range []string{"/../bob", "/a/../../bob", "/a\x00b", "/a//../..", "/.."} {
		if got, err := f.Abs("alice", bad); err == nil {
			t.Errorf("Abs(%q) = %q, want an error", bad, got)
		}
	}
	// A path in another home is not shown as a relative path.
	if got := f.Rel("alice", session.HomePath("bob")+"/x"); got != "" {
		t.Errorf("Rel of another home = %q, want empty", got)
	}
}

func TestADeletedFileIsInTheTrashUntilItIsRestored(t *testing.T) {
	ctx := context.Background()
	f := newFiles(t)
	if err := f.EnsureHome(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	alice := f.Actor("alice")
	put := func(rel string) {
		abs, _ := f.Abs("alice", rel)
		if _, err := f.Store().Put(ctx, alice, abs, strings.NewReader("data"), workspace.PutOptions{Overwrite: true}); err != nil {
			t.Fatal(err)
		}
	}
	put("/Docs/a.txt")
	put("/Docs/b.txt")
	absDocs, _ := f.Abs("alice", "/Docs")
	if err := f.Store().Delete(ctx, alice, absDocs); err != nil {
		t.Fatal(err)
	}
	trash, err := f.Trash(ctx, "alice", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(trash) != 1 || len(trash[0].Paths) != 1 || trash[0].Paths[0] != "/Docs" {
		t.Fatalf("trash = %+v, want one entry for the folder /Docs, not its files", trash)
	}
	if _, _, err := f.Store().Undo(ctx, alice, trash[0].Rev, workspace.UndoOptions{}); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if after, _ := f.Trash(ctx, "alice", 50); len(after) != 0 {
		t.Fatalf("trash after the restore = %+v, want empty", after)
	}
	bob := f.Actor("bob")
	if err := f.EnsureHome(ctx, "bob"); err != nil {
		t.Fatal(err)
	}
	if t2, _ := f.Trash(ctx, "bob", 50); len(t2) != 0 {
		t.Fatalf("bob sees %+v in his trash", t2)
	}
	_ = bob
}
