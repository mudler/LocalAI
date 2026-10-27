package sbtest

import (
	"errors"
	"strings"
	"testing"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

func opKinds(cs workspace.ChangeSet) map[workspace.OpKind]int {
	out := map[workspace.OpKind]int{}
	for _, op := range cs.Ops {
		out[op.Kind]++
	}
	return out
}

func testFiles(t *testing.T, newBackend NewBackend) {
	t.Run("a new file is in the workspace after a checkpoint", func(t *testing.T) {
		f := setup(t, newBackend, nil)
		f.mustRun("echo hi > proj/new.txt")
		cs, conflicts := f.checkpoint("turn 1")
		if cs.Rev == 0 || cs.Title != "turn 1" || cs.Kind != workspace.ChangeAgent || cs.Actor != Agent.Principal || len(conflicts) != 0 {
			t.Fatalf("change set = %+v, conflicts = %+v", cs, conflicts)
		}
		if got, _ := f.read("/proj/new.txt"); got != "hi\n" {
			t.Fatalf("new.txt = %q", got)
		}
	})
	t.Run("an existing file can be edited", func(t *testing.T) {
		f := setup(t, newBackend, nil)
		f.mustRun("echo more >> proj/notes.txt && echo b > proj/sub/a.txt")
		if _, c := f.checkpoint("edit"); len(c) != 0 {
			t.Fatalf("conflicts = %+v", c)
		}
		notes, _ := f.read("/proj/notes.txt")
		a, _ := f.read("/proj/sub/a.txt")
		if notes != "notes\nmore\n" || a != "b\n" {
			t.Fatalf("notes = %q, a = %q", notes, a)
		}
	})
	t.Run("a second edit after a checkpoint is not a conflict", func(t *testing.T) {
		f := setup(t, newBackend, nil)
		f.mustRun("echo one >> proj/notes.txt; echo 1 > proj/n.txt")
		f.checkpoint("one")
		f.mustRun("echo two >> proj/notes.txt; echo 2 > proj/n.txt")
		if _, c := f.checkpoint("two"); len(c) != 0 {
			t.Fatalf("conflicts = %+v", c)
		}
		notes, _ := f.read("/proj/notes.txt")
		n, _ := f.read("/proj/n.txt")
		if notes != "notes\none\ntwo\n" || n != "2\n" {
			t.Fatalf("notes = %q, n = %q", notes, n)
		}
	})
	t.Run("an edit of a file another writer changed is a conflict copy", func(t *testing.T) {
		f := setup(t, newBackend, nil)
		if _, err := f.store.Put(ctx(t), Admin, "/proj/old.txt", strings.NewReader("theirs\n"), workspace.PutOptions{Overwrite: true}); err != nil {
			t.Fatal(err)
		}
		f.mustRun("echo mine > proj/old.txt")
		_, c := f.checkpoint("edit")
		if len(c) != 1 || c[0].Reason != workspace.ReasonChanged || c[0].Kept == "" {
			t.Fatalf("conflicts = %+v", c)
		}
		theirs, _ := f.read("/proj/old.txt")
		mine, _ := f.read(c[0].Kept)
		if theirs != "theirs\n" || mine != "mine\n" {
			t.Fatalf("old = %q, kept = %q", theirs, mine)
		}
		if _, c := f.checkpoint("again"); len(c) != 0 {
			t.Fatalf("an unchanged file made another conflict: %+v", c)
		}
	})
	t.Run("a delete is a delete and a rename is one move", func(t *testing.T) {
		f := setup(t, newBackend, nil)
		f.mustRun("rm proj/old.txt && mv proj/sub/a.txt proj/b.txt")
		cs, c := f.checkpoint("tidy")
		if len(c) != 0 {
			t.Fatalf("conflicts = %+v", c)
		}
		if k := opKinds(cs); k[workspace.OpDelete] != 1 || k[workspace.OpMove] != 1 || len(cs.Ops) != 2 {
			t.Fatalf("ops = %+v", cs.Ops)
		}
		if _, ok := f.read("/proj/old.txt"); ok {
			t.Fatal("old.txt survived")
		}
		if got, _ := f.read("/proj/b.txt"); got != "a\n" {
			t.Fatalf("b.txt = %q", got)
		}
	})
	t.Run("a file from an earlier checkpoint can be deleted and renamed", func(t *testing.T) {
		f := setup(t, newBackend, nil)
		f.mustRun("echo n > proj/n.txt; echo m > proj/m.txt; mkdir -p proj/new/deep; echo x > proj/new/deep/x.txt")
		f.checkpoint("create")
		f.mustRun("rm proj/n.txt && mv proj/m.txt proj/m2.txt && rm -r proj/new")
		if _, c := f.checkpoint("tidy"); len(c) != 0 {
			t.Fatalf("conflicts = %+v", c)
		}
		for _, p := range []string{"/proj/n.txt", "/proj/m.txt", "/proj/new/deep/x.txt", "/proj/new"} {
			if _, err := f.store.Stat(ctx(t), Admin, p); !errors.Is(err, workspace.ErrNotFound) {
				t.Fatalf("%s survived: %v", p, err)
			}
		}
		if got, _ := f.read("/proj/m2.txt"); got != "m\n" {
			t.Fatalf("m2.txt = %q", got)
		}
	})
	t.Run("unchanged files in a replaced folder survive the next checkpoint", func(t *testing.T) {
		f := setup(t, newBackend, nil)
		f.mustRun("rm -r proj/sub && mkdir proj/sub && echo x > proj/sub/x.txt")
		f.checkpoint("replace")
		f.mustRun("echo y > proj/sub/y.txt")
		if _, c := f.checkpoint("add"); len(c) != 0 {
			t.Fatalf("conflicts = %+v", c)
		}
		x, okx := f.read("/proj/sub/x.txt")
		y, _ := f.read("/proj/sub/y.txt")
		_, oka := f.read("/proj/sub/a.txt")
		if !okx || x != "x\n" || y != "y\n" || oka {
			t.Fatalf("x = %q (%v), y = %q, a.txt present = %v", x, okx, y, oka)
		}
	})
	t.Run("a checkpoint without changes commits nothing", func(t *testing.T) {
		f := setup(t, newBackend, nil)
		f.mustRun("echo hi > proj/new.txt")
		if cs, c := f.checkpoint("one"); cs.Rev == 0 || len(c) != 0 {
			t.Fatalf("first checkpoint = %+v, %+v", cs, c)
		}
		if cs, c := f.checkpoint("two"); cs.Rev != 0 || len(c) != 0 {
			t.Fatalf("second checkpoint = %+v, %+v", cs, c)
		}
	})
	t.Run("a failed commit keeps the changes", func(t *testing.T) {
		f := setup(t, newBackend, nil)
		f.mustRun("echo keep > proj/keep.txt")
		if err := f.store.Revoke(ctx(t), Admin, Agent.Principal, "/proj"); err != nil {
			t.Fatal(err)
		}
		if _, _, err := f.sb.Checkpoint(ctx(t), "denied"); err == nil {
			t.Fatal("checkpoint without access worked")
		}
		if err := f.sb.Refresh(ctx(t)); err == nil {
			t.Fatal("refresh without access worked")
		}
		if got := f.mustRun("cat proj/keep.txt"); got != "keep\n" {
			t.Fatalf("keep.txt in the guest = %q", got)
		}
		if err := f.store.Grant(ctx(t), Admin, Agent.Principal, "/proj", workspace.ModeWrite); err != nil {
			t.Fatal(err)
		}
		f.checkpoint("retry")
		if got, _ := f.read("/proj/keep.txt"); got != "keep\n" {
			t.Fatalf("keep.txt = %q", got)
		}
	})
}
