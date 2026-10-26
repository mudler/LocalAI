package studio

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/gormstore"
)

// The refresh key tells the sandbox to refresh. A switch only takes a line out
// of the index, so it must leave the key alone; a skill that is new or changed
// must change it.
func TestTheRefreshKeyMovesOnlyWhenASkillFileDoes(t *testing.T) {
	dsn := "file:" + filepath.Join(t.TempDir(), "ws.db") + "?_busy_timeout=5000&_txlock=immediate"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	blobs, _ := blob.NewFS(filepath.Join(t.TempDir(), "blobs"))
	ws, err := gormstore.New(db, blobs, gormstore.Options{Admin: files.AdminPrincipal})
	if err != nil {
		t.Fatal(err)
	}
	f := files.New(ws)
	st, _ := store.NewJSON("")
	svc, err := New(Deps{Store: st, Provider: provider.New(), StateDir: t.TempDir(), Files: f})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = svc.Close(context.Background())
		if raw, err := db.DB(); err == nil {
			_ = raw.Close()
		}
	})
	ctx := context.Background()
	if err := f.EnsureHome(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	put := func(name, desc string) {
		abs, _ := f.Abs("alice", "/.skills/"+name+"/SKILL.md")
		body := "---\nname: " + name + "\ndescription: " + desc + "\n---\nBody.\n"
		if _, err := ws.Put(ctx, f.Actor("alice"), abs, strings.NewReader(body), workspace.PutOptions{Overwrite: true}); err != nil {
			t.Fatal(err)
		}
	}
	put("review", "v1")
	put("notes", "v1")

	_, _, base := svc.skillTurn(ctx, "alice", nil)
	if base == "" {
		t.Fatal("no key for two skills")
	}
	if _, _, k := svc.skillTurn(ctx, "alice", []string{"review"}); k != base {
		t.Fatalf("switching a skill off changed the key: %q != %q", k, base)
	}
	put("review", "v2")
	if _, _, k := svc.skillTurn(ctx, "alice", nil); k == base {
		t.Fatal("a changed SKILL.md left the key as it was")
	}
	put("extra", "new")
	_, _, again := svc.skillTurn(ctx, "alice", nil)
	if again == base {
		t.Fatal("a new skill left the key as it was")
	}
}
