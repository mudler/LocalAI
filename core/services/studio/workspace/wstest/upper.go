package wstest

import (
	"reflect"
	"strings"
	"testing"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

func upperSetup(t *testing.T, s workspace.Store) (workspace.View, int64) {
	t.Helper()
	for p, c := range map[string]string{
		"/w/keep.txt": "keep", "/w/edit.txt": "old", "/w/gone.txt": "gone", "/w/a.txt": "move me", "/w/c.txt": "c",
		"/w/d/x.txt": "x", "/w/d/y.txt": "y", "/w/i/z.txt": "z", "/r/ro.txt": "ro",
	} {
		MustPut(t, s, AdminActor, p, c)
	}
	v, err := s.View(Ctx(), AdminActor, []workspace.MountRequest{
		{Path: "/w", At: "w", Mode: workspace.ModeWrite},
		{Path: "/r", At: "r", Mode: workspace.ModeRead},
	})
	if err != nil {
		t.Fatal(err)
	}
	return v, latestRev(t, s)
}

func reasons(cs []workspace.Conflict) []string {
	var out []string
	for _, c := range cs {
		out = append(out, c.Reason)
	}
	return out
}

func testUpper(t *testing.T, newStore NewStore) {
	F, D, W := workspace.FixtureFile, workspace.FixtureDir, workspace.FixtureWhiteout
	t.Run("new, changed and deleted files", func(t *testing.T) {
		s := newStore(t)
		v, base := upperSetup(t, s)
		cs, conflicts, err := s.CommitUpper(Ctx(), v, AliceAdmin(), base, workspace.FixtureSource{
			D("w"), F("w/new.txt", "n"), F("w/edit.txt", "new"), W("w/gone.txt"),
		}, workspace.UpperOptions{Title: "Agent turn 1"})
		if err != nil || len(conflicts) != 0 {
			t.Fatalf("commit: %v %+v", err, conflicts)
		}
		if cs.Kind != workspace.ChangeAgent || cs.Title != "Agent turn 1" || len(cs.Ops) != 3 {
			t.Fatalf("change set = %+v", cs)
		}
		tree := Tree(t, s)
		if tree["/w/new.txt"] != "n" || tree["/w/edit.txt"] != "new" || tree["/w/keep.txt"] != "keep" {
			t.Fatalf("tree = %v", tree)
		}
		if _, ok := tree["/w/gone.txt"]; ok {
			t.Fatal("whiteout did not delete")
		}
	})
	t.Run("rename is one move", func(t *testing.T) {
		s := newStore(t)
		v, base := upperSetup(t, s)
		cs, conflicts, err := s.CommitUpper(Ctx(), v, AliceAdmin(), base, workspace.FixtureSource{
			D("w"), W("w/a.txt"), F("w/b.txt", "move me"),
		}, workspace.UpperOptions{Title: "rename"})
		if err != nil || len(conflicts) != 0 {
			t.Fatalf("commit: %v %+v", err, conflicts)
		}
		if len(cs.Ops) != 1 || cs.Ops[0].Kind != workspace.OpMove || cs.Ops[0].OldPath != "/w/a.txt" || cs.Ops[0].NewPath != "/w/b.txt" {
			t.Fatalf("ops = %+v", cs.Ops)
		}
	})
	for _, prefix := range []string{"trusted.", "user."} {
		t.Run("opaque folder replaces its content ("+prefix+")", func(t *testing.T) {
			s := newStore(t)
			v, base := upperSetup(t, s)
			if _, conflicts, err := s.CommitUpper(Ctx(), v, AliceAdmin(), base, workspace.FixtureSource{
				D("w"), workspace.FixtureOpaqueDir("w/d", prefix), F("w/d/new.txt", "n"),
			}, workspace.UpperOptions{Title: "replace"}); err != nil || len(conflicts) != 0 {
				t.Fatalf("commit: %v %+v", err, conflicts)
			}
			tree := Tree(t, s)
			_, x := tree["/w/d/x.txt"]
			_, y := tree["/w/d/y.txt"]
			if x || y || tree["/w/d/new.txt"] != "n" {
				t.Fatalf("tree = %v", tree)
			}
		})
	}
	t.Run("impure and origin attributes are ignored", func(t *testing.T) {
		s := newStore(t)
		v, base := upperSetup(t, s)
		dir := D("w/i")
		dir.Xattrs = map[string][]byte{"trusted.overlay.impure": []byte("y"), "trusted.overlay.origin": []byte{1, 2, 3}}
		if _, conflicts, err := s.CommitUpper(Ctx(), v, AliceAdmin(), base, workspace.FixtureSource{D("w"), dir, F("w/i/extra.txt", "e")}, workspace.UpperOptions{Title: "x"}); err != nil || len(conflicts) != 0 {
			t.Fatalf("commit: %v %+v", err, conflicts)
		}
		if Tree(t, s)["/w/i/z.txt"] != "z" {
			t.Fatal("a non-opaque folder lost its content")
		}
	})
	t.Run("a change after the base keeps both", func(t *testing.T) {
		s := newStore(t)
		v, base := upperSetup(t, s)
		MustPut(t, s, AdminActor, "/w/edit.txt", "theirs")
		_, conflicts, err := s.CommitUpper(Ctx(), v, AliceAdmin(), base, workspace.FixtureSource{D("w"), F("w/edit.txt", "mine")}, workspace.UpperOptions{Title: "x"})
		if err != nil {
			t.Fatal(err)
		}
		if len(conflicts) != 1 || conflicts[0].Reason != workspace.ReasonChanged || conflicts[0].Kept != "/w/edit (from chat 7).txt" {
			t.Fatalf("conflicts = %+v", conflicts)
		}
		if MustRead(t, s, AdminActor, "/w/edit.txt") != "theirs" || MustRead(t, s, AdminActor, conflicts[0].Kept) != "mine" {
			t.Fatal("both versions must be kept")
		}
	})
	t.Run("a delete after a change is a conflict", func(t *testing.T) {
		s := newStore(t)
		v, base := upperSetup(t, s)
		MustPut(t, s, AdminActor, "/w/gone.txt", "changed")
		_, conflicts, err := s.CommitUpper(Ctx(), v, AliceAdmin(), base, workspace.FixtureSource{D("w"), W("w/gone.txt")}, workspace.UpperOptions{Title: "x"})
		if err != nil || !reflect.DeepEqual(reasons(conflicts), []string{workspace.ReasonChanged}) {
			t.Fatalf("commit: %v %+v", err, conflicts)
		}
		if MustRead(t, s, AdminActor, "/w/gone.txt") != "changed" {
			t.Fatal("the changed file was deleted")
		}
	})
	t.Run("entries outside the view or in a read-only mount are refused", func(t *testing.T) {
		s := newStore(t)
		v, base := upperSetup(t, s)
		cs, conflicts, err := s.CommitUpper(Ctx(), v, AliceAdmin(), base, workspace.FixtureSource{
			D("other"), F("other/x", "x"), D("r"), F("r/ro.txt", "hacked"),
		}, workspace.UpperOptions{Title: "x"})
		if err != nil {
			t.Fatal(err)
		}
		want := []string{workspace.ReasonOutsideView, workspace.ReasonOutsideView, workspace.ReasonReadOnly}
		if !reflect.DeepEqual(reasons(conflicts), want) || cs.Rev != 0 {
			t.Fatalf("cs = %+v conflicts = %+v", cs, conflicts)
		}
		if MustRead(t, s, AdminActor, "/r/ro.txt") != "ro" {
			t.Fatal("read-only file changed")
		}
	})
	t.Run("symbolic links are reported", func(t *testing.T) {
		s := newStore(t)
		v, base := upperSetup(t, s)
		_, conflicts, err := s.CommitUpper(Ctx(), v, AliceAdmin(), base, workspace.FixtureSource{D("w"), workspace.FixtureSymlink("w/link")}, workspace.UpperOptions{Title: "x"})
		if err != nil || !reflect.DeepEqual(reasons(conflicts), []string{workspace.ReasonUnsupported}) {
			t.Fatalf("commit: %v %+v", err, conflicts)
		}
	})
	t.Run("bad name is reported", func(t *testing.T) {
		s := newStore(t)
		v, base := upperSetup(t, s)
		_, conflicts, err := s.CommitUpper(Ctx(), v, AliceAdmin(), base, workspace.FixtureSource{
			D("w"), F("w/bad\x01name", "b"), F("w/"+strings.Repeat("n", 300), "long"), F("w/ok.txt", "ok"),
		}, workspace.UpperOptions{Title: "x"})
		if err != nil || !reflect.DeepEqual(reasons(conflicts), []string{workspace.ReasonBadName, workspace.ReasonBadName}) {
			t.Fatalf("commit: %v %+v", err, conflicts)
		}
		if MustRead(t, s, AdminActor, "/w/ok.txt") != "ok" {
			t.Fatal("the valid entry was not committed")
		}
	})
	t.Run("a file where a folder was", func(t *testing.T) {
		s := newStore(t)
		v, base := upperSetup(t, s)
		_, conflicts, err := s.CommitUpper(Ctx(), v, AliceAdmin(), base, workspace.FixtureSource{D("w"), F("w/i", "now a file")}, workspace.UpperOptions{Title: "x"})
		if err != nil || len(conflicts) != 0 {
			t.Fatalf("commit: %v %+v", err, conflicts)
		}
		if MustRead(t, s, AdminActor, "/w/i") != "now a file" {
			t.Fatal("folder not replaced")
		}
		if _, ok := Tree(t, s)["/w/i/z.txt"]; ok {
			t.Fatal("the folder content is still there")
		}
	})
	t.Run("a second commit writes nothing", func(t *testing.T) {
		s := newStore(t)
		v, base := upperSetup(t, s)
		src := workspace.FixtureSource{
			D("w"), F("w/new.txt", "n"), F("w/edit.txt", "new"), W("w/gone.txt"),
			W("w/a.txt"), F("w/b.txt", "move me"), workspace.FixtureOpaqueDir("w/d", "user."), F("w/d/new.txt", "n"),
		}
		cs1, c1, err := s.CommitUpper(Ctx(), v, AliceAdmin(), base, src, workspace.UpperOptions{Title: "x"})
		if err != nil || len(c1) != 0 || cs1.Rev == 0 {
			t.Fatalf("first: %+v %+v %v", cs1, c1, err)
		}
		cs2, c2, err := s.CommitUpper(Ctx(), v, AliceAdmin(), cs1.Rev, src, workspace.UpperOptions{Title: "x"})
		if err != nil || len(c2) != 0 || cs2.Rev != 0 {
			t.Fatalf("second: %+v %+v %v", cs2, c2, err)
		}
	})

	t.Run("a renamed mount folder is followed by node", func(t *testing.T) {
		s := newStore(t)
		v, base := upperSetup(t, s)
		if _, err := s.Move(Ctx(), AdminActor, "/w", "/w2"); err != nil {
			t.Fatal(err)
		}
		_, conflicts, err := s.CommitUpper(Ctx(), v, AliceAdmin(), base, workspace.FixtureSource{D("w"), F("w/new.txt", "n")}, workspace.UpperOptions{Title: "x"})
		if err != nil || len(conflicts) != 0 {
			t.Fatalf("commit: %v %+v", err, conflicts)
		}
		tree := Tree(t, s)
		if tree["/w2/new.txt"] != "n" {
			t.Fatalf("tree = %v", tree)
		}
		for p := range tree {
			if p == "/w" || strings.HasPrefix(p, "/w/") {
				t.Fatalf("a write landed at the old path: %v", tree)
			}
		}
	})
	t.Run("a deleted mount folder reports every entry", func(t *testing.T) {
		s := newStore(t)
		v, base := upperSetup(t, s)
		if err := s.Delete(Ctx(), AdminActor, "/w"); err != nil {
			t.Fatal(err)
		}
		cs, conflicts, err := s.CommitUpper(Ctx(), v, AliceAdmin(), base, workspace.FixtureSource{D("w"), F("w/new.txt", "n"), W("w/keep.txt")}, workspace.UpperOptions{Title: "x"})
		if err != nil || cs.Rev != 0 {
			t.Fatalf("commit: %+v %v", cs, err)
		}
		want := []string{workspace.ReasonParentDeleted, workspace.ReasonParentDeleted}
		if !reflect.DeepEqual(reasons(conflicts), want) {
			t.Fatalf("conflicts = %+v", conflicts)
		}
		if _, ok := Tree(t, s)["/w"]; ok {
			t.Fatal("the deleted folder was recreated")
		}
	})
	t.Run("an edit of a file deleted after the base is a conflict", func(t *testing.T) {
		s := newStore(t)
		v, base := upperSetup(t, s)
		if err := s.Delete(Ctx(), AdminActor, "/w/edit.txt"); err != nil {
			t.Fatal(err)
		}
		cs, conflicts, err := s.CommitUpper(Ctx(), v, AliceAdmin(), base, workspace.FixtureSource{D("w"), F("w/edit.txt", "mine")}, workspace.UpperOptions{Title: "x"})
		if err != nil || cs.Rev == 0 || !reflect.DeepEqual(reasons(conflicts), []string{workspace.ReasonChanged}) {
			t.Fatalf("commit: %+v %+v %v", cs, conflicts, err)
		}
		tree := Tree(t, s)
		if _, ok := tree["/w/edit.txt"]; ok {
			t.Fatal("the deleted file was recreated")
		}
		if k := conflicts[0].Kept; !strings.HasPrefix(k, "/w/edit") || tree[k] != "mine" {
			t.Fatalf("kept = %q, tree = %v", k, tree)
		}
	})
	t.Run("a file created and deleted after the base can be created", func(t *testing.T) {
		s := newStore(t)
		v, base := upperSetup(t, s)
		MustPut(t, s, AdminActor, "/w/fresh.txt", "x")
		if err := s.Delete(Ctx(), AdminActor, "/w/fresh.txt"); err != nil {
			t.Fatal(err)
		}
		_, conflicts, err := s.CommitUpper(Ctx(), v, AliceAdmin(), base, workspace.FixtureSource{D("w"), F("w/fresh.txt", "mine")}, workspace.UpperOptions{Title: "x"})
		if err != nil || len(conflicts) != 0 || MustRead(t, s, AdminActor, "/w/fresh.txt") != "mine" {
			t.Fatalf("commit: %+v %v", conflicts, err)
		}
	})

	t.Run("an opaque folder keeps a lower child changed after the base", func(t *testing.T) {
		s := newStore(t)
		v, base := upperSetup(t, s)
		MustPut(t, s, AdminActor, "/w/d/x.txt", "theirs")
		_, conflicts, err := s.CommitUpper(Ctx(), v, AliceAdmin(), base, workspace.FixtureSource{
			D("w"), workspace.FixtureOpaqueDir("w/d", "user."), F("w/d/new.txt", "n"),
		}, workspace.UpperOptions{Title: "x"})
		if err != nil || !reflect.DeepEqual(reasons(conflicts), []string{workspace.ReasonChanged}) {
			t.Fatalf("commit: %v %+v", err, conflicts)
		}
		tree := Tree(t, s)
		if tree["/w/d/x.txt"] != "theirs" || tree["/w/d/new.txt"] != "n" {
			t.Fatalf("tree = %v", tree)
		}
		if _, ok := tree["/w/d/y.txt"]; ok {
			t.Fatal("the unchanged lower child was kept")
		}
	})
	t.Run("an opaque folder keeps a path whose entry became a conflict", func(t *testing.T) {
		s := newStore(t)
		v, base := upperSetup(t, s)
		_, conflicts, err := s.CommitUpper(Ctx(), v, AliceAdmin(), base, workspace.FixtureSource{
			D("w"), workspace.FixtureOpaqueDir("w/d", "user."), workspace.FixtureSymlink("w/d/x.txt"),
		}, workspace.UpperOptions{Title: "x"})
		if err != nil || !reflect.DeepEqual(reasons(conflicts), []string{workspace.ReasonUnsupported}) {
			t.Fatalf("commit: %v %+v", err, conflicts)
		}
		if MustRead(t, s, AdminActor, "/w/d/x.txt") != "x" {
			t.Fatal("the lower file was deleted")
		}
	})
	t.Run("a file over a folder changed after the base keeps both", func(t *testing.T) {
		s := newStore(t)
		v, base := upperSetup(t, s)
		MustPut(t, s, AdminActor, "/w/i/z.txt", "theirs")
		_, conflicts, err := s.CommitUpper(Ctx(), v, AliceAdmin(), base, workspace.FixtureSource{D("w"), F("w/i", "mine")}, workspace.UpperOptions{Title: "x"})
		if err != nil || len(conflicts) != 1 || conflicts[0].Reason != workspace.ReasonChanged || conflicts[0].Kept == "" {
			t.Fatalf("commit: %v %+v", err, conflicts)
		}
		if MustRead(t, s, AdminActor, conflicts[0].Kept) != "mine" || MustRead(t, s, AdminActor, "/w/i/z.txt") != "theirs" {
			t.Fatal("both versions must be kept")
		}
	})
	t.Run("a move target deleted after the base is not restored", func(t *testing.T) {
		s := newStore(t)
		v, base := upperSetup(t, s)
		if err := s.Delete(Ctx(), AdminActor, "/w/c.txt"); err != nil {
			t.Fatal(err)
		}
		_, conflicts, err := s.CommitUpper(Ctx(), v, AliceAdmin(), base, workspace.FixtureSource{D("w"), W("w/a.txt"), F("w/c.txt", "move me")}, workspace.UpperOptions{Title: "x"})
		if err != nil || !reflect.DeepEqual(reasons(conflicts), []string{workspace.ReasonChanged}) {
			t.Fatalf("commit: %v %+v", err, conflicts)
		}
		tree := Tree(t, s)
		if _, ok := tree["/w/c.txt"]; ok {
			t.Fatal("the deleted file came back")
		}
		if _, ok := tree["/w/a.txt"]; ok {
			t.Fatal("the whiteout did not delete")
		}
	})
	t.Run("a move into a folder deleted after the base does not recreate it", func(t *testing.T) {
		s := newStore(t)
		v, base := upperSetup(t, s)
		if err := s.Delete(Ctx(), AdminActor, "/w/d"); err != nil {
			t.Fatal(err)
		}
		_, conflicts, err := s.CommitUpper(Ctx(), v, AliceAdmin(), base, workspace.FixtureSource{D("w"), W("w/a.txt"), F("w/d/a.txt", "move me")}, workspace.UpperOptions{Title: "x"})
		if err != nil || !reflect.DeepEqual(reasons(conflicts), []string{workspace.ReasonChanged}) {
			t.Fatalf("commit: %v %+v", err, conflicts)
		}
		if _, ok := Tree(t, s)["/w/d"]; ok {
			t.Fatal("the deleted folder came back")
		}
	})
	t.Run("an edit of a file moved and then deleted is a conflict", func(t *testing.T) {
		s := newStore(t)
		v, base := upperSetup(t, s)
		if _, err := s.Move(Ctx(), AdminActor, "/w/edit.txt", "/w/other.txt"); err != nil {
			t.Fatal(err)
		}
		if err := s.Delete(Ctx(), AdminActor, "/w/other.txt"); err != nil {
			t.Fatal(err)
		}
		cs, conflicts, err := s.CommitUpper(Ctx(), v, AliceAdmin(), base, workspace.FixtureSource{D("w"), F("w/edit.txt", "mine")}, workspace.UpperOptions{Title: "x"})
		if err != nil || cs.Rev == 0 || !reflect.DeepEqual(reasons(conflicts), []string{workspace.ReasonChanged}) {
			t.Fatalf("commit: %+v %+v %v", cs, conflicts, err)
		}
		tree := Tree(t, s)
		if _, ok := tree["/w/edit.txt"]; ok {
			t.Fatal("the file came back")
		}
		if k := conflicts[0].Kept; k == "" || !strings.HasPrefix(k, "/w/") || tree[k] != "mine" {
			t.Fatalf("kept = %q, tree = %v", conflicts[0].Kept, tree)
		}
	})
	t.Run("an edit inside a renamed folder is a conflict and leaves no ghost folder", func(t *testing.T) {
		s := newStore(t)
		v, base := upperSetup(t, s)
		if _, err := s.Move(Ctx(), AdminActor, "/w/d", "/w/dd"); err != nil {
			t.Fatal(err)
		}
		cs, conflicts, err := s.CommitUpper(Ctx(), v, AliceAdmin(), base, workspace.FixtureSource{D("w"), D("w/d"), F("w/d/x.txt", "mine")}, workspace.UpperOptions{Title: "x"})
		if err != nil || cs.Rev == 0 || !reflect.DeepEqual(reasons(conflicts), []string{workspace.ReasonChanged}) {
			t.Fatalf("commit: %+v %+v %v", cs, conflicts, err)
		}
		tree := Tree(t, s)
		if _, ok := tree["/w/d"]; ok {
			t.Fatalf("ghost folder: %v", tree)
		}
		if tree["/w/dd/x.txt"] != "x" {
			t.Fatalf("tree = %v", tree)
		}
		if k := conflicts[0].Kept; !strings.HasPrefix(k, "/w/dd/x") || tree[k] != "mine" {
			t.Fatalf("kept = %q, tree = %v", k, tree)
		}
	})
	t.Run("an edit of a file moved after the base keeps a copy beside it", func(t *testing.T) {
		s := newStore(t)
		v, base := upperSetup(t, s)
		if _, err := s.Move(Ctx(), AdminActor, "/w/edit.txt", "/w/d/moved.txt"); err != nil {
			t.Fatal(err)
		}
		_, conflicts, err := s.CommitUpper(Ctx(), v, AliceAdmin(), base, workspace.FixtureSource{D("w"), F("w/edit.txt", "mine")}, workspace.UpperOptions{Title: "x"})
		if err != nil || len(conflicts) != 1 {
			t.Fatalf("commit: %+v %v", conflicts, err)
		}
		tree := Tree(t, s)
		if k := conflicts[0].Kept; !strings.HasPrefix(k, "/w/d/moved") || tree[k] != "mine" || tree["/w/d/moved.txt"] != "old" {
			t.Fatalf("kept = %q, tree = %v", k, tree)
		}
	})
	// A conflict copy lands only inside a write mount of the view.
	copyStaysInWritable := func(t *testing.T, dest, edit, wantPrefix string) {
		t.Helper()
		s := newStore(t)
		v, base := upperSetup(t, s)
		MustPut(t, s, AdminActor, "/other/keep.txt", "k")
		if _, err := s.Move(Ctx(), AdminActor, "/w/"+edit, dest); err != nil {
			t.Fatal(err)
		}
		before := Tree(t, s)
		_, conflicts, err := s.CommitUpper(Ctx(), v, AliceAdmin(), base, workspace.FixtureSource{F("w/"+edit, "mine")}, workspace.UpperOptions{Title: "t"})
		if err != nil || len(conflicts) != 1 || conflicts[0].Reason != workspace.ReasonChanged {
			t.Fatalf("commit: %v %+v", err, conflicts)
		}
		tree := Tree(t, s)
		k := conflicts[0].Kept
		if k == "" || !strings.HasPrefix(k, wantPrefix) || tree[k] != "mine" {
			t.Fatalf("kept = %q, want a copy under %s; tree = %v", k, wantPrefix, tree)
		}
		for p := range tree {
			if _, had := before[p]; !had && p != k {
				t.Fatalf("unexpected new path %s", p)
			}
			if _, had := before[p]; !had && !strings.HasPrefix(p, "/w/") {
				t.Fatalf("copy outside the writable mount: %s", p)
			}
		}
	}
	t.Run("a copy for a node moved into a read-only mount stays in the writable one", func(t *testing.T) {
		copyStaysInWritable(t, "/r/edit.txt", "edit.txt", "/w/edit")
	})
	t.Run("a copy for a node moved into an unmounted folder stays in the writable mount", func(t *testing.T) {
		copyStaysInWritable(t, "/other/edit.txt", "edit.txt", "/w/edit")
	})
	t.Run("a copy for a nested node moved out of the view goes to its live base folder", func(t *testing.T) {
		copyStaysInWritable(t, "/r/x.txt", "d/x.txt", "/w/d/x")
	})
	t.Run("no copy is kept when the writable mount is gone", func(t *testing.T) {
		s := newStore(t)
		v, base := upperSetup(t, s)
		if _, err := s.Move(Ctx(), AdminActor, "/w/edit.txt", "/r/edit.txt"); err != nil {
			t.Fatal(err)
		}
		if err := s.Delete(Ctx(), AdminActor, "/w"); err != nil {
			t.Fatal(err)
		}
		before := Tree(t, s)
		_, conflicts, err := s.CommitUpper(Ctx(), v, AliceAdmin(), base, workspace.FixtureSource{F("w/edit.txt", "mine")}, workspace.UpperOptions{Title: "t"})
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range conflicts {
			if c.Kept != "" {
				t.Fatalf("kept %q", c.Kept)
			}
		}
		if got := Tree(t, s); !reflect.DeepEqual(got, before) {
			t.Fatalf("tree changed: %v", got)
		}
	})
}
