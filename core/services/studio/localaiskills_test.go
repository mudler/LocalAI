package studio_test

import (
	"context"
	"strings"

	"github.com/mudler/LocalAI/core/services/localaitools"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/adminskills"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/builtins"
	"github.com/mudler/LocalAI/core/services/studio/builtinskills"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/session"
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
	Expect(f.SyncBuiltin(context.Background(), adminskills.With(builtinskills.FS()))).To(Succeed())
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

// promptAssistant is the admin assistant with the real prompts of
// localaitools. Without CompactSystemPrompt it stands for an assistant that
// has only the full prompt.
type promptAssistant struct{ *fakeAssistant }

func (p promptAssistant) SystemPrompt() string {
	return localaitools.SystemPrompt(localaitools.Options{})
}

// compactAssistant has the compact prompt too.
type compactAssistant struct{ promptAssistant }

func (p compactAssistant) CompactSystemPrompt() string {
	return localaitools.CompactSystemPrompt(localaitools.Options{})
}

func recipeNames() []string {
	var out []string
	for _, r := range localaitools.Recipes() {
		out = append(out, r.Name)
	}
	return out
}

// adminTurnPromptBudget caps the whole prompt of a turn of an administrator's
// chat that has the admin group: the compact assistant prompt, the skills
// index and the lines every chat gets. Every turn pays it. It is the measured
// size (8,656 bytes) plus about 10%.
const adminTurnPromptBudget = 9500

var _ = Describe("the admin recipes as skills", func() {
	ctx := context.Background()

	rig := func(script func(workspace.Actor, string) []studiotest.ToolStep, compact bool) *skillRigT {
		GinkgoHelper()
		f := newFiles()
		Expect(f.SyncBuiltin(ctx, adminskills.With(builtinskills.FS()))).To(Succeed())
		fa := &fakeAssistant{enabled: true}
		var a studio.Assistant = promptAssistant{fakeAssistant: fa}
		if compact {
			a = compactAssistant{promptAssistant{fakeAssistant: fa}}
		}
		r := newLocalAIRig(script, func(d *studio.Deps) { d.Files, d.Assistant = f, a })
		return &skillRigT{localaiRig: r, f: f}
	}
	turn := func(r *skillRigT, user, id string) string {
		GinkgoHelper()
		sent(r.svc, user, id, "hi")
		idle(r.svc, user, id)
		return r.h.TurnPrompt(id)
	}
	adminChat := func(r *skillRigT) string {
		id := create(r.svc, "root")
		r.adminOnFor("root")(id)
		return id
	}
	listsNone := func(prompt string) {
		GinkgoHelper()
		for _, n := range recipeNames() {
			Expect(prompt).NotTo(ContainSubstring(n), n)
		}
	}

	It("lists every recipe for an administrator's chat with the admin group on, and loads each by name", func() {
		names := recipeNames()
		Expect(names).NotTo(BeEmpty())
		load := func(_ workspace.Actor, text string) []studiotest.ToolStep {
			if text != "load" {
				return []studiotest.ToolStep{{Say: "ok"}}
			}
			var steps []studiotest.ToolStep
			for _, n := range names {
				steps = append(steps, studiotest.ToolStep{Tool: "load_skill", Args: `{"name":"` + n + `"}`})
			}
			return append(steps, studiotest.ToolStep{Say: "ok"})
		}
		r := rig(load, true)
		id := adminChat(r)
		sent(r.svc, "root", id, "load")
		idle(r.svc, "root", id)
		prompt := r.h.TurnPrompt(id)
		for _, n := range names {
			Expect(prompt).To(ContainSubstring(indexLine(n)), n)
		}
		results := r.h.ResultsFor("load_skill")
		Expect(results).To(HaveLen(len(names)))
		for i, rc := range localaitools.Recipes() {
			Expect(results[i].Err).NotTo(HaveOccurred(), rc.Name)
			Expect(results[i].Out).To(ContainSubstring(strings.TrimSpace(rc.Body)), rc.Name)
		}
	})

	It("sends the compact prompt, and the recipe text only when a skill is loaded", func() {
		r := rig(sayOK, true)
		prompt := turn(r, "root", adminChat(r))
		Expect(prompt).To(ContainSubstring("Confirm before mutating"))
		Expect(prompt).To(ContainSubstring("`load_skill`"))
		Expect(prompt).NotTo(ContainSubstring("# section: 20_tools"))
		for _, rc := range localaitools.Recipes() {
			Expect(prompt).NotTo(ContainSubstring(strings.TrimSpace(rc.Body)), rc.Name)
		}
	})

	It("keeps the full prompt, with every recipe inline, for an assistant that has no compact prompt", func() {
		r := rig(sayOK, false)
		prompt := turn(r, "root", adminChat(r))
		for _, rc := range localaitools.Recipes() {
			Expect(prompt).To(ContainSubstring(strings.TrimSpace(rc.Body)), rc.Name)
		}
	})

	It("names the real load tool in the compact prompt", func() {
		Expect(localaitools.CompactSystemPrompt(localaitools.Options{})).To(ContainSubstring("`" + skills.LoadTool + "`"))
	})

	It("drops the prompt of an administrator's chat by more than 60% and keeps it under its budget", func() {
		fullRig, compactRig := rig(sayOK, false), rig(sayOK, true)
		full := turn(fullRig, "root", adminChat(fullRig))
		compact := turn(compactRig, "root", adminChat(compactRig))
		GinkgoWriter.Printf("admin turn prompt: full %d bytes, compact %d bytes\n", len(full), len(compact))
		Expect(len(compact)).To(BeNumerically("<=", adminTurnPromptBudget),
			"every turn of an admin chat pays this prompt; move text into a recipe skill")
		Expect(len(compact)*10).To(BeNumerically("<", len(full)*4), "the prompt must be at least 60%% smaller")
	})

	It("lists no recipe until the chat switches the admin group on", func() {
		r := rig(sayOK, true)
		listsNone(turn(r, "root", create(r.svc, "root")))
	})

	It("lists no recipe when the chat switches the admin group on and off again, or the instance setting is off", func() {
		r := rig(sayOK, true)
		id := create(r.svc, "root")
		Expect(r.svc.SetBuiltinsOn(ctx, "root", id, []string{builtins.GroupLocalAIAdmin})).To(Succeed())
		Expect(r.svc.SetBuiltinsOn(ctx, "root", id, []string{})).To(Succeed())
		listsNone(turn(r, "root", id))

		r2 := rig(sayOK, true)
		r2.set(func() { r2.adminOn = false })
		id2 := create(r2.svc, "root")
		Expect(r2.svc.SetBuiltinsOn(ctx, "root", id2, []string{builtins.GroupLocalAIAdmin})).To(Succeed())
		listsNone(turn(r2, "root", id2))
	})

	It("never lists a recipe, its name or its text, for a user who is not an administrator", func() {
		r := rig(sayOK, true)
		id := create(r.svc, bob)
		Expect(r.svc.SetBuiltinsOn(ctx, bob, id, []string{builtins.GroupLocalAIAdmin})).To(Succeed())
		prompt := turn(r, bob, id)
		listsNone(prompt)
		Expect(prompt).NotTo(ContainSubstring("Confirm before mutating"))
		list, _, err := r.svc.Skills(ctx, bob, id)
		Expect(err).NotTo(HaveOccurred())
		for _, sk := range list {
			Expect(recipeNames()).NotTo(ContainElement(sk.Name))
		}
		list, _, err = r.svc.Skills(ctx, bob, "")
		Expect(err).NotTo(HaveOccurred())
		for _, sk := range list {
			Expect(recipeNames()).NotTo(ContainElement(sk.Name))
		}
		rootList, _, err := r.svc.Skills(ctx, "root", "")
		Expect(err).NotTo(HaveOccurred())
		var have []string
		for _, sk := range rootList {
			have = append(have, sk.Name)
		}
		Expect(have).To(ContainElements(recipeNames()))
	})

	It("lists no recipe in a sub-agent, even when its parent has the admin group", func() {
		r := rig(func(_ workspace.Actor, text string) []studiotest.ToolStep {
			if text == "go" {
				return []studiotest.ToolStep{{Tool: "spawn_agent", Args: `{"task":"look","wait":true}`}, {Say: "done"}}
			}
			return []studiotest.ToolStep{{Say: "seen"}}
		}, true)
		id := adminChat(r)
		sent(r.svc, "root", id, "go")
		idle(r.svc, "root", id)
		Expect(r.h.TurnPrompt(id)).To(ContainSubstring(indexLine(recipeNames()[0])))
		kids := childrenOf(r.svc, "root", id)
		Expect(kids).NotTo(BeEmpty())
		for _, k := range kids {
			listsNone(r.h.TurnPrompt(k.ID))
		}
	})

	It("lists no recipe in a messaging chat or a run that nobody watches", func() {
		for _, origin := range []string{session.OriginConnector, session.OriginTrigger, session.OriginJob} {
			r := rig(sayOK, true)
			saved, err := r.svc.SaveAgent(ctx, "root", agents.Agent{Name: "Auto", Model: "m", BuiltinsOn: []string{builtins.GroupLocalAIAdmin}})
			Expect(err).NotTo(HaveOccurred())
			rec, err := r.svc.CreateAs(ctx, "root", studio.CreateInput{Model: "m", AgentID: saved.ID}, studio.Origin{Kind: origin, ID: "x"})
			Expect(err).NotTo(HaveOccurred(), origin)
			prompt := turn(r, "root", rec.ID)
			listsNone(prompt)
			Expect(prompt).To(ContainSubstring(indexLine("localai")), origin+": the client skill stays")
		}
	})

	It("lists the recipes for a saved agent that has the admin group, owned by an administrator", func() {
		r := rig(sayOK, true)
		saved, err := r.svc.SaveAgent(ctx, "root", agents.Agent{Name: "Ops", Model: "m", BuiltinsOn: []string{builtins.GroupLocalAIAdmin}})
		Expect(err).NotTo(HaveOccurred())
		rec, err := r.svc.Create(ctx, "root", studio.CreateInput{Model: "m", AgentID: saved.ID})
		Expect(err).NotTo(HaveOccurred())
		Expect(turn(r, "root", rec.ID)).To(ContainSubstring(indexLine(recipeNames()[0])))
	})
})
