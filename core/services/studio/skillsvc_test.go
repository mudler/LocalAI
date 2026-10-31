package studio_test

import (
	"context"
	"fmt"
	"strings"
	"testing/fstest"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("skillsvc", func() {
	It("skill switches are checked and kept per chat", func() {
		svc := newService(studiotest.NewGateHarness())
		id := create(svc, alice)
		ctx := context.Background()

		Expect(svc.SetSkillsOff(ctx, alice, id, []string{"review", "notes", "review"})).To(Succeed())
		rec, err := svc.Get(ctx, alice, id)
		Expect(err).NotTo(HaveOccurred(), "SkillsOff = %v, %v, want review and notes once", rec.SkillsOff, err)
		Expect(rec.SkillsOff).To(HaveLen(2), "SkillsOff = %v, %v, want review and notes once", rec.SkillsOff, err)
		for _, bad := range [][]string{{"Not Valid"}, {"../x"}, {""}} {
			Expect(svc.SetSkillsOff(ctx, alice, id, bad)).To(MatchError(studio.ErrBadSkillName))
		}
		many := make([]string, 201)
		for i := range many {
			many[i] = fmt.Sprintf("skill-%d", i)
		}
		Expect(svc.SetSkillsOff(ctx, alice, id, many)).To(MatchError(studio.ErrBadSkillName))
		Expect(svc.SetSkillsOff(ctx, "bob", id, []string{"x"})).To(MatchError(session.ErrNotFound))
		rec, _ = svc.Get(ctx, alice, id)
		Expect(rec.SkillsOff).To(HaveLen(2), "a refused change altered the switches: %v", rec.SkillsOff)
	})
})

// A chat service with files on and a tool harness that reports the turn prompt.
func newSkillChat() (*studio.Service, *studiotest.ToolHarness, *files.Service) {
	f := newFiles()
	svc, h := newToolService(func(a workspace.Actor, text string) []studiotest.ToolStep {
		if text == "go" {
			return []studiotest.ToolStep{{Tool: "spawn_agent", Args: `{"task":"child job","wait":true}`}, {Say: "done"}}
		}
		return []studiotest.ToolStep{{Say: "ok"}}
	}, func(d *studio.Deps) { d.Files = f })
	return svc, h, f
}

func writeSkill(f *files.Service, who, p, name, desc string) {
	body := "---\nname: " + name + "\ndescription: " + desc + "\n---\nBody.\n"
	a := workspace.Actor{Principal: files.AdminPrincipal}
	if who != "" {
		Expect(f.EnsureHome(context.Background(), who)).To(Succeed())
		a = f.Actor(who)
		p, _ = f.Abs(who, p)
	} else {
		Expect(f.EnsureShared(context.Background())).To(Succeed())
	}
	_, err := f.Store().Put(context.Background(), a, p, strings.NewReader(body), workspace.PutOptions{Overwrite: true})
	Expect(err).NotTo(HaveOccurred())
}

var _ = Describe("skillsvc", func() {
	It("the model is told which skills it has", func() {
		svc, h, f := newSkillChat()
		writeSkill(f, alice, "/.skills/review/SKILL.md", "review", "check a change before it ships")
		writeSkill(f, "", "/skills/review/SKILL.md", "review", "the shared review skill")
		writeSkill(f, "", "/skills/house-style/SKILL.md", "house-style", "how we write")
		id := create(svc, alice)
		sent(svc, alice, id, "hello")
		idle(svc, alice, id)

		prompt := h.TurnPrompt(id)
		for _, want := range []string{"- review: check a change before it ships", "- house-style: how we write", "call the load_skill tool with its name"} {
			Expect(prompt).To(ContainSubstring(want), "the skill index lacks %q:\n%s", want, prompt)
		}
		Expect(prompt).NotTo(ContainSubstring("/workspace/home/.skills/review"), "the index names no path:\n%s", prompt)
		Expect(h.ToolNames(id)).To(ContainElement("load_skill"))
		Expect(prompt).NotTo(ContainSubstring("the shared review skill"), "the shared review skill shows although the personal one is nearer:\n%s", prompt)
	})

	It("a switched off skill leaves the index at the next turn", func() {
		svc, h, f := newSkillChat()
		writeSkill(f, alice, "/.skills/review/SKILL.md", "review", "check a change")
		writeSkill(f, alice, "/.skills/notes/SKILL.md", "notes", "keep notes")
		id := create(svc, alice)
		sent(svc, alice, id, "one")
		idle(svc, alice, id)
		p := h.TurnPrompt(id)
		Expect(p).To(ContainSubstring("review"), "first turn index:\n%s", p)
		Expect(p).To(ContainSubstring("notes"), "first turn index:\n%s", p)
		Expect(svc.SetSkillsOff(context.Background(), alice, id, []string{"review"})).To(Succeed())
		sent(svc, alice, id, "two")
		idle(svc, alice, id)
		p = h.TurnPrompt(id)
		Expect(p).NotTo(ContainSubstring("review"), "after switching review off, the index is:\n%s", p)
		Expect(p).To(ContainSubstring("notes"), "after switching review off, the index is:\n%s", p)
	})

	It("a broken skill is left out and the chat still works", func() {
		svc, h, f := newSkillChat()
		writeSkill(f, alice, "/.skills/good/SKILL.md", "good", "a good one")
		abs, _ := f.Abs(alice, "/.skills/broken/SKILL.md")
		_, err := f.Store().Put(context.Background(), f.Actor(alice), abs, strings.NewReader("no frontmatter at all"), workspace.PutOptions{})
		Expect(err).NotTo(HaveOccurred())
		id := create(svc, alice)
		sent(svc, alice, id, "hi")
		idle(svc, alice, id)
		p := h.TurnPrompt(id)
		Expect(p).To(ContainSubstring("good"), "index with one broken skill:\n%s", p)
		Expect(p).NotTo(ContainSubstring("broken"), "index with one broken skill:\n%s", p)
		Expect(turn(svc, alice, id).Last).To(Equal(store.TurnDone))
	})

	It("only the chat gets the index not its sub agents", func() {
		svc, h, f := newSkillChat()
		writeSkill(f, alice, "/.skills/review/SKILL.md", "review", "check a change")
		id := create(svc, alice)
		sent(svc, alice, id, "go")
		idle(svc, alice, id)
		kids := childrenOf(svc, alice, id)
		Expect(kids).To(HaveLen(1), "children = %d", len(kids))
		Expect(h.TurnPrompt(id)).NotTo(Equal(""), "chat prompt %q, sub-agent prompt %q: only the chat gets the index", h.TurnPrompt(id), h.TurnPrompt(kids[0].ID))
		// A sub-agent gets only the line that says it cannot run commands.
		Expect(h.TurnPrompt(kids[0].ID)).NotTo(ContainSubstring("Skills:"), "chat prompt %q, sub-agent prompt %q: only the chat gets the index", h.TurnPrompt(id), h.TurnPrompt(kids[0].ID))
		Expect(h.TurnPrompt(kids[0].ID)).NotTo(ContainSubstring("review"), "chat prompt %q, sub-agent prompt %q: only the chat gets the index", h.TurnPrompt(id), h.TurnPrompt(kids[0].ID))
	})

	It("another users skills never reach the index", func() {
		svc, h, f := newSkillChat()
		writeSkill(f, "bob", "/.skills/private/SKILL.md", "private", "bob's secret procedure")
		writeSkill(f, alice, "/.skills/mine/SKILL.md", "mine", "alice's own")
		id := create(svc, alice)
		sent(svc, alice, id, "hi")
		idle(svc, alice, id)
		p := h.TurnPrompt(id)
		Expect(p).NotTo(ContainSubstring("private"), "alice's index:\n%s", p)
		Expect(p).NotTo(ContainSubstring("secret"), "alice's index:\n%s", p)
		Expect(p).To(ContainSubstring("mine"), "alice's index:\n%s", p)
	})
})

var _ = Describe("skill path hints", func() {
	// readAll runs each tool call in a chat and returns the error text of each.
	readAll := func(calls []studiotest.ToolStep) []string {
		f := newFiles()
		svc, h := newToolService(func(_ workspace.Actor, text string) []studiotest.ToolStep {
			if text == "go" {
				return append(calls, studiotest.ToolStep{Say: "done"})
			}
			return []studiotest.ToolStep{{Say: "ok"}}
		}, func(d *studio.Deps) { d.Files = f })
		Expect(f.SyncBuiltin(context.Background(), fstest.MapFS{
			"docx/SKILL.md": {Data: []byte("---\nname: docx\ndescription: Word files.\n---\nBody.\n")},
		})).To(Succeed())
		writeSkill(f, alice, "/.skills/review/SKILL.md", "review", "check a change")
		id := create(svc, alice)
		sent(svc, alice, id, "go")
		idle(svc, alice, id)
		var out []string
		for _, r := range h.Results() {
			if r.Err != nil {
				out = append(out, r.Tool+": "+r.Err.Error())
			}
		}
		return out
	}

	It("says where a built-in skill is when read_file or list_files guess the wrong folder", func() {
		got := readAll([]studiotest.ToolStep{
			{Tool: "read_file", Args: `{"path":"/workspace/home/.skills/docx/SKILL.md"}`},
			{Tool: "list_files", Args: `{"path":"/workspace/home/skills/docx"}`},
		})
		want := "not found. The skill docx is at /workspace/shared-builtin-skills/docx/SKILL.md; load it with load_skill."
		Expect(got).To(Equal([]string{"read_file: " + want, "list_files: " + want}))
	})

	It("points to a user skill that was asked for in the wrong place", func() {
		got := readAll([]studiotest.ToolStep{{Tool: "read_file", Args: `{"path":"review/SKILL.md"}`}})
		Expect(got).To(Equal([]string{"read_file: not found. The skill review is at /workspace/home/.skills/review/SKILL.md; load it with load_skill."}))
	})

	It("gives no suggestion for a name that is not a listed skill", func() {
		got := readAll([]studiotest.ToolStep{
			{Tool: "read_file", Args: `{"path":"/workspace/home/.skills/nope/SKILL.md"}`},
			{Tool: "read_file", Args: `{"path":"/workspace/home/missing.txt"}`},
		})
		Expect(got).To(Equal([]string{"read_file: not found", "read_file: not found"}))
	})
})

var _ = Describe("load_skill in a chat", func() {
	It("is in the tool list only when the chat has skills", func() {
		svc, h, f := newSkillChat()
		id := create(svc, alice)
		sent(svc, alice, id, "hello")
		idle(svc, alice, id)
		Expect(h.ToolNames(id)).NotTo(ContainElement("load_skill"))
		Expect(h.TurnPrompt(id)).NotTo(ContainSubstring("Skills:"))

		writeSkill(f, alice, "/.skills/review/SKILL.md", "review", "check a change")
		sent(svc, alice, id, "again")
		idle(svc, alice, id)
		Expect(h.ToolNames(id)).To(ContainElement("load_skill"))
	})

	It("loads a skill by its name for the chat and for a sub-agent", func() {
		f := newFiles()
		svc, h := newToolService(func(_ workspace.Actor, text string) []studiotest.ToolStep {
			switch text {
			case "go":
				return []studiotest.ToolStep{
					{Tool: "load_skill", Args: `{"name":"review"}`},
					{Tool: "load_skill", Args: `{"name":"Review"}`},
					{Tool: "spawn_agent", Args: `{"task":"child job","wait":true}`},
					{Say: "done"},
				}
			case "child job":
				return []studiotest.ToolStep{{Tool: "load_skill", Args: `{"name":"review"}`}, {Say: "child done"}}
			}
			return []studiotest.ToolStep{{Say: "ok"}}
		}, func(d *studio.Deps) { d.Files = f })
		writeSkill(f, alice, "/.skills/review/SKILL.md", "review", "check a change")
		id := create(svc, alice)
		sent(svc, alice, id, "go")
		idle(svc, alice, id)
		got := h.ResultsFor("load_skill")
		Expect(got).To(HaveLen(3))
		Expect(got[0].Err).NotTo(HaveOccurred())
		Expect(got[0].Out).To(Equal("Body."))
		Expect(got[1].Err).To(MatchError(`unknown skill "Review". Did you mean review? The skills of this chat are: review.`))
		Expect(got[2].Chat).NotTo(Equal(id), "the sub-agent called it")
		Expect(got[2].Err).NotTo(HaveOccurred())
		Expect(got[2].Out).To(Equal("Body."))
	})
})
