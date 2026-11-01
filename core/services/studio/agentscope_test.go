package studio_test

import (
	"context"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/pkg/functions"

	mcpendpoint "github.com/mudler/LocalAI/core/http/endpoints/mcp"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// mcpAsk records what the service asked of the MCP hook.
type mcpAsk struct {
	only    []string
	limited bool
	missing []string
}

type agentRig struct {
	svc    *studio.Service
	h      *studiotest.ToolHarness
	agents agents.Store
	ask    *mcpAsk
}

func newAgentRig(script func(workspace.Actor, string) []studiotest.ToolStep) *agentRig {
	f := newFiles()
	writeSkill(f, alice, "/.skills/review/SKILL.md", "review", "check a change before it ships")
	writeSkill(f, alice, "/.skills/house-style/SKILL.md", "house-style", "how we write")
	as, err := agents.NewJSON("")
	Expect(err).NotTo(HaveOccurred())
	fa := &fakeAddons{tools: map[string][]studio.AddonTool{alice: {addonTool("office", "read-doc"), addonTool("sandbox", "bash")}}}
	fc := &fakeCollections{mine: map[string]map[string][]studio.CollectionHit{alice: {"docs": nil, "secret": nil}}}
	fm := &fakeMCP{fns: []functions.Function{lookupFn()}}
	ask := &mcpAsk{}
	svc, h := newToolService(script, func(d *studio.Deps) {
		d.Files, d.AddonTools, d.Collections, d.Agents = f, fa, fc, as
		d.MCP = func(_ context.Context, user, model string, only []string, limited bool) (mcpendpoint.ToolExecutor, []string) {
			ask.only, ask.limited = only, limited
			return fm, ask.missing
		}
	})
	return &agentRig{svc: svc, h: h, agents: as, ask: ask}
}

func (r *agentRig) save(a agents.Agent) {
	if a.Principal == "" {
		a.Principal = alice
	}
	a.Created = time.Now().UTC()
	Expect(r.agents.Put(context.Background(), a)).To(Succeed())
}

func (r *agentRig) chatWith(agentID string) string {
	rec, err := r.svc.Create(context.Background(), alice, studio.CreateInput{Model: "m", AgentID: agentID})
	Expect(err).NotTo(HaveOccurred())
	return rec.ID
}

var sayOK = func(workspace.Actor, string) []studiotest.ToolStep { return []studiotest.ToolStep{{Say: "ok"}} }

var _ = Describe("agentscope", func() {
	It("a chat with an agent gets only what the agent grants", func() {
		r := newAgentRig(func(_ workspace.Actor, text string) []studiotest.ToolStep {
			if text == "peek" {
				return []studiotest.ToolStep{{Tool: "search_collection", Args: `{"collection":"secret","query":"x"}`}, {Say: "ok"}}
			}
			return []studiotest.ToolStep{{Say: "ok"}}
		})
		r.save(agents.Agent{ID: "ag1", Name: "French reviewer", Instructions: "Always answer in French.",
			MCPServers: []string{"crm"}, Addons: []string{"office"}, Collections: []string{"docs"}, Skills: []string{"review"}})
		id := r.chatWith("ag1")
		sent(r.svc, alice, id, "peek")
		idle(r.svc, alice, id)

		names := r.h.ToolNames(id)
		Expect(names).To(ContainElement("addon__office__read-doc"), "add-on tools = %v, want office only", names)
		Expect(names).NotTo(ContainElement("addon__sandbox__bash"), "add-on tools = %v, want office only", names)
		Expect(names).To(ContainElement("crm_lookup"), "MCP: tools %v, asked %+v, want the crm server only", names, r.ask)
		Expect(r.ask.limited).To(BeTrue(), "MCP: tools %v, asked %+v, want the crm server only", names, r.ask)
		Expect(r.ask.only).To(Equal([]string{"crm"}), "MCP: tools %v, asked %+v, want the crm server only", names, r.ask)
		prompt := r.h.TurnPrompt(id)
		Expect(prompt).To(ContainSubstring("Always answer in French."), "prompt = %q, want the instructions and the review skill only", prompt)
		Expect(prompt).To(ContainSubstring("review"), "prompt = %q, want the instructions and the review skill only", prompt)
		Expect(prompt).NotTo(ContainSubstring("house-style"), "prompt = %q, want the instructions and the review skill only", prompt)
		res := r.h.ResultsFor("search_collection")
		Expect(res).To(HaveLen(1), "a collection the agent was not given: %+v, want not found", res)
		Expect(res[0].Err).NotTo(BeNil(), "a collection the agent was not given: %+v, want not found", res)
		Expect(res[0].Err.Error()).To(ContainSubstring("collection not found"), "a collection the agent was not given: %+v, want not found", res)
		rec, _ := r.svc.Get(context.Background(), alice, id)
		Expect(rec.AgentID).To(Equal("ag1"), "AgentID = %q", rec.AgentID)
	})

	It("an agent granting nothing gives no optional tools", func() {
		r := newAgentRig(sayOK)
		r.save(agents.Agent{ID: "ag0", Name: "Bare"})
		id := r.chatWith("ag0")
		sent(r.svc, alice, id, "hi")
		idle(r.svc, alice, id)
		for _, n := range r.h.ToolNames(id) {
			Expect(n).NotTo(HavePrefix("addon__"), "an agent with empty lists got %s", n)
			Expect(n).NotTo(Equal("crm_lookup"), "an agent with empty lists got %s", n)
			Expect(n).NotTo(Equal("list_collections"), "an agent with empty lists got %s", n)
			Expect(n).NotTo(Equal("search_collection"), "an agent with empty lists got %s", n)
		}
		Expect(r.h.TurnPrompt(id)).NotTo(ContainSubstring("review"), "an agent with no skills got a skill index: %q", r.h.TurnPrompt(id))
	})

	It("a grant the user lost is absent with a note", func() {
		r := newAgentRig(sayOK)
		r.ask.missing = []string{"mail"}
		r.save(agents.Agent{ID: "ag2", Name: "Stale", Addons: []string{"office", "gone"}, Collections: []string{"docs", "vanished"},
			Skills: []string{"review", "nope"}, MCPServers: []string{"crm", "mail"}})
		id := r.chatWith("ag2")
		sent(r.svc, alice, id, "hi")
		idle(r.svc, alice, id)
		Expect(turn(r.svc, alice, id).Last).To(Equal(store.TurnDone))
		msg := strings.Join(r.h.Sent(id), "\n")
		for _, want := range []string{"unavailable this turn", "add-on gone", "collection vanished", "skill nope", "MCP server mail"} {
			Expect(msg).To(ContainSubstring(want), "note lacks %q: %q", want, msg)
		}
		Expect(r.h.ToolNames(id)).To(ContainElement("addon__office__read-doc"), "the usable grant is missing: %v", r.h.ToolNames(id))
	})

	It("a sub agent cannot get what its parent lacks", func() {
		r := newAgentRig(func(_ workspace.Actor, text string) []studiotest.ToolStep {
			return []studiotest.ToolStep{{Tool: "spawn_agent", Args: `{"task":"t","tools":["addon__sandbox__bash"]}`}, {Say: "ok"}}
		})
		r.save(agents.Agent{ID: "ag3", Name: "Office only", Addons: []string{"office"}})
		id := r.chatWith("ag3")
		sent(r.svc, alice, id, "go")
		idle(r.svc, alice, id)
		res := r.h.ResultsFor("spawn_agent")
		Expect(res).To(HaveLen(1), "spawn_agent = %+v, want a refusal", res)
		Expect(res[0].Err).NotTo(BeNil(), "spawn_agent = %+v, want a refusal", res)
		Expect(res[0].Err.Error()).To(ContainSubstring("not one of your tools"), "spawn_agent = %+v, want a refusal", res)
	})

	It("a created chat names an agent of its owner only", func() {
		r := newAgentRig(sayOK)
		r.save(agents.Agent{ID: "mine", Name: "Mine"})
		r.save(agents.Agent{ID: "bobs", Name: "Bobs", Principal: "bob"})
		for _, id := range []string{"missing", "bobs"} {
			_, err := r.svc.Create(context.Background(), alice, studio.CreateInput{Model: "m", AgentID: id})
			Expect(err).To(MatchError(agents.ErrNotFound), "agent %q: %v, want ErrNotFound", id, err)
		}
	})

	It("an agent supplies the model and create as sets the origin", func() {
		r := newAgentRig(sayOK)
		r.save(agents.Agent{ID: "ag4", Name: "Own model", Model: "m2"})
		rec, err := r.svc.CreateAs(context.Background(), alice, studio.CreateInput{AgentID: "ag4", Title: "Nightly"},
			studio.Origin{Kind: session.OriginTrigger, ID: "trg1"})
		Expect(err).NotTo(HaveOccurred())
		Expect(rec.Model).To(Equal("m2"), "record = %+v", rec)
		Expect(rec.Origin).To(Equal(session.OriginTrigger), "record = %+v", rec)
		Expect(rec.OriginID).To(Equal("trg1"), "record = %+v", rec)
		Expect(rec.AgentID).To(Equal("ag4"), "record = %+v", rec)
		got, _ := r.svc.Get(context.Background(), alice, rec.ID)
		Expect(got.Origin).To(Equal(session.OriginTrigger), "stored record = %+v", got)
		Expect(got.OriginID).To(Equal("trg1"), "stored record = %+v", got)
		_, err = r.svc.CreateAs(context.Background(), alice, studio.CreateInput{}, studio.Origin{})
		Expect(err).To(MatchError(studio.ErrNoModel), "no model and no agent: %v", err)
	})

	It("without an agent store an agent is refused", func() {
		svc, _ := newToolService(sayOK)
		_, err := svc.Create(context.Background(), alice, studio.CreateInput{Model: "m", AgentID: "x"})
		Expect(err).To(MatchError(studio.ErrAgentsOff), "got %v, want ErrAgentsOff", err)
	})
})
