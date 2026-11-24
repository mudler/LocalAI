package studio

import (
	"context"
	"encoding/json"
	"errors"
	"testing/fstest"

	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("skill hints on failed reads", func() {
	It("names the right path for a built-in, an add-on and a user skill, for each read tool", func() {
		ctx := context.Background()
		b := &skillBroker{}
		svc, f := newSkillService(b)
		Expect(f.SyncBuiltin(ctx, fstest.MapFS{"docx/SKILL.md": {Data: md("docx", "Word files.")}})).To(Succeed())
		Expect(f.SyncAddonSkills(ctx, "notes", fstest.MapFS{"tips/SKILL.md": {Data: md("tips", "Tips.")}})).To(Succeed())
		b.set(AddonSkillSet{AddonID: "notes", Title: "Notes", Skills: []string{"tips"}, Running: true})
		Expect(f.WriteSkill(ctx, "alice", "personal", "mine", "My skill.", "Body.")).To(Succeed())

		// Each tool answers "not found", as the real ones do for a wrong path.
		missing := func(context.Context, json.RawMessage) (string, error) { return "", errors.New("not found") }
		var tools []session.Tool
		for _, n := range []string{"read_file", "read_document", "list_files"} {
			tools = append(tools, session.Tool{Name: n, Run: missing})
		}
		tools = svc.skillHintTools(f.Actor("alice"), tools)

		cases := map[string]string{
			"/workspace/home/.skills/docx/SKILL.md": "docx is at /workspace/shared-builtin-skills/docx/SKILL.md",
			"/workspace/home/.skills/tips":          "tips is at /workspace/shared-addon-skills/notes/tips/SKILL.md",
			"mine/SKILL.md":                         "mine is at /workspace/home/.skills/mine/SKILL.md",
		}
		for p, want := range cases {
			for _, tool := range tools {
				_, err := tool.Run(ctx, json.RawMessage(`{"path":"`+p+`"}`))
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(HavePrefix("not found. The skill "), "%s %s", tool.Name, p)
				Expect(err.Error()).To(ContainSubstring(want), "%s %s", tool.Name, p)
				Expect(err.Error()).To(HaveSuffix("load it with load_skill."))
			}
		}
		for _, tool := range tools {
			_, err := tool.Run(ctx, json.RawMessage(`{"path":"/workspace/home/.skills/nope/SKILL.md"}`))
			Expect(err).To(MatchError("not found"), "an unknown skill gets no suggestion")
		}
	})
})

var _ = Describe("skill place hint on writes", func() {
	var skillActor workspace.Actor
	BeforeEach(func() { _, f := newSkillService(&skillBroker{}); skillActor = f.Actor("alice") })
	ok := func(context.Context, json.RawMessage) (string, error) { return "written", nil }
	run := func(svc *Service, name, args string) string {
		tools := svc.skillHintTools(skillActor, []session.Tool{{Name: name, Run: ok}})
		out, err := tools[0].Run(context.Background(), json.RawMessage(args))
		Expect(err).NotTo(HaveOccurred())
		return out
	}

	It("tells the model that a SKILL.md outside a skills folder is not loaded, and where it goes", func() {
		svc, _ := newSkillService(&skillBroker{})
		// The path gemma4 wrote in 7 of 8 runs: the dot of .skills dropped.
		out := run(svc, "write_file", `{"path":"/workspace/home/skills/rechnung-pruefen/SKILL.md","content":"x"}`)
		Expect(out).To(HavePrefix("written\n\nNote: /workspace/home/skills/rechnung-pruefen/SKILL.md is not in a .skills folder"))
		Expect(out).To(ContainSubstring("/workspace/home/.skills/rechnung-pruefen/SKILL.md: move it there with move_file."))
		out = run(svc, "move_file", `{"from":"/workspace/home/a.md","to":"/workspace/home/wochenbericht/SKILL.md"}`)
		Expect(out).To(ContainSubstring("/workspace/home/.skills/wochenbericht/SKILL.md"))
	})

	It("leaves a SKILL.md in a skills folder, other files and failed calls alone", func() {
		svc, _ := newSkillService(&skillBroker{})
		for _, args := range []string{
			`{"path":"/workspace/home/.skills/mine/SKILL.md"}`,
			`{"path":"/workspace/shared-Reports/.skills/monthly-report/SKILL.md"}`,
			`{"path":"/workspace/home/.skills/mine/reference/SKILL.md"}`,
			`{"path":"/workspace/home/skills/notes.md"}`,
			`{"path":"SKILL.md"}`,
			`{"path":"/workspace/home/Projects/acme/skills/kickoff/SKILL.md"}`,
			`{"path":"/workspace/home/LocalAI/agents/a1/skills/triage/SKILL.md"}`,
		} {
			Expect(run(svc, "write_file", args)).To(Equal("written"), args)
		}
		fail := func(context.Context, json.RawMessage) (string, error) { return "", errors.New("denied") }
		tools := svc.skillHintTools(skillActor, []session.Tool{{Name: "write_file", Run: fail}})
		_, err := tools[0].Run(context.Background(), json.RawMessage(`{"path":"/workspace/home/skills/x/SKILL.md"}`))
		Expect(err).To(MatchError("denied"))
	})
})
