package studio_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/builtins"
	"github.com/mudler/LocalAI/core/services/studio/resourcesets"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// roleAddons is a broker that enforces the role by kind the way the real one
// does: a viewer may read and is refused a write. Both ways of calling a tool
// reach this one function.
type roleAddons struct {
	*fakeAddons
	role  map[string]string
	write map[string]bool
}

func (r roleAddons) Call(ctx context.Context, user, conv, name string, args json.RawMessage) (string, error) {
	if r.write[name] && r.role[user] == "viewer" {
		return `{"kind":"error","code":"not_permitted"}`, nil
	}
	return r.fakeAddons.Call(ctx, user, conv, name, args)
}

// deferredResources is resourceHost with its tools marked deferred.
type deferredResources struct{ *resourceHost }

func (h deferredResources) Tools(ctx context.Context, u string) ([]studio.AddonTool, error) {
	ts, err := h.resourceHost.Tools(ctx, u)
	for i := range ts {
		ts[i].Deferred = true
	}
	return ts, err
}

var _ = Describe("the deferred tools of the add-ons, in the situations of a chat", func() {
	ctx := context.Background()
	const (
		listNotes = "addon__notes__list-notes"
		addNote   = "addon__notes__add-note"
	)
	schema := json.RawMessage(`{"type":"object","properties":{"folder":{"type":"string"}}}`)
	tool := func(addon, name string, write bool) studio.AddonTool {
		return studio.AddonTool{Name: "addon__" + addon + "__" + name, AddonID: addon, Tool: name, Description: "Tool " + name + " of " + addon + ".", Schema: schema, Deferred: true, Action: write}
	}
	notes := func() []studio.AddonTool {
		return []studio.AddonTool{tool("notes", "list-notes", false), tool("notes", "add-note", true)}
	}
	on := func(d *studio.Deps) { withSearch(config.ToolSearchOn, 0)(d) }
	call := func(name, args string) []studiotest.ToolStep {
		return []studiotest.ToolStep{{Tool: "tool_call", Args: `{"name":"` + name + `","arguments":` + args + `}`}, {Say: "done"}}
	}
	run := func(svc *studio.Service, user, id string) {
		GinkgoHelper()
		sent(svc, user, id, "go")
		idle(svc, user, id)
	}
	agentRig := func(fa studio.AddonTools, script func(workspace.Actor, string) []studiotest.ToolStep, mode string) (*studio.Service, *studiotest.ToolHarness, agents.Store) {
		as, err := agents.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		svc, h := newToolService(script, func(d *studio.Deps) { d.AddonTools, d.Agents = fa, as; withSearch(mode, 0)(d) })
		return svc, h, as
	}
	save := func(as agents.Store, id string, res resourcesets.Sets, addons []string, grants ...string) {
		Expect(as.Put(ctx, agents.Agent{ID: id, Principal: alice, Name: id, Addons: addons, Resources: res, Created: time.Now().UTC(),
			Builtins: append([]string{builtins.GrantSavedAgents}, grants...), BuiltinsOn: []string{builtins.GroupSavedAgents}})).To(Succeed())
	}

	Describe("the decision per source", func() {
		huge := func() []studio.AddonTool {
			a, b := tool("notes", "list-notes", false), tool("notes", "add-note", true)
			a.Description = strings.Repeat("List the notes of one folder. ", 3500)
			b.Description = strings.Repeat("Add a note to one folder. ", 3500)
			return []studio.AddonTool{a, b}
		}
		It("defers a large set of add-on tools and leaves the LocalAI groups typed when the groups alone are small", func() {
			fa := &fakeAddons{tools: map[string][]studio.AddonTool{bob: huge()}}
			r := newLocalAIRig(sayOK, withSearch(config.ToolSearchAuto, 300000), func(d *studio.Deps) { d.AddonTools = fa })
			names := r.names(bob)
			Expect(names).To(ContainElements(bridgeNames))
			Expect(names).To(ContainElement("generate_image"), "the groups keep their own decision")
			Expect(names).NotTo(ContainElements(listNotes, addNote))
		})
		It("never moves the LocalAI groups because of an add-on: small add-on tools stay typed while the groups defer", func() {
			fa := &fakeAddons{tools: map[string][]studio.AddonTool{bob: notes()}}
			r := newLocalAIRig(sayOK, withSearch(config.ToolSearchAuto, 1000), func(d *studio.Deps) { d.AddonTools = fa })
			names := r.names(bob)
			Expect(names).To(ContainElements(bridgeNames))
			Expect(names).NotTo(ContainElement("generate_image"))
			Expect(names).To(ContainElements(listNotes, addNote), "the add-on tools are small: typed")
			// The same chat without the add-on defers the groups the same way.
			r2 := newLocalAIRig(sayOK, withSearch(config.ToolSearchAuto, 1000))
			Expect(r2.names(bob)).NotTo(ContainElement("generate_image"))
		})
		It("puts the add-on tools and the groups behind one bridge, and its hint says what is there", func() {
			fa := &fakeAddons{tools: map[string][]studio.AddonTool{bob: notes()}}
			r := newLocalAIRig(sayOK, withSearch(config.ToolSearchOn, 0), func(d *studio.Deps) { d.AddonTools = fa })
			id := create(r.svc, bob)
			names := r.turnNames(bob, id)
			Expect(names).To(ContainElements(bridgeNames))
			Expect(names).NotTo(ContainElements(listNotes, "generate_image"))
			Expect(r.h.ToolDescription(id, "tool_search")).To(ContainSubstring("add-ons"))
		})
		It("ranks a LocalAI tool before an add-on tool when the scores tie", func() {
			clash := tool("notes", "speak", false)
			clash.Description = "Make speech from text."
			fa := &fakeAddons{tools: map[string][]studio.AddonTool{bob: {clash}}}
			r := newLocalAIRig(func(workspace.Actor, string) []studiotest.ToolStep {
				return []studiotest.ToolStep{{Tool: "tool_search", Args: `{"query":"speak"}`}, {Say: "done"}}
			}, withSearch(config.ToolSearchOn, 0), func(d *studio.Deps) { d.AddonTools = fa })
			id := create(r.svc, bob)
			run(r.svc, bob, id)
			out := r.h.ResultsFor("tool_search")[0].Out
			Expect(strings.Index(out, "speak:")).To(BeNumerically(">=", 0))
			Expect(strings.Index(out, "speak:")).To(BeNumerically("<", strings.Index(out, "addon__notes__speak:")))
		})
		It("still offers the add-on bridge when the owner of the LocalAI tools cannot be found", func() {
			fa := &fakeAddons{tools: map[string][]studio.AddonTool{bob: notes()}}
			r := newLocalAIRig(func(workspace.Actor, string) []studiotest.ToolStep {
				return append([]studiotest.ToolStep{{Tool: "tool_search", Args: `{"query":"notes"}`}}, call(listNotes, `{}`)...)
			}, withSearch(config.ToolSearchOn, 0), func(d *studio.Deps) {
				d.AddonTools = fa
				d.Users = func(context.Context, string) (*auth.User, error) { return nil, errors.New("gone") }
			})
			id := create(r.svc, bob)
			names := r.turnNames(bob, id)
			Expect(names).To(ContainElements(bridgeNames))
			Expect(names).NotTo(ContainElement("generate_image"), "no LocalAI tool without an owner")
			Expect(r.h.ResultsFor("tool_search")[0].Out).To(ContainSubstring(listNotes))
			Expect(r.h.ResultsFor("tool_call")[0].Out).To(Equal("result of " + listNotes))
		})
	})

	Describe("the entry of a call made through tool_call", func() {
		It("is written under the add-on tool's own name, as the typed call is, so the chat shows its note", func() {
			var entries [2][]session.Entry
			for i, mode := range []string{config.ToolSearchOff, config.ToolSearchOn} {
				script := func(workspace.Actor, string) []studiotest.ToolStep {
					if mode == config.ToolSearchOn {
						return call(addNote, `{"folder":"a"}`)
					}
					return []studiotest.ToolStep{{Tool: addNote, Args: `{"folder":"a"}`}, {Say: "done"}}
				}
				fa := &fakeAddons{tools: map[string][]studio.AddonTool{alice: notes()}}
				svc, _ := newToolService(script, func(d *studio.Deps) { d.AddonTools = fa; withSearch(mode, 0)(d) })
				id := create(svc, alice)
				run(svc, alice, id)
				entries[i] = toolEntries(svc, alice, id)
				Expect(entries[i]).To(HaveLen(1))
			}
			Expect(entries[1][0].Text).To(HavePrefix(addNote + ":"))
			Expect(strings.SplitN(entries[1][0].Text, "\n", 2)[1]).To(Equal(strings.SplitN(entries[0][0].Text, "\n", 2)[1]))
		})
		It("keeps the name tool_call for a LocalAI tool and for a refused name", func() {
			fa := &fakeAddons{tools: map[string][]studio.AddonTool{bob: notes()}}
			r := newLocalAIRig(viaBridge("tool_call", `{"name":"speak","arguments":{"text":"hi"}}`), withSearch(config.ToolSearchOn, 0), func(d *studio.Deps) { d.AddonTools = fa })
			id := create(r.svc, bob)
			run(r.svc, bob, id)
			es := toolEntries(r.svc, bob, id)
			Expect(es).To(HaveLen(1))
			Expect(es[0].Text).To(HavePrefix("tool_call:"))
		})
	})

	Describe("the gates of a chat, through tool_call", func() {
		It("refuses a deferred write in a run nobody watches that lacks act.addons, as the typed call is refused", func() {
			outs := map[string]string{}
			for _, mode := range []string{config.ToolSearchOff, config.ToolSearchOn} {
				script := func(workspace.Actor, string) []studiotest.ToolStep {
					if mode == config.ToolSearchOn {
						return call(addNote, `{"folder":"a"}`)
					}
					return []studiotest.ToolStep{{Tool: addNote, Args: `{"folder":"a"}`}, {Say: "done"}}
				}
				fa := &fakeAddons{tools: map[string][]studio.AddonTool{alice: notes()}}
				svc, h, as := agentRig(fa, script, mode)
				save(as, "ag", nil, []string{"notes"})
				rec, err := svc.CreateAs(ctx, alice, studio.CreateInput{Model: "m", AgentID: "ag"}, studio.Origin{Kind: session.OriginJob, ID: "x"})
				Expect(err).NotTo(HaveOccurred())
				run(svc, alice, rec.ID)
				name := addNote
				if mode == config.ToolSearchOn {
					name = "tool_call"
				}
				res := h.ResultsFor(name)
				Expect(res).To(HaveLen(1))
				Expect(fa.calls).To(BeEmpty(), mode)
				outs[mode] = res[0].Out
			}
			Expect(outs[config.ToolSearchOn]).To(Equal(outs[config.ToolSearchOff]))
			Expect(outs[config.ToolSearchOn]).To(ContainSubstring("act.addons"))
		})
		It("runs it when the run holds act.addons", func() {
			fa := &fakeAddons{tools: map[string][]studio.AddonTool{alice: notes()}}
			svc, h, as := agentRig(fa, func(workspace.Actor, string) []studiotest.ToolStep { return call(addNote, `{"folder":"a"}`) }, config.ToolSearchOn)
			save(as, "ag", nil, []string{"notes"}, builtins.GrantActAddons)
			rec, err := svc.CreateAs(ctx, alice, studio.CreateInput{Model: "m", AgentID: "ag"}, studio.Origin{Kind: session.OriginJob, ID: "x"})
			Expect(err).NotTo(HaveOccurred())
			run(svc, alice, rec.ID)
			Expect(h.ResultsFor("tool_call")[0].Out).To(Equal("result of " + addNote))
			Expect(fa.calls).To(HaveLen(1))
		})
		It("does not list the tools of an add-on the saved agent does not grant, and answers no such tool", func() {
			fa := &fakeAddons{tools: map[string][]studio.AddonTool{alice: append(notes(), tool("other", "ping", false))}}
			svc, h, as := agentRig(fa, func(workspace.Actor, string) []studiotest.ToolStep {
				return append([]studiotest.ToolStep{{Tool: "tool_search", Args: `{"query":"list notes ping"}`}}, call(listNotes, `{}`)...)
			}, config.ToolSearchOn)
			save(as, "ag", nil, []string{"other"})
			rec, err := svc.Create(ctx, alice, studio.CreateInput{Model: "m", AgentID: "ag"})
			Expect(err).NotTo(HaveOccurred())
			run(svc, alice, rec.ID)
			Expect(h.ResultsFor("tool_search")[0].Out).NotTo(ContainSubstring(listNotes))
			Expect(h.ResultsFor("tool_search")[0].Out).To(ContainSubstring("addon__other__ping"))
			Expect(h.ResultsFor("tool_call")[0].Err).To(MatchError(ContainSubstring("no such tool")))
			Expect(fa.calls).To(BeEmpty())
		})
		It("holds a resource-scoped deferred tool to the resource list of the agent", func() {
			host := &resourceHost{scoped: true}
			var steps []studiotest.ToolStep
			svc, h, as := agentRig(deferredResources{host}, func(workspace.Actor, string) []studiotest.ToolStep { return steps }, config.ToolSearchOn)
			list := resourcesets.Sets{"tables": {{ID: "db-read", Mode: "read"}, {ID: "db-write", Mode: "write"}}}
			save(as, "ag", list, []string{"tables"})
			rec, err := svc.Create(ctx, alice, studio.CreateInput{Model: "m", AgentID: "ag"})
			Expect(err).NotTo(HaveOccurred())
			steps = []studiotest.ToolStep{
				{Tool: "tool_call", Args: `{"name":"addon__tables__rows-query","arguments":{"resourceId":"db-read"}}`},
				{Tool: "tool_call", Args: `{"name":"addon__tables__rows-query","arguments":{"resourceId":"db-other"}}`},
				{Tool: "tool_call", Args: `{"name":"addon__tables__rows-insert","arguments":{"resourceId":"db-read"}}`},
				{Tool: "tool_call", Args: `{"name":"addon__tables__rows-insert","arguments":{"resourceId":"db-write"}}`},
				{Say: "done"}}
			run(svc, alice, rec.ID)
			var outs []string
			for _, r := range h.ResultsFor("tool_call") {
				outs = append(outs, r.Out)
			}
			Expect(outs).To(Equal([]string{"ok:db-read", "refused:db-other", "refused:db-read", "ok:db-write"}))
		})
	})

	Describe("tool_describe", func() {
		It("answers no such tool for a typed tool, an add-on switched off and a tool of an add-on the user does not have", func() {
			fa := &fakeAddons{tools: map[string][]studio.AddonTool{
				alice: append(notes(), addonTool("example", "ping"), tool("hidden", "peek", false)),
				bob:   {tool("secret", "peek", false)},
			}}
			svc, h := newToolService(func(workspace.Actor, string) []studiotest.ToolStep {
				return []studiotest.ToolStep{
					{Tool: "tool_describe", Args: `{"name":"addon__example__ping"}`},
					{Tool: "tool_describe", Args: `{"name":"addon__hidden__peek"}`},
					{Tool: "tool_describe", Args: `{"name":"addon__secret__peek"}`},
					{Tool: "tool_describe", Args: `{"name":"` + listNotes + `"}`},
					{Say: "done"}}
			}, func(d *studio.Deps) { d.AddonTools = fa; on(d) })
			id := create(svc, alice)
			Expect(svc.SetAddonsOff(ctx, alice, id, []string{"hidden"})).To(Succeed())
			run(svc, alice, id)
			res := h.ResultsFor("tool_describe")
			Expect(res).To(HaveLen(4))
			Expect(res[0].Err).To(MatchError(ContainSubstring("no such tool")), "a typed add-on tool")
			Expect(res[1].Err).To(MatchError(ContainSubstring("no such tool")), "an add-on switched off in the chat")
			Expect(res[2].Err).To(MatchError(ContainSubstring("no such tool")), "the add-on of another user")
			Expect(res[3].Err).NotTo(HaveOccurred())
		})
	})

	It("refuses a viewer a deferred write tool end to end, as it refuses the typed call, and lets the viewer read", func() {
		for _, user := range []string{"viewer", alice} {
			fa := &fakeAddons{tools: map[string][]studio.AddonTool{user: notes()}}
			broker := roleAddons{fakeAddons: fa, role: map[string]string{"viewer": "viewer"}, write: map[string]bool{addNote: true}}
			outs := map[string]string{}
			for _, mode := range []string{config.ToolSearchOff, config.ToolSearchOn} {
				script := func(workspace.Actor, string) []studiotest.ToolStep {
					if mode == config.ToolSearchOn {
						return call(addNote, `{"folder":"a"}`)
					}
					return []studiotest.ToolStep{{Tool: addNote, Args: `{"folder":"a"}`}, {Say: "done"}}
				}
				svc, h := newToolService(script, func(d *studio.Deps) { d.AddonTools = broker; withSearch(mode, 0)(d) })
				id := create(svc, user)
				run(svc, user, id)
				name := addNote
				if mode == config.ToolSearchOn {
					name = "tool_call"
				}
				outs[mode] = h.ResultsFor(name)[0].Out
			}
			Expect(outs[config.ToolSearchOn]).To(Equal(outs[config.ToolSearchOff]), user)
			if user == "viewer" {
				Expect(outs[config.ToolSearchOn]).To(ContainSubstring("not_permitted"))
				Expect(fa.calls).To(BeEmpty())
			} else {
				Expect(outs[config.ToolSearchOn]).To(Equal("result of " + addNote))
			}
		}
	})

	Describe("sub-agents", func() {
		spawn := func(args string) func(workspace.Actor, string) []studiotest.ToolStep {
			return func(_ workspace.Actor, text string) []studiotest.ToolStep {
				if text == "go" {
					return []studiotest.ToolStep{{Tool: "spawn_agent", Args: args}, {Say: "parent done"}}
				}
				return []studiotest.ToolStep{{Say: "child done"}}
			}
		}
		It("gives the sub-agent of a saved agent the bridge, and keeps the typed tools of a plain sub-agent", func() {
			fa := &fakeAddons{tools: map[string][]studio.AddonTool{alice: notes()}}
			svc, h, as := agentRig(fa, spawn(`{"task":"child task","wait":true}`), config.ToolSearchOn)
			save(as, "parent", nil, []string{"notes"})
			rec, err := svc.Create(ctx, alice, studio.CreateInput{Model: "m", AgentID: "parent"})
			Expect(err).NotTo(HaveOccurred())
			run(svc, alice, rec.ID)
			kids := childrenOf(svc, alice, rec.ID)
			Expect(kids).To(HaveLen(1))
			plain := h.ToolNames(kids[0].ID)
			Expect(plain).To(ContainElements(listNotes, addNote), "a plain sub-agent has the typed tools of its parent")
			Expect(plain).NotTo(ContainElement("tool_search"))

			// A sub-agent of a saved agent is built like a chat of that agent.
			save(as, "child", nil, []string{"notes"})
			parent2 := create(svc, alice)
			Expect(svc.SetBuiltinsOn(ctx, alice, parent2, []string{builtins.GroupSavedAgents})).To(Succeed())
			h.Script = spawn(`{"task":"saved child task","agent":"child","wait":true}`)
			run(svc, alice, parent2)
			var saved string
			for _, k := range childrenOf(svc, alice, parent2) {
				saved = k.ID
			}
			Expect(saved).NotTo(BeEmpty(), "spawn: %+v", h.ResultsFor("spawn_agent"))
			names := h.ToolNames(saved)
			Expect(names).To(ContainElements(bridgeNames))
			Expect(names).NotTo(ContainElements(listNotes, addNote))
		})
	})
})
