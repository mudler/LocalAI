package studio

import (
	"context"
	"encoding/json"
	"errors"
	"testing/fstest"

	"github.com/mudler/LocalAI/core/services/studio/session"
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
				Expect(err.Error()).To(HaveSuffix("read it with read_file."))
			}
		}
		for _, tool := range tools {
			_, err := tool.Run(ctx, json.RawMessage(`{"path":"/workspace/home/.skills/nope/SKILL.md"}`))
			Expect(err).To(MatchError("not found"), "an unknown skill gets no suggestion")
		}
	})
})
