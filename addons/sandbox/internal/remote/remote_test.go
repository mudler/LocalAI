// SPDX-License-Identifier: MIT
package remote_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mudler/LocalAI/addons/sandbox/internal/remote"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// fakeHost records the host operations and answers them. onCommit sees the
// archive while it still exists.
type fakeHost struct {
	t        *testing.T
	dataDir  string
	calls    []call
	onCommit func(dir string)
	answers  map[string]any
	err      error
}

type call struct {
	op, opID string
	args     map[string]any
}

func (h *fakeHost) Do(_ context.Context, opID, op string, args, out any) error {
	raw, _ := json.Marshal(args)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	h.calls = append(h.calls, call{op: op, opID: opID, args: m})
	if h.err != nil {
		return h.err
	}
	if op == "workspace.commit" && h.onCommit != nil {
		h.onCommit(m["dir"].(string))
	}
	b, _ := json.Marshal(h.answers[op])
	return json.Unmarshal(b, out)
}

func newStore(t *testing.T) (*remote.Store, *fakeHost, string) {
	t.Helper()
	dir := t.TempDir()
	h := &fakeHost{t: t, dataDir: dir, answers: map[string]any{}}
	return remote.New(h, dir), h, dir
}

func ctxOf(id string) context.Context { return remote.WithOperation(context.Background(), id) }

var actor = workspace.Actor{Principal: "ignored", ChatID: "c1"}

func view(t *testing.T, s *remote.Store) workspace.View {
	t.Helper()
	v, err := s.View(context.Background(), actor, []workspace.MountRequest{{Path: "/proj", At: "proj", Mode: workspace.ModeWrite}, {Path: "/docs", At: "docs", Mode: workspace.ModeRead}})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestMaterializeAsksTheHostForTheMountsInsideTheDataDirectory(t *testing.T) {
	s, h, dir := newStore(t)
	h.answers["workspace.materialize"] = map[string]int64{"revision": 7}
	rev, err := s.Materialize(ctxOf("op1"), view(t, s), filepath.Join(dir, "sessions", "s1", "lower"))
	if err != nil || rev != 7 {
		t.Fatalf("Materialize = %d, %v", rev, err)
	}
	c := h.calls[0]
	if c.op != "workspace.materialize" || c.opID != "op1" || c.args["dir"] != "sessions/s1/lower" {
		t.Fatalf("call = %+v", c)
	}
	mounts := c.args["mounts"].([]any)
	first := mounts[0].(map[string]any)
	second := mounts[1].(map[string]any)
	if first["path"] != "/proj" || first["at"] != "proj" || first["write"] != true || second["write"] == true {
		t.Fatalf("mounts = %v", mounts)
	}
}

func TestADirectoryOutsideTheDataDirectoryIsRefusedBeforeTheHostIsAsked(t *testing.T) {
	s, h, dir := newStore(t)
	for _, bad := range []string{"/etc/lower", filepath.Join(dir, "..", "x"), "relative/lower", dir} {
		if _, err := s.Materialize(ctxOf("op1"), view(t, s), bad); err == nil {
			t.Errorf("Materialize into %q was accepted", bad)
		}
	}
	if len(h.calls) != 0 {
		t.Errorf("the host was asked %d times", len(h.calls))
	}
}

func TestACallWithoutAnOperationIsRefused(t *testing.T) {
	s, h, dir := newStore(t)
	if _, err := s.Materialize(context.Background(), view(t, s), filepath.Join(dir, "a", "lower")); err == nil {
		t.Error("Materialize without an operation was accepted")
	}
	if _, _, err := s.CommitUpper(context.Background(), view(t, s), actor, 0, workspace.FixtureSource{}, workspace.UpperOptions{}); err == nil {
		t.Error("CommitUpper without an operation was accepted")
	}
	if len(h.calls) != 0 {
		t.Errorf("the host was asked")
	}
}

func TestAnUpperLayerIsSentAsAnArchiveTheHostReadsBack(t *testing.T) {
	s, h, dir := newStore(t)
	h.answers["workspace.commit"] = map[string]any{"revision": 9, "conflicts": []map[string]string{{"path": "proj/a.txt", "reason": "changed"}}}
	layer := workspace.FixtureSource{
		workspace.FixtureDir("proj"),
		workspace.FixtureFile("proj/new.txt", "made in the sandbox\n"),
		workspace.FixtureOpaqueDir("proj/fresh", "trusted."),
		workspace.FixtureWhiteout("proj/old.txt"),
		workspace.FixtureSymlink("proj/link"),
	}
	var got []workspace.UpperEntry
	var contents = map[string]string{}
	h.onCommit = func(rel string) {
		f, err := os.Open(filepath.Join(dir, filepath.FromSlash(rel), "upper.tar"))
		if err != nil {
			t.Errorf("the archive is not in the directory the host was told: %v", err)
			return
		}
		defer func() { _ = f.Close() }()
		err = workspace.ArchiveSource(f).Walk(context.Background(), func(e workspace.UpperEntry) error {
			if e.Open != nil {
				rc, _ := e.Open()
				b, _ := io.ReadAll(rc)
				contents[e.Path] = string(b)
			}
			e.Open = nil
			got = append(got, e)
			return nil
		})
		if err != nil {
			t.Errorf("the host cannot read the archive: %v", err)
		}
	}
	cs, conflicts, err := s.CommitUpper(ctxOf("op2"), view(t, s), actor, 5, layer, workspace.UpperOptions{Title: "run", Own: []int64{3, 4}})
	if err != nil || cs.Rev != 9 || len(conflicts) != 1 || conflicts[0].Path != "proj/a.txt" || conflicts[0].Reason != "changed" {
		t.Fatalf("CommitUpper = %+v %+v %v", cs, conflicts, err)
	}
	kinds := map[string]workspace.EntryKind{}
	for _, e := range got {
		kinds[e.Path] = e.Kind
	}
	want := map[string]workspace.EntryKind{"proj": workspace.EntryDir, "proj/new.txt": workspace.EntryFile, "proj/fresh": workspace.EntryDir, "proj/old.txt": workspace.EntryWhiteout, "proj/link": workspace.EntrySymlink}
	if len(kinds) != len(want) {
		t.Fatalf("entries = %v", kinds)
	}
	for p, k := range want {
		if kinds[p] != k {
			t.Errorf("%s is kind %v, want %v", p, kinds[p], k)
		}
	}
	if contents["proj/new.txt"] != "made in the sandbox\n" {
		t.Errorf("content = %q", contents["proj/new.txt"])
	}
	for _, e := range got {
		if e.Path == "proj/fresh" && string(e.Xattrs["trusted.overlay.opaque"]) != "y" {
			t.Errorf("the opaque mark was lost: %v", e.Xattrs)
		}
	}
	c := h.calls[0]
	if c.op != "workspace.commit" || c.args["base"].(float64) != 5 || c.args["title"] != "run" {
		t.Fatalf("call = %+v", c)
	}
	if own := c.args["own"].([]any); len(own) != 2 || own[0].(float64) != 3 {
		t.Errorf("own = %v", own)
	}
	if left, _ := os.ReadDir(dir); len(left) != 0 {
		t.Errorf("the add-on left files behind: %v", left)
	}
}

func TestAHostRefusalComesBackAsTheError(t *testing.T) {
	s, h, dir := newStore(t)
	h.err = errors.New("host said no")
	if _, err := s.Materialize(ctxOf("op1"), view(t, s), filepath.Join(dir, "s", "lower")); err == nil || !strings.Contains(err.Error(), "host said no") {
		t.Errorf("error = %v", err)
	}
}

func TestChangesSinceListsTheRevisionsTheHostReports(t *testing.T) {
	s, h, _ := newStore(t)
	h.answers["workspace.changes"] = map[string]any{"changes": []map[string]any{{"revision": 11, "title": "a"}, {"revision": 12, "title": "b"}}}
	got, err := s.ChangesSince(ctxOf("op3"), actor, 10, 50)
	if err != nil || len(got) != 2 || got[0].Rev != 11 || got[1].Title != "b" {
		t.Fatalf("ChangesSince = %+v, %v", got, err)
	}
	if h.calls[0].args["since"].(float64) != 10 {
		t.Errorf("args = %v", h.calls[0].args)
	}
}

func TestEveryOtherMethodRefusesAndAsksNobody(t *testing.T) {
	s, h, _ := newStore(t)
	ctx := ctxOf("op1")
	checks := map[string]error{}
	_, checks["Put"] = s.Put(ctx, actor, "/a", strings.NewReader("x"), workspace.PutOptions{})
	_, checks["Mkdir"] = s.Mkdir(ctx, actor, "/a")
	_, checks["Move"] = s.Move(ctx, actor, "/a", "/b")
	checks["Delete"] = s.Delete(ctx, actor, "/a")
	_, _, checks["Open"] = s.Open(ctx, actor, "/a")
	_, checks["Stat"] = s.Stat(ctx, actor, "/a")
	_, checks["List"] = s.List(ctx, actor, "/")
	_, checks["Begin"] = s.Begin(ctx, actor, "t")
	_, checks["History"] = s.History(ctx, actor, workspace.HistoryQuery{})
	_, _, checks["Undo"] = s.Undo(ctx, actor, 1, workspace.UndoOptions{})
	checks["Grant"] = s.Grant(ctx, actor, "p", "/a", workspace.ModeRead)
	checks["Revoke"] = s.Revoke(ctx, actor, "p", "/a")
	_, checks["ReadablePrefixes"] = s.ReadablePrefixes(ctx, actor)
	for name, err := range checks {
		if !errors.Is(err, workspace.ErrInvalid) {
			t.Errorf("%s = %v, want ErrInvalid", name, err)
		}
	}
	if len(h.calls) != 0 {
		t.Errorf("the host was asked %d times", len(h.calls))
	}
}
