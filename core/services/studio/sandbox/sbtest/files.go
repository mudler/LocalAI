package sbtest

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

func opKinds(cs workspace.ChangeSet) map[workspace.OpKind]int {
	out := map[workspace.OpKind]int{}
	for _, op := range cs.Ops {
		out[op.Kind]++
	}
	return out
}

func testFiles(newBackend NewBackend) {
	It("a new file is in the workspace after a checkpoint", func() {
		f := setup(newBackend, nil)
		f.mustRun("echo hi > proj/new.txt")
		cs, conflicts := f.checkpoint("turn 1")
		Expect(cs.Rev).NotTo(BeZero())
		Expect(cs.Title).To(Equal("turn 1"))
		Expect(cs.Kind).To(Equal(workspace.ChangeAgent))
		Expect(cs.Actor).To(Equal(Agent.Principal))
		Expect(conflicts).To(BeEmpty())
		got, _ := f.read("/proj/new.txt")
		Expect(got).To(Equal("hi\n"))
	})
	It("an existing file can be edited", func() {
		f := setup(newBackend, nil)
		f.mustRun("echo more >> proj/notes.txt && echo b > proj/sub/a.txt")
		_, c := f.checkpoint("edit")
		Expect(c).To(BeEmpty())
		notes, _ := f.read("/proj/notes.txt")
		a, _ := f.read("/proj/sub/a.txt")
		Expect(notes).To(Equal("notes\nmore\n"))
		Expect(a).To(Equal("b\n"))
	})
	It("a second edit after a checkpoint is not a conflict", func() {
		f := setup(newBackend, nil)
		f.mustRun("echo one >> proj/notes.txt; echo 1 > proj/n.txt")
		f.checkpoint("one")
		f.mustRun("echo two >> proj/notes.txt; echo 2 > proj/n.txt")
		_, c := f.checkpoint("two")
		Expect(c).To(BeEmpty())
		notes, _ := f.read("/proj/notes.txt")
		n, _ := f.read("/proj/n.txt")
		Expect(notes).To(Equal("notes\none\ntwo\n"))
		Expect(n).To(Equal("2\n"))
	})
	It("an edit of a file another writer changed is a conflict copy", func() {
		f := setup(newBackend, nil)
		_, err := f.store.Put(ctx(), Admin, "/proj/old.txt", strings.NewReader("theirs\n"), workspace.PutOptions{Overwrite: true})
		Expect(err).NotTo(HaveOccurred())
		f.mustRun("echo mine > proj/old.txt")
		_, c := f.checkpoint("edit")
		Expect(c).To(HaveLen(1))
		Expect(c[0].Reason).To(Equal(workspace.ReasonChanged))
		Expect(c[0].Kept).NotTo(BeEmpty())
		theirs, _ := f.read("/proj/old.txt")
		mine, _ := f.read(c[0].Kept)
		Expect(theirs).To(Equal("theirs\n"))
		Expect(mine).To(Equal("mine\n"))
		_, c = f.checkpoint("again")
		Expect(c).To(BeEmpty(), "an unchanged file made another conflict")
	})
	It("a delete is a delete and a rename is one move", func() {
		f := setup(newBackend, nil)
		f.mustRun("rm proj/old.txt && mv proj/sub/a.txt proj/b.txt")
		cs, c := f.checkpoint("tidy")
		Expect(c).To(BeEmpty())
		k := opKinds(cs)
		Expect(k[workspace.OpDelete]).To(Equal(1), "ops = %+v", cs.Ops)
		Expect(k[workspace.OpMove]).To(Equal(1), "ops = %+v", cs.Ops)
		Expect(cs.Ops).To(HaveLen(2))
		_, ok := f.read("/proj/old.txt")
		Expect(ok).To(BeFalse(), "old.txt survived")
		got, _ := f.read("/proj/b.txt")
		Expect(got).To(Equal("a\n"))
	})
	It("a file from an earlier checkpoint can be deleted and renamed", func() {
		f := setup(newBackend, nil)
		f.mustRun("echo n > proj/n.txt; echo m > proj/m.txt; mkdir -p proj/new/deep; echo x > proj/new/deep/x.txt")
		f.checkpoint("create")
		f.mustRun("rm proj/n.txt && mv proj/m.txt proj/m2.txt && rm -r proj/new")
		_, c := f.checkpoint("tidy")
		Expect(c).To(BeEmpty())
		for _, p := range []string{"/proj/n.txt", "/proj/m.txt", "/proj/new/deep/x.txt", "/proj/new"} {
			_, err := f.store.Stat(ctx(), Admin, p)
			Expect(err).To(MatchError(workspace.ErrNotFound), "%s survived", p)
		}
		got, _ := f.read("/proj/m2.txt")
		Expect(got).To(Equal("m\n"))
	})
	It("unchanged files in a replaced folder survive the next checkpoint", func() {
		f := setup(newBackend, nil)
		f.mustRun("rm -r proj/sub && mkdir proj/sub && echo x > proj/sub/x.txt")
		f.checkpoint("replace")
		f.mustRun("echo y > proj/sub/y.txt")
		_, c := f.checkpoint("add")
		Expect(c).To(BeEmpty())
		x, okx := f.read("/proj/sub/x.txt")
		y, _ := f.read("/proj/sub/y.txt")
		_, oka := f.read("/proj/sub/a.txt")
		Expect(okx).To(BeTrue())
		Expect(x).To(Equal("x\n"))
		Expect(y).To(Equal("y\n"))
		Expect(oka).To(BeFalse(), "a.txt present")
	})
	It("a checkpoint without changes commits nothing", func() {
		f := setup(newBackend, nil)
		f.mustRun("echo hi > proj/new.txt")
		cs, c := f.checkpoint("one")
		Expect(cs.Rev).NotTo(BeZero(), "first checkpoint = %+v", cs)
		Expect(c).To(BeEmpty())
		cs, c = f.checkpoint("two")
		Expect(cs.Rev).To(BeZero(), "second checkpoint = %+v", cs)
		Expect(c).To(BeEmpty())
	})
	It("a failed commit keeps the changes", func() {
		f := setup(newBackend, nil)
		f.mustRun("echo keep > proj/keep.txt")
		Expect(f.store.Revoke(ctx(), Admin, Agent.Principal, "/proj")).To(Succeed())
		_, _, err := f.sb.Checkpoint(ctx(), "denied")
		Expect(err).To(HaveOccurred(), "checkpoint without access worked")
		Expect(f.sb.Refresh(ctx())).NotTo(Succeed(), "refresh without access worked")
		Expect(f.mustRun("cat proj/keep.txt")).To(Equal("keep\n"))
		Expect(f.store.Grant(ctx(), Admin, Agent.Principal, "/proj", workspace.ModeWrite)).To(Succeed())
		f.checkpoint("retry")
		got, _ := f.read("/proj/keep.txt")
		Expect(got).To(Equal("keep\n"))
	})
}
