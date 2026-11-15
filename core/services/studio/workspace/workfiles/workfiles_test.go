package workfiles_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/pages"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/skills"
	"github.com/mudler/LocalAI/core/services/studio/workspace/workfiles"
)

func TestWorkfiles(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "workfiles")
}

var _ = Describe("working files", func() {
	It("uses the folder names of the session and the pages", func() {
		Expect(workfiles.ChatsDir).To(Equal(session.ChatsDir))
		Expect(workfiles.SettingsDir).To(Equal(session.SettingsDir))
		Expect(workfiles.PagesDir).To(Equal(pages.Dir))
		Expect(workfiles.UsersRoot).To(Equal(session.UsersRoot))
		Expect(workfiles.SkillsDir).To(Equal(skills.SkillsDir))
	})

	It("names the folders of a home that nobody else may get a grant on", func() {
		for p, want := range map[string]bool{
			"/LocalAI": true, "/LocalAI/agents/a": true, "/Chats/x": true, "/Memory": true, "/memory/notes": true,
			"/pages": true, "/pages/home": true, "/.skills": true, "/Work/.skills/report": true, "/Work/.SKILLS": true,
			"/": false, "/Projects": false, "/Projects/pages": false, "/Projects/Memory": false, "/Work/skills": false,
		} {
			Expect(workfiles.Managed(p)).To(Equal(want), p)
		}
	})

	It("lets a grant move inside one home only, and never into a managed folder", func() {
		for _, c := range []struct {
			from, to string
			want     bool
		}{
			{"/users/a/X", "/users/a/Archive/X", true},
			{"/shared/x/A", "/shared/y/A", true},
			{"/users/a/X", "/users/b/X", false},
			{"/users/a/X", "/shared/X", false},
			{"/shared/X", "/users/a/X", false},
			{"/users/a/X", "/users/a/LocalAI/X", false},
			{"/users/a/X", "/users/a/Projects/.skills", false},
		} {
			Expect(workfiles.GrantsMayMove(c.from, c.to)).To(Equal(c.want), c.from+" -> "+c.to)
		}
	})

	DescribeTable("tells a working file from a deliverable by its path",
		func(p, want string) {
			Expect(workfiles.Kind(p)).To(Equal(want))
		},
		// The work folder of any chat holds working files, whatever they are.
		Entry("a script in the work folder", "/Chats/2026-10-31 Report/work/build_document.py", workfiles.Working),
		Entry("a document in the work folder", "/Chats/2026-10-31 Report/work/draft.docx", workfiles.Working),
		Entry("a nested file in the work folder", "/Chats/2026-10-31 Report/work/data/rows.json", workfiles.Working),
		Entry("the guest path of the work folder", "/workspace/home/Chats/2026-10-31 Report/work/notes.md", workfiles.Working),
		Entry("the Chats folder in another case", "/chats/2026-10-31 Report/work/a.csv", workfiles.Working),
		// What the user asked for.
		Entry("a document at the top", "/report.docx", workfiles.Deliverable),
		Entry("a spreadsheet in a folder", "/Budget/q1.xlsx", workfiles.Deliverable),
		Entry("an image by its guest path", "/workspace/home/chart.png", workfiles.Deliverable),
		Entry("a file in the chat folder", "/Chats/2026-10-31 Report/summary.pdf", workfiles.Deliverable),
		Entry("a folder named work outside a chat", "/work/plan.md", workfiles.Deliverable),
		Entry("a CSV at the top", "/rows.csv", workfiles.Deliverable),
		// Helpers by extension and by folder.
		Entry("a Python script at the top", "/scripts.py", workfiles.Working),
		Entry("a script in scripts/", "/scripts/build_document.py", workfiles.Working),
		Entry("data in scripts/", "/scripts/rows.json", workfiles.Working),
		Entry("a file in tmp/", "/tmp/out.txt", workfiles.Working),
		Entry("a file in Scratch/", "/Projects/Scratch/notes.md", workfiles.Working),
		Entry("a shell script", "/run.sh", workfiles.Working),
		Entry("a JavaScript module", "/tool.mjs", workfiles.Working),
		Entry("TypeScript", "/a.ts", workfiles.Working),
		Entry("Ruby", "/a.rb", workfiles.Working),
		Entry("Perl", "/a.pl", workfiles.Working),
		Entry("PowerShell", "/a.PS1", workfiles.Working),
		Entry("a batch file", "/a.bat", workfiles.Working),
		Entry("a log", "/build.log", workfiles.Working),
		Entry("a temporary file", "/x.tmp", workfiles.Working),
		Entry("a file named scripts", "/notes/scripts", workfiles.Deliverable),
		// Pages, the settings folder and the shared folders are deliverables.
		Entry("the script of a page", "/pages/tracker/app.js", workfiles.Deliverable),
		Entry("a page in a scripts folder", "/pages/tracker/scripts/app.js", workfiles.Deliverable),
		Entry("a settings file", "/LocalAI/persona.json", workfiles.Deliverable),
		Entry("a script in a shared folder", "/workspace/shared-team/build.py", workfiles.Deliverable),
		Entry("a log in a shared folder", "/workspace/shared-team/tmp/run.log", workfiles.Deliverable),
		Entry("the home", "/", workfiles.Deliverable),
		Entry("an empty path", "", workfiles.Deliverable),
	)

	DescribeTable("finds a helper left outside the work folder",
		func(p string, want bool) {
			Expect(workfiles.Stray(p)).To(Equal(want))
		},
		Entry("a script at the top", "/build_document.py", true),
		Entry("a script in scripts/", "/workspace/home/scripts/build.py", true),
		Entry("a script in the work folder", "/Chats/2026-10-31 Report/work/build.py", false),
		Entry("a document at the top", "/report.docx", false),
		Entry("the script of a page", "/pages/tracker/app.js", false),
		Entry("a script in a shared folder", "/workspace/shared-team/build.py", false),
	)

	It("names the work folder of a chat", func() {
		Expect(workfiles.Folder("2026-10-31 Report")).To(Equal("/Chats/2026-10-31 Report/work"))
		Expect(workfiles.GuestFolder("2026-10-31 Report")).To(Equal("/workspace/home/Chats/2026-10-31 Report/work/"))
		Expect(workfiles.Folder("")).To(BeEmpty())
		Expect(workfiles.GuestFolder("")).To(BeEmpty())
	})

	It("steers the model to the folder with a prompt that names no chat's folder", func() {
		p := workfiles.Prompt
		for _, want := range []string{
			"scripts", "intermediate data", "logs", "drafts", "note",
			"deliverable", "Never create new top-level folders", "python3 -c", "heredoc", "$WORK",
			"/workspace/home/pages/", "/workspace/home/LocalAI/",
		} {
			Expect(p).To(ContainSubstring(want))
		}
		Expect(p).NotTo(ContainSubstring("/workspace/home/Chats"), "a chat's folder in the prompt breaks the prompt cache")
		guest := workfiles.GuestFolder("2026-10-31 Report")
		Expect(workfiles.Note(guest)).To(ContainSubstring(`"/workspace/home/Chats/2026-10-31 Report/work/"`))
		Expect(workfiles.Note(guest)).To(ContainSubstring("work folder"))
		Expect(workfiles.Note(guest)).To(HavePrefix(workfiles.NoteFolder(guest)))
		Expect(workfiles.Note(guest)).To(ContainSubstring(workfiles.ShellHint))
		Expect(workfiles.NoteFolder(guest)).NotTo(ContainSubstring("$WORK"), "the short note leaves room for the notes after it")
		// The literal path stays for the file tools; the shell is told to use $WORK.
		for _, want := range []string{"The shell starts in $WORK", "Use $WORK or relative paths", "Quote every path with double quotes",
			"Never type out a name with spaces or non-ASCII characters", "ls or a glob", "file tools take that path"} {
			Expect(workfiles.Note(guest)).To(ContainSubstring(want))
		}
		n := workfiles.Nudge("/workspace/home/Chats/x/work/", "/workspace/home/build.py")
		Expect(n).To(ContainSubstring("/workspace/home/Chats/x/work/"))
		Expect(n).To(ContainSubstring("/workspace/home/build.py"))
		Expect(n).To(ContainSubstring("Consider moving it"))
		Expect(workfiles.ToolHint).To(ContainSubstring("work folder"))
		Expect(workfiles.ToolHint).To(ContainSubstring("note"))
	})

	DescribeTable("treats /workspace/chat/work as the work folder",
		func(p, want string, stray bool) {
			Expect(workfiles.Kind(p)).To(Equal(want), p)
			Expect(workfiles.Stray(p)).To(Equal(stray), p)
		},
		Entry("a script in the chat's work folder", "/workspace/chat/work/run.py", workfiles.Working, false),
		Entry("a draft in the chat's work folder", "/workspace/chat/work/draft.docx", workfiles.Working, false),
		Entry("a nested file in the chat's work folder", "/workspace/chat/work/data/rows.json", workfiles.Working, false),
		Entry("an attachment in the chat folder", "/workspace/chat/invoice.pdf", workfiles.Deliverable, false),
		Entry("a script beside the attachments", "/workspace/chat/run.py", workfiles.Working, true),
		Entry("a script in the agent's folder", "/workspace/home/run.py", workfiles.Working, true),
		Entry("a report in the agent's folder", "/workspace/home/report.docx", workfiles.Deliverable, false),
		Entry("the chat folder itself", "/workspace/chat", workfiles.Deliverable, false),
		Entry("a folder named chat in the home is no chat folder", "/workspace/home/chat/work/a.docx", workfiles.Deliverable, false),
	)

	It("never moves a file out of a hidden folder", func() {
		Expect(workfiles.Stray("/workspace/home/.state/run.sh")).To(BeFalse())
		Expect(workfiles.Stray("/workspace/home/jobs/.cache/run.py")).To(BeFalse())
		Expect(workfiles.Stray("/workspace/home/run.py")).To(BeTrue())
	})

	It("tells a scoped chat that a file a trigger uses must stay in the home", func() {
		n := workfiles.Nudge(workfiles.ChatGuestFolder, "/workspace/home/run.py")
		Expect(n).To(ContainSubstring("Consider moving it"))
		Expect(n).To(ContainSubstring("/workspace/home"))
		Expect(n).To(ContainSubstring("trigger"))
		Expect(n).To(ContainSubstring("never under /workspace/chat"))
		// An unscoped chat's work folder is inside the home: nothing to warn about.
		Expect(workfiles.Nudge("/workspace/home/Chats/x/work/", "/workspace/home/run.py")).NotTo(ContainSubstring("trigger"))
	})

	It("tells a shell tool where it starts and how to quote", func() {
		Expect(workfiles.ShellHint).To(Equal("The shell starts in $WORK, this chat's work folder. Use $WORK or relative paths instead of typing absolute paths. " +
			"Quote every path with double quotes. Never type out a name with spaces or non-ASCII characters; take it from ls or a glob."))
	})

	It("keeps the short shell hint to the same four points", func() {
		for _, want := range []string{"$WORK", "relative paths", "Double-quote every path", "spaces or non-ASCII", "ls or a glob"} {
			Expect(workfiles.ShellHintShort).To(ContainSubstring(want))
		}
		Expect(len(workfiles.ShellHintShort)).To(BeNumerically("<", len(workfiles.ShellHint)))
	})

	It("steers a scoped chat to its own work folder", func() {
		Expect(workfiles.ChatGuestFolder).To(Equal("/workspace/chat/work/"))
		p := workfiles.ScopedPrompt(true)
		for _, want := range []string{"/workspace/chat/work/", "$WORK", "deliverable", "/workspace/home/", "python3 -c"} {
			Expect(p).To(ContainSubstring(want))
		}
		for _, leak := range []string{"pages", "LocalAI", "Chats"} {
			Expect(p).NotTo(ContainSubstring(leak))
		}
		ro := workfiles.ScopedPrompt(false)
		Expect(ro).To(ContainSubstring("read only"))
		Expect(ro).To(ContainSubstring("/workspace/chat/"))
		Expect(ro).NotTo(ContainSubstring("else in /workspace/home/"))
	})
})
