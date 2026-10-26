package wstest

import (
	"reflect"
	"testing"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// checkpoint commits src as a checkpoint of one sandbox: base stays fixed and
// own holds the revisions of the earlier checkpoints.
func checkpoint(t *testing.T, s workspace.Store, v workspace.View, base int64, own *[]int64, src workspace.FixtureSource) []workspace.Conflict {
	t.Helper()
	cs, conflicts, err := s.CommitUpper(Ctx(), v, AliceAdmin(), base, src, workspace.UpperOptions{Title: "turn", Own: *own})
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if cs.Rev != 0 {
		*own = append(*own, cs.Rev)
	}
	return conflicts
}

func testUpperOwn(t *testing.T, newStore NewStore) {
	F, D, W := workspace.FixtureFile, workspace.FixtureDir, workspace.FixtureWhiteout
	t.Run("a second edit after a checkpoint is not a conflict", func(t *testing.T) {
		s := newStore(t)
		v, base := upperSetup(t, s)
		var own []int64
		if c := checkpoint(t, s, v, base, &own, workspace.FixtureSource{D("w"), F("w/edit.txt", "one"), F("w/new.txt", "n1")}); len(c) != 0 {
			t.Fatalf("first: %+v", c)
		}
		if c := checkpoint(t, s, v, base, &own, workspace.FixtureSource{D("w"), F("w/edit.txt", "two"), F("w/new.txt", "n2")}); len(c) != 0 {
			t.Fatalf("second: %+v", c)
		}
		tree := Tree(t, s)
		if tree["/w/edit.txt"] != "two" || tree["/w/new.txt"] != "n2" {
			t.Fatalf("tree = %v", tree)
		}
	})
	t.Run("a delete and a rename after a checkpoint are not conflicts", func(t *testing.T) {
		s := newStore(t)
		v, base := upperSetup(t, s)
		var own []int64
		checkpoint(t, s, v, base, &own, workspace.FixtureSource{D("w"), F("w/new.txt", "n"), F("w/edit.txt", "mine"), F("w/m.txt", "move me too")})
		c := checkpoint(t, s, v, base, &own, workspace.FixtureSource{D("w"), W("w/new.txt"), W("w/edit.txt"), W("w/m.txt"), F("w/m2.txt", "move me too")})
		if len(c) != 0 {
			t.Fatalf("conflicts = %+v", c)
		}
		tree := Tree(t, s)
		for _, p := range []string{"/w/new.txt", "/w/edit.txt", "/w/m.txt"} {
			if _, ok := tree[p]; ok {
				t.Fatalf("%s survived: %v", p, tree)
			}
		}
		if tree["/w/m2.txt"] != "move me too" {
			t.Fatalf("tree = %v", tree)
		}
	})
	t.Run("an edit of a file another writer changed between checkpoints is a conflict copy", func(t *testing.T) {
		s := newStore(t)
		v, base := upperSetup(t, s)
		var own []int64
		checkpoint(t, s, v, base, &own, workspace.FixtureSource{D("w"), F("w/edit.txt", "one")})
		MustPut(t, s, AdminActor, "/w/edit.txt", "theirs")
		c := checkpoint(t, s, v, base, &own, workspace.FixtureSource{D("w"), F("w/edit.txt", "two")})
		if len(c) != 1 || c[0].Reason != workspace.ReasonChanged || c[0].Kept != "/w/edit (from chat 7).txt" {
			t.Fatalf("conflicts = %+v", c)
		}
		if MustRead(t, s, AdminActor, "/w/edit.txt") != "theirs" || MustRead(t, s, AdminActor, c[0].Kept) != "two" {
			t.Fatal("both versions must be kept")
		}
	})
	t.Run("a change by another writer before an own change is still a conflict", func(t *testing.T) {
		s := newStore(t)
		v, base := upperSetup(t, s)
		var own []int64
		MustPut(t, s, AdminActor, "/w/d/x.txt", "theirs")
		checkpoint(t, s, v, base, &own, workspace.FixtureSource{D("w"), F("w/d/new.txt", "n")})
		c := checkpoint(t, s, v, base, &own, workspace.FixtureSource{D("w"), W("w/d")})
		if !reflect.DeepEqual(reasons(c), []string{workspace.ReasonChanged}) {
			t.Fatalf("conflicts = %+v", c)
		}
		if Tree(t, s)["/w/d/x.txt"] != "theirs" {
			t.Fatal("the folder with a change by another writer was deleted")
		}
	})
	t.Run("own revisions of another actor or chat are ignored", func(t *testing.T) {
		s := newStore(t)
		v, base := upperSetup(t, s)
		if err := s.Grant(Ctx(), AdminActor, Bob.Principal, "/w", workspace.ModeWrite); err != nil {
			t.Fatal(err)
		}
		other := []workspace.Actor{AdminActor, Bob, {Principal: Admin, ChatID: "8"}}
		for _, a := range other {
			n := MustPut(t, s, a, "/w/edit.txt", "theirs "+a.Principal+a.ChatID)
			_, c, err := s.CommitUpper(Ctx(), v, AliceAdmin(), base, workspace.FixtureSource{D("w"), F("w/edit.txt", "mine")},
				workspace.UpperOptions{Title: "turn", Own: []int64{n.Rev}})
			if err != nil {
				t.Fatal(err)
			}
			if len(c) != 1 || c[0].Reason != workspace.ReasonChanged {
				t.Fatalf("actor %+v: conflicts = %+v", a, c)
			}
		}
	})
	t.Run("a revision that does not exist is ignored", func(t *testing.T) {
		s := newStore(t)
		v, base := upperSetup(t, s)
		_, c, err := s.CommitUpper(Ctx(), v, AliceAdmin(), base, workspace.FixtureSource{D("w"), F("w/edit.txt", "mine")},
			workspace.UpperOptions{Title: "turn", Own: []int64{base + 100, -1}})
		if err != nil || len(c) != 0 {
			t.Fatalf("commit: %v %+v", err, c)
		}
	})
}
