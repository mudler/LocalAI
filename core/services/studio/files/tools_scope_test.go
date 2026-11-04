package files_test

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/extract/extracttest"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// The marker is in every file outside the agent's folder: it must never
// reach a tool result.
const scopeMarker = "OUTSIDE-7f3a9c"

const scopeChatFolder = "2026-11-01 Plan"

// scopedAccess is the access of alice's chat c1 whose agent works in
// /Projects/Acme at mode: the folder, the chat folder and the skill layers.
func scopedAccess(f *files.Service, mode workspace.Mode) session.Access {
	ctx := context.Background()
	Expect(f.EnsureHome(ctx, "alice")).To(Succeed())
	Expect(f.EnsureShared(ctx)).To(Succeed())
	user := f.Actor("alice")
	mk := func(rel string) workspace.Node {
		abs, err := f.Abs("alice", rel)
		Expect(err).NotTo(HaveOccurred())
		n, err := f.Store().Mkdir(ctx, user, abs)
		if err != nil {
			n, err = f.Store().Stat(ctx, user, abs)
		}
		Expect(err).NotTo(HaveOccurred())
		return n
	}
	acme := mk("/Projects/Acme")
	chat := mk("/" + session.ChatsDir + "/" + scopeChatFolder)
	roots := []workspace.FenceRoot{{Node: acme.ID, Mode: mode}, {Node: chat.ID, Mode: workspace.ModeWrite}}
	admin := workspace.Actor{Principal: files.AdminPrincipal}
	for _, l := range session.SkillLayers {
		if n, err := f.Store().Stat(ctx, admin, l.Path); err == nil {
			roots = append(roots, workspace.FenceRoot{Node: n.ID, Mode: workspace.ModeRead})
		}
	}
	a := workspace.Actor{Principal: "alice", ChatID: "c1", Groups: []string{files.EveryoneGroup}}.Fenced(roots...)
	v, err := session.BuildView(ctx, f.Store(), a, acme.Path, chat.Path, nil, slog.New(slog.DiscardHandler))
	Expect(err).NotTo(HaveOccurred())
	x, err := session.NewAccess(a, v)
	Expect(err).NotTo(HaveOccurred())
	return x
}

func newScopedBox(f *files.Service, mode workspace.Mode) *toolBox {
	b := &toolBox{f: f, tools: map[string]session.Tool{}}
	for _, tool := range f.Tools(scopedAccess(f, mode)) {
		b.tools[tool.Name] = tool
	}
	return b
}

// homeFiles lists every file of alice's home with its content, as the
// administrator sees it.
func homeFiles(f *files.Service) map[string]string {
	ctx := context.Background()
	admin := workspace.Actor{Principal: files.AdminPrincipal}
	out := map[string]string{}
	var walk func(dir string)
	walk = func(dir string) {
		kids, err := f.Store().List(ctx, admin, dir)
		Expect(err).NotTo(HaveOccurred())
		for _, k := range kids {
			if k.Kind == workspace.KindDir {
				out[k.Path+"/"] = ""
				walk(k.Path)
				continue
			}
			out[k.Path] = wstestRead(f, k.Path)
		}
	}
	walk(session.HomePath("alice"))
	return out
}

// scopePaths are paths that try to leave the agent's folder. None may reach
// a file outside it.
var scopePaths = []string{
	"../x", "/../x", "/workspace/home/../x", "/workspace/chat/../../x", "/workspace/shared-x/../home/x",
	"/users/alice/Other/secret.md", "//users/alice/Other/secret.md", "/workspace/home//../x",
	"..%2fOther%2fsecret.md", "%2e%2e/Other/secret.md", `..\Other\secret.md`, `\..\Other\secret.md`,
	"․․/Other/secret.md", "․․／Other／secret.md", "Other\x00/secret.md", "Other\n/secret.md",
	"/Workspace/Home/Other/secret.md", "/Other/secret.md", "/Other", "/Other/x.pdf",
	"/workspace/home/../../alice/Other/secret.md", "/workspace/Home/Other/secret.md", "/workspace/users/alice/Other/secret.md",
	"/users/bob/x", "/" + session.ChatsDir, "/" + session.SettingsDir + "/persona.md", "/workspace/chat/../Other/secret.md",
}

var _ = Describe("the file tools of a chat whose agent has a folder", func() {
	var (
		f      *files.Service
		ctx    = context.Background()
		before map[string]string
	)
	BeforeEach(func() {
		f = newFiles()
		Expect(f.EnsureHome(ctx, "alice")).To(Succeed())
		Expect(f.EnsureHome(ctx, "bob")).To(Succeed())
		userPut(f, "alice", "/Other/secret.md", "the "+scopeMarker+" plan\n")
		userPut(f, "alice", "/Other/x.pdf", scopeMarker)
		userPut(f, "alice", "/top.txt", scopeMarker+"\n")
		userPut(f, "alice", "/"+session.SettingsDir+"/persona.md", scopeMarker+"\n")
		userPut(f, "bob", "/x", scopeMarker)
		userPut(f, "alice", "/Projects/Acme/a.txt", "inside one\n")
		userPut(f, "alice", "/Projects/Acme2/near.txt", scopeMarker+"\n")
	})

	outside := func(state map[string]string) map[string]string {
		out := map[string]string{}
		for p, c := range state {
			if strings.HasPrefix(p, "/users/alice/Projects/Acme/") || strings.HasPrefix(p, "/users/alice/"+session.ChatsDir+"/"+scopeChatFolder+"/") {
				continue
			}
			out[p] = c
		}
		return out
	}

	It("cannot leave the agent's folder", func() {
		b := newScopedBox(f, workspace.ModeWrite)
		before = outside(homeFiles(f))
		for _, p := range scopePaths {
			calls := map[string]map[string]any{
				"read_file":    {"path": p},
				"list_files":   {"path": p},
				"list_files*":  {"path": p, "pattern": "*"},
				"search_files": {"query": "plan", "path": p},
				"write_file":   {"path": p, "content": "overwritten"},
				"edit_file":    {"path": p, "old_string": "plan", "new_string": "gone"},
				"delete_file":  {"path": p},
				"move_file":    {"from": p, "to": "/stolen.md"},
				"move_file<":   {"from": "/a.txt", "to": p},
			}
			for name, args := range calls {
				out, err := b.run(strings.TrimRight(name, "*<"), args)
				Expect(out).NotTo(ContainSubstring(scopeMarker), "%s %q", name, p)
				if err != nil {
					Expect(err.Error()).NotTo(ContainSubstring(scopeMarker), "%s %q", name, p)
					Expect(err.Error()).NotTo(ContainSubstring("/users/alice/Projects"), "%s %q: no workspace path", name, p)
				}
			}
			// Put a.txt back if a move took it away inside the folder.
			userPut(f, "alice", "/Projects/Acme/a.txt", "inside one\n")
		}
		Expect(outside(homeFiles(f))).To(Equal(before), "nothing outside the folder changed")
		bob, err := f.Store().Stat(ctx, workspace.Actor{Principal: files.AdminPrincipal}, "/users/bob/x")
		Expect(err).NotTo(HaveOccurred())
		Expect(bob.Kind).To(Equal(workspace.KindFile))
	})

	It("lists the folder at / and finds a name of the home inside it", func() {
		b := newScopedBox(f, workspace.ModeRead)
		list, err := b.run("list_files", map[string]any{"path": "/"})
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(Equal("a.txt (11 bytes)\n"))
		got, err := b.run("read_file", map[string]any{"path": "/workspace/home/a.txt"})
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal("inside one\n"))
		_, err = b.run("read_file", map[string]any{"path": "/Other/x.pdf"})
		Expect(err).To(MatchError("not found"))
		glob, err := b.run("list_files", map[string]any{"path": "/", "pattern": "*"})
		Expect(err).NotTo(HaveOccurred())
		Expect(glob).To(Equal("a.txt"))
	})

	It("refuses a write in a read-only folder and keeps the chat folder writable", func() {
		b := newScopedBox(f, workspace.ModeRead)
		for name, args := range map[string]map[string]any{
			"write_file":  {"path": "/b.txt", "content": "x"},
			"edit_file":   {"path": "/a.txt", "old_string": "one", "new_string": "two"},
			"delete_file": {"path": "/a.txt"},
			"move_file":   {"from": "/a.txt", "to": "/workspace/chat/a.txt"},
		} {
			_, err := b.run(name, args)
			Expect(err).To(MatchError(ContainSubstring("read-only")), name)
		}
		Expect(wstestRead(f, "/users/alice/Projects/Acme/a.txt")).To(Equal("inside one\n"))
		out, err := b.run("write_file", map[string]any{"path": "/workspace/chat/work/run.py", "content": "print(1)\n"})
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(Equal("Wrote 9 bytes to /workspace/chat/work/run.py."))
		Expect(wstestRead(f, "/users/alice/"+session.ChatsDir+"/"+scopeChatFolder+"/work/run.py")).To(Equal("print(1)\n"))
	})

	It("writes in a writable folder and never takes the folder away", func() {
		b := newScopedBox(f, workspace.ModeWrite)
		out, err := b.run("write_file", map[string]any{"path": "/notes/b.txt", "content": "new"})
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(Equal("Wrote 3 bytes to /workspace/home/notes/b.txt."))
		Expect(wstestRead(f, "/users/alice/Projects/Acme/notes/b.txt")).To(Equal("new"))
		for _, p := range []string{"/", "/workspace/home", "/workspace/chat"} {
			_, err := b.run("delete_file", map[string]any{"path": p})
			Expect(err).To(HaveOccurred(), p)
			_, err = b.run("move_file", map[string]any{"from": p, "to": "/moved"})
			Expect(err).To(HaveOccurred(), p)
		}
	})

	It("reads the skill layers read only", func() {
		adminPut(f, files.SharedSkillsPath+"/pdf/SKILL.md", "---\nname: pdf\n---\nread pdfs\n")
		b := newScopedBox(f, workspace.ModeWrite)
		got, err := b.run("read_file", map[string]any{"path": "/workspace/shared-skills/pdf/SKILL.md"})
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(ContainSubstring("read pdfs"))
		_, err = b.run("write_file", map[string]any{"path": "/workspace/shared-skills/pdf/SKILL.md", "content": "x"})
		Expect(err).To(MatchError(ContainSubstring("read-only")))
	})

	It("keeps a symbolic link name inside the folder", func() {
		b := newScopedBox(f, workspace.ModeWrite)
		// The workspace has no symbolic links: a name that looks like one is a
		// file name in the folder, and its content is plain text.
		_, err := b.run("write_file", map[string]any{"path": "/link", "content": "../Other/secret.md"})
		Expect(err).NotTo(HaveOccurred())
		got, err := b.run("read_file", map[string]any{"path": "/link"})
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal("../Other/secret.md"))
	})
})

var _ = Describe("search in a chat whose agent has a folder", func() {
	It("finds nothing outside the folder and counts no pending file outside it", func() {
		ctx := context.Background()
		f, ix := newIndexedFiles()
		Expect(f.EnsureHome(ctx, "alice")).To(Succeed())
		upload(f, "alice", "/Other/found.txt", []byte("a "+scopeMarker+" plan\n"))
		upload(f, "alice", "/Projects/Acme2/near.txt", []byte("a "+scopeMarker+" plan\n"))
		upload(f, "alice", "/Projects/Acme/in.txt", []byte("a plan inside\n"))
		upload(f, "alice", "/Other/ready.pdf", extracttest.TextPDF("an indexed plan "+scopeMarker))
		Expect(ix.Pass(ctx)).To(Succeed())
		// More pending documents than the search checks one by one, all
		// outside the folder (and some in a sibling whose name starts the same).
		for i := range 60 {
			dir := "/Other"
			if i%2 == 0 {
				dir = "/Projects/Acme2"
			}
			upload(f, "alice", fmt.Sprintf("%s/doc-%02d.pdf", dir, i), extracttest.TextPDF(fmt.Sprintf("draft plan %d", i)))
		}
		b := newScopedBox(f, workspace.ModeRead)
		got, err := b.run("search_files", map[string]any{"query": "plan"})
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal("/workspace/home/in.txt:1: a plan inside\n"))
		Expect(got).NotTo(ContainSubstring("indexed"))
		for _, q := range []string{"Other", "doc-", "found", "near", scopeMarker} {
			got, err := b.run("search_files", map[string]any{"query": q})
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(Equal("Nothing found."), q)
		}
		// The same search as the user, unscoped, sees the pending files: the
		// count above is 0 because of the folder, not because there are none.
		res, err := f.Search(ctx, "alice", "plan", "/", 10)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Indexing).To(Equal(60))
	})

	It("searches the chat folder too when no path is given", func() {
		ctx := context.Background()
		f, _ := newIndexedFiles()
		x := scopedAccess(f, workspace.ModeRead)
		_, err := f.Store().Put(ctx, x.Actor(), x.Place().Chat()+"/note.txt", strings.NewReader("a plan in the chat\n"), workspace.PutOptions{})
		Expect(err).NotTo(HaveOccurred())
		userPut(f, "alice", "/Projects/Acme/in.txt", "a plan inside\n")
		b := &toolBox{f: f, tools: map[string]session.Tool{}}
		for _, tool := range f.Tools(x) {
			b.tools[tool.Name] = tool
		}
		got, err := b.run("search_files", map[string]any{"query": "plan"})
		Expect(err).NotTo(HaveOccurred())
		lines := strings.Split(strings.TrimSpace(got), "\n")
		sort.Strings(lines)
		Expect(lines).To(Equal([]string{"/workspace/chat/note.txt:1: a plan in the chat", "/workspace/home/in.txt:1: a plan inside"}))
	})
})
