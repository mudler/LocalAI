package studio_test

import (
	"context"
	"slices"
	"strings"

	"github.com/mudler/LocalAI/core/config"
	localaitools "github.com/mudler/LocalAI/core/services/localaitools"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/builtins"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var bridgeNames = []string{"tool_search", "tool_describe", "tool_call"}

// withSearch sets the tool-search mode and the window of every model.
func withSearch(mode string, window int) func(*studio.Deps) {
	return func(d *studio.Deps) {
		d.ToolSearchMode = func() string { return mode }
		if window > 0 {
			d.ContextTokens = func(string) int { return window }
		}
	}
}

// scripted calls one tool through the bridge, then ends the turn.
func viaBridge(tool, args string) func(workspace.Actor, string) []studiotest.ToolStep {
	return func(workspace.Actor, string) []studiotest.ToolStep {
		return []studiotest.ToolStep{{Tool: tool, Args: args}, {Say: "done"}}
	}
}

var _ = Describe("the tool search of the chats", func() {
	ctx := context.Background()

	Describe("when the groups are deferred", func() {
		It("is decided by the setting and the window of the model", func() {
			cases := []struct {
				name   string
				opt    func(*studio.Deps)
				defers bool
			}{
				{"auto, small window", withSearch(config.ToolSearchAuto, 200), true},
				{"auto, large window", withSearch(config.ToolSearchAuto, 1<<20), false},
				{"auto, unknown window", withSearch(config.ToolSearchAuto, 0), false},
				{"on, large window", withSearch(config.ToolSearchOn, 1<<20), true},
				{"off, small window", withSearch(config.ToolSearchOff, 200), false},
				{"no setting wired", func(*studio.Deps) {}, false},
			}
			for _, c := range cases {
				r := newLocalAIRig(sayOK, c.opt)
				names := r.names(bob)
				if c.defers {
					Expect(names).To(ContainElements(bridgeNames), c.name)
					Expect(names).NotTo(ContainElement("generate_image"), c.name)
				} else {
					Expect(names).NotTo(ContainElements("tool_search"), c.name)
					Expect(names).To(ContainElement("generate_image"), c.name)
				}
			}
		})

		It("uses the window of the chat before the one of the model", func() {
			r := newLocalAIRig(sayOK, withSearch(config.ToolSearchAuto, 1<<20))
			rec, err := r.svc.Create(ctx, bob, studio.CreateInput{Model: "m"})
			Expect(err).NotTo(HaveOccurred())
			_, err = r.svc.Update(ctx, bob, rec.ID, studio.UpdateInput{Settings: &session.ChatSettings{ContextSize: 1024}})
			Expect(err).NotTo(HaveOccurred())
			Expect(r.turnNames(bob, rec.ID)).To(ContainElements(bridgeNames))
		})

		It("sends the same tools at every turn of a chat", func() {
			r := newLocalAIRig(sayOK, withSearch(config.ToolSearchAuto, 200))
			id := create(r.svc, bob)
			first := slices.Clone(r.turnNames(bob, id))
			for i := 0; i < 3; i++ {
				Expect(r.turnNames(bob, id)).To(ConsistOf(first))
			}
		})

		It("defers both groups together and keeps every core tool typed", func() {
			r := newLocalAIRig(sayOK, withSearch(config.ToolSearchOn, 0))
			names := r.names("root", r.adminOnFor("root"))
			Expect(names).To(ContainElements(bridgeNames))
			Expect(names).NotTo(ContainElements("install_model", "generate_image"))
			Expect(names).To(ContainElement("search"), "a tool of another source stays typed")
		})

		It("offers no bridge when no tool is deferred, even with the setting on", func() {
			r := newLocalAIRig(sayOK, withSearch(config.ToolSearchOn, 0))
			r.set(func() { r.clientOn = false })
			Expect(r.names(bob)).NotTo(ContainElement("tool_search"))
			// An account with none of the features has no client tool.
			r2 := newLocalAIRig(sayOK, withSearch(config.ToolSearchOn, 0))
			r2.set(func() {
				for _, s := range localaitools.ClientTools {
					r2.deniedFeats[bob] = append(r2.deniedFeats[bob], s.Feature)
				}
			})
			Expect(r2.names(bob)).NotTo(ContainElements(bridgeNames))
		})

		It("never offers a tool name twice", func() {
			r := newLocalAIRig(sayOK, withSearch(config.ToolSearchOn, 0))
			names := r.names("root", r.adminOnFor("root"))
			seen := map[string]bool{}
			for _, n := range names {
				Expect(seen[n]).To(BeFalse(), n)
				seen[n] = true
			}
		})

		It("tells the tools panel which groups load on demand", func() {
			r := newLocalAIRig(sayOK, withSearch(config.ToolSearchOn, 0))
			id := create(r.svc, bob)
			Expect(r.group(bob, id, builtins.GroupLocalAIClient).Deferred).To(BeTrue())
			r = newLocalAIRig(sayOK, withSearch(config.ToolSearchOff, 0))
			id = create(r.svc, bob)
			Expect(r.group(bob, id, builtins.GroupLocalAIClient).Deferred).To(BeFalse())
		})
	})

	Describe("tool_search and tool_describe", func() {
		It("list and describe only the tools the user could call", func() {
			// The admin tool is held back from a user: the search must not find it.
			r := newLocalAIRig(func(workspace.Actor, string) []studiotest.ToolStep {
				return []studiotest.ToolStep{
					{Tool: "tool_search", Args: `{"query":"install model"}`},
					{Tool: "tool_describe", Args: `{"name":"install_model"}`},
					{Tool: "tool_describe", Args: `{"name":"generate_image"}`},
					{Say: "done"},
				}
			}, withSearch(config.ToolSearchOn, 0))
			id := create(r.svc, bob)
			r.adminOnFor(bob)(id) // switched on, but bob is not an administrator
			sent(r.svc, bob, id, "go")
			idle(r.svc, bob, id)
			search := r.h.ResultsFor("tool_search")
			Expect(search).To(HaveLen(1))
			Expect(search[0].Out).NotTo(ContainSubstring("install_model"))
			desc := r.h.ResultsFor("tool_describe")
			Expect(desc).To(HaveLen(2))
			Expect(desc[0].Err).To(MatchError(ContainSubstring("no such tool")))
			Expect(desc[1].Err).NotTo(HaveOccurred())
			Expect(desc[1].Out).To(ContainSubstring(`"name":"generate_image"`))
			Expect(desc[1].Out).To(ContainSubstring(`"parameters"`))
		})

		It("find an admin tool for an administrator whose chat switched the group on", func() {
			r := newLocalAIRig(viaBridge("tool_search", `{"query":"install model"}`), withSearch(config.ToolSearchOn, 0))
			id := create(r.svc, "root")
			r.adminOnFor("root")(id)
			sent(r.svc, "root", id, "go")
			idle(r.svc, "root", id)
			Expect(r.h.ResultsFor("tool_search")[0].Out).To(ContainSubstring("install_model"))
		})
	})

	Describe("tool_call", func() {
		It("runs a client tool as the user of the chat", func() {
			r := newLocalAIRig(viaBridge("tool_call", `{"name":"speak","arguments":{"text":"hi"}}`), withSearch(config.ToolSearchOn, 0))
			id := create(r.svc, bob)
			sent(r.svc, bob, id, "go")
			idle(r.svc, bob, id)
			res := r.h.ResultsFor("tool_call")
			Expect(res).To(HaveLen(1))
			Expect(res[0].Err).NotTo(HaveOccurred())
			Expect(res[0].Out).To(Equal("made"))
			Expect(r.client.users).To(Equal([]string{bob}))
		})

		It("runs an admin tool for an administrator, as that administrator", func() {
			r := newLocalAIRig(viaBridge("tool_call", `{"name":"install_model","arguments":{"id":"x"}}`), withSearch(config.ToolSearchOn, 0))
			id := create(r.svc, "root")
			r.adminOnFor("root")(id)
			sent(r.svc, "root", id, "go")
			idle(r.svc, "root", id)
			res := r.h.ResultsFor("tool_call")
			Expect(res[0].Err).NotTo(HaveOccurred())
			Expect(res[0].Out).To(Equal("installed"))
			Expect(r.admin.users).To(Equal([]string{"root"}))
			Expect(r.admin.calls).To(HaveLen(1))
		})

		It("refuses an admin tool to a user, with the answer for a name that does not exist, and runs nothing", func() {
			var out []string
			for _, name := range []string{"install_model", "no_such_tool"} {
				r := newLocalAIRig(viaBridge("tool_call", `{"name":"`+name+`","arguments":{}}`), withSearch(config.ToolSearchOn, 0))
				id := create(r.svc, bob)
				r.adminOnFor(bob)(id)
				sent(r.svc, bob, id, "go")
				idle(r.svc, bob, id)
				res := r.h.ResultsFor("tool_call")
				Expect(res).To(HaveLen(1))
				Expect(res[0].Err).To(MatchError(ContainSubstring("no such tool")))
				Expect(r.admin.calls).To(BeEmpty(), "nothing runs")
				out = append(out, strings.ReplaceAll(res[0].Err.Error(), name, "X"))
			}
			Expect(out[0]).To(Equal(out[1]), "the refusal does not tell the tool exists")
		})

		It("refuses an admin tool while the chat has the admin group off", func() {
			r := newLocalAIRig(viaBridge("tool_call", `{"name":"install_model","arguments":{}}`), withSearch(config.ToolSearchOn, 0))
			id := create(r.svc, "root") // opt-in group, not switched on
			sent(r.svc, "root", id, "go")
			idle(r.svc, "root", id)
			Expect(r.h.ResultsFor("tool_call")[0].Err).To(MatchError(ContainSubstring("no such tool")))
			Expect(r.admin.calls).To(BeEmpty())
		})

		It("refuses a client tool of a group the chat switched off", func() {
			r := newLocalAIRig(viaBridge("tool_call", `{"name":"generate_image","arguments":{"prompt":"x"}}`), withSearch(config.ToolSearchOn, 0))
			id := create(r.svc, "root")
			r.adminOnFor("root")(id) // keeps the bridge present through the admin group
			Expect(r.svc.SetBuiltinsOff(ctx, "root", id, []string{builtins.GroupLocalAIClient})).To(Succeed())
			sent(r.svc, "root", id, "go")
			idle(r.svc, "root", id)
			Expect(r.h.ResultsFor("tool_call")[0].Err).To(MatchError(ContainSubstring("no such tool")))
			Expect(r.client.callCount()).To(Equal(0))
		})

		It("refuses a tool the saved agent's ceiling removed", func() {
			r := newLocalAIRig(viaBridge("tool_call", `{"name":"generate_image","arguments":{"prompt":"x"}}`), withSearch(config.ToolSearchOn, 0))
			saved, err := r.svc.SaveAgent(ctx, "root", agents.Agent{Name: "Narrow", Model: "m",
				BuiltinsOff: []string{builtins.GroupLocalAIClient}, BuiltinsOn: []string{builtins.GroupLocalAIAdmin}})
			Expect(err).NotTo(HaveOccurred())
			rec, err := r.svc.Create(ctx, "root", studio.CreateInput{Model: "m", AgentID: saved.ID})
			Expect(err).NotTo(HaveOccurred())
			sent(r.svc, "root", rec.ID, "go")
			idle(r.svc, "root", rec.ID)
			Expect(r.h.ResultsFor("tool_call")[0].Err).To(MatchError(ContainSubstring("no such tool")))
			Expect(r.client.callCount()).To(Equal(0))
		})

		It("refuses a client tool the account has no permission for", func() {
			r := newLocalAIRig(viaBridge("tool_call", `{"name":"generate_image","arguments":{"prompt":"x"}}`), withSearch(config.ToolSearchOn, 0))
			r.set(func() { r.deniedFeats[bob] = []string{"images"} })
			id := create(r.svc, bob)
			sent(r.svc, bob, id, "go")
			idle(r.svc, bob, id)
			Expect(r.h.ResultsFor("tool_call")[0].Err).To(MatchError(ContainSubstring("no such tool")))
			Expect(r.client.callCount()).To(Equal(0))
		})

		Describe("in a run that nobody watches", func() {
			run := func(r *localaiRig, ag agents.Agent) {
				GinkgoHelper()
				saved, err := r.svc.SaveAgent(ctx, "root", ag)
				Expect(err).NotTo(HaveOccurred())
				rec, err := r.svc.CreateAs(ctx, "root", studio.CreateInput{Model: "m", AgentID: saved.ID}, studio.Origin{Kind: session.OriginJob, ID: "x"})
				Expect(err).NotTo(HaveOccurred())
				sent(r.svc, "root", rec.ID, "go")
				idle(r.svc, "root", rec.ID)
			}
			script := viaBridge("tool_call", `{"name":"generate_image","arguments":{"prompt":"a cat"}}`)

			It("gates a spend-class tool like a direct call", func() {
				r := newLocalAIRig(script, withSearch(config.ToolSearchOn, 0))
				run(r, agents.Agent{Name: "Poor", Model: "m"})
				res := r.h.ResultsFor("tool_call")
				Expect(res).To(HaveLen(1))
				Expect(res[0].Err).NotTo(HaveOccurred(), "a refusal is an answer, as for a direct call")
				Expect(res[0].Out).To(ContainSubstring("act.spend"))
				Expect(r.client.callCount()).To(Equal(0))

				r = newLocalAIRig(script, withSearch(config.ToolSearchOn, 0))
				run(r, agents.Agent{Name: "Rich", Model: "m", Builtins: []string{builtins.GrantActSpend}})
				res = r.h.ResultsFor("tool_call")
				Expect(res[0].Out).To(Equal("made"))
				Expect(r.client.users).To(Equal([]string{"root"}))
			})

			It("still never offers the admin tools", func() {
				r := newLocalAIRig(viaBridge("tool_call", `{"name":"install_model","arguments":{}}`), withSearch(config.ToolSearchOn, 0))
				run(r, agents.Agent{Name: "Auto", Model: "m", BuiltinsOn: []string{builtins.GroupLocalAIAdmin}})
				Expect(r.h.ResultsFor("tool_call")[0].Err).To(MatchError(ContainSubstring("no such tool")))
				Expect(r.admin.calls).To(BeEmpty())
			})
		})
	})
})
