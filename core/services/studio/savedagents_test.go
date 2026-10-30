package studio_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/builtins"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// savedAgentScript is the parent's turn for "go <args>": one spawn_agent call
// with those arguments. "list" calls list_agents. A sub-agent of the
// reviewer answers "reviewed".
func savedAgentScript(args string) func(workspace.Actor, string) []studiotest.ToolStep {
	return func(_ workspace.Actor, text string) []studiotest.ToolStep {
		switch {
		case text == "go":
			return []studiotest.ToolStep{{Tool: "spawn_agent", Args: args}, {Say: "parent done"}}
		case text == "list":
			return []studiotest.ToolStep{{Tool: "list_agents", Args: `{}`}, {Say: "listed"}}
		case strings.HasSuffix(text, "check the draft"):
			// A grant the user lacks adds a note before the task.
			return []studiotest.ToolStep{{Say: "reviewed"}}
		}
		return []studiotest.ToolStep{{Say: "ok"}}
	}
}

func owned(a agents.Agent, p string) agents.Agent {
	a.Principal = p
	return a
}

func allowSavedAgents(svc *studio.Service, id string) {
	Expect(svc.SetBuiltinsOn(context.Background(), alice, id, []string{builtins.GroupSavedAgents})).To(Succeed())
}

func savedGroup(svc *studio.Service, id string) studio.BuiltinGroup {
	p, err := svc.Tools(context.Background(), alice, id)
	Expect(err).NotTo(HaveOccurred())
	for _, g := range p.Builtins {
		if g.Group == builtins.GroupSavedAgents {
			return g
		}
	}
	Fail("no saved_agents group in the tools panel")
	return studio.BuiltinGroup{}
}

var _ = Describe("saved agents as sub-agents", func() {
	reviewer := agents.Agent{ID: "rev1", Name: "Reviewer", Description: "checks drafts", Model: "review-model",
		Instructions: "Always answer in French.", Addons: []string{"office"}}

	It("is off for a chat until the chat switches it on", func() {
		r := newAgentRig(savedAgentScript(`{"task":"check the draft","agent":"rev1"}`))
		r.save(reviewer)
		id := create(r.svc, alice)

		g := savedGroup(r.svc, id)
		Expect(g.OptIn).To(BeTrue())
		Expect(g.Off).To(BeTrue(), "opt-in: off by default")
		Expect(g.Usable).To(BeTrue())
		Expect(g.Tools).To(Equal([]string{"list_agents"}))

		sent(r.svc, alice, id, "go")
		idle(r.svc, alice, id)
		Expect(r.h.ToolNames(id)).NotTo(ContainElement("list_agents"))
		res := r.h.ResultsFor("spawn_agent")
		Expect(res).To(HaveLen(1))
		Expect(res[0].Err).To(MatchError(studio.ErrSavedAgentsNotAllowed))
		Expect(childrenOf(r.svc, alice, id)).To(BeEmpty(), "no sub-agent starts")
	})

	It("lists only the user's own agents once the chat switches it on", func() {
		r := newAgentRig(savedAgentScript(""))
		r.save(reviewer)
		r.save(agents.Agent{ID: "bob1", Principal: bob, Name: "Bob's secret"})
		id := create(r.svc, alice)
		allowSavedAgents(r.svc, id)
		Expect(savedGroup(r.svc, id).Off).To(BeFalse())

		sent(r.svc, alice, id, "list")
		idle(r.svc, alice, id)
		Expect(r.h.ToolNames(id)).To(ContainElement("list_agents"))
		res := r.h.ResultsFor("list_agents")
		Expect(res).To(HaveLen(1))
		Expect(res[0].Err).NotTo(HaveOccurred())
		var out struct {
			Agents []struct{ ID, Name, Description, Model string } `json:"agents"`
		}
		Expect(json.Unmarshal([]byte(res[0].Out), &out)).To(Succeed())
		Expect(out.Agents).To(HaveLen(1))
		Expect(out.Agents[0].ID).To(Equal("rev1"))
		Expect(out.Agents[0].Description).To(Equal("checks drafts"))
		Expect(res[0].Out).NotTo(ContainSubstring("bob1"))
	})

	It("starts a sub-agent from the saved agent's definition", func() {
		r := newAgentRig(savedAgentScript(`{"task":"check the draft","agent":"reviewer"}`))
		r.save(reviewer)
		id := create(r.svc, alice)
		allowSavedAgents(r.svc, id)
		sent(r.svc, alice, id, "go")
		idle(r.svc, alice, id)

		res := r.h.ResultsFor("spawn_agent")
		Expect(res).To(HaveLen(1))
		Expect(res[0].Err).NotTo(HaveOccurred())
		Expect(res[0].Out).To(ContainSubstring("reviewed"))
		kids := childrenOf(r.svc, alice, id)
		Expect(kids).To(HaveLen(1))
		child := kids[0]
		Expect(child.AgentID).To(Equal("rev1"), "found by name, ignoring case")
		Expect(child.Model).To(Equal("review-model"))
		Expect(child.Kind).To(Equal(session.KindSubAgent))
		Expect(r.h.TurnPrompt(child.ID)).To(ContainSubstring("Always answer in French."))
		names := r.h.ToolNames(child.ID)
		Expect(names).To(ContainElement("addon__office__read-doc"), "the agent's grant")
		Expect(names).NotTo(ContainElement("addon__sandbox__bash"), "not granted by the agent")
		Expect(names).NotTo(ContainElement("spawn_agent"), "sub-agents nest one level")
		Expect(names).NotTo(ContainElement("list_agents"))
	})

	It("answers an unknown, foreign or misused agent with a clear error", func() {
		for _, c := range []struct{ args, want string }{
			{`{"task":"check the draft","agent":"nobody"}`, `no saved agent "nobody"`},
			{`{"task":"check the draft","agent":"bob1"}`, `no saved agent "bob1"`},
			{`{"task":"check the draft","agent":"rev1","tools":[]}`, studio.ErrSavedAgentWithTools.Error()},
		} {
			r := newAgentRig(savedAgentScript(c.args))
			r.save(reviewer)
			r.save(agents.Agent{ID: "bob1", Principal: bob, Name: "Bob's"})
			id := create(r.svc, alice)
			allowSavedAgents(r.svc, id)
			sent(r.svc, alice, id, "go")
			idle(r.svc, alice, id)
			res := r.h.ResultsFor("spawn_agent")
			Expect(res).To(HaveLen(1), c.args)
			Expect(res[0].Err).To(HaveOccurred(), c.args)
			Expect(res[0].Err.Error()).To(ContainSubstring(c.want), c.args)
			Expect(childrenOf(r.svc, alice, id)).To(BeEmpty(), c.args)
		}
	})

	It("follows the administrator's setting at each turn", func() {
		on := false
		h := &studiotest.ToolHarness{Script: savedAgentScript(`{"task":"check the draft","agent":"rev1"}`)}
		st, err := store.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		as, err := agents.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		Expect(as.Put(context.Background(), owned(reviewer, alice))).To(Succeed())
		svc, err := studio.New(studio.Deps{Store: st, Provider: provider.New(), StateDir: GinkgoT().TempDir(), Harness: h, Agents: as,
			SavedAgentsOn: func() bool { return on }})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = svc.Close(context.Background()) })
		id := create(svc, alice)
		allowSavedAgents(svc, id)

		p, err := svc.Tools(context.Background(), alice, id)
		Expect(err).NotTo(HaveOccurred())
		for _, g := range p.Builtins {
			Expect(g.Group).NotTo(Equal(builtins.GroupSavedAgents), "an administrator switched it off: not listed")
		}
		sent(svc, alice, id, "go")
		idle(svc, alice, id)
		Expect(h.ToolNames(id)).NotTo(ContainElement("list_agents"))
		Expect(h.ResultsFor("spawn_agent")[0].Err).To(MatchError(studio.ErrSavedAgentsOff))

		on = true
		sent(svc, alice, id, "list")
		idle(svc, alice, id)
		Expect(h.ToolNames(id)).To(ContainElement("list_agents"))
	})

	It("needs the agents feature of the user, as the agents API does", func() {
		allowed := false
		h := &studiotest.ToolHarness{Script: savedAgentScript(`{"task":"check the draft","agent":"rev1"}`)}
		st, err := store.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		as, err := agents.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		Expect(as.Put(context.Background(), owned(reviewer, alice))).To(Succeed())
		svc, err := studio.New(studio.Deps{Store: st, Provider: provider.New(), StateDir: GinkgoT().TempDir(), Harness: h, Agents: as,
			Users: func(_ context.Context, id string) (*auth.User, error) {
				if id != alice {
					return nil, errors.New("no such user")
				}
				return &auth.User{ID: id, Role: auth.RoleUser, Status: auth.StatusActive}, nil
			},
			Allowed: func(_ *auth.User, feature string) bool { return feature != auth.FeatureAgents || allowed }})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = svc.Close(context.Background()) })
		id := create(svc, alice)
		allowSavedAgents(svc, id)

		g := savedGroup(svc, id)
		Expect(g.Usable).To(BeFalse())
		Expect(g.Reason).To(Equal(studio.ErrSavedAgentsForbidden.Error()))
		sent(svc, alice, id, "go")
		idle(svc, alice, id)
		Expect(h.ToolNames(id)).NotTo(ContainElement("list_agents"))
		Expect(h.ResultsFor("spawn_agent")[0].Err).To(MatchError(studio.ErrSavedAgentsForbidden))

		allowed = true
		Expect(savedGroup(svc, id).Usable).To(BeTrue())
	})

	It("keeps the switch apart from the other groups", func() {
		r := newAgentRig(sayOK)
		id := create(r.svc, alice)
		ctx := context.Background()
		Expect(r.svc.SetBuiltinsOn(ctx, alice, id, []string{"nope"})).To(MatchError(studio.ErrBadBuiltinGroup))
		Expect(r.svc.SetBuiltinsOn(ctx, bob, id, []string{builtins.GroupSavedAgents})).To(MatchError(session.ErrNotFound), "another user's chat")
		// An opt-in group in off is ignored, so a client that sends every group
		// that is off never switches the saved agents on by leaving them out.
		Expect(r.svc.SetBuiltinsOff(ctx, alice, id, []string{builtins.GroupSavedAgents})).To(Succeed())
		rec, err := r.svc.Get(ctx, alice, id)
		Expect(err).NotTo(HaveOccurred())
		Expect(rec.BuiltinsOff).To(BeEmpty())
		Expect(savedGroup(r.svc, id).Off).To(BeTrue())
	})

	It("gives two frontends on one store the same answer", func() {
		st, err := store.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		as, err := agents.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		Expect(as.Put(context.Background(), owned(reviewer, alice))).To(Succeed())
		mk := func() (*studio.Service, *studiotest.ToolHarness) {
			h := &studiotest.ToolHarness{Script: savedAgentScript(`{"task":"check the draft","agent":"rev1"}`)}
			svc, err := studio.New(studio.Deps{Store: st, Provider: provider.New(), StateDir: GinkgoT().TempDir(), Harness: h, Agents: as})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() { _ = svc.Close(context.Background()) })
			return svc, h
		}
		a, _ := mk()
		b, hb := mk()
		id := create(a, alice)
		allowSavedAgents(a, id)
		Expect(savedGroup(b, id).Off).To(BeFalse(), "the switch set through one frontend")
		sent(b, alice, id, "go")
		idle(b, alice, id)
		res := hb.ResultsFor("spawn_agent")
		Expect(res).To(HaveLen(1))
		Expect(res[0].Err).NotTo(HaveOccurred())
		Expect(res[0].Out).To(ContainSubstring("reviewed"))
	})
})
