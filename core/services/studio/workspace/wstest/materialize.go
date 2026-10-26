package wstest

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

func latestRev(t *testing.T, s workspace.Store) int64 {
	t.Helper()
	h, err := s.History(Ctx(), AdminActor, workspace.HistoryQuery{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(h) == 0 {
		return 0
	}
	return h[0].Rev
}

func testMaterialize(t *testing.T, newStore NewStore) {
	t.Run("builds a read-only tree of the view", func(t *testing.T) {
		s := newStore(t)
		MustPut(t, s, AdminActor, "/w/a.txt", "a")
		MustPut(t, s, AdminActor, "/w/sub/b.txt", "b")
		MustPut(t, s, AdminActor, "/w/gone.txt", "g")
		if err := s.Delete(Ctx(), AdminActor, "/w/gone.txt"); err != nil {
			t.Fatal(err)
		}
		MustPut(t, s, AdminActor, "/docs/d.txt", "d")
		if err := s.Grant(Ctx(), AdminActor, Alice.Principal, "/w", workspace.ModeRead); err != nil {
			t.Fatal(err)
		}
		if err := s.Grant(Ctx(), AdminActor, Alice.Principal, "/docs", workspace.ModeWrite); err != nil {
			t.Fatal(err)
		}
		v, err := s.View(Ctx(), Alice, []workspace.MountRequest{
			{Path: "/w", At: "work", Mode: workspace.ModeRead},
			{Path: "/docs", At: "docs", Mode: workspace.ModeWrite},
		})
		if err != nil {
			t.Fatal(err)
		}
		dir := filepath.Join(t.TempDir(), "view")
		base, err := s.Materialize(Ctx(), v, dir)
		if err != nil {
			t.Fatal(err)
		}
		if base != latestRev(t, s) {
			t.Fatalf("base = %d, want %d", base, latestRev(t, s))
		}
		want := map[string]string{"work/a.txt": "a", "work/sub/b.txt": "b", "docs/d.txt": "d"}
		for rel, content := range want {
			p := filepath.Join(dir, filepath.FromSlash(rel))
			got, err := os.ReadFile(p)
			if err != nil || string(got) != content {
				t.Errorf("%s = %q, %v", rel, got, err)
			}
			fi, err := os.Stat(p)
			if err == nil && fi.Mode().Perm()&0o222 != 0 {
				t.Errorf("%s is writable: %v", rel, fi.Mode().Perm())
			}
		}
		if _, err := os.Stat(filepath.Join(dir, "work", "gone.txt")); !os.IsNotExist(err) {
			t.Errorf("deleted file is in the view: %v", err)
		}
	})
	t.Run("refuses a folder that is not empty", func(t *testing.T) {
		s := newStore(t)
		MustPut(t, s, AdminActor, "/w/a.txt", "a")
		v, err := s.View(Ctx(), AdminActor, []workspace.MountRequest{{Path: "/w", At: "w", Mode: workspace.ModeRead}})
		if err != nil {
			t.Fatal(err)
		}
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "x"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Materialize(Ctx(), v, dir); !errors.Is(err, workspace.ErrExists) {
			t.Fatalf("err = %v, want ErrExists", err)
		}
	})
	t.Run("a deleted mount is not found", func(t *testing.T) {
		s := newStore(t)
		MustPut(t, s, AdminActor, "/w/a.txt", "a")
		v, err := s.View(Ctx(), AdminActor, []workspace.MountRequest{{Path: "/w", At: "w", Mode: workspace.ModeRead}})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Delete(Ctx(), AdminActor, "/w"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Materialize(Ctx(), v, filepath.Join(t.TempDir(), "v")); !errors.Is(err, workspace.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})
	t.Run("refuses a mount point that escapes the folder", func(t *testing.T) {
		for _, at := range []string{"../escape", "a/../../x", "/abs", "", ".", "a//b", "a/./b"} {
			s := newStore(t)
			MustPut(t, s, AdminActor, "/w/a.txt", "a")
			v, err := s.View(Ctx(), AdminActor, []workspace.MountRequest{{Path: "/w", At: "w", Mode: workspace.ModeRead}})
			if err != nil {
				t.Fatal(err)
			}
			v.Mounts[0].At = at
			parent := t.TempDir()
			dir := filepath.Join(parent, "view")
			if _, err := s.Materialize(Ctx(), v, dir); !errors.Is(err, workspace.ErrInvalid) {
				t.Errorf("At %q: err = %v, want ErrInvalid", at, err)
			}
			ents, _ := os.ReadDir(parent)
			for _, e := range ents {
				if e.Name() != "view" {
					t.Errorf("At %q: %s appeared next to the target", at, e.Name())
				}
			}
			if _, err := os.Stat(filepath.Join(parent, "..", "escape")); err == nil {
				t.Errorf("At %q: escape exists", at)
			}
		}
	})
	t.Run("refuses duplicate and nested mount points", func(t *testing.T) {
		s := newStore(t)
		MustPut(t, s, AdminActor, "/w/a.txt", "a")
		MustPut(t, s, AdminActor, "/x/b.txt", "b")
		for _, ats := range [][2]string{{"m", "m"}, {"m", "m/n"}, {"m/n", "m"}} {
			v, err := s.View(Ctx(), AdminActor, []workspace.MountRequest{
				{Path: "/w", At: "p", Mode: workspace.ModeRead},
				{Path: "/x", At: "q", Mode: workspace.ModeRead},
			})
			if err != nil {
				t.Fatal(err)
			}
			v.Mounts[0].At, v.Mounts[1].At = ats[0], ats[1]
			if _, err := s.Materialize(Ctx(), v, filepath.Join(t.TempDir(), "v")); !errors.Is(err, workspace.ErrInvalid) {
				t.Errorf("%v: err = %v, want ErrInvalid", ats, err)
			}
		}
	})
	t.Run("a nested mount point works", func(t *testing.T) {
		s := newStore(t)
		MustPut(t, s, AdminActor, "/w/a.txt", "a")
		v, err := s.View(Ctx(), AdminActor, []workspace.MountRequest{{Path: "/w", At: "p", Mode: workspace.ModeRead}})
		if err != nil {
			t.Fatal(err)
		}
		v.Mounts[0].At = "work/in" // View accepts one name; Materialize accepts a relative path
		dir := filepath.Join(t.TempDir(), "v")
		if _, err := s.Materialize(Ctx(), v, dir); err != nil {
			t.Fatal(err)
		}
		if got, err := os.ReadFile(filepath.Join(dir, "work", "in", "a.txt")); err != nil || string(got) != "a" {
			t.Fatalf("got %q, %v", got, err)
		}
	})
	t.Run("a renamed mounted folder still materializes", func(t *testing.T) {
		s := newStore(t)
		MustPut(t, s, AdminActor, "/w/a.txt", "a")
		v, err := s.View(Ctx(), AdminActor, []workspace.MountRequest{{Path: "/w", At: "w", Mode: workspace.ModeRead}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Move(Ctx(), AdminActor, "/w", "/renamed"); err != nil {
			t.Fatal(err)
		}
		dir := filepath.Join(t.TempDir(), "v")
		if _, err := s.Materialize(Ctx(), v, dir); err != nil {
			t.Fatal(err)
		}
		if got, err := os.ReadFile(filepath.Join(dir, "w", "a.txt")); err != nil || string(got) != "a" {
			t.Fatalf("got %q, %v", got, err)
		}
	})
}
