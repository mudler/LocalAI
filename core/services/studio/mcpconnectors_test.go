package studio_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	mcpendpoint "github.com/mudler/LocalAI/core/http/endpoints/mcp"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/mcpconns"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/pkg/functions"
)

// fakeMCPConnectors stands in for the user's MCP connectors: each user sees their
// own, and a switched-off or foreign one is a note, as the real service does.
type fakeMCPConnectors struct {
	mu    sync.Mutex
	conns map[string][]mcpconns.Connector // by user
	tools map[string][]string             // tool names by connector id
	out   string
	asked []string // the refs of every Resolve, flattened
	calls []string // connector id + "/" + tool
}

func (f *fakeMCPConnectors) List(_ context.Context, user string) ([]mcpconns.Connector, error) {
	return f.conns[user], nil
}

func (f *fakeMCPConnectors) Resolve(_ context.Context, user string, refs []string, all bool) ([]mcpconns.Tool, []string) {
	f.mu.Lock()
	f.asked = append(f.asked, refs...)
	f.mu.Unlock()
	var tools []mcpconns.Tool
	var missing []string
	use := func(c mcpconns.Connector) {
		for _, name := range f.tools[c.ID] {
			id := c.ID
			tools = append(tools, mcpconns.Tool{ConnectorID: id, Connector: c.Name, Name: name, Schema: json.RawMessage(`{"type":"object"}`),
				Run: func(context.Context, json.RawMessage) (string, error) {
					f.mu.Lock()
					f.calls = append(f.calls, id+"/"+name)
					f.mu.Unlock()
					if f.out != "" {
						return f.out, nil
					}
					return "from " + id, nil
				}})
		}
	}
	if all {
		for _, c := range f.conns[user] {
			if c.Enabled {
				use(c)
			}
		}
		return tools, nil
	}
	for _, ref := range refs {
		var found *mcpconns.Connector
		for i, c := range f.conns[user] {
			if c.ID == ref {
				found = &f.conns[user][i]
			}
		}
		switch {
		case found == nil:
			missing = append(missing, "MCP connector "+ref+" (not found)")
		case !found.Enabled:
			missing = append(missing, "MCP connector "+found.Name+" (switched off)")
		default:
			use(*found)
		}
	}
	return tools, missing
}

func mcpConn(id, name string, on bool) mcpconns.Connector {
	return mcpconns.Connector{ID: id, Owner: alice, Name: name, Transport: mcpconns.TransportHTTP, URL: "https://x.example", Enabled: on}
}

func newFakeMCPConnectors() *fakeMCPConnectors {
	return &fakeMCPConnectors{
		conns: map[string][]mcpconns.Connector{
			alice: {mcpConn("c1", "Docs", true), mcpConn("c2", "Mail", true), mcpConn("c3", "Off", false)},
			"bob": {mcpConn("b1", "Secret", true)},
		},
		tools: map[string][]string{"c1": {"doc_search"}, "c2": {"mail_send"}, "c3": {"never"}, "b1": {"bob_tool"}},
	}
}

type mcpConnRig struct {
	*agentRig
	fc *fakeMCPConnectors
	// asks counts calls of the model MCP hook.
	asks *int
	// servers is what the model has.
	servers *[]studio.ModelMCPServer
}

func newMCPConnRig(t *testing.T, script func(workspace.Actor, string) []studiotest.ToolStep, fc *fakeMCPConnectors, modelFns ...functions.Function) *mcpConnRig {
	t.Helper()
	as, err := agents.NewJSON("")
	if err != nil {
		t.Fatal(err)
	}
	fm := &fakeMCP{fns: modelFns}
	ask := &mcpAsk{}
	asks := 0
	servers := []studio.ModelMCPServer{{Name: "crm", Auto: true}, {Name: "mail", Auto: true}, {Name: "manual", Auto: false}}
	svc, h := newToolService(t, script, func(d *studio.Deps) {
		d.Agents, d.Connectors = as, fc
		d.MCPServers = func(_ context.Context, user, model string) []studio.ModelMCPServer {
			if user != alice {
				return nil
			}
			return servers
		}
		d.MCP = func(_ context.Context, user, model string, only []string, limited bool) (mcpendpoint.ToolExecutor, []string) {
			asks++
			ask.only, ask.limited = only, limited
			if len(modelFns) == 0 {
				return nil, nil
			}
			return fm, nil
		}
	})
	return &mcpConnRig{agentRig: &agentRig{svc: svc, h: h, agents: as, ask: ask}, fc: fc, asks: &asks, servers: &servers}
}

func (r *mcpConnRig) turn(t *testing.T, id, text string) {
	t.Helper()
	sent(t, r.svc, alice, id, text)
	idle(t, r.svc, alice, id)
}

func TestAPlainChatGetsTheEnabledConnectorsOfItsUserOnly(t *testing.T) {
	fc := newFakeMCPConnectors()
	r := newMCPConnRig(t, func(workspace.Actor, string) []studiotest.ToolStep {
		return []studiotest.ToolStep{{Tool: "doc_search", Args: `{}`}, {Say: "ok"}}
	}, fc)
	id := create(t, r.svc, alice)
	r.turn(t, id, "hi")
	names := r.h.ToolNames(id)
	if !slices.Contains(names, "doc_search") || !slices.Contains(names, "mail_send") {
		t.Fatalf("tools = %v, want both enabled connectors", names)
	}
	for _, bad := range []string{"never", "bob_tool"} {
		if slices.Contains(names, bad) {
			t.Fatalf("tools = %v hold %s", names, bad)
		}
	}
	if res := r.h.ResultsFor("doc_search"); len(res) != 1 || res[0].Out != "from c1" {
		t.Fatalf("result = %+v", res)
	}
	if !slices.Contains(fc.calls, "c1/doc_search") {
		t.Fatalf("calls = %v", fc.calls)
	}
}

func TestAConnectorToolCannotTakeABuiltInOrAnAddonName(t *testing.T) {
	fc := newFakeMCPConnectors()
	fc.tools["c1"] = []string{"spawn_agent", "addon__office__read-doc", "fine"}
	r := newMCPConnRig(t, sayOK, fc)
	id := create(t, r.svc, alice)
	r.turn(t, id, "hi")
	names := r.h.ToolNames(id)
	if !slices.Contains(names, "fine") || slices.Contains(names, "addon__office__read-doc") {
		t.Fatalf("tools = %v", names)
	}
	n := 0
	for _, x := range names {
		if x == "spawn_agent" {
			n++
		}
	}
	if n != 1 || len(fc.calls) != 0 {
		t.Fatalf("spawn_agent appears %d times in %v; connector calls %v", n, names, fc.calls)
	}
}

func TestTwoConnectorsWithTheSameToolNameAreBothPrefixed(t *testing.T) {
	fc := newFakeMCPConnectors()
	fc.tools["c1"], fc.tools["c2"] = []string{"dup", "only_docs"}, []string{"dup"}
	r := newMCPConnRig(t, sayOK, fc, lookupFn())
	fc.tools["c2"] = []string{"dup", "crm_lookup"} // the second collides with the model's tool too
	id := create(t, r.svc, alice)
	r.turn(t, id, "hi")
	names := r.h.ToolNames(id)
	for _, want := range []string{"docs__dup", "mail__dup", "only_docs", "mail__crm_lookup", "crm_lookup"} {
		if !slices.Contains(names, want) {
			t.Errorf("tools %v lack %s", names, want)
		}
	}
	if slices.Contains(names, "dup") {
		t.Errorf("an ambiguous name stayed bare: %v", names)
	}
}

func TestAConnectorToolOutputIsCapped(t *testing.T) {
	fc := newFakeMCPConnectors()
	fc.out = strings.Repeat("x", session.MaxOutput*3)
	r := newMCPConnRig(t, func(workspace.Actor, string) []studiotest.ToolStep {
		return []studiotest.ToolStep{{Tool: "doc_search", Args: `{}`}, {Say: "ok"}}
	}, fc)
	id := create(t, r.svc, alice)
	r.turn(t, id, "hi")
	res := r.h.ResultsFor("doc_search")
	if len(res) != 1 || len(res[0].Out) > session.MaxOutput+200 || !strings.Contains(res[0].Out, "cut") {
		t.Fatalf("result of %d bytes: %.80q", len(res[0].Out), res[0].Out)
	}
}

func TestAnAgentUsesTheConnectorsItNamesAndNotesTheOthers(t *testing.T) {
	fc := newFakeMCPConnectors()
	r := newMCPConnRig(t, sayOK, fc, lookupFn())
	r.save(t, agents.Agent{ID: "ag", Name: "A", MCPServers: []string{"crm", "connector:c1", "connector:c3", "connector:gone", "connector:b1"}})
	id := r.chatWith(t, "ag")
	r.turn(t, id, "hi")
	names := r.h.ToolNames(id)
	if !slices.Contains(names, "doc_search") || slices.Contains(names, "mail_send") || slices.Contains(names, "never") || slices.Contains(names, "bob_tool") {
		t.Fatalf("tools = %v, want only the granted connector c1", names)
	}
	if !r.ask.limited || !slices.Equal(r.ask.only, []string{"crm"}) {
		t.Fatalf("the model hook was asked %+v, want the unprefixed name only", r.ask)
	}
	msg := strings.Join(r.h.Sent(id), "\n")
	for _, want := range []string{"unavailable this turn", "MCP connector Off", "MCP connector gone", "MCP connector b1"} {
		if !strings.Contains(msg, want) {
			t.Errorf("note lacks %q: %q", want, msg)
		}
	}
	if strings.Contains(msg, "Secret") {
		t.Errorf("the note names a foreign connector: %q", msg)
	}
}

func TestAnAgentWithOnlyConnectorsDoesNotAskForModelServers(t *testing.T) {
	fc := newFakeMCPConnectors()
	r := newMCPConnRig(t, sayOK, fc, lookupFn())
	r.save(t, agents.Agent{ID: "ag", Name: "A", MCPServers: []string{"connector:c2"}})
	id := r.chatWith(t, "ag")
	r.turn(t, id, "hi")
	if *r.asks != 0 {
		t.Fatalf("the model hook was called %d times", *r.asks)
	}
	if names := r.h.ToolNames(id); !slices.Contains(names, "mail_send") || slices.Contains(names, "doc_search") {
		t.Fatalf("tools = %v", names)
	}
}

func TestMCPSwitchesAreCheckedBoundedAndBelongToTheOwner(t *testing.T) {
	r := newMCPConnRig(t, sayOK, newFakeMCPConnectors())
	id := create(t, r.svc, alice)
	ctx := context.Background()
	for _, bad := range [][]string{{""}, {"connector:"}, {"connector:a b"}, {"connector:../x"}, {strings.Repeat("s", 300)}, {"bad\nname"}} {
		if err := r.svc.SetMcpOff(ctx, alice, id, bad); !errors.Is(err, studio.ErrBadMCPSwitch) {
			t.Errorf("SetMcpOff(%q) = %v, want ErrBadMCPSwitch", bad, err)
		}
	}
	many := make([]string, 201)
	for i := range many {
		many[i] = "s" + strings.Repeat("a", i%5) + string(rune('a'+i%26)) + strings.Repeat("b", i/26)
	}
	if err := r.svc.SetMcpOff(ctx, alice, id, many); !errors.Is(err, studio.ErrBadMCPSwitch) {
		t.Errorf("too many switches = %v", err)
	}
	if err := r.svc.SetMcpOff(ctx, "bob", id, []string{"crm"}); !errors.Is(err, session.ErrNotFound) {
		t.Errorf("another user's SetMcpOff = %v, want ErrNotFound", err)
	}
	if err := r.svc.SetMcpOff(ctx, alice, id, []string{"crm", "crm", "connector:c1"}); err != nil {
		t.Fatal(err)
	}
	rec, _ := r.svc.Get(ctx, alice, id)
	_ = rec
	panel, err := r.svc.Tools(ctx, alice, id)
	if err != nil || len(panel.Off) != 2 {
		t.Fatalf("panel = %+v, %v, want the two switches deduplicated", panel, err)
	}
}

func TestASwitchedOffConnectorAndModelServerLeaveTheChatAtTheNextTurn(t *testing.T) {
	fc := newFakeMCPConnectors()
	r := newMCPConnRig(t, sayOK, fc, lookupFn())
	id := create(t, r.svc, alice)
	r.turn(t, id, "one")
	if !slices.Contains(r.h.ToolNames(id), "doc_search") || r.ask.limited {
		t.Fatalf("before: tools %v, asked %+v", r.h.ToolNames(id), r.ask)
	}
	if err := r.svc.SetMcpOff(context.Background(), alice, id, []string{"connector:c1", "crm"}); err != nil {
		t.Fatal(err)
	}
	r.turn(t, id, "two")
	names := r.h.ToolNames(id)
	if slices.Contains(names, "doc_search") || !slices.Contains(names, "mail_send") {
		t.Fatalf("tools after switching Docs off = %v", names)
	}
	// The plain chat's own servers are the auto ones; with crm off, only mail is asked for.
	if !r.ask.limited || !slices.Equal(r.ask.only, []string{"mail"}) {
		t.Fatalf("the model hook was asked %+v, want the auto servers less crm", r.ask)
	}
}

func TestAnAgentChatSwitchesOffWithinItsGrantsAndNeverAddsBeyondThem(t *testing.T) {
	fc := newFakeMCPConnectors()
	r := newMCPConnRig(t, sayOK, fc, lookupFn())
	r.save(t, agents.Agent{ID: "ag", Name: "A", MCPServers: []string{"crm", "connector:c1", "connector:c2"}})
	id := r.chatWith(t, "ag")
	// c3 is not granted: switching something off cannot add it; "manual" is not granted either.
	if err := r.svc.SetMcpOff(context.Background(), alice, id, []string{"connector:c1", "connector:c3"}); err != nil {
		t.Fatal(err)
	}
	r.turn(t, id, "hi")
	names := r.h.ToolNames(id)
	if slices.Contains(names, "doc_search") || slices.Contains(names, "never") || !slices.Contains(names, "mail_send") {
		t.Fatalf("tools = %v", names)
	}
	if !slices.Equal(r.ask.only, []string{"crm"}) {
		t.Fatalf("asked %+v", r.ask)
	}
	panel, err := r.svc.Tools(context.Background(), alice, id)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, c := range panel.Connectors {
		ids = append(ids, c.ID)
	}
	slices.Sort(ids)
	if !slices.Equal(ids, []string{"c1", "c2"}) {
		t.Fatalf("the panel of an agent chat lists %v, want the granted connectors only", ids)
	}
	for _, s := range panel.Servers {
		if s.Name != "crm" {
			t.Fatalf("the panel lists the model server %q the agent does not grant", s.Name)
		}
	}
}

func TestThePanelListsEverythingAChatCanUseAndMarksWhatIsUnusable(t *testing.T) {
	fc := newFakeMCPConnectors()
	r := newMCPConnRig(t, sayOK, fc)
	id := create(t, r.svc, alice)
	if err := r.svc.SetMcpOff(context.Background(), alice, id, []string{"mail"}); err != nil {
		t.Fatal(err)
	}
	p, err := r.svc.Tools(context.Background(), alice, id)
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]studio.ModelMCPServerSwitch{}
	for _, s := range p.Servers {
		by[s.Name] = s
	}
	if !by["crm"].Usable || by["crm"].Off || !by["mail"].Off || by["manual"].Usable || by["manual"].Reason == "" {
		t.Fatalf("servers = %+v", p.Servers)
	}
	cs := map[string]studio.ConnectorSwitch{}
	for _, c := range p.Connectors {
		cs[c.ID] = c
	}
	if len(cs) != 3 || !cs["c1"].Usable || cs["c3"].Usable || cs["c3"].Reason == "" || cs["b1"].ID != "" {
		t.Fatalf("connectors = %+v", p.Connectors)
	}
}

func TestATriggerRunWithoutAnAgentCopiesTheMCPSwitchesOfItsSourceChat(t *testing.T) {
	r := newTrigRig(t, saySummary)
	ctx := context.Background()
	src, err := r.svc.Create(ctx, alice, studio.CreateInput{Model: "src-model"})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.svc.SetMcpOff(ctx, alice, src.ID, []string{"connector:c1", "crm"}); err != nil {
		t.Fatal(err)
	}
	tr := r.schedule(t, studio.TriggerInput{SourceChatID: src.ID})
	r.due(t)
	run := r.waitEnded(t, tr.ID)
	rec := r.chatOf(t, run)
	if !slices.Equal(rec.McpOff, []string{"connector:c1", "crm"}) {
		t.Fatalf("McpOff of the run = %v", rec.McpOff)
	}
}
