package workfiles_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/pages"
	"github.com/mudler/LocalAI/core/services/studio/session"
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

	It("steers the model with the path of the folder", func() {
		p := workfiles.Prompt("/workspace/home/Chats/2026-10-31 Report/work/")
		for _, want := range []string{
			"/workspace/home/Chats/2026-10-31 Report/work/", "scripts", "intermediate data", "logs", "drafts",
			"deliverable", "Never create new top-level folders", "python3 -c", "heredoc", "mkdir -p",
			"/workspace/home/pages/", "/workspace/home/LocalAI/",
		} {
			Expect(p).To(ContainSubstring(want))
		}
		n := workfiles.Nudge("/workspace/home/Chats/x/work/", "/workspace/home/build.py")
		Expect(n).To(ContainSubstring("/workspace/home/Chats/x/work/"))
		Expect(n).To(ContainSubstring("/workspace/home/build.py"))
		Expect(n).To(ContainSubstring("Consider moving it"))
		Expect(workfiles.ToolHint).To(ContainSubstring("work folder"))
	})
})
