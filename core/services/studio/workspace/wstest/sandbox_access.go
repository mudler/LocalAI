package wstest

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

func testSandboxAccess(t *testing.T, newStore NewStore) {
	setup := func(t *testing.T) workspace.Store {
		t.Helper()
		s := newStore(t)
		MustPut(t, s, AdminActor, "/w/a.txt", "a")
		MustPut(t, s, AdminActor, "/shared/proj/p.txt", "p")
		MustPut(t, s, AdminActor, "/private/p.txt", "p")
		return s
	}
	adminView := func(t *testing.T, s workspace.Store, p string, m workspace.Mode) workspace.View {
		t.Helper()
		v, err := s.View(Ctx(), AdminActor, []workspace.MountRequest{{Path: p, At: "w", Mode: m}})
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	src := workspace.FixtureSource{workspace.FixtureDir("w"), workspace.FixtureFile("w/new.txt", "n")}
	t.Run("CommitUpper without a grant is not found", func(t *testing.T) {
		s := setup(t)
		v := adminView(t, s, "/w", workspace.ModeWrite)
		base := latestRev(t, s)
		if _, _, err := s.CommitUpper(Ctx(), v, Alice, base, src, workspace.UpperOptions{Title: "x"}); !errors.Is(err, workspace.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
		if _, err := s.Stat(Ctx(), AdminActor, "/w/new.txt"); !errors.Is(err, workspace.ErrNotFound) {
			t.Fatalf("a file was written: %v", err)
		}
	})
	t.Run("CommitUpper with a read grant is read-only", func(t *testing.T) {
		s := setup(t)
		if err := s.Grant(Ctx(), AdminActor, Alice.Principal, "/w", workspace.ModeRead); err != nil {
			t.Fatal(err)
		}
		v := adminView(t, s, "/w", workspace.ModeWrite)
		if _, _, err := s.CommitUpper(Ctx(), v, Alice, latestRev(t, s), src, workspace.UpperOptions{Title: "x"}); !errors.Is(err, workspace.ErrReadOnly) {
			t.Fatalf("err = %v, want ErrReadOnly", err)
		}
	})
	t.Run("CommitUpper refuses a view whose mode was raised by hand", func(t *testing.T) {
		s := setup(t)
		if err := s.Grant(Ctx(), AdminActor, Alice.Principal, "/w", workspace.ModeRead); err != nil {
			t.Fatal(err)
		}
		v, err := s.View(Ctx(), Alice, []workspace.MountRequest{{Path: "/w", At: "w", Mode: workspace.ModeWrite}})
		if err != nil {
			t.Fatal(err)
		}
		if v.Mounts[0].Mode != workspace.ModeRead {
			t.Fatalf("mode = %v, want read", v.Mounts[0].Mode)
		}
		v.Mounts[0].Mode = workspace.ModeWrite
		if _, _, err := s.CommitUpper(Ctx(), v, Alice, latestRev(t, s), src, workspace.UpperOptions{Title: "x"}); !errors.Is(err, workspace.ErrReadOnly) {
			t.Fatalf("err = %v, want ErrReadOnly", err)
		}
		if _, err := s.Stat(Ctx(), AdminActor, "/w/new.txt"); !errors.Is(err, workspace.ErrNotFound) {
			t.Fatalf("a file was written: %v", err)
		}
	})
	t.Run("Materialize after the grant is revoked is not found", func(t *testing.T) {
		s := setup(t)
		if err := s.Grant(Ctx(), AdminActor, Alice.Principal, "/w", workspace.ModeRead); err != nil {
			t.Fatal(err)
		}
		v, err := s.View(Ctx(), Alice, []workspace.MountRequest{{Path: "/w", At: "w", Mode: workspace.ModeRead}})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Revoke(Ctx(), AdminActor, Alice.Principal, "/w"); err != nil {
			t.Fatal(err)
		}
		dir := filepath.Join(t.TempDir(), "view")
		if _, err := s.Materialize(Ctx(), v, dir); !errors.Is(err, workspace.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})
	t.Run("Materialize of a mount moved out of the readable tree is not found", func(t *testing.T) {
		s := setup(t)
		if err := s.Grant(Ctx(), AdminActor, Alice.Principal, "/shared", workspace.ModeRead); err != nil {
			t.Fatal(err)
		}
		v, err := s.View(Ctx(), Alice, []workspace.MountRequest{{Path: "/shared/proj", At: "w", Mode: workspace.ModeRead}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Move(Ctx(), AdminActor, "/shared/proj", "/private/proj"); err != nil {
			t.Fatal(err)
		}
		dir := filepath.Join(t.TempDir(), "view")
		if _, err := s.Materialize(Ctx(), v, dir); !errors.Is(err, workspace.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})
	t.Run("View of a file in a hidden folder is not found", func(t *testing.T) {
		s := setup(t)
		_, err := s.View(Ctx(), Alice, []workspace.MountRequest{{Path: "/private/p.txt", At: "w", Mode: workspace.ModeRead}})
		if !errors.Is(err, workspace.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})
}
