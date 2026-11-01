package files_test

import (
	"context"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

var _ = Describe("emptying the Trash", func() {
	ctx := context.Background()

	It("counts, purges one entry and then empties the rest", func() {
		f := newFiles()
		b := newToolBox(f, "alice", "chat1")
		for _, p := range []string{"/a.txt", "/b.txt", "/c.txt"} {
			_, err := b.run("write_file", map[string]any{"path": p, "content": "12345"})
			Expect(err).NotTo(HaveOccurred())
			_, err = b.run("delete_file", map[string]any{"path": p})
			Expect(err).NotTo(HaveOccurred())
		}
		trash, err := f.Trash(ctx, "alice", 50)
		Expect(err).NotTo(HaveOccurred())
		Expect(trash).To(HaveLen(3))
		for _, e := range trash {
			Expect(e.Files).To(Equal(1))
			Expect(e.Bytes).To(Equal(int64(5)))
		}

		dry, err := f.EmptyTrash(ctx, "alice", true)
		Expect(err).NotTo(HaveOccurred())
		Expect(dry.Files).To(Equal(3))
		Expect(dry.Bytes).To(Equal(int64(15)))
		after, _ := f.Trash(ctx, "alice", 50)
		Expect(after).To(HaveLen(3), "a dry run removed something")

		one, err := f.PurgeTrashEntry(ctx, "alice", trash[0].Rev)
		Expect(err).NotTo(HaveOccurred())
		Expect(one.Files).To(Equal(1))
		after, _ = f.Trash(ctx, "alice", 50)
		Expect(after).To(HaveLen(2))
		_, err = f.PurgeTrashEntry(ctx, "alice", trash[0].Rev)
		Expect(err).To(MatchError(workspace.ErrNotFound), "a purged entry purged twice")

		all, err := f.EmptyTrash(ctx, "alice", false)
		Expect(err).NotTo(HaveOccurred())
		Expect(all.Files).To(Equal(2))
		after, _ = f.Trash(ctx, "alice", 50)
		Expect(after).To(BeEmpty())
	})

	It("does not reach the Trash of another user or a revision it does not list", func() {
		f := newFiles()
		alice := newToolBox(f, "alice", "chat1")
		bob := newToolBox(f, "bob", "chat2")
		_, _ = alice.run("write_file", map[string]any{"path": "/a.txt", "content": "alice"})
		_, _ = alice.run("delete_file", map[string]any{"path": "/a.txt"})
		_, _ = bob.run("write_file", map[string]any{"path": "/b.txt", "content": "bob"})
		_, _ = bob.run("delete_file", map[string]any{"path": "/b.txt"})
		at, _ := f.Trash(ctx, "alice", 50)
		Expect(at).To(HaveLen(1))

		_, err := f.PurgeTrashEntry(ctx, "bob", at[0].Rev)
		Expect(err).To(MatchError(workspace.ErrNotFound))
		_, err = f.EmptyTrash(ctx, "bob", false)
		Expect(err).NotTo(HaveOccurred())
		at, _ = f.Trash(ctx, "alice", 50)
		Expect(at).To(HaveLen(1), "bob emptied alice's Trash")
		bt, _ := f.Trash(ctx, "bob", 50)
		Expect(bt).To(BeEmpty())
	})

	It("is not something the agent tools can do", func() {
		f := newFiles()
		b := newToolBox(f, "alice", "chat1")
		names := make([]string, 0, len(b.tools))
		for n := range b.tools {
			names = append(names, n)
			lower := strings.ToLower(n)
			Expect(lower).NotTo(ContainSubstring("purge"))
			Expect(lower).NotTo(ContainSubstring("trash"))
			Expect(lower).NotTo(ContainSubstring("empty"))
		}
		Expect(names).To(ConsistOf("list_files", "read_file", "write_file", "edit_file", "move_file", "delete_file", "search_files"))
		_, err := b.run("write_file", map[string]any{"path": "/a.txt", "content": "x"})
		Expect(err).NotTo(HaveOccurred())
		out, err := b.run("delete_file", map[string]any{"path": "/a.txt"})
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("Trash"))
		trash, _ := f.Trash(ctx, "alice", 50)
		Expect(trash).To(HaveLen(1), "an agent delete is not restorable")
	})
})
