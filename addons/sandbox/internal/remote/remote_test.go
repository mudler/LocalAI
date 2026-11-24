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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// fakeHost records the host operations and answers them. onCommit sees the
// archive while it still exists.
type fakeHost struct {
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

func TestRemote(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Sandbox Remote Store Suite")
}

func newStore() (*remote.Store, *fakeHost, string) {
	dir := GinkgoT().TempDir()
	h := &fakeHost{dataDir: dir, answers: map[string]any{}}
	return remote.New(h, dir), h, dir
}

func ctxOf(id string) context.Context { return remote.WithOperation(context.Background(), id) }

var actor = workspace.Actor{Principal: "ignored", ChatID: "c1"}

func view(s *remote.Store) workspace.View {
	v, err := s.View(context.Background(), actor, []workspace.MountRequest{{Path: "/proj", At: "proj", Mode: workspace.ModeWrite}, {Path: "/docs", At: "docs", Mode: workspace.ModeRead}})
	Expect(err).NotTo(HaveOccurred())
	return v
}

var _ = Describe("Remote workspace store", func() {
	It("asks the host to materialize the mounts inside the data directory", func() {
		s, h, dir := newStore()
		h.answers["workspace.materialize"] = map[string]int64{"revision": 7}
		rev, err := s.Materialize(ctxOf("op1"), view(s), filepath.Join(dir, "sessions", "s1", "lower"))
		Expect(err).NotTo(HaveOccurred())
		Expect(rev).To(BeEquivalentTo(7))
		c := h.calls[0]
		Expect(c.op).To(Equal("workspace.materialize"))
		Expect(c.opID).To(Equal("op1"))
		Expect(c.args["dir"]).To(Equal("sessions/s1/lower"))
		mounts := c.args["mounts"].([]any)
		first := mounts[0].(map[string]any)
		second := mounts[1].(map[string]any)
		Expect(first["path"]).To(Equal("/proj"), "mounts = %v", mounts)
		Expect(first["at"]).To(Equal("proj"), "mounts = %v", mounts)
		Expect(first["write"]).To(Equal(true), "mounts = %v", mounts)
		Expect(second["write"]).NotTo(Equal(true), "mounts = %v", mounts)
	})

	It("refuses a directory outside the data directory before the host is asked", func() {
		s, h, dir := newStore()
		for _, bad := range []string{"/etc/lower", filepath.Join(dir, "..", "x"), "relative/lower", dir} {
			_, err := s.Materialize(ctxOf("op1"), view(s), bad)
			Expect(err).To(HaveOccurred(), "Materialize into %q was accepted", bad)
		}
		Expect(h.calls).To(BeEmpty(), "the host was asked")
	})

	It("refuses a call without an operation", func() {
		s, h, dir := newStore()
		_, err := s.Materialize(context.Background(), view(s), filepath.Join(dir, "a", "lower"))
		Expect(err).To(HaveOccurred(), "Materialize without an operation was accepted")
		_, _, err = s.CommitUpper(context.Background(), view(s), actor, 0, workspace.FixtureSource{workspace.FixtureFile("a", "x")}, workspace.UpperOptions{})
		Expect(err).To(HaveOccurred(), "CommitUpper of a change without an operation was accepted")
		Expect(h.calls).To(BeEmpty(), "the host was asked")
	})

	It("needs no request for an empty layer and asks nobody", func() {
		s, h, dir := newStore()
		// A sandbox that sleeps or stops after a quiet time has nothing to commit,
		// and no user is waiting on it, so this must work without an operation.
		cs, conflicts, err := s.CommitUpper(context.Background(), view(s), actor, 5, workspace.FixtureSource{}, workspace.UpperOptions{Title: "idle"})
		Expect(err).NotTo(HaveOccurred())
		Expect(cs.Rev).To(BeZero(), "CommitUpper = %+v", cs)
		Expect(conflicts).To(BeEmpty())
		Expect(h.calls).To(BeEmpty(), "the host was asked for nothing to commit")
		left, _ := os.ReadDir(dir)
		Expect(left).To(BeEmpty(), "files left behind")
		// Folders alone are what a checkpoint after a quiet time sends again; they
		// need no request either, and with one they are sent.
		dirs := workspace.FixtureSource{workspace.FixtureDir("proj")}
		_, _, err = s.CommitUpper(context.Background(), view(s), actor, 5, dirs, workspace.UpperOptions{})
		Expect(err).NotTo(HaveOccurred(), "a layer of folders without a request")
		Expect(h.calls).To(BeEmpty(), "a layer of folders without a request")
		// A layer with content still needs a request.
		_, _, err = s.CommitUpper(context.Background(), view(s), actor, 5, workspace.FixtureSource{workspace.FixtureFile("proj/a", "x")}, workspace.UpperOptions{})
		Expect(err).To(HaveOccurred(), "a layer with content was committed without a request")
	})

	It("sends an upper layer as an archive the host reads back", func() {
		s, h, dir := newStore()
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
			Expect(err).NotTo(HaveOccurred(), "the archive is not in the directory the host was told")
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
			Expect(err).NotTo(HaveOccurred(), "the host cannot read the archive")
		}
		cs, conflicts, err := s.CommitUpper(ctxOf("op2"), view(s), actor, 5, layer, workspace.UpperOptions{Title: "run", Own: []int64{3, 4}})
		Expect(err).NotTo(HaveOccurred())
		Expect(cs.Rev).To(BeEquivalentTo(9))
		Expect(conflicts).To(HaveLen(1))
		Expect(conflicts[0].Path).To(Equal("proj/a.txt"))
		Expect(conflicts[0].Reason).To(Equal("changed"))
		kinds := map[string]workspace.EntryKind{}
		for _, e := range got {
			kinds[e.Path] = e.Kind
		}
		want := map[string]workspace.EntryKind{"proj": workspace.EntryDir, "proj/new.txt": workspace.EntryFile, "proj/fresh": workspace.EntryDir, "proj/old.txt": workspace.EntryWhiteout, "proj/link": workspace.EntrySymlink}
		Expect(kinds).To(Equal(want))
		Expect(contents["proj/new.txt"]).To(Equal("made in the sandbox\n"))
		for _, e := range got {
			if e.Path == "proj/fresh" {
				Expect(string(e.Xattrs["trusted.overlay.opaque"])).To(Equal("y"), "the opaque mark was lost: %v", e.Xattrs)
			}
		}
		c := h.calls[0]
		Expect(c.op).To(Equal("workspace.commit"))
		Expect(c.args["base"]).To(BeEquivalentTo(5))
		Expect(c.args["title"]).To(Equal("run"))
		own := c.args["own"].([]any)
		Expect(own).To(HaveLen(2))
		Expect(own[0]).To(BeEquivalentTo(3))
		left, _ := os.ReadDir(dir)
		Expect(left).To(BeEmpty(), "the add-on left files behind")
	})

	It("returns a host refusal as the error", func() {
		s, h, dir := newStore()
		h.err = errors.New("host said no")
		_, err := s.Materialize(ctxOf("op1"), view(s), filepath.Join(dir, "s", "lower"))
		Expect(err).To(MatchError(ContainSubstring("host said no")))
	})

	It("lists the revisions the host reports in ChangesSince", func() {
		s, h, _ := newStore()
		h.answers["workspace.changes"] = map[string]any{"changes": []map[string]any{{"revision": 11, "title": "a"}, {"revision": 12, "title": "b"}}}
		got, err := s.ChangesSince(ctxOf("op3"), actor, 10, 50)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(HaveLen(2))
		Expect(got[0].Rev).To(BeEquivalentTo(11))
		Expect(got[1].Title).To(Equal("b"))
		Expect(h.calls[0].args["since"]).To(BeEquivalentTo(10), "args = %v", h.calls[0].args)
	})

	It("refuses every other method and asks nobody", func() {
		s, h, _ := newStore()
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
			Expect(err).To(MatchError(workspace.ErrInvalid), name)
		}
		Expect(h.calls).To(BeEmpty(), "the host was asked")
	})
})
