package wstest

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

func upperSetup(s workspace.Store) (workspace.View, int64) {
	GinkgoHelper()
	for p, c := range map[string]string{
		"/w/keep.txt": "keep", "/w/edit.txt": "old", "/w/gone.txt": "gone", "/w/a.txt": "move me", "/w/c.txt": "c",
		"/w/d/x.txt": "x", "/w/d/y.txt": "y", "/w/i/z.txt": "z", "/r/ro.txt": "ro",
	} {
		MustPut(s, AdminActor, p, c)
	}
	v, err := s.View(Ctx(), AdminActor, []workspace.MountRequest{
		{Path: "/w", At: "w", Mode: workspace.ModeWrite},
		{Path: "/r", At: "r", Mode: workspace.ModeRead},
	})
	Expect(err).ToNot(HaveOccurred())
	return v, latestRev(s)
}

func reasons(cs []workspace.Conflict) []string {
	var out []string
	for _, c := range cs {
		out = append(out, c.Reason)
	}
	return out
}

func describeUpper(newStore NewStore) {
	F, D, W := workspace.FixtureFile, workspace.FixtureDir, workspace.FixtureWhiteout
	Describe("Upper", func() {
		It("new, changed and deleted files", func() {
			s := newStore()
			v, base := upperSetup(s)
			cs, conflicts, err := s.CommitUpper(Ctx(), v, AliceAdmin(), base, workspace.FixtureSource{
				D("w"), F("w/new.txt", "n"), F("w/edit.txt", "new"), W("w/gone.txt"),
			}, workspace.UpperOptions{Title: "Agent turn 1"})
			Expect(err).ToNot(HaveOccurred())
			Expect(conflicts).To(BeEmpty())
			Expect(cs.Kind).To(Equal(workspace.ChangeAgent))
			Expect(cs.Title).To(Equal("Agent turn 1"))
			Expect(cs.Ops).To(HaveLen(3))
			tree := Tree(s)
			Expect(tree["/w/new.txt"]).To(Equal("n"))
			Expect(tree["/w/edit.txt"]).To(Equal("new"))
			Expect(tree["/w/keep.txt"]).To(Equal("keep"))
			Expect(tree).ToNot(HaveKey("/w/gone.txt"), "whiteout did not delete")
		})
		It("rename is one move", func() {
			s := newStore()
			v, base := upperSetup(s)
			cs, conflicts, err := s.CommitUpper(Ctx(), v, AliceAdmin(), base, workspace.FixtureSource{
				D("w"), W("w/a.txt"), F("w/b.txt", "move me"),
			}, workspace.UpperOptions{Title: "rename"})
			Expect(err).ToNot(HaveOccurred())
			Expect(conflicts).To(BeEmpty())
			Expect(cs.Ops).To(HaveLen(1), "ops = %+v", cs.Ops)
			Expect(cs.Ops[0].Kind).To(Equal(workspace.OpMove))
			Expect(cs.Ops[0].OldPath).To(Equal("/w/a.txt"))
			Expect(cs.Ops[0].NewPath).To(Equal("/w/b.txt"))
		})
		for _, prefix := range []string{"trusted.", "user."} {
			It("opaque folder replaces its content ("+prefix+")", func() {
				s := newStore()
				v, base := upperSetup(s)
				_, conflicts, err := s.CommitUpper(Ctx(), v, AliceAdmin(), base, workspace.FixtureSource{
					D("w"), workspace.FixtureOpaqueDir("w/d", prefix), F("w/d/new.txt", "n"),
				}, workspace.UpperOptions{Title: "replace"})
				Expect(err).ToNot(HaveOccurred())
				Expect(conflicts).To(BeEmpty())
				tree := Tree(s)
				Expect(tree).ToNot(HaveKey("/w/d/x.txt"))
				Expect(tree).ToNot(HaveKey("/w/d/y.txt"))
				Expect(tree["/w/d/new.txt"]).To(Equal("n"))
			})
		}
		It("impure and origin attributes are ignored", func() {
			s := newStore()
			v, base := upperSetup(s)
			dir := D("w/i")
			dir.Xattrs = map[string][]byte{"trusted.overlay.impure": []byte("y"), "trusted.overlay.origin": []byte{1, 2, 3}}
			_, conflicts, err := s.CommitUpper(Ctx(), v, AliceAdmin(), base, workspace.FixtureSource{D("w"), dir, F("w/i/extra.txt", "e")}, workspace.UpperOptions{Title: "x"})
			Expect(err).ToNot(HaveOccurred())
			Expect(conflicts).To(BeEmpty())
			Expect(Tree(s)["/w/i/z.txt"]).To(Equal("z"), "a non-opaque folder lost its content")
		})
		It("a change after the base keeps both", func() {
			s := newStore()
			v, base := upperSetup(s)
			MustPut(s, AdminActor, "/w/edit.txt", "theirs")
			_, conflicts, err := s.CommitUpper(Ctx(), v, AliceAdmin(), base, workspace.FixtureSource{D("w"), F("w/edit.txt", "mine")}, workspace.UpperOptions{Title: "x"})
			Expect(err).ToNot(HaveOccurred())
			Expect(conflicts).To(HaveLen(1))
			Expect(conflicts[0].Reason).To(Equal(workspace.ReasonChanged))
			Expect(conflicts[0].Kept).To(Equal("/w/edit (from chat 7).txt"))
			Expect(MustRead(s, AdminActor, "/w/edit.txt")).To(Equal("theirs"), "both versions must be kept")
			Expect(MustRead(s, AdminActor, conflicts[0].Kept)).To(Equal("mine"), "both versions must be kept")
		})
		It("a delete after a change is a conflict", func() {
			s := newStore()
			v, base := upperSetup(s)
			MustPut(s, AdminActor, "/w/gone.txt", "changed")
			_, conflicts, err := s.CommitUpper(Ctx(), v, AliceAdmin(), base, workspace.FixtureSource{D("w"), W("w/gone.txt")}, workspace.UpperOptions{Title: "x"})
			Expect(err).ToNot(HaveOccurred())
			Expect(reasons(conflicts)).To(Equal([]string{workspace.ReasonChanged}), "conflicts = %+v", conflicts)
			Expect(MustRead(s, AdminActor, "/w/gone.txt")).To(Equal("changed"), "the changed file was deleted")
		})
		It("entries outside the view or in a read-only mount are refused", func() {
			s := newStore()
			v, base := upperSetup(s)
			cs, conflicts, err := s.CommitUpper(Ctx(), v, AliceAdmin(), base, workspace.FixtureSource{
				D("other"), F("other/x", "x"), D("r"), F("r/ro.txt", "hacked"),
			}, workspace.UpperOptions{Title: "x"})
			Expect(err).ToNot(HaveOccurred())
			want := []string{workspace.ReasonOutsideView, workspace.ReasonOutsideView, workspace.ReasonReadOnly}
			Expect(reasons(conflicts)).To(Equal(want), "conflicts = %+v", conflicts)
			Expect(cs.Rev).To(BeZero(), "cs = %+v", cs)
			Expect(MustRead(s, AdminActor, "/r/ro.txt")).To(Equal("ro"), "read-only file changed")
		})
		It("symbolic links are reported", func() {
			s := newStore()
			v, base := upperSetup(s)
			_, conflicts, err := s.CommitUpper(Ctx(), v, AliceAdmin(), base, workspace.FixtureSource{D("w"), workspace.FixtureSymlink("w/link")}, workspace.UpperOptions{Title: "x"})
			Expect(err).ToNot(HaveOccurred())
			Expect(reasons(conflicts)).To(Equal([]string{workspace.ReasonUnsupported}), "conflicts = %+v", conflicts)
		})
		It("bad name is reported", func() {
			s := newStore()
			v, base := upperSetup(s)
			_, conflicts, err := s.CommitUpper(Ctx(), v, AliceAdmin(), base, workspace.FixtureSource{
				D("w"), F("w/bad\x01name", "b"), F("w/"+strings.Repeat("n", 300), "long"), F("w/ok.txt", "ok"),
			}, workspace.UpperOptions{Title: "x"})
			Expect(err).ToNot(HaveOccurred())
			Expect(reasons(conflicts)).To(Equal([]string{workspace.ReasonBadName, workspace.ReasonBadName}), "conflicts = %+v", conflicts)
			Expect(MustRead(s, AdminActor, "/w/ok.txt")).To(Equal("ok"), "the valid entry was not committed")
		})
		It("a file where a folder was", func() {
			s := newStore()
			v, base := upperSetup(s)
			_, conflicts, err := s.CommitUpper(Ctx(), v, AliceAdmin(), base, workspace.FixtureSource{D("w"), F("w/i", "now a file")}, workspace.UpperOptions{Title: "x"})
			Expect(err).ToNot(HaveOccurred())
			Expect(conflicts).To(BeEmpty())
			Expect(MustRead(s, AdminActor, "/w/i")).To(Equal("now a file"), "folder not replaced")
			Expect(Tree(s)).ToNot(HaveKey("/w/i/z.txt"), "the folder content is still there")
		})
		It("a second commit writes nothing", func() {
			s := newStore()
			v, base := upperSetup(s)
			src := workspace.FixtureSource{
				D("w"), F("w/new.txt", "n"), F("w/edit.txt", "new"), W("w/gone.txt"),
				W("w/a.txt"), F("w/b.txt", "move me"), workspace.FixtureOpaqueDir("w/d", "user."), F("w/d/new.txt", "n"),
			}
			cs1, c1, err := s.CommitUpper(Ctx(), v, AliceAdmin(), base, src, workspace.UpperOptions{Title: "x"})
			Expect(err).ToNot(HaveOccurred(), "first")
			Expect(c1).To(BeEmpty(), "first")
			Expect(cs1.Rev).ToNot(BeZero(), "first: %+v", cs1)
			cs2, c2, err := s.CommitUpper(Ctx(), v, AliceAdmin(), cs1.Rev, src, workspace.UpperOptions{Title: "x"})
			Expect(err).ToNot(HaveOccurred(), "second")
			Expect(c2).To(BeEmpty(), "second")
			Expect(cs2.Rev).To(BeZero(), "second: %+v", cs2)
		})

		It("a renamed mount folder is followed by node", func() {
			s := newStore()
			v, base := upperSetup(s)
			_, err := s.Move(Ctx(), AdminActor, "/w", "/w2")
			Expect(err).ToNot(HaveOccurred())
			_, conflicts, err := s.CommitUpper(Ctx(), v, AliceAdmin(), base, workspace.FixtureSource{D("w"), F("w/new.txt", "n")}, workspace.UpperOptions{Title: "x"})
			Expect(err).ToNot(HaveOccurred())
			Expect(conflicts).To(BeEmpty())
			tree := Tree(s)
			Expect(tree["/w2/new.txt"]).To(Equal("n"), "tree = %v", tree)
			for p := range tree {
				Expect(p == "/w" || strings.HasPrefix(p, "/w/")).To(BeFalse(), "a write landed at the old path: %v", tree)
			}
		})
		It("a deleted mount folder reports every entry", func() {
			s := newStore()
			v, base := upperSetup(s)
			Expect(s.Delete(Ctx(), AdminActor, "/w")).To(Succeed())
			cs, conflicts, err := s.CommitUpper(Ctx(), v, AliceAdmin(), base, workspace.FixtureSource{D("w"), F("w/new.txt", "n"), W("w/keep.txt")}, workspace.UpperOptions{Title: "x"})
			Expect(err).ToNot(HaveOccurred())
			Expect(cs.Rev).To(BeZero(), "commit: %+v", cs)
			want := []string{workspace.ReasonParentDeleted, workspace.ReasonParentDeleted}
			Expect(reasons(conflicts)).To(Equal(want), "conflicts = %+v", conflicts)
			Expect(Tree(s)).ToNot(HaveKey("/w"), "the deleted folder was recreated")
		})
		It("an edit of a file deleted after the base is a conflict", func() {
			s := newStore()
			v, base := upperSetup(s)
			Expect(s.Delete(Ctx(), AdminActor, "/w/edit.txt")).To(Succeed())
			cs, conflicts, err := s.CommitUpper(Ctx(), v, AliceAdmin(), base, workspace.FixtureSource{D("w"), F("w/edit.txt", "mine")}, workspace.UpperOptions{Title: "x"})
			Expect(err).ToNot(HaveOccurred())
			Expect(cs.Rev).ToNot(BeZero(), "commit: %+v", cs)
			Expect(reasons(conflicts)).To(Equal([]string{workspace.ReasonChanged}), "conflicts = %+v", conflicts)
			tree := Tree(s)
			Expect(tree).ToNot(HaveKey("/w/edit.txt"), "the deleted file was recreated")
			k := conflicts[0].Kept
			Expect(k).To(HavePrefix("/w/edit"), "kept = %q, tree = %v", k, tree)
			Expect(tree[k]).To(Equal("mine"), "kept = %q, tree = %v", k, tree)
		})
		It("a file created and deleted after the base can be created", func() {
			s := newStore()
			v, base := upperSetup(s)
			MustPut(s, AdminActor, "/w/fresh.txt", "x")
			Expect(s.Delete(Ctx(), AdminActor, "/w/fresh.txt")).To(Succeed())
			_, conflicts, err := s.CommitUpper(Ctx(), v, AliceAdmin(), base, workspace.FixtureSource{D("w"), F("w/fresh.txt", "mine")}, workspace.UpperOptions{Title: "x"})
			Expect(err).ToNot(HaveOccurred())
			Expect(conflicts).To(BeEmpty())
			Expect(MustRead(s, AdminActor, "/w/fresh.txt")).To(Equal("mine"))
		})

		It("an opaque folder keeps a lower child changed after the base", func() {
			s := newStore()
			v, base := upperSetup(s)
			MustPut(s, AdminActor, "/w/d/x.txt", "theirs")
			_, conflicts, err := s.CommitUpper(Ctx(), v, AliceAdmin(), base, workspace.FixtureSource{
				D("w"), workspace.FixtureOpaqueDir("w/d", "user."), F("w/d/new.txt", "n"),
			}, workspace.UpperOptions{Title: "x"})
			Expect(err).ToNot(HaveOccurred())
			Expect(reasons(conflicts)).To(Equal([]string{workspace.ReasonChanged}), "conflicts = %+v", conflicts)
			tree := Tree(s)
			Expect(tree["/w/d/x.txt"]).To(Equal("theirs"), "tree = %v", tree)
			Expect(tree["/w/d/new.txt"]).To(Equal("n"), "tree = %v", tree)
			Expect(tree).ToNot(HaveKey("/w/d/y.txt"), "the unchanged lower child was kept")
		})
		It("an opaque folder keeps a path whose entry became a conflict", func() {
			s := newStore()
			v, base := upperSetup(s)
			_, conflicts, err := s.CommitUpper(Ctx(), v, AliceAdmin(), base, workspace.FixtureSource{
				D("w"), workspace.FixtureOpaqueDir("w/d", "user."), workspace.FixtureSymlink("w/d/x.txt"),
			}, workspace.UpperOptions{Title: "x"})
			Expect(err).ToNot(HaveOccurred())
			Expect(reasons(conflicts)).To(Equal([]string{workspace.ReasonUnsupported}), "conflicts = %+v", conflicts)
			Expect(MustRead(s, AdminActor, "/w/d/x.txt")).To(Equal("x"), "the lower file was deleted")
		})
		It("a file over a folder changed after the base keeps both", func() {
			s := newStore()
			v, base := upperSetup(s)
			MustPut(s, AdminActor, "/w/i/z.txt", "theirs")
			_, conflicts, err := s.CommitUpper(Ctx(), v, AliceAdmin(), base, workspace.FixtureSource{D("w"), F("w/i", "mine")}, workspace.UpperOptions{Title: "x"})
			Expect(err).ToNot(HaveOccurred())
			Expect(conflicts).To(HaveLen(1), "conflicts = %+v", conflicts)
			Expect(conflicts[0].Reason).To(Equal(workspace.ReasonChanged))
			Expect(conflicts[0].Kept).ToNot(BeEmpty())
			Expect(MustRead(s, AdminActor, conflicts[0].Kept)).To(Equal("mine"), "both versions must be kept")
			Expect(MustRead(s, AdminActor, "/w/i/z.txt")).To(Equal("theirs"), "both versions must be kept")
		})
		It("a move target deleted after the base is not restored", func() {
			s := newStore()
			v, base := upperSetup(s)
			Expect(s.Delete(Ctx(), AdminActor, "/w/c.txt")).To(Succeed())
			_, conflicts, err := s.CommitUpper(Ctx(), v, AliceAdmin(), base, workspace.FixtureSource{D("w"), W("w/a.txt"), F("w/c.txt", "move me")}, workspace.UpperOptions{Title: "x"})
			Expect(err).ToNot(HaveOccurred())
			Expect(reasons(conflicts)).To(Equal([]string{workspace.ReasonChanged}), "conflicts = %+v", conflicts)
			tree := Tree(s)
			Expect(tree).ToNot(HaveKey("/w/c.txt"), "the deleted file came back")
			Expect(tree).ToNot(HaveKey("/w/a.txt"), "the whiteout did not delete")
		})
		It("a move into a folder deleted after the base does not recreate it", func() {
			s := newStore()
			v, base := upperSetup(s)
			Expect(s.Delete(Ctx(), AdminActor, "/w/d")).To(Succeed())
			_, conflicts, err := s.CommitUpper(Ctx(), v, AliceAdmin(), base, workspace.FixtureSource{D("w"), W("w/a.txt"), F("w/d/a.txt", "move me")}, workspace.UpperOptions{Title: "x"})
			Expect(err).ToNot(HaveOccurred())
			Expect(reasons(conflicts)).To(Equal([]string{workspace.ReasonChanged}), "conflicts = %+v", conflicts)
			Expect(Tree(s)).ToNot(HaveKey("/w/d"), "the deleted folder came back")
		})
		It("an edit of a file moved and then deleted is a conflict", func() {
			s := newStore()
			v, base := upperSetup(s)
			_, err := s.Move(Ctx(), AdminActor, "/w/edit.txt", "/w/other.txt")
			Expect(err).ToNot(HaveOccurred())
			Expect(s.Delete(Ctx(), AdminActor, "/w/other.txt")).To(Succeed())
			cs, conflicts, err := s.CommitUpper(Ctx(), v, AliceAdmin(), base, workspace.FixtureSource{D("w"), F("w/edit.txt", "mine")}, workspace.UpperOptions{Title: "x"})
			Expect(err).ToNot(HaveOccurred())
			Expect(cs.Rev).ToNot(BeZero(), "commit: %+v", cs)
			Expect(reasons(conflicts)).To(Equal([]string{workspace.ReasonChanged}), "conflicts = %+v", conflicts)
			tree := Tree(s)
			Expect(tree).ToNot(HaveKey("/w/edit.txt"), "the file came back")
			k := conflicts[0].Kept
			Expect(k).To(HavePrefix("/w/"), "kept = %q, tree = %v", k, tree)
			Expect(tree[k]).To(Equal("mine"), "kept = %q, tree = %v", k, tree)
		})
		It("an edit inside a renamed folder is a conflict and leaves no ghost folder", func() {
			s := newStore()
			v, base := upperSetup(s)
			_, err := s.Move(Ctx(), AdminActor, "/w/d", "/w/dd")
			Expect(err).ToNot(HaveOccurred())
			cs, conflicts, err := s.CommitUpper(Ctx(), v, AliceAdmin(), base, workspace.FixtureSource{D("w"), D("w/d"), F("w/d/x.txt", "mine")}, workspace.UpperOptions{Title: "x"})
			Expect(err).ToNot(HaveOccurred())
			Expect(cs.Rev).ToNot(BeZero(), "commit: %+v", cs)
			Expect(reasons(conflicts)).To(Equal([]string{workspace.ReasonChanged}), "conflicts = %+v", conflicts)
			tree := Tree(s)
			Expect(tree).ToNot(HaveKey("/w/d"), "ghost folder: %v", tree)
			Expect(tree["/w/dd/x.txt"]).To(Equal("x"), "tree = %v", tree)
			k := conflicts[0].Kept
			Expect(k).To(HavePrefix("/w/dd/x"), "kept = %q, tree = %v", k, tree)
			Expect(tree[k]).To(Equal("mine"), "kept = %q, tree = %v", k, tree)
		})
		It("an edit of a file moved after the base keeps a copy beside it", func() {
			s := newStore()
			v, base := upperSetup(s)
			_, err := s.Move(Ctx(), AdminActor, "/w/edit.txt", "/w/d/moved.txt")
			Expect(err).ToNot(HaveOccurred())
			_, conflicts, err := s.CommitUpper(Ctx(), v, AliceAdmin(), base, workspace.FixtureSource{D("w"), F("w/edit.txt", "mine")}, workspace.UpperOptions{Title: "x"})
			Expect(err).ToNot(HaveOccurred())
			Expect(conflicts).To(HaveLen(1), "conflicts = %+v", conflicts)
			tree := Tree(s)
			k := conflicts[0].Kept
			Expect(k).To(HavePrefix("/w/d/moved"), "kept = %q, tree = %v", k, tree)
			Expect(tree[k]).To(Equal("mine"), "kept = %q, tree = %v", k, tree)
			Expect(tree["/w/d/moved.txt"]).To(Equal("old"), "kept = %q, tree = %v", k, tree)
		})
		// A conflict copy lands only inside a write mount of the view.
		copyStaysInWritable := func(dest, edit, wantPrefix string) {
			GinkgoHelper()
			s := newStore()
			v, base := upperSetup(s)
			MustPut(s, AdminActor, "/other/keep.txt", "k")
			_, err := s.Move(Ctx(), AdminActor, "/w/"+edit, dest)
			Expect(err).ToNot(HaveOccurred())
			before := Tree(s)
			_, conflicts, err := s.CommitUpper(Ctx(), v, AliceAdmin(), base, workspace.FixtureSource{F("w/"+edit, "mine")}, workspace.UpperOptions{Title: "t"})
			Expect(err).ToNot(HaveOccurred())
			Expect(conflicts).To(HaveLen(1), "conflicts = %+v", conflicts)
			Expect(conflicts[0].Reason).To(Equal(workspace.ReasonChanged))
			tree := Tree(s)
			k := conflicts[0].Kept
			Expect(k).To(HavePrefix(wantPrefix), "kept = %q, want a copy under %s; tree = %v", k, wantPrefix, tree)
			Expect(tree[k]).To(Equal("mine"), "kept = %q, want a copy under %s; tree = %v", k, wantPrefix, tree)
			for p := range tree {
				if _, had := before[p]; !had {
					Expect(p).To(Equal(k), "unexpected new path %s", p)
					Expect(p).To(HavePrefix("/w/"), "copy outside the writable mount: %s", p)
				}
			}
		}
		It("a copy for a node moved into a read-only mount stays in the writable one", func() {
			copyStaysInWritable("/r/edit.txt", "edit.txt", "/w/edit")
		})
		It("a copy for a node moved into an unmounted folder stays in the writable mount", func() {
			copyStaysInWritable("/other/edit.txt", "edit.txt", "/w/edit")
		})
		It("a copy for a nested node moved out of the view goes to its live base folder", func() {
			copyStaysInWritable("/r/x.txt", "d/x.txt", "/w/d/x")
		})
		It("no copy is kept when the writable mount is gone", func() {
			s := newStore()
			v, base := upperSetup(s)
			_, err := s.Move(Ctx(), AdminActor, "/w/edit.txt", "/r/edit.txt")
			Expect(err).ToNot(HaveOccurred())
			Expect(s.Delete(Ctx(), AdminActor, "/w")).To(Succeed())
			before := Tree(s)
			_, conflicts, err := s.CommitUpper(Ctx(), v, AliceAdmin(), base, workspace.FixtureSource{F("w/edit.txt", "mine")}, workspace.UpperOptions{Title: "t"})
			Expect(err).ToNot(HaveOccurred())
			for _, c := range conflicts {
				Expect(c.Kept).To(BeEmpty(), "kept %q", c.Kept)
			}
			Expect(Tree(s)).To(Equal(before), "tree changed")
		})
	})
}
