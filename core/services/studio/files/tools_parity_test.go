package files_test

import (
	"context"
	"fmt"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// The built-in file tools are the one file toolset of a chat: they do what the
// Sandbox's read, write, edit, glob and grep did, on the user's files.

// lastAgentRev returns the newest change set that chat made in alice's files.
func lastAgentRev(f *files.Service, user, chat string) int64 {
	hist, err := f.Store().History(context.Background(), f.Actor(user), workspace.HistoryQuery{Path: session.HomePath(user), Limit: 50})
	Expect(err).NotTo(HaveOccurred())
	for _, cs := range hist {
		if cs.Kind == workspace.ChangeAgent && cs.ChatID == chat {
			return cs.Rev
		}
	}
	Fail("no agent change set of " + chat)
	return 0
}

var _ = Describe("the file tools that replace the Sandbox's", func() {
	Describe("edit_file", func() {
		var (
			f *files.Service
			b *toolBox
		)
		BeforeEach(func() {
			f = newFiles()
			b = newToolBox(f, "alice", "c1")
			_, err := b.run("write_file", map[string]any{"path": "/src/app.py", "content": "x = 1\ny = 1\nprint(x)\n"})
			Expect(err).NotTo(HaveOccurred())
		})

		DescribeTable("the cases of a replacement",
			func(args map[string]any, wantErr, wantContent string) {
				args["path"] = "/workspace/home/src/app.py"
				out, err := b.run("edit_file", args)
				if wantErr != "" {
					Expect(err).To(MatchError(ContainSubstring(wantErr)), "out = %q", out)
				} else {
					Expect(err).NotTo(HaveOccurred())
				}
				Expect(wstestRead(f, "/users/alice/src/app.py")).To(Equal(wantContent))
			},
			Entry("one match is replaced", map[string]any{"old_string": "print(x)", "new_string": "print(x + y)"}, "", "x = 1\ny = 1\nprint(x + y)\n"),
			Entry("no match is refused", map[string]any{"old_string": "z = 3", "new_string": "z = 4"}, "not found", "x = 1\ny = 1\nprint(x)\n"),
			Entry("two matches are refused without replace_all", map[string]any{"old_string": "= 1", "new_string": "= 2"}, "2 times", "x = 1\ny = 1\nprint(x)\n"),
			Entry("replace_all replaces every match", map[string]any{"old_string": "= 1", "new_string": "= 2", "replace_all": true}, "", "x = 2\ny = 2\nprint(x)\n"),
			Entry("an empty old_string is refused", map[string]any{"old_string": "", "new_string": "a"}, "old_string", "x = 1\ny = 1\nprint(x)\n"),
			Entry("the same text is refused", map[string]any{"old_string": "x", "new_string": "x"}, "the same", "x = 1\ny = 1\nprint(x)\n"),
		)

		It("can be undone like a write", func() {
			_, err := b.run("edit_file", map[string]any{"path": "/src/app.py", "old_string": "print(x)", "new_string": "print(y)"})
			Expect(err).NotTo(HaveOccurred())
			rev := lastAgentRev(f, "alice", "c1")
			_, _, err = f.Store().Undo(context.Background(), f.Actor("alice"), rev, workspace.UndoOptions{})
			Expect(err).NotTo(HaveOccurred())
			Expect(wstestRead(f, "/users/alice/src/app.py")).To(Equal("x = 1\ny = 1\nprint(x)\n"))
		})

		It("refuses a file that is not text and a path outside the home", func() {
			abs, _ := f.Abs("alice", "/blob.bin")
			_, err := f.Store().Put(context.Background(), f.Actor("alice"), abs, strings.NewReader("a\x00b"), workspace.PutOptions{})
			Expect(err).NotTo(HaveOccurred())
			_, err = b.run("edit_file", map[string]any{"path": "/blob.bin", "old_string": "a", "new_string": "c"})
			Expect(err).To(MatchError(ContainSubstring("not a text file")))
			for _, p := range []string{"/../bob/a.txt", "/workspace/home/../../users/bob/a.txt", "/workspace/shared-builtin-skills/x/SKILL.md"} {
				_, err := b.run("edit_file", map[string]any{"path": p, "old_string": "a", "new_string": "b"})
				Expect(err).To(HaveOccurred(), p)
			}
		})
	})

	Describe("list_files with a pattern", func() {
		It("lists what matches below a folder, * crossing folders", func() {
			f := newFiles()
			b := newToolBox(f, "alice", "c1")
			for _, p := range []string{"/notes/a.md", "/notes/deep/b.md", "/reports/q1.xlsx", "/reports/q1.csv", "/readme.md"} {
				_, err := b.run("write_file", map[string]any{"path": p, "content": "x"})
				Expect(err).NotTo(HaveOccurred())
			}
			out, err := b.run("list_files", map[string]any{"path": "/", "pattern": "*.md"})
			Expect(err).NotTo(HaveOccurred())
			Expect(strings.Fields(out)).To(Equal([]string{"notes/a.md", "notes/deep/b.md", "readme.md"}))

			out, err = b.run("list_files", map[string]any{"pattern": "reports/*.xlsx"})
			Expect(err).NotTo(HaveOccurred())
			Expect(strings.TrimSpace(out)).To(Equal("reports/q1.xlsx"))

			out, err = b.run("list_files", map[string]any{"path": "/workspace/home/notes", "pattern": "deep/?.md"})
			Expect(err).NotTo(HaveOccurred())
			Expect(strings.TrimSpace(out)).To(Equal("deep/b.md"))

			out, err = b.run("list_files", map[string]any{"pattern": "*.pdf"})
			Expect(err).NotTo(HaveOccurred())
			Expect(out).To(Equal("No files match."))
		})

		It("stops at 500 paths and says so", func() {
			f := newFiles()
			b := newToolBox(f, "alice", "c1")
			for i := range 503 {
				userPut(f, "alice", fmt.Sprintf("/many/f%03d.txt", i), "x")
			}
			out, err := b.run("list_files", map[string]any{"pattern": "many/*"})
			Expect(err).NotTo(HaveOccurred())
			lines := strings.Split(strings.TrimSpace(out), "\n")
			Expect(lines).To(HaveLen(501))
			Expect(lines[500]).To(ContainSubstring("more"))
		})
	})

	Describe("search_files with a regular expression", func() {
		var b *toolBox
		BeforeEach(func() {
			f := newFiles()
			b = newToolBox(f, "alice", "c1")
			_, _ = b.run("write_file", map[string]any{"path": "/src/main.go", "content": "func Main() {}\nfunc helper() {}\n// TODO: fix\n"})
			_, _ = b.run("write_file", map[string]any{"path": "/src/util.go", "content": "func Util() {}\n"})
		})

		It("matches lines, case-insensitive unless asked", func() {
			out, err := b.run("search_files", map[string]any{"query": `^func [A-Z]`, "regex": true, "ignore_case": false})
			Expect(err).NotTo(HaveOccurred())
			Expect(out).To(ContainSubstring("/src/main.go:1: func Main() {}"))
			Expect(out).To(ContainSubstring("/src/util.go:1: func Util() {}"))
			Expect(out).NotTo(ContainSubstring("helper"))

			out, err = b.run("search_files", map[string]any{"query": `^func [A-Z]`, "regex": true})
			Expect(err).NotTo(HaveOccurred())
			Expect(out).To(ContainSubstring("helper"), "ignore_case is on by default")
		})

		It("searches one file when the path is a file", func() {
			out, err := b.run("search_files", map[string]any{"query": "func (Main|Util)", "regex": true, "path": "/workspace/home/src/util.go"})
			Expect(err).NotTo(HaveOccurred())
			Expect(strings.TrimSpace(out)).To(Equal("/workspace/home/src/util.go:1: func Util() {}"), "a hit names the path the model uses")
		})

		It("refuses a pattern that is not a regular expression", func() {
			_, err := b.run("search_files", map[string]any{"query": "func (", "regex": true})
			Expect(err).To(MatchError(ContainSubstring("regular expression")))
		})

		It("stops at 200 lines", func() {
			f := newFiles()
			b := newToolBox(f, "alice", "c1")
			_, _ = b.run("write_file", map[string]any{"path": "/big.txt", "content": strings.Repeat("hit\n", 300)})
			out, err := b.run("search_files", map[string]any{"query": "hit", "regex": true})
			Expect(err).NotTo(HaveOccurred())
			Expect(strings.Count(out, "/big.txt:")).To(Equal(200))
		})
	})

	Describe("shared folders", func() {
		var (
			f *files.Service
			b *toolBox
		)
		BeforeEach(func() {
			f = newFiles()
			Expect(f.EnsureHome(context.Background(), "alice")).To(Succeed())
			adminPut(f, "/shared/team/plan.md", "step one\n")
			adminPut(f, "/shared/docs/guide.md", "read me\n")
			admin := workspace.Actor{Principal: files.AdminPrincipal}
			Expect(f.Store().Grant(context.Background(), admin, "alice", "/shared/team", workspace.ModeWrite)).To(Succeed())
			Expect(f.Store().Grant(context.Background(), admin, "alice", "/shared/docs", workspace.ModeRead)).To(Succeed())
			f.SetSharedRoots(func(context.Context, workspace.Actor) ([]session.SharedRoot, error) {
				return []session.SharedRoot{{Path: "/shared/team", Mode: workspace.ModeWrite}, {Path: "/shared/docs", Mode: workspace.ModeRead}}, nil
			})
			b = newToolBox(f, "alice", "c1")
		})

		It("reads, lists and searches them at /workspace/shared-<name>", func() {
			got, err := b.run("read_file", map[string]any{"path": "/workspace/shared-docs/guide.md"})
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(Equal("read me\n"))
			list, err := b.run("list_files", map[string]any{"path": "/workspace/shared-team"})
			Expect(err).NotTo(HaveOccurred())
			Expect(list).To(ContainSubstring("plan.md"))
			hits, err := b.run("search_files", map[string]any{"query": "step", "path": "/workspace/shared-team"})
			Expect(err).NotTo(HaveOccurred())
			Expect(hits).To(ContainSubstring("/workspace/shared-team/plan.md:1: step one"))
		})

		It("writes a folder that can be edited and refuses one that can only be read", func() {
			_, err := b.run("edit_file", map[string]any{"path": "/workspace/shared-team/plan.md", "old_string": "one", "new_string": "two"})
			Expect(err).NotTo(HaveOccurred())
			Expect(wstestRead(f, "/shared/team/plan.md")).To(Equal("step two\n"))
			for name, args := range map[string]map[string]any{
				"write_file":  {"path": "/workspace/shared-docs/guide.md", "content": "x"},
				"edit_file":   {"path": "/workspace/shared-docs/guide.md", "old_string": "read", "new_string": "x"},
				"delete_file": {"path": "/workspace/shared-docs/guide.md"},
				"move_file":   {"from": "/workspace/shared-docs/guide.md", "to": "/stolen.md"},
			} {
				_, err := b.run(name, args)
				Expect(err).To(MatchError(ContainSubstring("read-only")), name)
			}
			Expect(wstestRead(f, "/shared/docs/guide.md")).To(Equal("read me\n"))
		})

		It("cannot reach a folder that is not shared with the user", func() {
			adminPut(f, "/shared/secret/x.md", "no")
			for _, p := range []string{"/workspace/shared-secret/x.md", "/workspace/shared-team/../secret/x.md", "/workspace/shared-team/../../users/bob"} {
				_, err := b.run("read_file", map[string]any{"path": p})
				Expect(err).To(HaveOccurred(), p)
			}
		})
	})

	Describe("the spellings of a path", func() {
		DescribeTable("all name the same file in the home",
			func(p string) {
				f := newFiles()
				b := newToolBox(f, "alice", "c1")
				_, err := b.run("write_file", map[string]any{"path": p, "content": "same"})
				Expect(err).NotTo(HaveOccurred(), p)
				Expect(wstestRead(f, "/users/alice/notes/a.txt")).To(Equal("same"))
				kids, err := f.Store().List(context.Background(), f.Actor("alice"), "/users/alice")
				Expect(err).NotTo(HaveOccurred())
				for _, k := range kids {
					Expect(k.Name).NotTo(BeElementOf("workspace", "home"), "%s made a folder %s", p, k.Name)
				}
			},
			Entry("/workspace/home/<p>", "/workspace/home/notes/a.txt"),
			Entry("workspace/home/<p>", "workspace/home/notes/a.txt"),
			Entry("home/<p>", "home/notes/a.txt"),
			Entry("/<p>", "/notes/a.txt"),
			Entry("<p>", "notes/a.txt"),
		)

		It("refuses another path below /workspace, writes nothing and teaches the right one", func() {
			f := newFiles()
			b := newToolBox(f, "alice", "c1")
			for _, p := range []string{"/workspace/newsapp/page.html", "workspace/build_deck.py"} {
				_, err := b.run("write_file", map[string]any{"path": p, "content": "x"})
				Expect(err).To(HaveOccurred(), p)
				Expect(err.Error()).To(ContainSubstring("/workspace/home/"))
				Expect(err.Error()).To(ContainSubstring("pages/<name>/index.html"))
			}
			_, err := b.run("write_file", map[string]any{"path": "/workspace/newsapp/page.html", "content": "x"})
			Expect(err).To(MatchError(ContainSubstring("Did you mean /workspace/home/newsapp/page.html?")))
			kids, err := f.Store().List(context.Background(), f.Actor("alice"), "/users/alice")
			Expect(err).NotTo(HaveOccurred())
			for _, k := range kids {
				Expect(k.Name).NotTo(Equal("workspace"))
			}
			for _, tool := range []string{"read_file", "list_files", "edit_file", "delete_file"} {
				_, err := b.run(tool, map[string]any{"path": "/workspace/newsapp/page.html", "old_string": "a", "new_string": "b"})
				Expect(err).To(MatchError(ContainSubstring("/workspace/home/")), tool)
			}
		})
	})
})
