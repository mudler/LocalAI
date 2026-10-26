package wstest

import (
	"errors"
	"reflect"
	"testing"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

func testMoves(t *testing.T, newStore NewStore) {
	t.Run("move a file", func(t *testing.T) {
		s := newStore(t)
		n1 := MustPut(t, s, AdminActor, "/a.txt", "a")
		n2, err := s.Move(Ctx(), AdminActor, "/a.txt", "/b.txt")
		if err != nil || n2.Path != "/b.txt" || n2.ID != n1.ID || n2.Version != 2 {
			t.Fatalf("move = %+v, %v", n2, err)
		}
		if got := Tree(t, s); !reflect.DeepEqual(got, map[string]string{"/b.txt": "a"}) {
			t.Fatalf("tree = %v", got)
		}
	})
	t.Run("case-only rename is allowed", func(t *testing.T) {
		s := newStore(t)
		MustPut(t, s, AdminActor, "/a.txt", "a")
		if n, err := s.Move(Ctx(), AdminActor, "/a.txt", "/A.txt"); err != nil || n.Path != "/A.txt" {
			t.Fatalf("move = %+v, %v", n, err)
		}
	})
	t.Run("move creates missing folders", func(t *testing.T) {
		s := newStore(t)
		MustPut(t, s, AdminActor, "/a.txt", "a")
		if _, err := s.Move(Ctx(), AdminActor, "/a.txt", "/x/y/a.txt"); err != nil {
			t.Fatal(err)
		}
		want := map[string]string{"/x": "/", "/x/y": "/", "/x/y/a.txt": "a"}
		if got := Tree(t, s); !reflect.DeepEqual(got, want) {
			t.Fatalf("tree = %v", got)
		}
	})
	t.Run("move onto a taken name is refused", func(t *testing.T) {
		s := newStore(t)
		MustPut(t, s, AdminActor, "/a.txt", "a")
		MustPut(t, s, AdminActor, "/B.txt", "b")
		if _, err := s.Move(Ctx(), AdminActor, "/a.txt", "/b.txt"); !errors.Is(err, workspace.ErrExists) {
			t.Fatalf("err = %v, want ErrExists", err)
		}
	})
	t.Run("a folder cannot move into itself", func(t *testing.T) {
		s := newStore(t)
		MustPut(t, s, AdminActor, "/d/f.txt", "f")
		before := Tree(t, s)
		if _, err := s.Move(Ctx(), AdminActor, "/d", "/d/e/d"); !errors.Is(err, workspace.ErrInvalid) {
			t.Fatalf("err = %v, want ErrInvalid", err)
		}
		if got := Tree(t, s); !reflect.DeepEqual(got, before) {
			t.Fatalf("a refused move changed the tree: %v", got)
		}
		if _, err := s.Move(Ctx(), AdminActor, "/", "/x"); !errors.Is(err, workspace.ErrInvalid) {
			t.Fatalf("move root: %v", err)
		}
	})
	t.Run("a moved folder keeps its children", func(t *testing.T) {
		s := newStore(t)
		MustPut(t, s, AdminActor, "/d/c.txt", "c")
		if _, err := s.Move(Ctx(), AdminActor, "/d", "/e"); err != nil {
			t.Fatal(err)
		}
		if MustRead(t, s, AdminActor, "/e/c.txt") != "c" {
			t.Fatal("child not moved")
		}
	})
	t.Run("delete sets tombstones on the subtree", func(t *testing.T) {
		s := newStore(t)
		MustPut(t, s, AdminActor, "/d/a.txt", "a")
		MustPut(t, s, AdminActor, "/d/b.txt", "b")
		MustPut(t, s, AdminActor, "/keep.txt", "k")
		tx, err := s.Begin(Ctx(), AdminActor, "Clean")
		if err != nil {
			t.Fatal(err)
		}
		if err := tx.Delete(Ctx(), "/d"); err != nil {
			t.Fatal(err)
		}
		cs, err := tx.Commit()
		if err != nil {
			t.Fatal(err)
		}
		if len(cs.Ops) != 3 || cs.Ops[2].NewPath != "/d" {
			t.Fatalf("ops = %+v; want the two files first, then the folder", cs.Ops)
		}
		for _, op := range cs.Ops {
			if op.Kind != workspace.OpDelete {
				t.Fatalf("op = %+v", op)
			}
			// Op has no deleted flags. A tombstone keeps the node, so both
			// paths name it; a hard delete leaves no new path.
			if op.NodeID == 0 || op.OldPath == "" || op.OldPath != op.NewPath {
				t.Fatalf("a delete must keep the node and its path: %+v", op)
			}
		}
		if got := Tree(t, s); !reflect.DeepEqual(got, map[string]string{"/keep.txt": "k"}) {
			t.Fatalf("tree = %v", got)
		}
		if _, err := s.Stat(Ctx(), AdminActor, "/d/a.txt"); !errors.Is(err, workspace.ErrNotFound) {
			t.Fatalf("stat deleted: %v", err)
		}
		MustPut(t, s, AdminActor, "/d/a.txt", "again")
		if err := s.Delete(Ctx(), AdminActor, "/"); !errors.Is(err, workspace.ErrInvalid) {
			t.Fatalf("delete root: %v", err)
		}
	})
	t.Run("moves and deletes need write access", func(t *testing.T) {
		s := newStore(t)
		MustPut(t, s, AdminActor, "/w/a.txt", "a")
		MustPut(t, s, AdminActor, "/r/b.txt", "b")
		MustPut(t, s, AdminActor, "/hidden/c.txt", "c")
		if err := s.Grant(Ctx(), AdminActor, Alice.Principal, "/w", workspace.ModeWrite); err != nil {
			t.Fatal(err)
		}
		if err := s.Grant(Ctx(), AdminActor, Alice.Principal, "/r", workspace.ModeRead); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Move(Ctx(), Alice, "/w/a.txt", "/r/a.txt"); !errors.Is(err, workspace.ErrReadOnly) {
			t.Errorf("move into a read-only folder: %v", err)
		}
		if err := s.Delete(Ctx(), Alice, "/r/b.txt"); !errors.Is(err, workspace.ErrReadOnly) {
			t.Errorf("delete read-only: %v", err)
		}
		if err := s.Delete(Ctx(), Alice, "/hidden/c.txt"); !errors.Is(err, workspace.ErrNotFound) {
			t.Errorf("delete hidden: %v", err)
		}
		if _, err := s.Move(Ctx(), Alice, "/w/a.txt", "/w/z.txt"); err != nil {
			t.Errorf("move inside the write grant: %v", err)
		}
		if _, err := s.Move(Ctx(), Alice, "/w/z.txt", "/hidden/z.txt"); !errors.Is(err, workspace.ErrNotFound) {
			t.Errorf("move into a hidden folder: %v", err)
		}
		if _, err := s.Move(Ctx(), Alice, "/w/z.txt", "/hidden/new/z.txt"); !errors.Is(err, workspace.ErrNotFound) {
			t.Errorf("move below a missing path in a hidden folder: %v", err)
		}
		if got := Tree(t, s); got["/hidden/new"] != "" {
			t.Errorf("a refused move created a folder: %v", got)
		}
	})
	t.Run("a write grant under a read-only parent protects the folder itself", func(t *testing.T) {
		s := newStore(t)
		MustPut(t, s, AdminActor, "/shared/mine/f.txt", "f")
		MustPut(t, s, AdminActor, "/w/x.txt", "x")
		if err := s.Grant(Ctx(), AdminActor, Alice.Principal, "/shared", workspace.ModeRead); err != nil {
			t.Fatal(err)
		}
		if err := s.Grant(Ctx(), AdminActor, Alice.Principal, "/shared/mine", workspace.ModeWrite); err != nil {
			t.Fatal(err)
		}
		if err := s.Grant(Ctx(), AdminActor, Alice.Principal, "/w", workspace.ModeWrite); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Move(Ctx(), Alice, "/shared/mine", "/w/mine"); !errors.Is(err, workspace.ErrReadOnly) {
			t.Errorf("move the folder out: %v", err)
		}
		if err := s.Delete(Ctx(), Alice, "/shared/mine"); !errors.Is(err, workspace.ErrReadOnly) {
			t.Errorf("delete the folder: %v", err)
		}
		if _, err := s.Move(Ctx(), Alice, "/shared/mine/f.txt", "/shared/mine/g.txt"); err != nil {
			t.Errorf("rename inside the folder: %v", err)
		}
		MustPut(t, s, Alice, "/shared/mine/f2.txt", "2")
		if got := Tree(t, s); got["/shared/mine/g.txt"] != "f" {
			t.Errorf("tree = %v", got)
		}
	})
}
