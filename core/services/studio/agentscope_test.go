package studio_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/pkg/functions"

	mcpendpoint "github.com/mudler/LocalAI/core/http/endpoints/mcp"
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

func newAgentRig(t *testing.T, script func(workspace.Actor, string) []studiotest.ToolStep) *agentRig {
	t.Helper()
	f := newFiles(t)
	writeSkill(t, f, alice, "/.skills/review/SKILL.md", "review", "check a change before it ships")
	writeSkill(t, f, alice, "/.skills/house-style/SKILL.md", "house-style", "how we write")
	as, err := agents.NewJSON("")
	if err != nil {
		t.Fatal(err)
	}
	fa := &fakeAddons{tools: map[string][]studio.AddonTool{alice: {addonTool("office", "read-doc"), addonTool("sandbox", "bash")}}}
	fc := &fakeCollections{mine: map[string]map[string][]studio.CollectionHit{alice: {"docs": nil, "secret": nil}}}
	fm := &fakeMCP{fns: []functions.Function{lookupFn()}}
	ask := &mcpAsk{}
	svc, h := newToolService(t, script, func(d *studio.Deps) {
		d.Files, d.AddonTools, d.Collections, d.Agents = f, fa, fc, as
		d.MCP = func(_ context.Context, user, model string, only []string, limited bool) (mcpendpoint.ToolExecutor, []string) {
			ask.only, ask.limited = only, limited
			return fm, ask.missing
		}
	})
	return &agentRig{svc: svc, h: h, agents: as, ask: ask}
}

func (r *agentRig) save(t *testing.T, a agents.Agent) {
	t.Helper()
	if a.Principal == "" {
		a.Principal = alice
	}
	a.Created = time.Now().UTC()
	if err := r.agents.Put(context.Background(), a); err != nil {
		t.Fatal(err)
	}
}

func (r *agentRig) chatWith(t *testing.T, agentID string) string {
	t.Helper()
	rec, err := r.svc.Create(context.Background(), alice, studio.CreateInput{Model: "m", AgentID: agentID})
	if err != nil {
		t.Fatal(err)
	}
	return rec.ID
}

var sayOK = func(workspace.Actor, string) []studiotest.ToolStep { return []studiotest.ToolStep{{Say: "ok"}} }

func TestAChatWithAnAgentGetsOnlyWhatTheAgentGrants(t *testing.T) {
	r := newAgentRig(t, func(_ workspace.Actor, text string) []studiotest.ToolStep {
		if text == "peek" {
			return []studiotest.ToolStep{{Tool: "search_collection", Args: `{"collection":"secret","query":"x"}`}, {Say: "ok"}}
		}
		return []studiotest.ToolStep{{Say: "ok"}}
	})
	r.save(t, agents.Agent{ID: "ag1", Name: "French reviewer", Instructions: "Always answer in French.",
		MCPServers: []string{"crm"}, Addons: []string{"office"}, Collections: []string{"docs"}, Skills: []string{"review"}})
	id := r.chatWith(t, "ag1")
	sent(t, r.svc, alice, id, "peek")
	idle(t, r.svc, alice, id)

	names := r.h.ToolNames(id)
	if !slices.Contains(names, "addon__office__read-doc") || slices.Contains(names, "addon__sandbox__bash") {
		t.Errorf("add-on tools = %v, want office only", names)
	}
	if !slices.Contains(names, "crm_lookup") || !r.ask.limited || !slices.Equal(r.ask.only, []string{"crm"}) {
		t.Errorf("MCP: tools %v, asked %+v, want the crm server only", names, r.ask)
	}
	prompt := r.h.TurnPrompt(id)
	if !strings.Contains(prompt, "Always answer in French.") || !strings.Contains(prompt, "review") || strings.Contains(prompt, "house-style") {
		t.Errorf("prompt = %q, want the instructions and the review skill only", prompt)
	}
	res := r.h.ResultsFor("search_collection")
	if len(res) != 1 || res[0].Err == nil || !strings.Contains(res[0].Err.Error(), "collection not found") {
		t.Errorf("a collection the agent was not given: %+v, want not found", res)
	}
	rec, _ := r.svc.Get(context.Background(), alice, id)
	if rec.AgentID != "ag1" {
		t.Errorf("AgentID = %q", rec.AgentID)
	}
}

func TestAnAgentGrantingNothingGivesNoOptionalTools(t *testing.T) {
	r := newAgentRig(t, sayOK)
	r.save(t, agents.Agent{ID: "ag0", Name: "Bare"})
	id := r.chatWith(t, "ag0")
	sent(t, r.svc, alice, id, "hi")
	idle(t, r.svc, alice, id)
	for _, n := range r.h.ToolNames(id) {
		if strings.HasPrefix(n, "addon__") || n == "crm_lookup" || n == "list_collections" || n == "search_collection" {
			t.Errorf("an agent with empty lists got %s", n)
		}
	}
	if strings.Contains(r.h.TurnPrompt(id), "review") {
		t.Errorf("an agent with no skills got a skill index: %q", r.h.TurnPrompt(id))
	}
}

func TestAGrantTheUserLostIsAbsentWithANote(t *testing.T) {
	r := newAgentRig(t, sayOK)
	r.ask.missing = []string{"mail"}
	r.save(t, agents.Agent{ID: "ag2", Name: "Stale", Addons: []string{"office", "gone"}, Collections: []string{"docs", "vanished"},
		Skills: []string{"review", "nope"}, MCPServers: []string{"crm", "mail"}})
	id := r.chatWith(t, "ag2")
	sent(t, r.svc, alice, id, "hi")
	idle(t, r.svc, alice, id)
	if got := turn(t, r.svc, alice, id).Last; got != store.TurnDone {
		t.Fatalf("turn = %v, want done", got)
	}
	msg := strings.Join(r.h.Sent(id), "\n")
	for _, want := range []string{"unavailable this turn", "add-on gone", "collection vanished", "skill nope", "MCP server mail"} {
		if !strings.Contains(msg, want) {
			t.Errorf("note lacks %q: %q", want, msg)
		}
	}
	if !slices.Contains(r.h.ToolNames(id), "addon__office__read-doc") {
		t.Errorf("the usable grant is missing: %v", r.h.ToolNames(id))
	}
}

func TestASubAgentCannotGetWhatItsParentLacks(t *testing.T) {
	r := newAgentRig(t, func(_ workspace.Actor, text string) []studiotest.ToolStep {
		return []studiotest.ToolStep{{Tool: "spawn_agent", Args: `{"task":"t","tools":["addon__sandbox__bash"]}`}, {Say: "ok"}}
	})
	r.save(t, agents.Agent{ID: "ag3", Name: "Office only", Addons: []string{"office"}})
	id := r.chatWith(t, "ag3")
	sent(t, r.svc, alice, id, "go")
	idle(t, r.svc, alice, id)
	res := r.h.ResultsFor("spawn_agent")
	if len(res) != 1 || res[0].Err == nil || !strings.Contains(res[0].Err.Error(), "not one of your tools") {
		t.Fatalf("spawn_agent = %+v, want a refusal", res)
	}
}

func TestACreatedChatNamesAnAgentOfItsOwnerOnly(t *testing.T) {
	r := newAgentRig(t, sayOK)
	r.save(t, agents.Agent{ID: "mine", Name: "Mine"})
	r.save(t, agents.Agent{ID: "bobs", Name: "Bobs", Principal: "bob"})
	for _, id := range []string{"missing", "bobs"} {
		if _, err := r.svc.Create(context.Background(), alice, studio.CreateInput{Model: "m", AgentID: id}); !errors.Is(err, agents.ErrNotFound) {
			t.Errorf("agent %q: %v, want ErrNotFound", id, err)
		}
	}
}

func TestAnAgentSuppliesTheModelAndCreateAsSetsTheOrigin(t *testing.T) {
	r := newAgentRig(t, sayOK)
	r.save(t, agents.Agent{ID: "ag4", Name: "Own model", Model: "m2"})
	rec, err := r.svc.CreateAs(context.Background(), alice, studio.CreateInput{AgentID: "ag4", Title: "Nightly"},
		studio.Origin{Kind: session.OriginTrigger, ID: "trg1"})
	if err != nil {
		t.Fatal(err)
	}
	if rec.Model != "m2" || rec.Origin != session.OriginTrigger || rec.OriginID != "trg1" || rec.AgentID != "ag4" {
		t.Fatalf("record = %+v", rec)
	}
	got, _ := r.svc.Get(context.Background(), alice, rec.ID)
	if got.Origin != session.OriginTrigger || got.OriginID != "trg1" {
		t.Fatalf("stored record = %+v", got)
	}
	if _, err := r.svc.CreateAs(context.Background(), alice, studio.CreateInput{}, studio.Origin{}); !errors.Is(err, studio.ErrNoModel) {
		t.Fatalf("no model and no agent: %v", err)
	}
}

func TestWithoutAnAgentStoreAnAgentIsRefused(t *testing.T) {
	svc, _ := newToolService(t, sayOK)
	if _, err := svc.Create(context.Background(), alice, studio.CreateInput{Model: "m", AgentID: "x"}); !errors.Is(err, studio.ErrAgentsOff) {
		t.Fatalf("got %v, want ErrAgentsOff", err)
	}
}
