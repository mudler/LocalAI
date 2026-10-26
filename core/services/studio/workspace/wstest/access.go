package wstest

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

func setupAccess(t *testing.T, s workspace.Store) {
	t.Helper()
	MustPut(t, s, AdminActor, "/shared/a.txt", "a")
	MustPut(t, s, AdminActor, "/private/p.txt", "p")
	MustPut(t, s, AdminActor, "/office/o.txt", "o")
}

func testAccess(t *testing.T, newStore NewStore) {
	t.Run("no grant reads as not found", func(t *testing.T) {
		s := newStore(t)
		setupAccess(t, s)
		for _, p := range []string{"/", "/shared", "/shared/a.txt", "/nope"} {
			if _, err := s.Stat(Ctx(), Alice, p); !errors.Is(err, workspace.ErrNotFound) {
				t.Errorf("stat %s: %v, want ErrNotFound", p, err)
			}
		}
		if _, err := s.Put(Ctx(), Alice, "/shared/b.txt", strings.NewReader("b"), workspace.PutOptions{}); !errors.Is(err, workspace.ErrNotFound) {
			t.Errorf("put: %v", err)
		}
		if _, err := s.List(Ctx(), Alice, "/"); !errors.Is(err, workspace.ErrNotFound) {
			t.Errorf("list: %v", err)
		}
	})
	t.Run("read grant allows reads only", func(t *testing.T) {
		s := newStore(t)
		setupAccess(t, s)
		if err := s.Grant(Ctx(), AdminActor, Alice.Principal, "/shared", workspace.ModeRead); err != nil {
			t.Fatal(err)
		}
		if MustRead(t, s, Alice, "/shared/a.txt") != "a" {
			t.Fatal("read through a read grant")
		}
		if _, err := s.Put(Ctx(), Alice, "/shared/b.txt", strings.NewReader("b"), workspace.PutOptions{}); !errors.Is(err, workspace.ErrReadOnly) {
			t.Errorf("put: %v, want ErrReadOnly", err)
		}
		if _, err := s.Mkdir(Ctx(), Alice, "/shared/sub"); !errors.Is(err, workspace.ErrReadOnly) {
			t.Errorf("mkdir: %v, want ErrReadOnly", err)
		}
		if _, err := s.Stat(Ctx(), Alice, "/private/p.txt"); !errors.Is(err, workspace.ErrNotFound) {
			t.Errorf("stat private: %v", err)
		}
	})
	t.Run("write grant allows writes below the folder", func(t *testing.T) {
		s := newStore(t)
		setupAccess(t, s)
		if err := s.Grant(Ctx(), AdminActor, Alice.Principal, "/shared", workspace.ModeWrite); err != nil {
			t.Fatal(err)
		}
		MustPut(t, s, Alice, "/shared/deep/b.txt", "b")
		if _, err := s.Put(Ctx(), Alice, "/elsewhere.txt", strings.NewReader("x"), workspace.PutOptions{}); !errors.Is(err, workspace.ErrNotFound) {
			t.Errorf("put outside the grant: %v", err)
		}
	})
	t.Run("group grant applies to members", func(t *testing.T) {
		s := newStore(t)
		setupAccess(t, s)
		if err := s.Grant(Ctx(), AdminActor, "group:office", "/office", workspace.ModeWrite); err != nil {
			t.Fatal(err)
		}
		MustPut(t, s, Bob, "/office/new.txt", "n")
		if _, err := s.Stat(Ctx(), Alice, "/office/new.txt"); !errors.Is(err, workspace.ErrNotFound) {
			t.Errorf("non-member: %v", err)
		}
	})
	t.Run("revoke removes access", func(t *testing.T) {
		s := newStore(t)
		setupAccess(t, s)
		if err := s.Grant(Ctx(), AdminActor, Alice.Principal, "/shared", workspace.ModeWrite); err != nil {
			t.Fatal(err)
		}
		if err := s.Revoke(Ctx(), AdminActor, Alice.Principal, "/shared"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Stat(Ctx(), Alice, "/shared"); !errors.Is(err, workspace.ErrNotFound) {
			t.Errorf("after revoke: %v", err)
		}
		if err := s.Revoke(Ctx(), AdminActor, Alice.Principal, "/shared"); err != nil {
			t.Errorf("second revoke: %v", err)
		}
	})
	t.Run("only the admin manages grants", func(t *testing.T) {
		s := newStore(t)
		setupAccess(t, s)
		if err := s.Grant(Ctx(), Alice, Alice.Principal, "/shared", workspace.ModeWrite); !errors.Is(err, workspace.ErrNotAdmin) {
			t.Errorf("grant by a user: %v", err)
		}
		if err := s.Grant(Ctx(), AdminActor, Alice.Principal, "/shared/a.txt", workspace.ModeRead); !errors.Is(err, workspace.ErrInvalid) {
			t.Errorf("grant on a file: %v", err)
		}
		if err := s.Grant(Ctx(), AdminActor, "", "/shared", workspace.ModeRead); !errors.Is(err, workspace.ErrInvalid) {
			t.Errorf("grant to nobody: %v", err)
		}
		if err := s.Grant(Ctx(), AdminActor, Alice.Principal, "/shared", workspace.ModeNone); !errors.Is(err, workspace.ErrInvalid) {
			t.Errorf("grant of no mode: %v", err)
		}
	})
	t.Run("a view never grows a grant", func(t *testing.T) {
		s := newStore(t)
		setupAccess(t, s)
		if err := s.Grant(Ctx(), AdminActor, Alice.Principal, "/shared", workspace.ModeRead); err != nil {
			t.Fatal(err)
		}
		v, err := s.View(Ctx(), Alice, []workspace.MountRequest{{Path: "/shared", At: "shared", Mode: workspace.ModeWrite}})
		if err != nil {
			t.Fatal(err)
		}
		if len(v.Mounts) != 1 || v.Mounts[0].Mode != workspace.ModeRead || v.Mounts[0].Path != "/shared" || v.Mounts[0].At != "shared" {
			t.Fatalf("view = %+v", v)
		}
		if _, err := s.View(Ctx(), Alice, []workspace.MountRequest{{Path: "/private", At: "p", Mode: workspace.ModeRead}}); !errors.Is(err, workspace.ErrNotFound) {
			t.Errorf("view of a hidden folder: %v", err)
		}
	})
	t.Run("view requests are checked", func(t *testing.T) {
		s := newStore(t)
		setupAccess(t, s)
		cases := []struct {
			reqs []workspace.MountRequest
			want error
		}{
			{[]workspace.MountRequest{{Path: "/shared/a.txt", At: "a", Mode: workspace.ModeRead}}, workspace.ErrInvalid},
			{[]workspace.MountRequest{{Path: "/shared", At: "x", Mode: workspace.ModeRead}, {Path: "/office", At: "X", Mode: workspace.ModeRead}}, workspace.ErrInvalid},
			{[]workspace.MountRequest{{Path: "/shared", At: "a/b", Mode: workspace.ModeRead}}, workspace.ErrBadName},
			{[]workspace.MountRequest{{Path: "/shared", At: "s", Mode: workspace.ModeNone}}, workspace.ErrInvalid},
		}
		for i, c := range cases {
			if _, err := s.View(Ctx(), AdminActor, c.reqs); !errors.Is(err, c.want) {
				t.Errorf("case %d: %v, want %v", i, err, c.want)
			}
		}
	})
}

func testReadablePrefixes(t *testing.T, newStore NewStore) {
	prefixes := func(t *testing.T, s workspace.Store, a workspace.Actor) []string {
		t.Helper()
		got, err := s.ReadablePrefixes(Ctx(), a)
		if err != nil {
			t.Fatalf("ReadablePrefixes: %v", err)
		}
		if got == nil {
			t.Fatal("ReadablePrefixes returned nil, want a list")
		}
		return got
	}
	want := func(t *testing.T, what string, got []string, w ...string) {
		t.Helper()
		if w == nil {
			w = []string{}
		}
		if !reflect.DeepEqual(got, w) {
			t.Errorf("%s: %q, want %q", what, got, w)
		}
	}
	t.Run("the admin gets the root and an actor with no grant gets nothing", func(t *testing.T) {
		s := newStore(t)
		setupAccess(t, s)
		want(t, "admin", prefixes(t, s, AdminActor), "/")
		want(t, "no grant", prefixes(t, s, Alice))
		want(t, "no principal", prefixes(t, s, workspace.Actor{}))
	})
	t.Run("a grant on a folder and one below it give the outer folder", func(t *testing.T) {
		s := newStore(t)
		MustPut(t, s, AdminActor, "/a/b/f.txt", "f")
		MustPut(t, s, AdminActor, "/ab/g.txt", "g")
		for _, p := range []string{"/a/b", "/a", "/ab"} {
			if err := s.Grant(Ctx(), AdminActor, Alice.Principal, p, workspace.ModeRead); err != nil {
				t.Fatal(err)
			}
		}
		want(t, "nested", prefixes(t, s, Alice), "/a", "/ab")
	})
	t.Run("a write grant counts and a grant on the root gives the root", func(t *testing.T) {
		s := newStore(t)
		MustPut(t, s, AdminActor, "/x/f.txt", "f")
		if err := s.Grant(Ctx(), AdminActor, Alice.Principal, "/x", workspace.ModeWrite); err != nil {
			t.Fatal(err)
		}
		want(t, "write", prefixes(t, s, Alice), "/x")
		if err := s.Grant(Ctx(), AdminActor, Alice.Principal, "/", workspace.ModeRead); err != nil {
			t.Fatal(err)
		}
		want(t, "root", prefixes(t, s, Alice), "/")
	})
	t.Run("group grants count", func(t *testing.T) {
		s := newStore(t)
		setupAccess(t, s)
		if err := s.Grant(Ctx(), AdminActor, "group:office", "/office", workspace.ModeRead); err != nil {
			t.Fatal(err)
		}
		if err := s.Grant(Ctx(), AdminActor, Bob.Principal, "/shared", workspace.ModeRead); err != nil {
			t.Fatal(err)
		}
		want(t, "member", prefixes(t, s, Bob), "/office", "/shared")
		want(t, "non-member", prefixes(t, s, Alice))
	})
	t.Run("a revoked grant drops out", func(t *testing.T) {
		s := newStore(t)
		setupAccess(t, s)
		for _, p := range []string{"/shared", "/office"} {
			if err := s.Grant(Ctx(), AdminActor, Alice.Principal, p, workspace.ModeRead); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.Revoke(Ctx(), AdminActor, Alice.Principal, "/shared"); err != nil {
			t.Fatal(err)
		}
		want(t, "after revoke", prefixes(t, s, Alice), "/office")
	})
	t.Run("a grant on a deleted folder drops out and comes back with an undo", func(t *testing.T) {
		s := newStore(t)
		setupAccess(t, s)
		if err := s.Grant(Ctx(), AdminActor, Alice.Principal, "/shared", workspace.ModeRead); err != nil {
			t.Fatal(err)
		}
		if err := s.Delete(Ctx(), AdminActor, "/shared"); err != nil {
			t.Fatal(err)
		}
		want(t, "deleted", prefixes(t, s, Alice))
		sets, err := s.History(Ctx(), AdminActor, workspace.HistoryQuery{Path: "/", Limit: 1})
		if err != nil || len(sets) == 0 {
			t.Fatalf("history: %v %v", sets, err)
		}
		if _, _, err := s.Undo(Ctx(), AdminActor, sets[0].Rev, workspace.UndoOptions{}); err != nil {
			t.Fatal(err)
		}
		want(t, "restored", prefixes(t, s, Alice), "/shared")
	})
	t.Run("a sibling whose name sorts inside the path is not dropped", func(t *testing.T) {
		s := newStore(t)
		MustPut(t, s, AdminActor, "/a/b/f.txt", "f")
		MustPut(t, s, AdminActor, "/a-b/g.txt", "g")
		for _, p := range []string{"/a", "/a-b", "/a/b"} {
			if err := s.Grant(Ctx(), AdminActor, Alice.Principal, p, workspace.ModeRead); err != nil {
				t.Fatal(err)
			}
		}
		want(t, "dash sibling", prefixes(t, s, Alice), "/a", "/a-b")
	})
	t.Run("a grant on a subfolder of a deleted folder gives no prefix", func(t *testing.T) {
		s := newStore(t)
		MustPut(t, s, AdminActor, "/dir/sub/f.txt", "f")
		if err := s.Grant(Ctx(), AdminActor, Alice.Principal, "/dir/sub", workspace.ModeRead); err != nil {
			t.Fatal(err)
		}
		want(t, "before delete", prefixes(t, s, Alice), "/dir/sub")
		if err := s.Delete(Ctx(), AdminActor, "/dir"); err != nil {
			t.Fatal(err)
		}
		want(t, "after delete", prefixes(t, s, Alice))
	})
	t.Run("a moved folder keeps its grant at the new path", func(t *testing.T) {
		s := newStore(t)
		setupAccess(t, s)
		if err := s.Grant(Ctx(), AdminActor, Alice.Principal, "/shared", workspace.ModeRead); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Move(Ctx(), AdminActor, "/shared", "/moved"); err != nil {
			t.Fatal(err)
		}
		want(t, "moved", prefixes(t, s, Alice), "/moved")
	})
}
