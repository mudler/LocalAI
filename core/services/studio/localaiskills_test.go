package studio_test

import (
	"context"

	"github.com/mudler/LocalAI/core/services/localaitools"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/builtins"
	"github.com/mudler/LocalAI/core/services/studio/builtinskills"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/skills"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// skillRig is the LocalAI rig with files on and the built-in skills synced,
// so the chats have the skills layer.
func skillRig(script func(workspace.Actor, string) []studiotest.ToolStep) *skillRigT {
	GinkgoHelper()
	f := newFiles()
	Expect(f.SyncBuiltin(context.Background(), builtinskills.FS())).To(Succeed())
	return &skillRigT{localaiRig: newLocalAIRig(script, func(d *studio.Deps) { d.Files = f }), f: f}
}

type skillRigT struct {
	*localaiRig
	f *files.Service
}

// indexLine is the line of a skill in the index of the prompt.
func indexLine(name string) string { return "- " + name + ": " }

var _ = Describe("the localai skill in a chat", func() {
	ctx := context.Background()

	loadScript := func(name string) func(workspace.Actor, string) []studiotest.ToolStep {
		return func(_ workspace.Actor, text string) []studiotest.ToolStep {
			if text == "load" {
				return []studiotest.ToolStep{{Tool: "load_skill", Args: `{"name":"` + name + `"}`}, {Say: "ok"}}
			}
			return []studiotest.ToolStep{{Say: "ok"}}
		}
	}

	It("is listed and loads by name while the chat has the client group", func() {
		r := skillRig(loadScript("localai"))
		id := create(r.svc, bob)
		sent(r.svc, bob, id, "load")
		idle(r.svc, bob, id)
		Expect(r.h.TurnPrompt(id)).To(ContainSubstring(indexLine("localai")))
		loaded := r.h.ResultsFor("load_skill")
		Expect(loaded).To(HaveLen(1))
		Expect(loaded[0].Err).NotTo(HaveOccurred())
		Expect(loaded[0].Out).To(ContainSubstring("# LocalAI tools"))
		Expect(loaded[0].Out).To(ContainSubstring("`voice_profile`"))
	})

	It("is not listed, and not loadable, when the chat switches the client group off", func() {
		r := skillRig(loadScript("localai"))
		id := create(r.svc, bob)
		Expect(r.svc.SetBuiltinsOff(ctx, bob, id, []string{builtins.GroupLocalAIClient})).To(Succeed())
		sent(r.svc, bob, id, "load")
		idle(r.svc, bob, id)
		Expect(r.h.TurnPrompt(id)).NotTo(ContainSubstring(indexLine("localai")))
		for _, res := range r.h.ResultsFor("load_skill") {
			Expect(res.Out).NotTo(ContainSubstring("# LocalAI tools"))
		}
	})

	It("is not listed when the instance setting turns the group off, or the account holds none of the features", func() {
		r := skillRig(sayOK)
		r.set(func() { r.clientOn = false })
		id := create(r.svc, bob)
		sent(r.svc, bob, id, "hi")
		idle(r.svc, bob, id)
		Expect(r.h.TurnPrompt(id)).NotTo(ContainSubstring(indexLine("localai")))

		r2 := skillRig(sayOK)
		r2.set(func() {
			for _, spec := range localaitools.ClientTools {
				r2.deniedFeats[bob] = append(r2.deniedFeats[bob], spec.Feature)
			}
		})
		id2 := create(r2.svc, bob)
		sent(r2.svc, bob, id2, "hi")
		idle(r2.svc, bob, id2)
		Expect(r2.h.TurnPrompt(id2)).NotTo(ContainSubstring(indexLine("localai")))
	})

	It("is listed for a saved agent that has the client group, and not for one that switched it off", func() {
		r := skillRig(sayOK)
		plain, err := r.svc.SaveAgent(ctx, bob, agents.Agent{Name: "Plain", Model: "m"})
		Expect(err).NotTo(HaveOccurred())
		narrow, err := r.svc.SaveAgent(ctx, bob, agents.Agent{Name: "Narrow", Model: "m", BuiltinsOff: []string{builtins.GroupLocalAIClient}})
		Expect(err).NotTo(HaveOccurred())
		for agent, want := range map[string]bool{plain.ID: true, narrow.ID: false} {
			rec, err := r.svc.Create(ctx, bob, studio.CreateInput{Model: "m", AgentID: agent})
			Expect(err).NotTo(HaveOccurred())
			sent(r.svc, bob, rec.ID, "hi")
			idle(r.svc, bob, rec.ID)
			if want {
				Expect(r.h.TurnPrompt(rec.ID)).To(ContainSubstring(indexLine("localai")))
			} else {
				Expect(r.h.TurnPrompt(rec.ID)).NotTo(ContainSubstring(indexLine("localai")))
			}
		}
	})

	It("is a core skill that no add-on may take over, but a skill of the user's own layers shadows it", func() {
		r := skillRig(sayOK)
		writeSkill(r.f, bob, "/.skills/localai/SKILL.md", "localai", "my own notes on the tools")
		list, _, err := r.svc.Skills(ctx, bob, "")
		Expect(err).NotTo(HaveOccurred())
		var builtin, personal *skills.Skill
		for i := range list {
			switch {
			case list[i].Name == "localai" && list[i].Layer == skills.LayerBuiltin:
				builtin = &list[i]
			case list[i].Name == "localai" && list[i].Layer == skills.LayerPersonal:
				personal = &list[i]
			}
		}
		Expect(builtin).NotTo(BeNil())
		Expect(personal).NotTo(BeNil())
		Expect(personal.Shadowed).To(BeEmpty())
		Expect(builtin.Shadowed).To(Equal(string(skills.LayerPersonal)))
	})
})
