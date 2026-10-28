package studio

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/skills"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/gormstore"
	"github.com/mudler/LocalAI/pkg/sqlitedb"
)

// The refresh key tells the sandbox to refresh. A switch only takes a line out
// of the index, so it must leave the key alone; a skill that is new or changed
// must change it.
func TestTheRefreshKeyMovesOnlyWhenASkillFileDoes(t *testing.T) {
	dsn := "file:" + filepath.Join(t.TempDir(), "ws.db") + "?_busy_timeout=5000&_txlock=immediate"
	db, err := sqlitedb.Open(dsn, &gorm.Config{})
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

	_, _, base := svc.skillTurn(ctx, "alice", nil, nil)
	if base == "" {
		t.Fatal("no key for two skills")
	}
	if _, _, k := svc.skillTurn(ctx, "alice", []string{"review"}, nil); k != base {
		t.Fatalf("switching a skill off changed the key: %q != %q", k, base)
	}
	put("review", "v2")
	if _, _, k := svc.skillTurn(ctx, "alice", nil, nil); k == base {
		t.Fatal("a changed SKILL.md left the key as it was")
	}
	put("extra", "new")
	_, _, again := svc.skillTurn(ctx, "alice", nil, nil)
	if again == base {
		t.Fatal("a new skill left the key as it was")
	}
}

// A switch with a plain name turns off the skill that wins, so the one below it
// shows through; "layer:name" turns off that layer's skill only.
func TestASkillSwitchTurnsOffTheWinnerOrTheNamedLayer(t *testing.T) {
	dsn := "file:" + filepath.Join(t.TempDir(), "ws.db") + "?_busy_timeout=5000&_txlock=immediate"
	db, err := sqlitedb.Open(dsn, &gorm.Config{})
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
	if err := f.EnsureShared(ctx); err != nil {
		t.Fatal(err)
	}
	if err := f.WriteSkill(ctx, "alice", skills.LayerPersonal, "report", "Personal report.", "Steps."); err != nil {
		t.Fatal(err)
	}
	if err := f.WriteSkill(ctx, files.AdminPrincipal, skills.LayerShared, "report", "Shared report.", "Steps."); err != nil {
		t.Fatal(err)
	}
	index := func(off ...string) string {
		p, _, _ := svc.skillTurn(ctx, "alice", off, nil)
		return p
	}
	cases := []struct {
		off         []string
		personal    bool
		shared      bool
		explanation string
	}{
		{nil, true, false, "the nearest skill wins"},
		{[]string{"report"}, false, true, "a plain name turns off the winner and uncovers the one below"},
		{[]string{"personal:report"}, false, true, "the personal skill off"},
		{[]string{"shared:report"}, true, false, "the shared skill is covered anyway"},
		{[]string{"personal:report", "shared:report"}, false, false, "both layers off"},
		{[]string{"report", "report"}, false, true, "a name twice is still one switch"},
	}
	for _, c := range cases {
		got := index(c.off...)
		if has := strings.Contains(got, "Personal report."); has != c.personal {
			t.Errorf("%s: off %v: personal listed = %v", c.explanation, c.off, has)
		}
		if has := strings.Contains(got, "Shared report."); has != c.shared {
			t.Errorf("%s: off %v: shared listed = %v", c.explanation, c.off, has)
		}
	}
	for _, bad := range []string{"nolayer:report", "personal:", ":report", "personal:bad name", "a:b:c"} {
		if err := svc.SetSkillsOff(ctx, "alice", "nope", []string{bad}); !errors.Is(err, ErrBadSkillName) {
			t.Errorf("switch %q: %v, want ErrBadSkillName", bad, err)
		}
	}
}
