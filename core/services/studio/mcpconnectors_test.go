package studio_test

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"

	mcpendpoint "github.com/mudler/LocalAI/core/http/endpoints/mcp"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/mcpconns"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/pkg/functions"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
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

func newMCPConnRig(script func(workspace.Actor, string) []studiotest.ToolStep, fc *fakeMCPConnectors, modelFns ...functions.Function) *mcpConnRig {
	as, err := agents.NewJSON("")
	Expect(err).NotTo(HaveOccurred())
	fm := &fakeMCP{fns: modelFns}
	ask := &mcpAsk{}
	asks := 0
	servers := []studio.ModelMCPServer{{Name: "crm", Auto: true}, {Name: "mail", Auto: true}, {Name: "manual", Auto: false}}
	svc, h := newToolService(script, func(d *studio.Deps) {
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

func (r *mcpConnRig) turn(id, text string) {
	sent(r.svc, alice, id, text)
	idle(r.svc, alice, id)
}

var _ = Describe("mcpconnectors", func() {
	It("a plain chat gets the enabled connectors of its user only", func() {
		fc := newFakeMCPConnectors()
		r := newMCPConnRig(func(workspace.Actor, string) []studiotest.ToolStep {
			return []studiotest.ToolStep{{Tool: "doc_search", Args: `{}`}, {Say: "ok"}}
		}, fc)
		id := create(r.svc, alice)
		r.turn(id, "hi")
		names := r.h.ToolNames(id)
		Expect(names).To(ContainElement("doc_search"), "tools = %v, want both enabled connectors", names)
		Expect(names).To(ContainElement("mail_send"), "tools = %v, want both enabled connectors", names)
		for _, bad := range []string{"never", "bob_tool"} {
			Expect(names).NotTo(ContainElement(bad), "tools = %v hold %s", names, bad)
		}
		res := r.h.ResultsFor("doc_search")
		Expect(res).To(HaveLen(1), "result = %+v", res)
		Expect(res[0].Out).To(Equal("from c1"), "result = %+v", res)
		Expect(fc.calls).To(ContainElement("c1/doc_search"), "calls = %v", fc.calls)
	})

	It("a connector tool cannot take a built in or an addon name", func() {
		fc := newFakeMCPConnectors()
		fc.tools["c1"] = []string{"spawn_agent", "addon__office__read-doc", "fine"}
		r := newMCPConnRig(sayOK, fc)
		id := create(r.svc, alice)
		r.turn(id, "hi")
		names := r.h.ToolNames(id)
		Expect(names).To(ContainElement("fine"), "tools = %v", names)
		Expect(names).NotTo(ContainElement("addon__office__read-doc"), "tools = %v", names)
		n := 0
		for _, x := range names {
			if x == "spawn_agent" {
				n++
			}
		}
		Expect(n).To(BeNumerically("==", 1), "spawn_agent appears %d times in %v; connector calls %v", n, names, fc.calls)
		Expect(fc.calls).To(BeEmpty(), "spawn_agent appears %d times in %v; connector calls %v", n, names, fc.calls)
	})

	It("two connectors with the same tool name are both prefixed", func() {
		fc := newFakeMCPConnectors()
		fc.tools["c1"], fc.tools["c2"] = []string{"dup", "only_docs"}, []string{"dup"}
		r := newMCPConnRig(sayOK, fc, lookupFn())
		fc.tools["c2"] = []string{"dup", "crm_lookup"} // the second collides with the model's tool too
		id := create(r.svc, alice)
		r.turn(id, "hi")
		names := r.h.ToolNames(id)
		for _, want := range []string{"docs__dup", "mail__dup", "only_docs", "mail__crm_lookup", "crm_lookup"} {
			Expect(names).To(ContainElement(want), "tools %v lack %s", names, want)
		}
		Expect(names).NotTo(ContainElement("dup"), "an ambiguous name stayed bare: %v", names)
	})

	It("a connector tool output is capped", func() {
		fc := newFakeMCPConnectors()
		fc.out = strings.Repeat("x", session.MaxOutput*3)
		r := newMCPConnRig(func(workspace.Actor, string) []studiotest.ToolStep {
			return []studiotest.ToolStep{{Tool: "doc_search", Args: `{}`}, {Say: "ok"}}
		}, fc)
		id := create(r.svc, alice)
		r.turn(id, "hi")
		res := r.h.ResultsFor("doc_search")
		Expect(res).To(HaveLen(1), "result of %d bytes: %.80q", len(res[0].Out), res[0].Out)
		Expect(len(res[0].Out)).To(BeNumerically("<=", session.MaxOutput+200), "result of %d bytes: %.80q", len(res[0].Out), res[0].Out)
		Expect(res[0].Out).To(ContainSubstring("cut"), "result of %d bytes: %.80q", len(res[0].Out), res[0].Out)
	})

	It("an agent uses the connectors it names and notes the others", func() {
		fc := newFakeMCPConnectors()
		r := newMCPConnRig(sayOK, fc, lookupFn())
		r.save(agents.Agent{ID: "ag", Name: "A", MCPServers: []string{"crm", "connector:c1", "connector:c3", "connector:gone", "connector:b1"}})
		id := r.chatWith("ag")
		r.turn(id, "hi")
		names := r.h.ToolNames(id)
		Expect(names).To(ContainElement("doc_search"), "tools = %v, want only the granted connector c1", names)
		Expect(names).NotTo(ContainElement("mail_send"), "tools = %v, want only the granted connector c1", names)
		Expect(names).NotTo(ContainElement("never"), "tools = %v, want only the granted connector c1", names)
		Expect(names).NotTo(ContainElement("bob_tool"), "tools = %v, want only the granted connector c1", names)
		Expect(r.ask.limited).To(BeTrue(), "the model hook was asked %+v, want the unprefixed name only", r.ask)
		Expect(r.ask.only).To(Equal([]string{"crm"}), "the model hook was asked %+v, want the unprefixed name only", r.ask)
		msg := strings.Join(r.h.Sent(id), "\n")
		for _, want := range []string{"unavailable this turn", "MCP connector Off", "MCP connector gone", "MCP connector b1"} {
			Expect(msg).To(ContainSubstring(want), "note lacks %q: %q", want, msg)
		}
		Expect(msg).NotTo(ContainSubstring("Secret"), "the note names a foreign connector: %q", msg)
	})

	It("an agent with only connectors does not ask for model servers", func() {
		fc := newFakeMCPConnectors()
		r := newMCPConnRig(sayOK, fc, lookupFn())
		r.save(agents.Agent{ID: "ag", Name: "A", MCPServers: []string{"connector:c2"}})
		id := r.chatWith("ag")
		r.turn(id, "hi")
		Expect(*r.asks).To(BeNumerically("==", 0), "the model hook was called %d times", *r.asks)
		names := r.h.ToolNames(id)
		Expect(names).To(ContainElement("mail_send"), "tools = %v", names)
		Expect(names).NotTo(ContainElement("doc_search"), "tools = %v", names)
	})

	It("MCP switches are checked bounded and belong to the owner", func() {
		r := newMCPConnRig(sayOK, newFakeMCPConnectors())
		id := create(r.svc, alice)
		ctx := context.Background()
		for _, bad := range [][]string{{""}, {"connector:"}, {"connector:a b"}, {"connector:../x"}, {strings.Repeat("s", 300)}, {"bad\nname"}} {
			Expect(r.svc.SetMcpOff(ctx, alice, id, bad)).To(MatchError(studio.ErrBadMCPSwitch))
		}
		many := make([]string, 201)
		for i := range many {
			many[i] = "s" + strings.Repeat("a", i%5) + string(rune('a'+i%26)) + strings.Repeat("b", i/26)
		}
		Expect(r.svc.SetMcpOff(ctx, alice, id, many)).To(MatchError(studio.ErrBadMCPSwitch))
		Expect(r.svc.SetMcpOff(ctx, "bob", id, []string{"crm"})).To(MatchError(session.ErrNotFound))
		Expect(r.svc.SetMcpOff(ctx, alice, id, []string{"crm", "crm", "connector:c1"})).To(Succeed())
		rec, _ := r.svc.Get(ctx, alice, id)
		_ = rec
		panel, err := r.svc.Tools(ctx, alice, id)
		Expect(err).NotTo(HaveOccurred(), "panel = %+v, %v, want the two switches deduplicated", panel, err)
		Expect(panel.Off).To(HaveLen(2), "panel = %+v, %v, want the two switches deduplicated", panel, err)
	})

	It("a switched off connector and model server leave the chat at the next turn", func() {
		fc := newFakeMCPConnectors()
		r := newMCPConnRig(sayOK, fc, lookupFn())
		id := create(r.svc, alice)
		r.turn(id, "one")
		Expect(r.h.ToolNames(id)).To(ContainElement("doc_search"), "before: tools %v, asked %+v", r.h.ToolNames(id), r.ask)
		Expect(r.ask.limited).To(BeFalse(), "before: tools %v, asked %+v", r.h.ToolNames(id), r.ask)
		Expect(r.svc.SetMcpOff(context.Background(), alice, id, []string{"connector:c1", "crm"})).To(Succeed())
		r.turn(id, "two")
		names := r.h.ToolNames(id)
		Expect(names).NotTo(ContainElement("doc_search"), "tools after switching Docs off = %v", names)
		Expect(names).To(ContainElement("mail_send"), "tools after switching Docs off = %v", names)
		// The plain chat's own servers are the auto ones; with crm off, only mail is asked for.
		Expect(r.ask.limited).To(BeTrue(), "the model hook was asked %+v, want the auto servers less crm", r.ask)
		Expect(r.ask.only).To(Equal([]string{"mail"}), "the model hook was asked %+v, want the auto servers less crm", r.ask)
	})

	It("an agent chat switches off within its grants and never adds beyond them", func() {
		fc := newFakeMCPConnectors()
		r := newMCPConnRig(sayOK, fc, lookupFn())
		r.save(agents.Agent{ID: "ag", Name: "A", MCPServers: []string{"crm", "connector:c1", "connector:c2"}})
		id := r.chatWith("ag")
		// c3 is not granted: switching something off cannot add it; "manual" is not granted either.
		Expect(r.svc.SetMcpOff(context.Background(), alice, id, []string{"connector:c1", "connector:c3"})).To(Succeed())
		r.turn(id, "hi")
		names := r.h.ToolNames(id)
		Expect(names).NotTo(ContainElement("doc_search"), "tools = %v", names)
		Expect(names).NotTo(ContainElement("never"), "tools = %v", names)
		Expect(names).To(ContainElement("mail_send"), "tools = %v", names)
		Expect(r.ask.only).To(Equal([]string{"crm"}), "asked %+v", r.ask)
		panel, err := r.svc.Tools(context.Background(), alice, id)
		Expect(err).NotTo(HaveOccurred())
		var ids []string
		for _, c := range panel.Connectors {
			ids = append(ids, c.ID)
		}
		slices.Sort(ids)
		Expect(ids).To(Equal([]string{"c1", "c2"}), "the panel of an agent chat lists %v, want the granted connectors only", ids)
		for _, s := range panel.Servers {
			Expect(s.Name).To(Equal("crm"), "the panel lists the model server %q the agent does not grant", s.Name)
		}
	})

	It("the panel lists everything a chat can use and marks what is unusable", func() {
		fc := newFakeMCPConnectors()
		r := newMCPConnRig(sayOK, fc)
		id := create(r.svc, alice)
		Expect(r.svc.SetMcpOff(context.Background(), alice, id, []string{"mail"})).To(Succeed())
		p, err := r.svc.Tools(context.Background(), alice, id)
		Expect(err).NotTo(HaveOccurred())
		by := map[string]studio.ModelMCPServerSwitch{}
		for _, s := range p.Servers {
			by[s.Name] = s
		}
		Expect(by["crm"].Usable).To(BeTrue(), "servers = %+v", p.Servers)
		Expect(by["crm"].Off).To(BeFalse(), "servers = %+v", p.Servers)
		Expect(by["mail"].Off).To(BeTrue(), "servers = %+v", p.Servers)
		Expect(by["manual"].Usable).To(BeFalse(), "servers = %+v", p.Servers)
		Expect(by["manual"].Reason).NotTo(Equal(""), "servers = %+v", p.Servers)
		cs := map[string]studio.ConnectorSwitch{}
		for _, c := range p.Connectors {
			cs[c.ID] = c
		}
		Expect(cs).To(HaveLen(3), "connectors = %+v", p.Connectors)
		Expect(cs["c1"].Usable).To(BeTrue(), "connectors = %+v", p.Connectors)
		Expect(cs["c3"].Usable).To(BeFalse(), "connectors = %+v", p.Connectors)
		Expect(cs["c3"].Reason).NotTo(Equal(""), "connectors = %+v", p.Connectors)
		Expect(cs["b1"].ID).To(Equal(""), "connectors = %+v", p.Connectors)
	})

	It("a trigger run without an agent copies the MCP switches of its source chat", func() {
		r := newTrigRig(saySummary)
		ctx := context.Background()
		src, err := r.svc.Create(ctx, alice, studio.CreateInput{Model: "src-model"})
		Expect(err).NotTo(HaveOccurred())
		Expect(r.svc.SetMcpOff(ctx, alice, src.ID, []string{"connector:c1", "crm"})).To(Succeed())
		tr := r.schedule(studio.TriggerInput{SourceChatID: src.ID})
		r.due()
		run := r.waitEnded(tr.ID)
		rec := r.chatOf(run)
		Expect(rec.McpOff).To(Equal([]string{"connector:c1", "crm"}), "McpOff of the run = %v", rec.McpOff)
	})
})
