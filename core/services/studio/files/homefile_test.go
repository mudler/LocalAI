package files_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

var _ = Describe("home files for the host", func() {
	It("writes a file as one change set that can be undone, and reads it back", func() {
		ctx := context.Background()
		f := newFiles()
		Expect(f.EnsureHome(ctx, "alice")).To(Succeed())
		text, ok, err := f.ReadHome(ctx, "alice", "Memory/index.md", 1024)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeFalse(), "text = %q", text)

		rev, err := f.WriteHome(ctx, "alice", "Memory/index.md", "- home: Rome\n", "Memory: home", 1024)
		Expect(err).NotTo(HaveOccurred())
		Expect(rev).To(BeNumerically(">", 0))
		text, ok, err = f.ReadHome(ctx, "alice", "Memory/index.md", 1024)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		Expect(text).To(Equal("- home: Rome\n"))

		hist, err := f.Store().History(ctx, f.Actor("alice"), workspace.HistoryQuery{Path: session.HomePath("alice"), Limit: 5})
		Expect(err).NotTo(HaveOccurred())
		Expect(hist).NotTo(BeEmpty())
		Expect(hist[0].Title).To(Equal("Memory: home"))
		Expect(hist[0].Rev).To(Equal(rev))

		_, _, err = f.Store().Undo(ctx, f.Actor("alice"), rev, workspace.UndoOptions{})
		Expect(err).NotTo(HaveOccurred())
		_, ok, _ = f.ReadHome(ctx, "alice", "Memory/index.md", 1024)
		Expect(ok).To(BeFalse(), "the undo should remove the file the change created")
	})

	It("refuses a path outside the home, a bigger file, and another user's home", func() {
		ctx := context.Background()
		f := newFiles()
		Expect(f.EnsureHome(ctx, "alice")).To(Succeed())
		_, err := f.WriteHome(ctx, "alice", "../bob/x.md", "x", "t", 1024)
		Expect(err).To(MatchError(workspace.ErrBadName))
		_, err = f.WriteHome(ctx, "alice", "Memory/big.md", "0123456789", "t", 4)
		Expect(err).To(HaveOccurred())
		_, _, err = f.ReadHome(ctx, "alice", "../x", 10)
		Expect(err).To(MatchError(workspace.ErrBadName))
	})
})
