package studio_test

import (
	"context"
	"encoding/json"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/builtins"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

const skillText = "---\nname: daily-note\ndescription: Write the daily note.\n---\nList what changed today.\n"

// grantCase is one builtin grant with a proposal that only it allows.
type grantCase struct {
	grant string
	args  map[string]any
	// plainOK is false where a chat without an agent cannot use the kind at all.
	plainOK bool
	file    string // the file or folder the proposal names
}

func pageFilesJSON() map[string]string {
	return map[string]string{"index.html": "<h1>Notes</h1>", "page.json": `{"title":"Notes"}`}
}

var grantCases = []grantCase{
	{"memory.write", map[string]any{"kind": "memory", "scope": "user", "target": "a.md", "content": "- a\n", "summary": "s"}, true, "Memory/a.md"},
	{"profile.write", map[string]any{"kind": "profile", "scope": "agent", "content": "Be brief.", "summary": "s"}, false, ""},
	{"skills.write", map[string]any{"kind": "skill", "scope": "user", "target": "daily-note", "content": skillText, "summary": "s"}, true, ".skills/daily-note/SKILL.md"},
	{"pages.write", map[string]any{"kind": "page", "scope": "user", "target": "notes", "files": pageFilesJSON(), "summary": "s"}, true, "pages/notes"},
	{"dashboard.write", map[string]any{"kind": "dashboard", "scope": "user", "content": `{"widgets":[{"id":"c","type":"chats","title":"Chats"}]}`, "summary": "s"}, true, "LocalAI/home.json"},
}

func argsJSON(m map[string]any) string {
	b, _ := json.Marshal(m)
	return string(b)
}

func grantScript(c grantCase) func(workspace.Actor, string) []studiotest.ToolStep {
	return func(_ workspace.Actor, text string) []studiotest.ToolStep {
		if text == "go" {
			return []studiotest.ToolStep{{Tool: "propose_change", Args: argsJSON(c.args)}, {Say: "ok"}}
		}
		return []studiotest.ToolStep{{Say: "ok"}}
	}
}

var _ = Describe("builtin grants", func() {
	ctx := context.Background()

	Describe("the declarations", func() {
		It("names every grant of the spec once, with a title and a description", func() {
			var names []string
			for _, g := range builtins.Grants() {
				names = append(names, g.Name)
				Expect(g.Title).NotTo(BeEmpty(), g.Name)
				Expect(g.Description).NotTo(BeEmpty(), g.Name)
			}
			Expect(names).To(Equal([]string{"memory.write", "profile.write", "skills.write", "pages.write", "dashboard.write",
				"triggers.propose", "triggers.run", "talkback.channels", "talkback.new_channel", "files.upload",
				"act.files", "act.addons", "act.send", "act.spend", "saved_agents"}))
		})

		It("marks the sensitive ones and shares the sensitivity of the contract", func() {
			for _, g := range builtins.Grants() {
				if contract.KnownCapability(g.Name) {
					Expect(g.Sensitive).To(Equal(contract.SensitiveCapability(g.Name)), g.Name)
				}
			}
			for _, n := range []string{"profile.write", "skills.write", "pages.write", "dashboard.write", "talkback.new_channel", "act.files", "act.addons", "act.send", "act.spend"} {
				Expect(builtins.SensitiveGrant(n)).To(BeTrue(), n)
			}
			Expect(builtins.SensitiveGrant("memory.write")).To(BeFalse())
		})

		It("turns on nothing by default: a new agent, a template and an import have no grants", func() {
			r := newTrigRig(func(workspace.Actor, string) []studiotest.ToolStep { return nil })
			ag, err := r.svc.SaveAgent(ctx, alice, agents.Agent{Name: "Plain", Model: "m"})
			Expect(err).NotTo(HaveOccurred())
			Expect(ag.Builtins).To(BeEmpty())
			for _, t := range agents.Templates() {
				made, err := r.svc.CreateAgentFromTemplate(ctx, alice, t.ID, "From "+t.ID)
				Expect(err).NotTo(HaveOccurred())
				Expect(made.Builtins).To(BeEmpty(), t.ID)
			}
			imp, err := r.svc.ImportAgent(ctx, alice, agents.Agent{Name: "Old file", Model: "m"}, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(imp.Builtins).To(BeEmpty())
		})
	})

	Describe("saving an agent", func() {
		It("refuses a grant that is not declared, keeps the grants of a write that omits them and clears them with an empty list", func() {
			r := newTrigRig(func(workspace.Actor, string) []studiotest.ToolStep { return nil })
			_, err := r.svc.SaveAgent(ctx, alice, agents.Agent{Name: "A", Model: "m", Builtins: []string{"memory.write", "root.shell"}})
			Expect(err).To(MatchError(studio.ErrUnknownGrant))
			Expect(err).To(MatchError(agents.ErrInvalid))
			ag, err := r.svc.SaveAgent(ctx, alice, agents.Agent{Name: "A", Model: "m", Builtins: []string{"skills.write", "memory.write", "skills.write"}})
			Expect(err).NotTo(HaveOccurred())
			Expect(ag.Builtins).To(Equal([]string{"skills.write", "memory.write"}))
			ag.Builtins = nil
			kept, err := r.svc.SaveAgent(ctx, alice, ag)
			Expect(err).NotTo(HaveOccurred())
			Expect(kept.Builtins).To(Equal([]string{"skills.write", "memory.write"}), "a client that does not know the field must not narrow or widen")
			ag.Builtins = []string{}
			cleared, err := r.svc.SaveAgent(ctx, alice, ag)
			Expect(err).NotTo(HaveOccurred())
			Expect(cleared.Builtins).To(BeEmpty())
		})

		It("drops a grant of a newer server in an import and turns saved_agents on as the opt-in group", func() {
			r := newTrigRig(func(workspace.Actor, string) []studiotest.ToolStep { return nil })
			ag, err := r.svc.ImportAgent(ctx, alice, agents.Agent{Name: "Imported", Model: "m", Builtins: []string{"memory.write", "future.grant", "saved_agents"}}, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(ag.Builtins).To(Equal([]string{"memory.write", "saved_agents"}))
			Expect(ag.BuiltinsOn).To(ContainElement("saved_agents"))
		})
	})

	Describe("the grants an agent holds", func() {
		It("are the granted ones the user may use now", func() {
			r := newTrigRig(func(workspace.Actor, string) []studiotest.ToolStep { return nil })
			ag, err := r.svc.SaveAgent(ctx, alice, agents.Agent{Name: "A", Model: "m", Builtins: []string{"memory.write", "triggers.propose", "act.files"}})
			Expect(err).NotTo(HaveOccurred())
			held := r.svc.AgentGrants(ctx, alice, ag.ID)
			Expect(held).To(ContainElements("memory.write", "triggers.propose", "act.files"))
			Expect(r.svc.AgentGrants(ctx, alice, "missing")).To(BeEmpty())
			Expect(r.svc.AgentGrants(ctx, "bob", ag.ID)).To(BeEmpty(), "another user's agent")
		})
	})

	Describe("propose_change by grant and by kind of chat", func() {
		for _, c := range grantCases {
			c := c
			Describe(c.grant, func() {
				It("exists in a chat of an agent that holds it, and the proposal waits for the owner", func() {
					r := changeShared(nil).rig(grantScript(c), studio.TriggerOptions{})
					ag, err := r.svc.SaveAgent(ctx, alice, agents.Agent{Name: "A", Model: "m", Builtins: []string{c.grant}})
					Expect(err).NotTo(HaveOccurred())
					rec, err := r.svc.Create(ctx, alice, studio.CreateInput{AgentID: ag.ID})
					Expect(err).NotTo(HaveOccurred())
					sent(r.svc, alice, rec.ID, "go")
					idle(r.svc, alice, rec.ID)
					res := r.h.ResultsFor("propose_change")
					Expect(res).To(HaveLen(1))
					Expect(res[0].Err).To(BeNil(), "%+v", res)
					p := onlyProposal(r)
					Expect(p.Status).To(Equal(contract.ProposalPending))
					Expect(p.Agent).To(Equal(ag.ID))
					if c.file != "" {
						Expect(p.Target).To(Equal(c.file))
						_, ok := readHome(r, c.file)
						Expect(ok).To(BeFalse(), "the proposal wrote the file")
					}
				})

				It("does not exist in a chat of an agent without it", func() {
					r := changeShared(nil).rig(grantScript(c), studio.TriggerOptions{})
					ag, err := r.svc.SaveAgent(ctx, alice, agents.Agent{Name: "A", Model: "m"})
					Expect(err).NotTo(HaveOccurred())
					rec, _ := r.svc.Create(ctx, alice, studio.CreateInput{AgentID: ag.ID})
					sent(r.svc, alice, rec.ID, "go")
					idle(r.svc, alice, rec.ID)
					Expect(r.h.ToolNames(rec.ID)).NotTo(ContainElement("propose_change"))
					list, _ := r.svc.Changes().List(ctx, alice)
					Expect(list).To(BeEmpty())
				})

				It("is not enough for the kinds of other grants", func() {
					r := changeShared(nil).rig(grantScript(c), studio.TriggerOptions{})
					other := "triggers.propose"
					if c.grant == other {
						other = "act.files"
					}
					// memory.write is the grant of the memory kind, so any other
					// case holds a different grant than the one the call needs.
					held := "memory.write"
					if c.grant == held {
						held = "skills.write"
					}
					ag, _ := r.svc.SaveAgent(ctx, alice, agents.Agent{Name: "A", Model: "m", Builtins: []string{held, other}})
					rec, _ := r.svc.Create(ctx, alice, studio.CreateInput{AgentID: ag.ID})
					sent(r.svc, alice, rec.ID, "go")
					idle(r.svc, alice, rec.ID)
					res := r.h.ResultsFor("propose_change")
					Expect(res).To(HaveLen(1))
					Expect(res[0].Err).To(HaveOccurred())
					Expect(res[0].Err.Error()).To(ContainSubstring("not given the right"))
					list, _ := r.svc.Changes().List(ctx, alice)
					Expect(list).To(BeEmpty())
				})

				It("exists in a chat without an agent, which holds what the user may use", func() {
					r := changeShared(nil).rig(grantScript(c), studio.TriggerOptions{})
					id := create(r.svc, alice)
					sent(r.svc, alice, id, "go")
					idle(r.svc, alice, id)
					res := r.h.ResultsFor("propose_change")
					Expect(res).To(HaveLen(1))
					if c.plainOK {
						Expect(res[0].Err).To(BeNil(), "%+v", res)
						Expect(onlyProposal(r).Kind).To(Equal(contract.ChangeKind(c.args["kind"].(string))))
					} else {
						Expect(res[0].Err).To(HaveOccurred())
					}
				})

				It("is never held by an unattended run of an agent that has it", func() {
					r := changeShared(nil).rig(grantScript(c), studio.TriggerOptions{})
					ag, _ := r.svc.SaveAgent(ctx, alice, agents.Agent{Name: "A", Model: "m", Builtins: []string{c.grant}})
					tr := r.schedule(studio.TriggerInput{AgentID: ag.ID})
					r.due()
					run := r.waitEnded(tr.ID)
					Expect(r.h.ToolNames(run.ChatID)).NotTo(ContainElement("propose_change"))
					Expect(r.h.ToolNames(run.ChatID)).NotTo(ContainElement("revert_change"))
				})

				It("is held by a connector chat of an agent that has it and refused when the source is a run that started by itself", func() {
					r := changeShared(nil).rig(grantScript(c), studio.TriggerOptions{})
					ag, _ := r.svc.SaveAgent(ctx, alice, agents.Agent{Name: "A", Model: "m", Builtins: []string{c.grant}})
					in := proposeInputOf(c, ag.ID)
					in.Source = contract.ChangeSource{Kind: session.OriginConnector, ID: "b1", Chat: "c1"}
					p, err := r.svc.Changes().Propose(ctx, in)
					Expect(err).NotTo(HaveOccurred())
					Expect(p.Source.Kind).To(Equal(session.OriginConnector))
					Expect(r.svc.Changes().Dismiss(ctx, alice, p.ID)).Error().NotTo(HaveOccurred())
					in.Source = contract.ChangeSource{Kind: session.OriginTrigger, ID: "t1", Chat: "c2"}
					_, err = r.svc.Changes().Propose(ctx, in)
					Expect(err).To(HaveOccurred())
					Expect(err.Error()).To(ContainSubstring("started by itself"))
				})
			})
		}
	})

	Describe("builtins.Chat.HoldsGrant", func() {
		DescribeTable("the tool-exists rule",
			func(c builtins.Chat, want bool) { Expect(c.HoldsGrant("skills.write")).To(Equal(want)) },
			Entry("a chat without an agent", builtins.Chat{}, true),
			Entry("an agent without the grant", builtins.Chat{Agent: true, Grants: []string{"memory.write"}}, false),
			Entry("an agent with the grant", builtins.Chat{Agent: true, Grants: []string{"skills.write"}}, true),
			Entry("a connector chat of an agent with the grant", builtins.Chat{Agent: true, Origin: session.OriginConnector, Grants: []string{"skills.write"}}, true),
			Entry("a trigger run of an agent with the grant", builtins.Chat{Agent: true, Origin: session.OriginTrigger, Grants: []string{"skills.write"}}, false),
			Entry("a job run", builtins.Chat{Agent: true, Origin: session.OriginJob, Grants: []string{"skills.write"}}, false),
			Entry("a sub-agent of an agent with the grant", builtins.Chat{Agent: true, SubAgent: true, Grants: []string{"skills.write"}}, false),
		)
	})

	Describe("the schema of propose_change", func() {
		It("lists the kinds the chat holds and no others", func() {
			r := changeShared(nil).rig(func(workspace.Actor, string) []studiotest.ToolStep { return nil }, studio.TriggerOptions{})
			ag, _ := r.svc.SaveAgent(ctx, alice, agents.Agent{Name: "A", Model: "m", Builtins: []string{"memory.write", "pages.write"}})
			rec, _ := r.svc.Create(ctx, alice, studio.CreateInput{AgentID: ag.ID})
			sent(r.svc, alice, rec.ID, "hi")
			idle(r.svc, alice, rec.ID)
			schema := r.h.ToolSchema(rec.ID, "propose_change")
			Expect(schema).To(ContainSubstring(`"enum":["memory","page"]`))
			Expect(strings.Contains(schema, `"skill"`)).To(BeFalse())
		})
	})
})

// proposeInputOf is the direct call of the case for an agent.
func proposeInputOf(c grantCase, agent string) studio.ProposeInput {
	in := studio.ProposeInput{User: alice, Agent: agent, Kind: c.args["kind"].(string), Scope: c.args["scope"].(string), Summary: "s"}
	in.Target, _ = c.args["target"].(string)
	in.Content, _ = c.args["content"].(string)
	if files, ok := c.args["files"].(map[string]string); ok {
		b, _ := json.Marshal(map[string]any{"files": files})
		in.Content = string(b)
	}
	return in
}
