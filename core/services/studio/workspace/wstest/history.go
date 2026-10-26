package wstest

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// commit runs fn in one Tx and returns the change set.
func commit(t *testing.T, s workspace.Store, a workspace.Actor, fn func(tx workspace.Tx) error) workspace.ChangeSet {
	t.Helper()
	tx, err := s.Begin(Ctx(), a, "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	cs, err := tx.Commit()
	if err != nil {
		t.Fatal(err)
	}
	return cs
}

func putIn(tx workspace.Tx, p, content string) error {
	_, err := tx.Put(Ctx(), p, strings.NewReader(content), workspace.PutOptions{Overwrite: true})
	return err
}

func mustUndo(t *testing.T, s workspace.Store, rev int64) workspace.ChangeSet {
	t.Helper()
	cs, conflicts, err := s.Undo(Ctx(), AdminActor, rev, workspace.UndoOptions{})
	if err != nil {
		t.Fatalf("undo %d: %v %v", rev, err, conflicts)
	}
	return cs
}

func testHistory(t *testing.T, newStore NewStore) {
	t.Run("undo a create", func(t *testing.T) {
		s := newStore(t)
		cs := commit(t, s, AdminActor, func(tx workspace.Tx) error { return putIn(tx, "/d/a.txt", "a") })
		u := mustUndo(t, s, cs.Rev)
		if u.Kind != workspace.ChangeUndo || u.Undoes != cs.Rev || u.Title != fmt.Sprintf("Undo of revision %d", cs.Rev) {
			t.Fatalf("undo change set = %+v", u)
		}
		if got := Tree(t, s); len(got) != 0 {
			t.Fatalf("tree = %v", got)
		}
	})
	t.Run("undo a write", func(t *testing.T) {
		s := newStore(t)
		MustPut(t, s, AdminActor, "/a.txt", "v1")
		cs := commit(t, s, AdminActor, func(tx workspace.Tx) error { return putIn(tx, "/a.txt", "v2") })
		mustUndo(t, s, cs.Rev)
		if MustRead(t, s, AdminActor, "/a.txt") != "v1" {
			t.Fatal("content not restored")
		}
	})
	t.Run("undo a move and a folder delete", func(t *testing.T) {
		s := newStore(t)
		MustPut(t, s, AdminActor, "/d/a.txt", "a")
		MustPut(t, s, AdminActor, "/d/b.txt", "b")
		start := Tree(t, s)
		mv := commit(t, s, AdminActor, func(tx workspace.Tx) error {
			_, err := tx.Move(Ctx(), "/d/a.txt", "/e/a.txt")
			return err
		})
		del := commit(t, s, AdminActor, func(tx workspace.Tx) error { return tx.Delete(Ctx(), "/d") })
		mustUndo(t, s, del.Rev)
		mustUndo(t, s, mv.Rev)
		if got := Tree(t, s); !reflect.DeepEqual(got, start) {
			t.Fatalf("tree = %v, want %v", got, start)
		}
	})
	t.Run("undo of an undo applies the change again", func(t *testing.T) {
		s := newStore(t)
		cs := commit(t, s, AdminActor, func(tx workspace.Tx) error { return putIn(tx, "/a.txt", "a") })
		u := mustUndo(t, s, cs.Rev)
		mustUndo(t, s, u.Rev)
		if MustRead(t, s, AdminActor, "/a.txt") != "a" {
			t.Fatal("redo failed")
		}
	})
	t.Run("a later change is a conflict and nothing changes", func(t *testing.T) {
		s := newStore(t)
		cs := commit(t, s, AdminActor, func(tx workspace.Tx) error { return putIn(tx, "/a.txt", "v1") })
		MustPut(t, s, AdminActor, "/a.txt", "v2")
		before := Tree(t, s)
		_, conflicts, err := s.Undo(Ctx(), AdminActor, cs.Rev, workspace.UndoOptions{})
		if !errors.Is(err, workspace.ErrConflict) {
			t.Fatalf("err = %v, want ErrConflict", err)
		}
		if len(conflicts) != 1 || conflicts[0].Path != "/a.txt" || conflicts[0].Reason != workspace.ReasonChanged {
			t.Fatalf("conflicts = %+v", conflicts)
		}
		if got := Tree(t, s); !reflect.DeepEqual(got, before) {
			t.Fatalf("tree changed: %v", got)
		}
	})
	t.Run("skip conflicts reverts the rest", func(t *testing.T) {
		s := newStore(t)
		cs := commit(t, s, AdminActor, func(tx workspace.Tx) error {
			if err := putIn(tx, "/a.txt", "a"); err != nil {
				return err
			}
			return putIn(tx, "/b.txt", "b")
		})
		MustPut(t, s, AdminActor, "/a.txt", "changed")
		u, conflicts, err := s.Undo(Ctx(), AdminActor, cs.Rev, workspace.UndoOptions{SkipConflicts: true})
		if err != nil || u.Rev == 0 || len(conflicts) != 1 {
			t.Fatalf("undo = %+v, %v, %v", u, conflicts, err)
		}
		if got := Tree(t, s); !reflect.DeepEqual(got, map[string]string{"/a.txt": "changed"}) {
			t.Fatalf("tree = %v", got)
		}
	})
	t.Run("a taken name is a conflict", func(t *testing.T) {
		s := newStore(t)
		MustPut(t, s, AdminActor, "/a.txt", "a")
		mv := commit(t, s, AdminActor, func(tx workspace.Tx) error {
			_, err := tx.Move(Ctx(), "/a.txt", "/b.txt")
			return err
		})
		MustPut(t, s, AdminActor, "/A.TXT", "new")
		_, conflicts, err := s.Undo(Ctx(), AdminActor, mv.Rev, workspace.UndoOptions{})
		if !errors.Is(err, workspace.ErrConflict) || len(conflicts) != 1 || conflicts[0].Reason != workspace.ReasonNameTaken {
			t.Fatalf("undo = %+v, %v", conflicts, err)
		}
	})
	t.Run("a deleted parent is a conflict", func(t *testing.T) {
		s := newStore(t)
		MustPut(t, s, AdminActor, "/d/x.txt", "x")
		delX := commit(t, s, AdminActor, func(tx workspace.Tx) error { return tx.Delete(Ctx(), "/d/x.txt") })
		commit(t, s, AdminActor, func(tx workspace.Tx) error { return tx.Delete(Ctx(), "/d") })
		_, conflicts, err := s.Undo(Ctx(), AdminActor, delX.Rev, workspace.UndoOptions{})
		if !errors.Is(err, workspace.ErrConflict) || len(conflicts) != 1 || conflicts[0].Reason != workspace.ReasonParentDeleted {
			t.Fatalf("undo = %+v, %v", conflicts, err)
		}
	})
	t.Run("undo after change and change back", func(t *testing.T) {
		s := newStore(t)
		MustPut(t, s, AdminActor, "/a.txt", "v1")
		cs := commit(t, s, AdminActor, func(tx workspace.Tx) error { return putIn(tx, "/a.txt", "v2") })
		MustPut(t, s, AdminActor, "/a.txt", "v3")
		MustPut(t, s, AdminActor, "/a.txt", "v2")
		mustUndo(t, s, cs.Rev)
		if MustRead(t, s, AdminActor, "/a.txt") != "v1" {
			t.Fatal("content not restored")
		}
	})
	t.Run("undo needs access", func(t *testing.T) {
		s := newStore(t)
		cs := commit(t, s, AdminActor, func(tx workspace.Tx) error { return putIn(tx, "/r/a.txt", "a") })
		if _, _, err := s.Undo(Ctx(), Alice, cs.Rev, workspace.UndoOptions{}); !errors.Is(err, workspace.ErrNotFound) {
			t.Errorf("no grant: %v", err)
		}
		if err := s.Grant(Ctx(), AdminActor, Alice.Principal, "/", workspace.ModeRead); err != nil {
			t.Fatal(err)
		}
		if _, _, err := s.Undo(Ctx(), Alice, cs.Rev, workspace.UndoOptions{}); !errors.Is(err, workspace.ErrReadOnly) {
			t.Errorf("read grant: %v", err)
		}
		if _, _, err := s.Undo(Ctx(), AdminActor, 999, workspace.UndoOptions{}); !errors.Is(err, workspace.ErrNotFound) {
			t.Errorf("unknown revision: %v", err)
		}
	})
	t.Run("history and the change feed respect grants", func(t *testing.T) {
		s := newStore(t)
		MustPut(t, s, AdminActor, "/shared/a.txt", "a")        // rev 1
		MustPut(t, s, AdminActor, "/private/p.txt", "p")       // rev 2
		commit(t, s, AdminActor, func(tx workspace.Tx) error { // rev 3
			if err := putIn(tx, "/shared/b.txt", "b"); err != nil {
				return err
			}
			return putIn(tx, "/private/q.txt", "q")
		})
		if err := s.Grant(Ctx(), AdminActor, Alice.Principal, "/shared", workspace.ModeRead); err != nil {
			t.Fatal(err)
		}
		revsOf := func(css []workspace.ChangeSet) []int64 {
			var out []int64
			for _, cs := range css {
				out = append(out, cs.Rev)
			}
			return out
		}
		h, err := s.History(Ctx(), Alice, workspace.HistoryQuery{Path: "/shared"})
		if err != nil || !reflect.DeepEqual(revsOf(h), []int64{3, 1}) {
			t.Fatalf("alice history = %v, %v", revsOf(h), err)
		}
		if len(h[0].Ops) != 1 || h[0].Ops[0].NewPath != "/shared/b.txt" {
			t.Fatalf("alice sees ops %+v", h[0].Ops)
		}
		if _, err := s.History(Ctx(), Alice, workspace.HistoryQuery{}); !errors.Is(err, workspace.ErrNotFound) {
			t.Fatalf("alice history of the root: %v", err)
		}
		h, _ = s.History(Ctx(), AdminActor, workspace.HistoryQuery{Limit: 2})
		if !reflect.DeepEqual(revsOf(h), []int64{3, 2}) {
			t.Fatalf("limit = %v", revsOf(h))
		}
		h, _ = s.History(Ctx(), AdminActor, workspace.HistoryQuery{BeforeRev: 3})
		if !reflect.DeepEqual(revsOf(h), []int64{2, 1}) {
			t.Fatalf("before = %v", revsOf(h))
		}
		c, err := s.ChangesSince(Ctx(), Alice, 0, 10)
		if err != nil || !reflect.DeepEqual(revsOf(c), []int64{1, 3}) {
			t.Fatalf("alice feed = %v, %v", revsOf(c), err)
		}
		c, _ = s.ChangesSince(Ctx(), AdminActor, 0, 2)
		if !reflect.DeepEqual(revsOf(c), []int64{1, 2}) {
			t.Fatalf("feed limit = %v", revsOf(c))
		}
		c, _ = s.ChangesSince(Ctx(), AdminActor, 2, 10)
		if !reflect.DeepEqual(revsOf(c), []int64{3}) {
			t.Fatalf("feed after 2 = %v", revsOf(c))
		}
	})
	t.Run("a folder with later children is a conflict", func(t *testing.T) {
		s := newStore(t)
		mk := commit(t, s, AdminActor, func(tx workspace.Tx) error {
			_, err := tx.Mkdir(Ctx(), "/a")
			return err
		})
		put := commit(t, s, AdminActor, func(tx workspace.Tx) error { return putIn(tx, "/a/x", "x") })
		before := Tree(t, s)
		_, conflicts, err := s.Undo(Ctx(), AdminActor, mk.Rev, workspace.UndoOptions{})
		if !errors.Is(err, workspace.ErrConflict) || len(conflicts) != 1 || conflicts[0].Path != "/a" || conflicts[0].Reason != workspace.ReasonChanged {
			t.Fatalf("undo = %+v, %v", conflicts, err)
		}
		if got := Tree(t, s); !reflect.DeepEqual(got, before) {
			t.Fatalf("tree changed: %v", got)
		}
		if MustRead(t, s, AdminActor, "/a/x") != "x" {
			t.Fatal("child hidden")
		}
		mustUndo(t, s, put.Rev)
		mustUndo(t, s, mk.Rev)
		if got := Tree(t, s); len(got) != 0 {
			t.Fatalf("tree = %v", got)
		}
	})
	t.Run("undo that would make a cycle is a conflict", func(t *testing.T) {
		s := newStore(t)
		MustPut(t, s, AdminActor, "/a/b/f", "f")
		mv := commit(t, s, AdminActor, func(tx workspace.Tx) error {
			_, err := tx.Move(Ctx(), "/a/b", "/b")
			return err
		})
		commit(t, s, AdminActor, func(tx workspace.Tx) error {
			_, err := tx.Move(Ctx(), "/a", "/b/a")
			return err
		})
		before := Tree(t, s)
		ctx, cancel := context.WithTimeout(Ctx(), 5*time.Second)
		defer cancel()
		_, conflicts, err := s.Undo(ctx, AdminActor, mv.Rev, workspace.UndoOptions{})
		if !errors.Is(err, workspace.ErrConflict) || len(conflicts) != 1 || conflicts[0].Reason != workspace.ReasonChanged {
			t.Fatalf("undo = %+v, %v", conflicts, err)
		}
		if got := Tree(t, s); !reflect.DeepEqual(got, before) {
			t.Fatalf("tree changed: %v", got)
		}
	})
	t.Run("undo needs write on the parents it edits", func(t *testing.T) {
		s := newStore(t)
		MustPut(t, s, AdminActor, "/ro/x", "x")
		MustPut(t, s, AdminActor, "/mine/keep", "k")
		for p, m := range map[string]workspace.Mode{"/ro": workspace.ModeRead, "/mine": workspace.ModeWrite} {
			if err := s.Grant(Ctx(), AdminActor, Alice.Principal, p, m); err != nil {
				t.Fatal(err)
			}
		}
		mv := commit(t, s, AdminActor, func(tx workspace.Tx) error {
			_, err := tx.Move(Ctx(), "/ro/x", "/mine/x")
			return err
		})
		before := Tree(t, s)
		_, conflicts, err := s.Undo(Ctx(), Alice, mv.Rev, workspace.UndoOptions{})
		if !errors.Is(err, workspace.ErrReadOnly) || len(conflicts) != 0 {
			t.Fatalf("into a read-only folder: %+v, %v", conflicts, err)
		}
		if got := Tree(t, s); !reflect.DeepEqual(got, before) {
			t.Fatalf("tree changed: %v", got)
		}
		if _, err := s.Mkdir(Ctx(), AdminActor, "/ro/w"); err != nil {
			t.Fatal(err)
		}
		mk := commit(t, s, AdminActor, func(tx workspace.Tx) error {
			_, err := tx.Mkdir(Ctx(), "/ro/w2")
			return err
		})
		if err := s.Grant(Ctx(), AdminActor, Alice.Principal, "/ro/w2", workspace.ModeWrite); err != nil {
			t.Fatal(err)
		}
		_, conflicts, err = s.Undo(Ctx(), Alice, mk.Rev, workspace.UndoOptions{})
		if !errors.Is(err, workspace.ErrReadOnly) || len(conflicts) != 0 {
			t.Fatalf("out of a read-only folder: %+v, %v", conflicts, err)
		}
		if _, err := s.Stat(Ctx(), AdminActor, "/ro/w2"); err != nil {
			t.Fatalf("w2 gone: %v", err)
		}
	})
	t.Run("the change feed hides paths the actor cannot read", func(t *testing.T) {
		s := newStore(t)
		MustPut(t, s, AdminActor, "/private/secret-plan.txt", "p")
		commit(t, s, AdminActor, func(tx workspace.Tx) error {
			_, err := tx.Move(Ctx(), "/private/secret-plan.txt", "/shared/plan.txt")
			return err
		})
		if err := s.Grant(Ctx(), AdminActor, Alice.Principal, "/shared", workspace.ModeRead); err != nil {
			t.Fatal(err)
		}
		check := func(name string, css []workspace.ChangeSet) {
			var ops []workspace.Op
			for _, cs := range css {
				for _, o := range cs.Ops {
					if strings.Contains(o.OldPath+o.NewPath, "private") {
						t.Fatalf("%s leaks %+v", name, o)
					}
					ops = append(ops, o)
				}
			}
			if len(ops) != 1 || ops[0].Kind != workspace.OpCreate || ops[0].NewPath != "/shared/plan.txt" || ops[0].OldPath != "" {
				t.Fatalf("%s ops = %+v", name, ops)
			}
		}
		c, err := s.ChangesSince(Ctx(), Alice, 0, 10)
		if err != nil {
			t.Fatal(err)
		}
		check("feed", c)
		h, err := s.History(Ctx(), Alice, workspace.HistoryQuery{Path: "/shared"})
		if err != nil {
			t.Fatal(err)
		}
		check("history", h)
	})
	t.Run("a change set title that names hidden paths is replaced", func(t *testing.T) {
		s := newStore(t)
		MustPut(t, s, AdminActor, "/secret/clientX-plan.pdf", "p")
		MustPut(t, s, AdminActor, "/shared/other.txt", "o")
		if _, err := s.Move(Ctx(), AdminActor, "/secret/clientX-plan.pdf", "/shared/plan.pdf"); err != nil {
			t.Fatal(err)
		}
		mv := latestRev(t, s)
		mustUndo(t, s, mv)
		if err := s.Grant(Ctx(), AdminActor, Alice.Principal, "/shared", workspace.ModeRead); err != nil {
			t.Fatal(err)
		}
		check := func(name string, css []workspace.ChangeSet) {
			t.Helper()
			titles := map[int64]string{}
			for _, cs := range css {
				titles[cs.Rev] = cs.Title
				if strings.Contains(cs.Title, "secret") || strings.Contains(cs.Title, "clientX") {
					t.Fatalf("%s leaks title %q", name, cs.Title)
				}
			}
			if titles[mv] != "Move" || titles[mv+1] != "Undo" {
				t.Fatalf("%s titles = %v", name, titles)
			}
		}
		c, err := s.ChangesSince(Ctx(), Alice, 0, 10)
		if err != nil {
			t.Fatal(err)
		}
		check("feed", c)
		h, err := s.History(Ctx(), Alice, workspace.HistoryQuery{Path: "/shared"})
		if err != nil {
			t.Fatal(err)
		}
		check("history", h)
		// A change set the reader sees whole keeps its stored title.
		for _, cs := range c {
			if cs.Rev == 2 && cs.Title == "" {
				t.Fatalf("title lost: %+v", cs)
			}
		}
		admin, err := s.ChangesSince(Ctx(), AdminActor, mv-1, 1)
		if err != nil || len(admin) != 1 || admin[0].Title != "Move /secret/clientX-plan.pdf to /shared/plan.pdf" {
			t.Fatalf("admin title: %+v %v", admin, err)
		}
	})
	t.Run("an undo title does not name the paths of the undone change set", func(t *testing.T) {
		s := newStore(t)
		MustPut(t, s, AdminActor, "/secret/x.txt", "x")
		MustPut(t, s, AdminActor, "/shared/other.txt", "o")
		if _, err := s.Move(Ctx(), AdminActor, "/secret/x.txt", "/shared/new/y.txt"); err != nil {
			t.Fatal(err)
		}
		first := latestRev(t, s)
		if _, err := s.Move(Ctx(), AdminActor, "/shared/new/y.txt", "/shared/z.txt"); err != nil {
			t.Fatal(err)
		}
		// The node of the first move conflicts; only the folder it created is
		// removed, so every operation of the undo is visible to the reader.
		u, conflicts, err := s.Undo(Ctx(), AdminActor, first, workspace.UndoOptions{SkipConflicts: true})
		if err != nil || len(conflicts) != 1 {
			t.Fatalf("undo: %v %+v", err, conflicts)
		}
		if want := fmt.Sprintf("Undo of revision %d", first); u.Title != want {
			t.Fatalf("stored title = %q, want %q", u.Title, want)
		}
		if err := s.Grant(Ctx(), AdminActor, Alice.Principal, "/shared", workspace.ModeRead); err != nil {
			t.Fatal(err)
		}
		check := func(name string, css []workspace.ChangeSet) {
			t.Helper()
			found := false
			for _, cs := range css {
				if strings.Contains(cs.Title, "secret") || strings.Contains(cs.Title, "x.txt") {
					t.Fatalf("%s leaks title %q", name, cs.Title)
				}
				if cs.Rev == u.Rev {
					found = true
					if len(cs.Ops) != 1 || cs.Ops[0].Kind != workspace.OpDelete || cs.Ops[0].OldPath != "/shared/new" {
						t.Fatalf("%s undo ops = %+v", name, cs.Ops)
					}
				}
			}
			if !found {
				t.Fatalf("%s lacks the undo", name)
			}
		}
		c, err := s.ChangesSince(Ctx(), Alice, 0, 10)
		if err != nil {
			t.Fatal(err)
		}
		check("feed", c)
		h, err := s.History(Ctx(), Alice, workspace.HistoryQuery{Path: "/shared"})
		if err != nil {
			t.Fatal(err)
		}
		check("history", h)
	})
}
