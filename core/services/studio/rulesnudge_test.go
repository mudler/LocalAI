package studio_test

import (
	"context"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/triggers"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const rulesNote = "Rules for th"

func pointer(rel string) string { return "/workspace/home" + rel + "/AGENTS.md" }

func newRulesChat(script map[string][]studiotest.ToolStep) (*studio.Service, *studiotest.ToolHarness, *files.Service) {
	f := newFiles()
	svc, h := newToolService(func(_ workspace.Actor, text string) []studiotest.ToolStep {
		// The turn note comes before the user's words.
		for k, s := range script {
			if text == k || strings.HasSuffix(text, "\n\n"+k) {
				return s
			}
		}
		return []studiotest.ToolStep{{Say: "ok"}}
	}, func(d *studio.Deps) { d.Files = f })
	return svc, h, f
}

func outsOf(h *studiotest.ToolHarness, chat string) []string {
	var out []string
	for _, r := range h.Results() {
		if r.Chat == chat {
			Expect(r.Err).NotTo(HaveOccurred(), r.Tool)
			out = append(out, r.Out)
		}
	}
	return out
}

var _ = Describe("pointing the model at AGENTS.md", func() {
	It("adds nothing when there is no AGENTS.md", func() {
		svc, h, f := newRulesChat(map[string][]studiotest.ToolStep{
			"read": {{Tool: "read_file", Args: `{"path":"/notes/a.txt"}`}, {Say: "ok"}},
		})
		writeHome(f, alice, "/notes/a.txt", "hello")
		id := create(svc, alice)
		turnWith(svc, id, "read")
		Expect(h.Sent(id)[0]).NotTo(ContainSubstring("AGENTS.md"))
		Expect(outsOf(h, id)).To(Equal([]string{"hello"}))
	})

	It("names the home's AGENTS.md in the turn note, never its text, and not in the prompt", func() {
		svc, h, f := newRulesChat(nil)
		writeHome(f, alice, "/AGENTS.md", "SECRET RULE TEXT")
		id := create(svc, alice)
		turnWith(svc, id, "hi")
		turnWith(svc, id, "again")
		got := h.Sent(id)
		line := "Rules for this folder are in " + pointer("") + ". Read that file before you start."
		Expect(got[0]).To(ContainSubstring(line))
		Expect(got[1]).To(ContainSubstring(line), "each turn")
		Expect(got[0]).NotTo(ContainSubstring("SECRET RULE TEXT"))
		Expect(h.TurnPrompt(id)).NotTo(ContainSubstring("AGENTS.md"))
	})

	It("names the chat's own folder first, then the home", func() {
		svc, h, f := newRulesChat(nil)
		writeHome(f, alice, "/AGENTS.md", "home rules")
		id := create(svc, alice)
		_, err := svc.SendWithFiles(context.Background(), alice, id, "look", []session.Upload{upload("a.txt", "x")})
		Expect(err).NotTo(HaveOccurred())
		idle(svc, alice, id)
		folder := chatFolder(svc)
		writeHome(f, alice, folder+"/AGENTS.md", "chat rules")
		turnWith(svc, id, "again")
		got := h.Sent(id)
		Expect(got[1]).To(ContainSubstring("Rules for these folders are in " + pointer(folder) + " and " + pointer("") + ". Read those files before you start."))
	})

	It("adds the line to the first file call in a folder with rules, nearest first, two at most, once a turn", func() {
		svc, h, f := newRulesChat(map[string][]studiotest.ToolStep{
			"work": {
				{Tool: "read_file", Args: `{"path":"/workspace/home/a/b/c/x.txt"}`},
				{Tool: "read_file", Args: `{"path":"/a/b/c/y.txt"}`},
				{Tool: "list_files", Args: `{"path":"/a"}`},
				{Say: "ok"},
			},
		})
		for _, p := range []string{"/AGENTS.md", "/a/AGENTS.md", "/a/b/AGENTS.md", "/a/b/c/AGENTS.md"} {
			writeHome(f, alice, p, "rules")
		}
		writeHome(f, alice, "/a/b/c/x.txt", "X")
		writeHome(f, alice, "/a/b/c/y.txt", "Y")
		id := create(svc, alice)
		turnWith(svc, id, "work")
		Expect(h.Sent(id)[0]).To(ContainSubstring(pointer("")), "the home goes in the note")
		outs := outsOf(h, id)
		Expect(outs).To(HaveLen(3))
		Expect(outs[0]).To(Equal("X\n\nRules for these folders are in " + pointer("/a/b/c") + " and " + pointer("/a/b") + ". Read those files before you start."))
		// The third folder with rules, which the cap left out, comes with the
		// next call; the folders named already do not come again.
		Expect(outs[1]).To(Equal("Y\n\nRules for this folder are in " + pointer("/a") + ". Read that file before you start."))
		Expect(outs[2]).NotTo(ContainSubstring(rulesNote), "every folder was named this turn")

		By("naming them again in the next turn")
		turnWith(svc, id, "work")
		outs = outsOf(h, id)
		Expect(outs[3]).To(ContainSubstring(pointer("/a/b/c")))
	})

	It("counts a read of AGENTS.md as being pointed at it", func() {
		svc, h, f := newRulesChat(map[string][]studiotest.ToolStep{
			"work": {
				{Tool: "read_file", Args: `{"path":"/docs/AGENTS.md"}`},
				{Tool: "write_file", Args: `{"path":"/docs/out.md","content":"done"}`},
				{Say: "ok"},
			},
		})
		writeHome(f, alice, "/docs/AGENTS.md", "Write in English.")
		id := create(svc, alice)
		turnWith(svc, id, "work")
		outs := outsOf(h, id)
		Expect(outs[0]).To(Equal("Write in English."))
		Expect(outs[1]).NotTo(ContainSubstring("AGENTS.md"))
	})

	It("does not point a trigger run at the rules its first message holds", func() {
		sh := newShared()
		ctx := context.Background()
		Expect(sh.files.EnsureHome(ctx, alice)).To(Succeed())
		act := sh.files.Actor(alice)
		folder, _ := sh.files.Abs(alice, "/Contracts")
		_, err := sh.files.Store().Mkdir(ctx, act, folder)
		Expect(err).NotTo(HaveOccurred())
		home, _ := sh.files.Abs(alice, "/AGENTS.md")
		for _, p := range []string{home, folder + "/" + triggers.RulesFile} {
			_, err = sh.files.Store().Put(ctx, act, p, strings.NewReader("Be brief."), workspace.PutOptions{})
			Expect(err).NotTo(HaveOccurred())
		}
		r := sh.rig(func(workspace.Actor, string) []studiotest.ToolStep {
			return []studiotest.ToolStep{{Tool: "read_file", Args: `{"path":"/Contracts/a.txt"}`}, {Say: "ok"}}
		}, studio.TriggerOptions{})
		tr := r.schedule(studio.TriggerInput{Kind: triggers.KindFile, Folder: "/Contracts", Quiet: time.Second, Name: "Contracts"})
		r.pass()
		_, err = sh.files.Store().Put(ctx, act, folder+"/a.txt", strings.NewReader("hello"), workspace.PutOptions{})
		Expect(err).NotTo(HaveOccurred())
		r.pass()
		sh.clock.Advance(2 * time.Second)
		r.pass()
		run := r.waitEnded(tr.ID)
		chat := r.chatOf(run).ID
		sent := r.h.Sent(chat)
		Expect(sent).To(HaveLen(1))
		Expect(sent[0]).To(ContainSubstring("# Folder rules: /Contracts"))
		Expect(sent[0]).NotTo(ContainSubstring(rulesNote))
		reads := r.h.ResultsFor("read_file")
		Expect(reads).NotTo(BeEmpty())
		Expect(reads[len(reads)-1].Out).To(Equal("hello"))
	})
})
