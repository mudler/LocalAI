package builtinskills_test

import (
	"context"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/studio/builtinskills"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/skills"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/gormstore"
)

var names = []string{"docx", "xlsx", "pptx", "pdf", "writing-skills"}

func TestEveryBuiltInSkillParses(t *testing.T) {
	fsys := builtinskills.FS()
	for _, n := range names {
		data, err := fs.ReadFile(fsys, n+"/SKILL.md")
		if err != nil {
			t.Fatalf("%s: %v", n, err)
		}
		name, desc, err := skills.Parse(data)
		if err != nil || name != n || strings.TrimSpace(desc) == "" {
			t.Errorf("%s: parsed as %q, %q, %v", n, name, desc, err)
		}
	}
}

func TestTheNoticeKeepsTheSourceAndTheLicence(t *testing.T) {
	data, err := fs.ReadFile(builtinskills.FS(), "NOTICE")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"paper-instruments/feather-train", "b7463092af862ebbdf4c5920c4bc9ab38ab17ad9", "MIT License", "Permission is hereby granted"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("the NOTICE lacks %q", want)
		}
	}
}

func TestNoInternalNameLeaksIntoTheTree(t *testing.T) {
	err := fs.WalkDir(builtinskills.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(builtinskills.FS(), p)
		if err != nil {
			return err
		}
		for _, bad := range []string{"aioffice", "localstudio", "LocalStudio", "/home/mudler"} {
			if strings.Contains(string(data), bad) {
				t.Errorf("%s mentions %q", p, bad)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSyncedIntoTheBuiltInLayerTheSkillsResolveForAUser(t *testing.T) {
	ctx := context.Background()
	db, err := gorm.Open(sqlite.Open("file:"+filepath.Join(t.TempDir(), "ws.db")+"?_busy_timeout=5000&_txlock=immediate"), &gorm.Config{})
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
	f := files.New(ws)
	if err := f.SyncBuiltin(ctx, builtinskills.FS()); err != nil {
		t.Fatal(err)
	}
	if err := f.EnsureHome(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	got, _, err := skills.Resolve(ctx, ws, f.Actor("alice"), f.SkillRoots("alice"), skills.Options{})
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, s := range got {
		if s.Layer == skills.LayerBuiltin {
			have[s.Name] = true
		}
	}
	for _, n := range names {
		if !have[n] {
			t.Errorf("the built-in skill %s is not found by a user", n)
		}
	}
}
