package files_test

import (
	"context"
	"encoding/json"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// toolBox runs the model's file tools for one user in one chat.
type toolBox struct {
	f     *files.Service
	tools map[string]session.Tool
}

func newToolBox(f *files.Service, user, chat string) *toolBox {
	Expect(f.EnsureHome(context.Background(), user)).To(Succeed())
	b := &toolBox{f: f, tools: map[string]session.Tool{}}
	for _, tool := range f.Tools(workspace.Actor{Principal: user, ChatID: chat}) {
		b.tools[tool.Name] = tool
	}
	return b
}

func (b *toolBox) run(name string, args map[string]any) (string, error) {
	tool, ok := b.tools[name]
	Expect(ok).To(BeTrue(), "no tool %q; have %v", name, len(b.tools))
	raw, _ := json.Marshal(args)
	return tool.Run(context.Background(), raw)
}

func wstestRead(f *files.Service, abs string) string {
	r, _, err := f.Store().Open(context.Background(), workspace.Actor{Principal: files.AdminPrincipal}, abs)
	Expect(err).NotTo(HaveOccurred())
	defer func() { _ = r.Close() }()
	b := new(strings.Builder)
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		b.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return b.String()
}

var _ = Describe("the file tools of the model", func() {
	It("writes and reads files in the user's home", func() {
		f := newFiles()
		b := newToolBox(f, "alice", "chat1")
		for _, name := range []string{"list_files", "read_file", "write_file", "move_file", "delete_file", "search_files"} {
			Expect(b.tools).To(HaveKey(name), "missing tool %s", name)
		}
		_, err := b.run("write_file", map[string]any{"path": "/Reports/q1.txt", "content": "line one\nline two\nline three\n"})
		Expect(err).NotTo(HaveOccurred())
		// The path the model sees in the sandbox works too.
		got, err := b.run("read_file", map[string]any{"path": "/workspace/home/Reports/q1.txt"})
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(ContainSubstring("line two"))
		part, _ := b.run("read_file", map[string]any{"path": "/Reports/q1.txt", "offset": 2, "limit": 1})
		Expect(part).To(ContainSubstring("line two"), "read_file offset 2 limit 1 should give only line two")
		Expect(part).NotTo(ContainSubstring("line one"), "read_file offset 2 limit 1 should give only line two")
		Expect(part).NotTo(ContainSubstring("line three"), "read_file offset 2 limit 1 should give only line two")
		list, err := b.run("list_files", map[string]any{"path": "/Reports"})
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(ContainSubstring("q1.txt"))
		_, err = b.run("move_file", map[string]any{"from": "/Reports/q1.txt", "to": "/Archive/q1.txt"})
		Expect(err).NotTo(HaveOccurred())
		_, err = b.run("delete_file", map[string]any{"path": "/Archive"})
		Expect(err).NotTo(HaveOccurred())
		trash, _ := f.Trash(context.Background(), "alice", 10)
		Expect(trash).To(HaveLen(1), "want the folder the model deleted")
		Expect(trash[0].Paths[0]).To(Equal("/Archive"))
	})

	It("makes the model's changes agent changes of its chat", func() {
		f := newFiles()
		b := newToolBox(f, "alice", "chat7")
		_, _ = b.run("write_file", map[string]any{"path": "/a.txt", "content": "x"})
		hist, err := f.Store().History(context.Background(), f.Actor("alice"), workspace.HistoryQuery{Path: "/users/alice", Limit: 10})
		Expect(err).NotTo(HaveOccurred())
		var found bool
		for _, cs := range hist {
			if cs.Kind == workspace.ChangeAgent && cs.ChatID == "chat7" {
				found = true
			}
		}
		Expect(found).To(BeTrue(), "history = %+v, want an agent change set of chat7", hist)
	})

	It("cannot leave the user's home", func() {
		f := newFiles()
		_ = f.EnsureHome(context.Background(), "bob")
		bobBox := newToolBox(f, "bob", "c")
		_, _ = bobBox.run("write_file", map[string]any{"path": "/secret.txt", "content": "bob's"})
		b := newToolBox(f, "alice", "chat1")
		for _, p := range []string{"/../bob/secret.txt", "/users/bob/secret.txt", "../bob/secret.txt", "/workspace/home/../bob/secret.txt"} {
			out, err := b.run("read_file", map[string]any{"path": p})
			Expect(err).To(HaveOccurred(), "read_file(%q) = %q, want an error", p, out)
			if _, err := b.run("write_file", map[string]any{"path": p, "content": "x"}); err == nil {
				// A path inside her own home that happens to look like it is fine; it
				// must not have written into bob's.
				got, err := bobBox.run("read_file", map[string]any{"path": "/secret.txt"})
				Expect(err).NotTo(HaveOccurred(), "write_file(%q) changed bob's file", p)
				Expect(got).To(Equal("bob's"), "write_file(%q) changed bob's file", p)
			}
		}
		out, _ := b.run("search_files", map[string]any{"query": "bob's"})
		Expect(out).NotTo(ContainSubstring("secret.txt"), "search found bob's file")
	})

	It("finds names and words inside files and skips what is not text", func() {
		f := newFiles()
		b := newToolBox(f, "alice", "c")
		_, _ = b.run("write_file", map[string]any{"path": "/Notes/budget-2026.txt", "content": "nothing here"})
		_, _ = b.run("write_file", map[string]any{"path": "/Notes/meeting.txt", "content": "we discussed the Budget for Q3\nand hiring"})
		abs, _ := f.Abs("alice", "/blob.bin")
		_, _ = f.Store().Put(context.Background(), f.Actor("alice"), abs, strings.NewReader("budget\x00\x01\x02"), workspace.PutOptions{})
		big, _ := f.Abs("alice", "/big.txt")
		_, _ = f.Store().Put(context.Background(), f.Actor("alice"), big, strings.NewReader(strings.Repeat("budget ", 300000)), workspace.PutOptions{})

		out, err := b.run("search_files", map[string]any{"query": "budget"})
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("/Notes/budget-2026.txt"), "search missed the file named budget")
		Expect(out).To(ContainSubstring("/Notes/meeting.txt"), "search missed the word inside meeting.txt")
		Expect(out).To(ContainSubstring("Budget for Q3"), "search missed the word inside meeting.txt")
		Expect(strings.Contains(out, "blob.bin") && strings.Contains(out, "budget\x00")).To(BeFalse(), "search read a binary file: %q", out)
		Expect(out).NotTo(ContainSubstring("/big.txt:"), "search read a file over the content limit")
	})

	It("reads shared and built-in skills but cannot change them", func() {
		f := newFiles()
		b := newToolBox(f, "alice", "c1")
		adminPut(f, "/skills/team/SKILL.md", skillMD("team", "for everyone"))
		adminPut(f, "/skills/team/references/style.md", "use short words")
		adminPut(f, "/builtin-skills/base/SKILL.md", skillMD("base", "built in"))

		got, err := b.run("read_file", map[string]any{"path": "/workspace/shared-skills/team/SKILL.md"})
		Expect(err).NotTo(HaveOccurred(), "read_file of a shared skill")
		Expect(got).To(ContainSubstring("Body of team"))
		ref, err := b.run("read_file", map[string]any{"path": "/workspace/shared-skills/team/references/style.md"})
		Expect(err).NotTo(HaveOccurred(), "read_file of a reference next to a shared skill")
		Expect(ref).To(Equal("use short words"))
		got, err = b.run("read_file", map[string]any{"path": "/workspace/shared-builtin-skills/base/SKILL.md"})
		Expect(err).NotTo(HaveOccurred(), "read_file of a built-in skill")
		Expect(got).To(ContainSubstring("Body of base"))
		list, err := b.run("list_files", map[string]any{"path": "/workspace/shared-skills"})
		Expect(err).NotTo(HaveOccurred(), "list_files of the shared skills")
		Expect(list).To(ContainSubstring("team/"))

		for name, args := range map[string]map[string]any{
			"write_file":  {"path": "/workspace/shared-skills/team/SKILL.md", "content": "x"},
			"delete_file": {"path": "/workspace/shared-skills/team"},
			"move_file":   {"from": "/workspace/shared-skills/team", "to": "/stolen"},
		} {
			_, err := b.run(name, args)
			Expect(err).To(MatchError(ContainSubstring("read-only")), "%s on a shared skill", name)
		}
		Expect(wstestRead(f, "/skills/team/SKILL.md")).To(ContainSubstring("Body of team"), "the shared skill changed")
	})

	It("cannot lead out of its layer with a shared path", func() {
		f := newFiles()
		b := newToolBox(f, "alice", "c1")
		_ = f.EnsureHome(context.Background(), "bob")
		userPut(f, "bob", "/.skills/private/SKILL.md", skillMD("private", "bob only"))
		adminPut(f, "/skills/team/SKILL.md", skillMD("team", "for everyone"))
		for _, p := range []string{
			"/workspace/shared-skills/../users/bob/.skills/private/SKILL.md",
			"/workspace/shared-skills/team/../../users/bob/.skills/private/SKILL.md",
			"/workspace/shared-skills/..",
		} {
			out, err := b.run("read_file", map[string]any{"path": p})
			Expect(err).To(HaveOccurred(), "read_file(%q) = %q, want an error", p, out)
			out, err = b.run("list_files", map[string]any{"path": p})
			Expect(err).To(HaveOccurred(), "list_files(%q) = %q, want an error", p, out)
		}
		// A name that only starts like the guest prefix is an ordinary path in the home.
		_, err := b.run("read_file", map[string]any{"path": "/workspace/shared-skills-extra/x"})
		Expect(err).To(HaveOccurred(), "a path that only looks like the shared prefix was read")
	})

	// Arguments that are not JSON must not turn into an empty path, which would
	// list, read or search the whole home folder.
	It("refuses malformed arguments instead of reading them as the home folder", func() {
		f := newFiles()
		b := newToolBox(f, "alice", "chat1")
		_, err := b.run("write_file", map[string]any{"path": "/secret.txt", "content": "needle"})
		Expect(err).NotTo(HaveOccurred())
		for _, name := range []string{"list_files", "read_file", "delete_file", "search_files"} {
			for _, raw := range []string{`{"path": `, `"a string"`, `{"path": 7}`} {
				out, err := b.tools[name].Run(context.Background(), json.RawMessage(raw))
				Expect(err).To(HaveOccurred(), "%s(%s) = %q, want an error", name, raw, out)
				Expect(err.Error()).To(ContainSubstring("arguments"), "%s(%s) error, want a message about the arguments", name, raw)
			}
		}
		// No arguments at all still lists the home folder.
		out, err := b.tools["list_files"].Run(context.Background(), nil)
		Expect(err).NotTo(HaveOccurred(), "list_files with no arguments")
		Expect(out).To(ContainSubstring("secret.txt"))
	})
})
