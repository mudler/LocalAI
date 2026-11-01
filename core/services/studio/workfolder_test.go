package studio_test

import (
	"context"
	"regexp"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/workfiles"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the work folder of a chat", func() {
	// Each message is a turn: "script" runs bash twice, "write" saves a
	// script with write_file twice.
	script := func(_ workspace.Actor, text string) []studiotest.ToolStep {
		switch text {
		case "script":
			return []studiotest.ToolStep{
				{Tool: "bash", Args: `{"command":"python3 build.py"}`},
				{Tool: "bash", Args: `{"command":"python3 build.py"}`},
				{Say: "Done."},
			}
		case "write":
			return []studiotest.ToolStep{
				{Tool: "write_file", Args: `{"path":"/workspace/home/build_document.py","content":"print(1)"}`},
				{Tool: "write_file", Args: `{"path":"/workspace/home/build_more.py","content":"print(2)"}`},
				{Say: "Done."},
			}
		}
		return []studiotest.ToolStep{{Say: "ok"}}
	}
	sandbox := func(f *files.Service, write map[string]string) func(*studio.Deps) {
		sb := &sandboxThatWrites{fakeAddons: &fakeAddons{tools: map[string][]studio.AddonTool{alice: shadowingSandbox()}}, files: f, write: write}
		return func(d *studio.Deps) { d.Files = f; d.AddonTools = sb }
	}
	folderOf := func(svc *studio.Service, user, id string) string {
		cf, err := svc.ChatFiles(context.Background(), user, id)
		Expect(err).NotTo(HaveOccurred())
		return cf.Folder
	}

	It("names the folder in the prompt, makes it before a command and marks the files of the card", func() {
		f := newFiles()
		svc, h := newToolService(script, sandbox(f, map[string]string{"/summary.docx": "PK", "/scripts/build.py": "x"}))
		id := create(svc, alice)
		sent(svc, alice, id, "script")
		idle(svc, alice, id)

		p := h.TurnPrompt(id)
		Expect(p).To(ContainSubstring("Working files:"))
		Expect(p).To(MatchRegexp(`/workspace/home/Chats/[^/]+/work/`))
		Expect(folderOf(svc, alice, id)).NotTo(BeEmpty(), "the command did not make the chat folder")
		guest := strings.TrimPrefix(regexpFind(p, `/workspace/home/Chats/[^/]+/work/`), "/workspace/home")
		n, err := f.Store().Stat(context.Background(), files.UserActor(alice), session.HomePath(alice)+strings.TrimSuffix(guest, "/"))
		Expect(err).NotTo(HaveOccurred(), "the work folder was not made")
		Expect(n.Kind).To(Equal(workspace.KindDir))

		card := toolEntries(svc, alice, id)[0].Card // the first call saved the files
		Expect(card).NotTo(BeNil())
		Expect(cardFile(card, "/summary.docx").Kind).To(Equal(workfiles.Deliverable))
		Expect(cardFile(card, "/scripts/build.py").Kind).To(Equal(workfiles.Working))
		got := chatFiles(svc, alice, id)
		Expect(got["summary.docx"].Kind).To(Equal(workfiles.Deliverable))
		Expect(got["build.py"].Kind).To(Equal(workfiles.Working))
	})

	It("nudges once a turn when a helper file lands outside the work folder", func() {
		f := newFiles()
		svc, h := newToolService(script, func(d *studio.Deps) { d.Files = f })
		id := create(svc, alice)
		sent(svc, alice, id, "write")
		idle(svc, alice, id)
		res := h.ResultsFor("write_file")
		Expect(res).To(HaveLen(2))
		Expect(res[0].Err).NotTo(HaveOccurred())
		Expect(res[0].Out).To(ContainSubstring("/workspace/home/build_document.py looks like a helper file"))
		Expect(res[0].Out).To(MatchRegexp(`belong in /workspace/home/Chats/[^/]+/work/`))
		Expect(res[1].Out).NotTo(ContainSubstring("helper file"), "the second call of the turn was nudged again")

		// The next turn may be nudged again.
		sent(svc, alice, id, "write")
		idle(svc, alice, id)
		res = h.ResultsFor("write_file")
		Expect(res).To(HaveLen(4))
		Expect(res[2].Out).To(ContainSubstring("looks like a helper file"))
		Expect(res[3].Out).NotTo(ContainSubstring("looks like a helper file"))
	})

	It("nudges a command that saved a script outside the work folder, never one that saved a document", func() {
		f := newFiles()
		svc, h := newToolService(script, sandbox(f, map[string]string{"/report.docx": "PK"}))
		id := create(svc, alice)
		sent(svc, alice, id, "script")
		idle(svc, alice, id)
		for _, r := range h.ResultsFor("bash") {
			Expect(r.Out).NotTo(ContainSubstring("helper file"))
		}

		f2 := newFiles()
		svc2, h2 := newToolService(script, sandbox(f2, map[string]string{"/scripts/build_document.py": "x"}))
		id2 := create(svc2, alice)
		sent(svc2, alice, id2, "script")
		idle(svc2, alice, id2)
		res := h2.ResultsFor("bash")
		Expect(res).NotTo(BeEmpty())
		Expect(res[0].Out).To(ContainSubstring("/workspace/home/scripts/build_document.py looks like a helper file"))
	})

	It("steers nothing when the setting is off", func() {
		f := newFiles()
		svc, h := newToolService(script, func(d *studio.Deps) {
			d.Files = f
			d.WorkFolderOn = func() bool { return false }
		})
		id := create(svc, alice)
		sent(svc, alice, id, "write")
		idle(svc, alice, id)
		Expect(h.TurnPrompt(id)).NotTo(ContainSubstring("Working files:"))
		for _, r := range h.ResultsFor("write_file") {
			Expect(r.Out).NotTo(ContainSubstring("helper file"))
		}
		// The file list still tells a script apart.
		Expect(chatFiles(svc, alice, id)["build_document.py"].Kind).To(Equal(workfiles.Working))
	})
})

// regexpFind returns the first match of re in s, or "".
func regexpFind(s, re string) string {
	return regexp.MustCompile(re).FindString(s)
}
