package studio_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	mcpendpoint "github.com/mudler/LocalAI/core/http/endpoints/mcp"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/mcpconns"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/session/sessiontest"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/pkg/mcptransport"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const bob = "bob"

type appIn struct {
	Zone string `json:"zone"`
}

// appsMCPServer is a real MCP server over HTTP with a clock app: a tool with a
// view, a tool only the app may call, a tool only the model may call, a plain
// tool, and resources that are declared, undeclared, too large and not HTML.
func appsMCPServer() *httptest.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: "clock", Version: "1"}, nil)
	add := func(name string, meta mcp.Meta, text string) {
		mcp.AddTool(srv, &mcp.Tool{Name: name, Meta: meta}, func(_ context.Context, _ *mcp.CallToolRequest, in appIn) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text + in.Zone}}, StructuredContent: map[string]any{"zone": in.Zone}}, nil, nil
		})
	}
	add("get-time", mcp.Meta{"ui": map[string]any{"resourceUri": "ui://clock/app.html"}}, "12:00 ")
	add("refresh", mcp.Meta{"ui": map[string]any{"resourceUri": "ui://clock/app.html", "visibility": []any{"app"}}}, "refreshed ")
	add("model_only", mcp.Meta{"ui": map[string]any{"resourceUri": "ui://clock/app.html", "visibility": []any{"model"}}}, "model ")
	add("plain", nil, "plain ")
	add("big", mcp.Meta{"ui": map[string]any{"resourceUri": "ui://big/app.html"}}, "big ")
	add("json", mcp.Meta{"ui": map[string]any{"resourceUri": "ui://json/app.html"}}, "json ")
	page := func(uri, mime, body string, meta mcp.Meta) {
		srv.AddResource(&mcp.Resource{URI: uri, Name: uri, MIMEType: mime}, func(_ context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: req.Params.URI, MIMEType: mime, Text: body, Meta: meta}}}, nil
		})
	}
	page("ui://clock/app.html", "text/html;profile=mcp-app", "<p>clock</p>", mcp.Meta{"ui": map[string]any{
		"csp": map[string]any{"connectDomains": []any{"https://api.example.com"}}, "prefersBorder": true}})
	page("ui://undeclared/page.html", "text/html", "<p>no tool declares me</p>", nil)
	page("ui://big/app.html", "text/html", strings.Repeat("a", 5<<20), nil)
	page("ui://json/app.html", "application/json", "{}", nil)
	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil)
	ts := httptest.NewServer(h)
	DeferCleanup(func() { ts.CloseClientConnections(); ts.Close() })
	return ts
}

type appsEnv struct {
	svc   *studio.Service
	h     *sessiontest.FakeHarness
	conns *mcpconns.Service
	ag    agents.Store
	conn  mcpconns.Connector // alice's connector
	other mcpconns.Connector // bob's connector, same server
	ts    *httptest.Server
}

func newAppsEnv(turns ...[]sessiontest.Step) *appsEnv {
	ts := appsMCPServer()
	conns, _ := realConnectors(true)
	in := mcpconns.Input{Name: "Clock", Transport: mcpconns.TransportHTTP, URL: ts.URL, Enabled: true}
	token := "tok-clock-secret"
	in.Token = &token
	c, err := conns.Create(context.Background(), alice, in)
	Expect(err).NotTo(HaveOccurred())
	in.Name = "Bob clock"
	o, err := conns.Create(context.Background(), bob, in)
	Expect(err).NotTo(HaveOccurred())
	// The model's own server: one auto, one that only an agent may name.
	cs, err := mcptransport.Connect(context.Background(), mcp.NewClient(&mcp.Implementation{Name: "t", Version: "1"}, nil), ts.URL, nil, 5*time.Second)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = cs.Close() })
	byName := map[string]mcpendpoint.NamedSession{
		"clock":  {Name: "clock", Type: "remote", Session: cs},
		"manual": {Name: "manual", Type: "remote", Session: cs},
	}
	st, _ := store.NewJSON("")
	ag, _ := agents.NewJSON("")
	h := &sessiontest.FakeHarness{Turns: turns}
	svc, err := studio.New(studio.Deps{
		Store: st, Provider: provider.New(), StateDir: GinkgoT().TempDir(), Harness: h, Agents: ag, MCPConnectors: conns,
		MCPServers: func(context.Context, string, string) []studio.ModelMCPServer {
			return []studio.ModelMCPServer{{Name: "clock", Auto: true}, {Name: "manual"}}
		},
		MCP: func(ctx context.Context, user, model string, only []string, limited bool) (mcpendpoint.ToolExecutor, []string) {
			var ns []mcpendpoint.NamedSession
			for name, s := range byName {
				if (limited && slices.Contains(only, name)) || (!limited && name == "clock") {
					ns = append(ns, s)
				}
			}
			if len(ns) == 0 {
				return nil, only
			}
			return mcpendpoint.NewLocalToolExecutor(ctx, ns), nil
		},
	})
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = svc.Close(context.Background()) })
	return &appsEnv{svc: svc, h: h, conns: conns, ag: ag, conn: c, other: o, ts: ts}
}

func (e *appsEnv) ref() string { return mcpconns.Prefix + e.conn.ID }

func toolEntries(svc *studio.Service, user, id string) []session.Entry {
	all, err := svc.Messages(context.Background(), user, id, 0, 100)
	Expect(err).NotTo(HaveOccurred())
	var out []session.Entry
	for _, e := range all {
		if e.Role == session.RoleTool {
			out = append(out, e)
		}
	}
	return out
}

var _ = Describe("mcpapps", func() {
	It("a connector tool with a view gives the transcript an app reference", func() {
		// The model's own server has the same tool names, so the connector's tools
		// take the connector's name in front; the app still names the tool of the server.
		e := newAppsEnv([]sessiontest.Step{{Tool: "clock__get-time", Args: `{"zone":"UTC"}`}, {Tool: "clock__plain", Args: `{"zone":"UTC"}`}, {Say: "done"}})
		id := create(e.svc, alice)
		sent(e.svc, alice, id, "what time is it")
		idle(e.svc, alice, id)

		tools := toolEntries(e.svc, alice, id)
		Expect(tools).To(HaveLen(2), "tool entries = %+v", tools)
		var withApp, without session.Entry
		for _, en := range tools {
			if strings.HasPrefix(en.Text, "clock__get-time") {
				withApp = en
			} else {
				without = en
			}
		}
		Expect(without.Card).To(BeNil(), "a tool with no view got a card: %+v", without.Card)
		Expect(withApp.Card).NotTo(BeNil(), "no app card: %+v", withApp)
		Expect(withApp.Card.App).NotTo(BeNil(), "no app card: %+v", withApp)
		Expect(withApp.Card.Kind).To(Equal("mcp_app"), "no app card: %+v", withApp)
		a := withApp.Card.App
		Expect(a.Server).To(Equal(e.ref()), "app = %+v", a)
		Expect(a.Tool).To(Equal("get-time"), "app = %+v", a)
		Expect(a.ResourceURI).To(Equal("ui://clock/app.html"), "app = %+v", a)
		Expect(a.Visibility).To(ContainElement("app"), "app = %+v", a)
		Expect(string(a.Input)).To(Equal(`{"zone":"UTC"}`), "the app did not get the input and the full result: %s / %s", a.Input, a.Result)
		Expect(string(a.Result)).To(ContainSubstring(`"structuredContent"`), "the app did not get the input and the full result: %s / %s", a.Input, a.Result)
		Expect(string(a.Result)).To(ContainSubstring("12:00 UTC"), "the app did not get the input and the full result: %s / %s", a.Input, a.Result)
		// The transcript text keeps the 2 KiB rule: it never holds the structured result.
		Expect(withApp.Text).NotTo(ContainSubstring("structuredContent"), "entry text = %q", withApp.Text)
		Expect(len(withApp.Text)).To(BeNumerically("<=", 2300), "entry text = %q", withApp.Text)
		// The secret of the connector is nowhere in what the interface gets.
		raw, _ := json.Marshal(withApp.Card)
		Expect(string(raw)).NotTo(ContainSubstring("tok-clock-secret"), "the card holds the connector's token")
	})

	It("a turn does not offer a tool only the app may call", func() {
		e := newAppsEnv()
		id := create(e.svc, alice)
		sent(e.svc, alice, id, "hi")
		idle(e.svc, alice, id)
		var names []string
		for _, tl := range e.h.Starts()[0].Tools {
			names = append(names, tl.Name)
		}
		Expect(names).NotTo(ContainElement("refresh"), "an app-only tool was offered to the model: %v", names)
		for _, want := range []string{"get-time", "model_only", "plain"} {
			Expect(names).To(ContainElement(want), "%s missing from %v", want, names)
		}
	})

	It("a model server tool with a view gives the transcript an app reference", func() {
		e := newAppsEnv([]sessiontest.Step{{Tool: "get-time", Args: `{"zone":"UTC"}`}, {Say: "ok"}})
		// The connector's tools are also offered; the model server's name wins the
		// plain name, and the connector's copy is prefixed.
		id := create(e.svc, alice)
		sent(e.svc, alice, id, "go")
		idle(e.svc, alice, id)
		var servers []string
		for _, en := range toolEntries(e.svc, alice, id) {
			if en.Card != nil && en.Card.App != nil {
				servers = append(servers, en.Card.App.Server)
			}
		}
		Expect(servers).To(HaveLen(1), "app servers = %v, want the model's server clock", servers)
		Expect(servers[0]).To(Equal("clock"), "app servers = %v, want the model's server clock", servers)
	})

	It("the resource route serves a declared page", func() {
		e := newAppsEnv()
		id := create(e.svc, alice)
		for _, server := range []string{e.ref(), "clock"} {
			r, err := e.svc.MCPAppResource(context.Background(), alice, id, server, "ui://clock/app.html")
			Expect(err).NotTo(HaveOccurred(), "%s: %+v %v", server, r, err)
			Expect(r.HTML).To(Equal("<p>clock</p>"), "%s: %+v %v", server, r, err)
			Expect(r.MIMEType).To(Equal("text/html;profile=mcp-app"), "%s: %+v %v", server, r, err)
			Expect(r.Meta.CSP.ConnectDomains).To(HaveLen(1), "%s: the resource meta was lost: %+v", server, r.Meta)
			Expect(r.Meta.PrefersBorder).NotTo(BeNil(), "%s: the resource meta was lost: %+v", server, r.Meta)
			Expect(*r.Meta.PrefersBorder).To(BeTrue(), "%s: the resource meta was lost: %+v", server, r.Meta)
		}
	})

	It("the resource route refuses what the chat may not use", func() {
		e := newAppsEnv()
		id := create(e.svc, alice)
		ctx := context.Background()
		refused := func(what, user, chat, server, uri string) {
			_, err := e.svc.MCPAppResource(ctx, user, chat, server, uri)
			Expect(err).To(MatchError(studio.ErrMCPAppRefused), "%s: err = %v, want a refusal", what, err)
		}
		refused("an undeclared resource", alice, id, e.ref(), "ui://undeclared/page.html")
		refused("a file URI", alice, id, e.ref(), "file:///etc/passwd")
		refused("an http URI", alice, id, e.ref(), "https://example.com/x")
		refused("a foreign connector", alice, id, mcpconns.Prefix+e.other.ID, "ui://clock/app.html")
		refused("a server the model does not have", alice, id, "nope", "ui://clock/app.html")
		refused("a model server that is not on by itself", alice, id, "manual", "ui://clock/app.html")
		refused("a malformed connector ref", alice, id, "connector:../x", "ui://clock/app.html")
		refused("an oversize page", alice, id, e.ref(), "ui://big/app.html")
		refused("a page that is not HTML", alice, id, e.ref(), "ui://json/app.html")

		// Another user's chat is a missing chat.
		_, err := e.svc.MCPAppResource(ctx, bob, id, e.ref(), "ui://clock/app.html")
		Expect(err).To(MatchError(session.ErrNotFound), "another user's chat: %v", err)

		// A connector switched off for this chat, in the tools panel.
		Expect(e.svc.SetMcpOff(ctx, alice, id, []string{e.ref(), "clock"})).To(Succeed())
		refused("a connector switched off in the chat", alice, id, e.ref(), "ui://clock/app.html")
		refused("a model server switched off in the chat", alice, id, "clock", "ui://clock/app.html")
		Expect(e.svc.SetMcpOff(ctx, alice, id, nil)).To(Succeed())
		_, err = e.svc.MCPAppResource(ctx, alice, id, e.ref(), "ui://clock/app.html")
		Expect(err).NotTo(HaveOccurred(), "after switching on")

		// A connector switched off on the Connectors page.
		_, err = e.conns.SetEnabled(ctx, alice, e.conn.ID, false)
		Expect(err).NotTo(HaveOccurred())
		refused("a disabled connector", alice, id, e.ref(), "ui://clock/app.html")
	})

	It("an agent grant limits which servers serve apps", func() {
		e := newAppsEnv()
		ctx := context.Background()
		_ = e.ag.Put(ctx, agents.Agent{ID: "ag", Principal: alice, Name: "A", MCPServers: []string{"manual"}})
		chat, err := e.svc.Create(ctx, alice, studio.CreateInput{Model: "m", AgentID: "ag"})
		Expect(err).NotTo(HaveOccurred())
		_, err = e.svc.MCPAppResource(ctx, alice, chat.ID, "manual", "ui://clock/app.html")
		Expect(err).NotTo(HaveOccurred(), "the agent names manual")
		for _, server := range []string{"clock", e.ref()} {
			_, err := e.svc.MCPAppResource(ctx, alice, chat.ID, server, "ui://clock/app.html")
			Expect(err).To(MatchError(studio.ErrMCPAppRefused), "%s is outside the agent's grant: %v", server, err)
			_, err = e.svc.MCPAppCall(ctx, alice, chat.ID, server, "get-time", json.RawMessage(`{"zone":"UTC"}`))
			Expect(err).To(MatchError(studio.ErrMCPAppRefused), "%s call is outside the agent's grant: %v", server, err)
		}
		// A deleted agent grants nothing.
		_ = e.ag.Delete(ctx, alice, "ag")
		_, err = e.svc.MCPAppResource(ctx, alice, chat.ID, "manual", "ui://clock/app.html")
		Expect(err).To(MatchError(studio.ErrMCPAppRefused), "a deleted agent: %v", err)
	})

	It("the call route runs a tool for the app", func() {
		e := newAppsEnv()
		id := create(e.svc, alice)
		ctx := context.Background()
		for _, server := range []string{e.ref(), "clock"} {
			for _, tool := range []string{"refresh", "get-time", "plain"} {
				out, err := e.svc.MCPAppCall(ctx, alice, id, server, tool, json.RawMessage(`{"zone":"EU"}`))
				Expect(err).NotTo(HaveOccurred(), "%s/%s: %s %v", server, tool, out, err)
				Expect(string(out)).To(ContainSubstring(`"zone":"EU"`), "%s/%s: %s %v", server, tool, out, err)
			}
		}
	})

	It("the call route refuses what the app may not call", func() {
		e := newAppsEnv()
		id := create(e.svc, alice)
		ctx := context.Background()
		args := json.RawMessage(`{"zone":"UTC"}`)
		refused := func(what, user, chat, server, tool string) {
			_, err := e.svc.MCPAppCall(ctx, user, chat, server, tool, args)
			Expect(err).To(MatchError(studio.ErrMCPAppRefused), "%s: err = %v, want a refusal", what, err)
		}
		for _, server := range []string{e.ref(), "clock"} {
			refused("a tool only the model may call ("+server+")", alice, id, server, "model_only")
			refused("an unknown tool ("+server+")", alice, id, server, "nope")
			refused("an add-on tool ("+server+")", alice, id, server, "addon__x__y")
			refused("a built-in name ("+server+")", alice, id, server, "spawn_agent")
		}
		refused("a foreign connector", alice, id, mcpconns.Prefix+e.other.ID, "get-time")
		refused("a model server that is not on by itself", alice, id, "manual", "get-time")
		_, err := e.svc.MCPAppCall(ctx, bob, id, e.ref(), "get-time", args)
		Expect(err).To(MatchError(session.ErrNotFound), "another user's chat: %v", err)
		_, err = e.svc.MCPAppCall(ctx, alice, id, e.ref(), "get-time", json.RawMessage(`[1,2`))
		Expect(err).To(HaveOccurred(), "arguments that are not JSON were accepted")
		_ = e.svc.SetMcpOff(ctx, alice, id, []string{e.ref()})
		refused("a connector switched off in the chat", alice, id, e.ref(), "get-time")
	})

	// A live tool_end must name its transcript entry, so that the interface can read
	// the one card it needs and show the app at once, then keep the same element when
	// the transcript is read again.
	It("the tool end of an app names its transcript entry", func() {
		e := newAppsEnv([]sessiontest.Step{{Tool: "clock__plain", Args: `{"zone":"UTC"}`}, {Tool: "clock__get-time", Args: `{"zone":"UTC"}`}, {Say: "done"}})
		id := create(e.svc, alice)
		events, cancel, err := e.svc.Events(context.Background(), alice, id)
		Expect(err).NotTo(HaveOccurred())
		defer cancel()
		sent(e.svc, alice, id, "what time is it")
		idle(e.svc, alice, id)

		var ends []*session.ToolEvent
		for drained := false; !drained; {
			select {
			case ev := <-events:
				if ev.Kind == session.KindToolEnd {
					ends = append(ends, ev.Tool)
				}
			default:
				drained = true
			}
		}
		tools := toolEntries(e.svc, alice, id)
		Expect(ends).To(HaveLen(2), "ends = %d, tool entries = %d", len(ends), len(tools))
		Expect(tools).To(HaveLen(2), "ends = %d, tool entries = %d", len(ends), len(tools))
		for i := range ends {
			Expect(ends[i].Seq).NotTo(BeNumerically("==", 0), "tool_end %d has seq %d, its entry has %d", i, ends[i].Seq, tools[i].Seq)
			Expect(ends[i].Seq).To(Equal(tools[i].Seq), "tool_end %d has seq %d, its entry has %d", i, ends[i].Seq, tools[i].Seq)
		}
		Expect(tools[0].Seq).NotTo(Equal(tools[1].Seq), "two calls share a seq")
	})
})
