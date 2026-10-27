package studio_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
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
)

const bob = "bob"

type appIn struct {
	Zone string `json:"zone"`
}

// appsMCPServer is a real MCP server over HTTP with a clock app: a tool with a
// view, a tool only the app may call, a tool only the model may call, a plain
// tool, and resources that are declared, undeclared, too large and not HTML.
func appsMCPServer(t *testing.T) *httptest.Server {
	t.Helper()
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
	t.Cleanup(func() { ts.CloseClientConnections(); ts.Close() })
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

func newAppsEnv(t *testing.T, turns ...[]sessiontest.Step) *appsEnv {
	t.Helper()
	ts := appsMCPServer(t)
	conns, _ := realConnectors(t, true)
	in := mcpconns.Input{Name: "Clock", Transport: mcpconns.TransportHTTP, URL: ts.URL, Enabled: true}
	token := "tok-clock-secret"
	in.Token = &token
	c, err := conns.Create(context.Background(), alice, in)
	if err != nil {
		t.Fatal(err)
	}
	in.Name = "Bob clock"
	o, err := conns.Create(context.Background(), bob, in)
	if err != nil {
		t.Fatal(err)
	}
	// The model's own server: one auto, one that only an agent may name.
	cs, err := mcptransport.Connect(context.Background(), mcp.NewClient(&mcp.Implementation{Name: "t", Version: "1"}, nil), ts.URL, nil, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	byName := map[string]mcpendpoint.NamedSession{
		"clock":  {Name: "clock", Type: "remote", Session: cs},
		"manual": {Name: "manual", Type: "remote", Session: cs},
	}
	st, _ := store.NewJSON("")
	ag, _ := agents.NewJSON("")
	h := &sessiontest.FakeHarness{Turns: turns}
	svc, err := studio.New(studio.Deps{
		Store: st, Provider: provider.New(), StateDir: t.TempDir(), Harness: h, Agents: ag, MCPConnectors: conns,
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
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close(context.Background()) })
	return &appsEnv{svc: svc, h: h, conns: conns, ag: ag, conn: c, other: o, ts: ts}
}

func (e *appsEnv) ref() string { return mcpconns.Prefix + e.conn.ID }

func toolEntries(t *testing.T, svc *studio.Service, user, id string) []session.Entry {
	t.Helper()
	all, err := svc.Messages(context.Background(), user, id, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var out []session.Entry
	for _, e := range all {
		if e.Role == session.RoleTool {
			out = append(out, e)
		}
	}
	return out
}

func TestAConnectorToolWithAViewGivesTheTranscriptAnAppReference(t *testing.T) {
	// The model's own server has the same tool names, so the connector's tools
	// take the connector's name in front; the app still names the tool of the server.
	e := newAppsEnv(t, []sessiontest.Step{{Tool: "Clock_get-time", Args: `{"zone":"UTC"}`}, {Tool: "Clock_plain", Args: `{"zone":"UTC"}`}, {Say: "done"}})
	id := create(t, e.svc, alice)
	sent(t, e.svc, alice, id, "what time is it")
	idle(t, e.svc, alice, id)

	tools := toolEntries(t, e.svc, alice, id)
	if len(tools) != 2 {
		t.Fatalf("tool entries = %+v", tools)
	}
	var withApp, without session.Entry
	for _, en := range tools {
		if strings.HasPrefix(en.Text, "Clock_get-time") {
			withApp = en
		} else {
			without = en
		}
	}
	if without.Card != nil {
		t.Fatalf("a tool with no view got a card: %+v", without.Card)
	}
	if withApp.Card == nil || withApp.Card.App == nil || withApp.Card.Kind != "mcp_app" {
		t.Fatalf("no app card: %+v", withApp)
	}
	a := withApp.Card.App
	if a.Server != e.ref() || a.Tool != "get-time" || a.ResourceURI != "ui://clock/app.html" || !slices.Contains(a.Visibility, "app") {
		t.Fatalf("app = %+v", a)
	}
	if string(a.Input) != `{"zone":"UTC"}` || !strings.Contains(string(a.Result), `"structuredContent"`) || !strings.Contains(string(a.Result), "12:00 UTC") {
		t.Fatalf("the app did not get the input and the full result: %s / %s", a.Input, a.Result)
	}
	// The transcript text keeps the 2 KiB rule: it never holds the structured result.
	if strings.Contains(withApp.Text, "structuredContent") || len(withApp.Text) > 2300 {
		t.Fatalf("entry text = %q", withApp.Text)
	}
	// The secret of the connector is nowhere in what the interface gets.
	if raw, _ := json.Marshal(withApp.Card); strings.Contains(string(raw), "tok-clock-secret") {
		t.Fatal("the card holds the connector's token")
	}
}

func TestATurnDoesNotOfferAToolOnlyTheAppMayCall(t *testing.T) {
	e := newAppsEnv(t)
	id := create(t, e.svc, alice)
	sent(t, e.svc, alice, id, "hi")
	idle(t, e.svc, alice, id)
	var names []string
	for _, tl := range e.h.Starts()[0].Tools {
		names = append(names, tl.Name)
	}
	if slices.Contains(names, "refresh") {
		t.Fatalf("an app-only tool was offered to the model: %v", names)
	}
	for _, want := range []string{"get-time", "model_only", "plain"} {
		if !slices.Contains(names, want) {
			t.Fatalf("%s missing from %v", want, names)
		}
	}
}

func TestAModelServerToolWithAViewGivesTheTranscriptAnAppReference(t *testing.T) {
	e := newAppsEnv(t, []sessiontest.Step{{Tool: "get-time", Args: `{"zone":"UTC"}`}, {Say: "ok"}})
	// The connector's tools are also offered; the model server's name wins the
	// plain name, and the connector's copy is prefixed.
	id := create(t, e.svc, alice)
	sent(t, e.svc, alice, id, "go")
	idle(t, e.svc, alice, id)
	var servers []string
	for _, en := range toolEntries(t, e.svc, alice, id) {
		if en.Card != nil && en.Card.App != nil {
			servers = append(servers, en.Card.App.Server)
		}
	}
	if len(servers) != 1 || servers[0] != "clock" {
		t.Fatalf("app servers = %v, want the model's server clock", servers)
	}
}

func TestTheResourceRouteServesADeclaredPage(t *testing.T) {
	e := newAppsEnv(t)
	id := create(t, e.svc, alice)
	for _, server := range []string{e.ref(), "clock"} {
		r, err := e.svc.MCPAppResource(context.Background(), alice, id, server, "ui://clock/app.html")
		if err != nil || r.HTML != "<p>clock</p>" || r.MIMEType != "text/html;profile=mcp-app" {
			t.Fatalf("%s: %+v %v", server, r, err)
		}
		if len(r.Meta.CSP.ConnectDomains) != 1 || r.Meta.PrefersBorder == nil || !*r.Meta.PrefersBorder {
			t.Fatalf("%s: the resource meta was lost: %+v", server, r.Meta)
		}
	}
}

func TestTheResourceRouteRefusesWhatTheChatMayNotUse(t *testing.T) {
	e := newAppsEnv(t)
	id := create(t, e.svc, alice)
	ctx := context.Background()
	refused := func(what, user, chat, server, uri string) {
		t.Helper()
		_, err := e.svc.MCPAppResource(ctx, user, chat, server, uri)
		if !errors.Is(err, studio.ErrMCPAppRefused) {
			t.Errorf("%s: err = %v, want a refusal", what, err)
		}
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
	if _, err := e.svc.MCPAppResource(ctx, bob, id, e.ref(), "ui://clock/app.html"); !errors.Is(err, session.ErrNotFound) {
		t.Errorf("another user's chat: %v", err)
	}

	// A connector switched off for this chat, in the tools panel.
	if err := e.svc.SetMcpOff(ctx, alice, id, []string{e.ref(), "clock"}); err != nil {
		t.Fatal(err)
	}
	refused("a connector switched off in the chat", alice, id, e.ref(), "ui://clock/app.html")
	refused("a model server switched off in the chat", alice, id, "clock", "ui://clock/app.html")
	if err := e.svc.SetMcpOff(ctx, alice, id, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.MCPAppResource(ctx, alice, id, e.ref(), "ui://clock/app.html"); err != nil {
		t.Fatalf("after switching on: %v", err)
	}

	// A connector switched off on the Connectors page.
	if _, err := e.conns.SetEnabled(ctx, alice, e.conn.ID, false); err != nil {
		t.Fatal(err)
	}
	refused("a disabled connector", alice, id, e.ref(), "ui://clock/app.html")
}

func TestAnAgentGrantLimitsWhichServersServeApps(t *testing.T) {
	e := newAppsEnv(t)
	ctx := context.Background()
	_ = e.ag.Put(ctx, agents.Agent{ID: "ag", Principal: alice, Name: "A", MCPServers: []string{"manual"}})
	chat, err := e.svc.Create(ctx, alice, studio.CreateInput{Model: "m", AgentID: "ag"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.MCPAppResource(ctx, alice, chat.ID, "manual", "ui://clock/app.html"); err != nil {
		t.Fatalf("the agent names manual: %v", err)
	}
	for _, server := range []string{"clock", e.ref()} {
		if _, err := e.svc.MCPAppResource(ctx, alice, chat.ID, server, "ui://clock/app.html"); !errors.Is(err, studio.ErrMCPAppRefused) {
			t.Errorf("%s is outside the agent's grant: %v", server, err)
		}
		if _, err := e.svc.MCPAppCall(ctx, alice, chat.ID, server, "get-time", json.RawMessage(`{"zone":"UTC"}`)); !errors.Is(err, studio.ErrMCPAppRefused) {
			t.Errorf("%s call is outside the agent's grant: %v", server, err)
		}
	}
	// A deleted agent grants nothing.
	_ = e.ag.Delete(ctx, alice, "ag")
	if _, err := e.svc.MCPAppResource(ctx, alice, chat.ID, "manual", "ui://clock/app.html"); !errors.Is(err, studio.ErrMCPAppRefused) {
		t.Errorf("a deleted agent: %v", err)
	}
}

func TestTheCallRouteRunsAToolForTheApp(t *testing.T) {
	e := newAppsEnv(t)
	id := create(t, e.svc, alice)
	ctx := context.Background()
	for _, server := range []string{e.ref(), "clock"} {
		for _, tool := range []string{"refresh", "get-time", "plain"} {
			out, err := e.svc.MCPAppCall(ctx, alice, id, server, tool, json.RawMessage(`{"zone":"EU"}`))
			if err != nil || !strings.Contains(string(out), `"zone":"EU"`) {
				t.Errorf("%s/%s: %s %v", server, tool, out, err)
			}
		}
	}
}

func TestTheCallRouteRefusesWhatTheAppMayNotCall(t *testing.T) {
	e := newAppsEnv(t)
	id := create(t, e.svc, alice)
	ctx := context.Background()
	args := json.RawMessage(`{"zone":"UTC"}`)
	refused := func(what, user, chat, server, tool string) {
		t.Helper()
		_, err := e.svc.MCPAppCall(ctx, user, chat, server, tool, args)
		if !errors.Is(err, studio.ErrMCPAppRefused) {
			t.Errorf("%s: err = %v, want a refusal", what, err)
		}
	}
	for _, server := range []string{e.ref(), "clock"} {
		refused("a tool only the model may call ("+server+")", alice, id, server, "model_only")
		refused("an unknown tool ("+server+")", alice, id, server, "nope")
		refused("an add-on tool ("+server+")", alice, id, server, "addon__x__y")
		refused("a built-in name ("+server+")", alice, id, server, "spawn_agent")
	}
	refused("a foreign connector", alice, id, mcpconns.Prefix+e.other.ID, "get-time")
	refused("a model server that is not on by itself", alice, id, "manual", "get-time")
	if _, err := e.svc.MCPAppCall(ctx, bob, id, e.ref(), "get-time", args); !errors.Is(err, session.ErrNotFound) {
		t.Errorf("another user's chat: %v", err)
	}
	if _, err := e.svc.MCPAppCall(ctx, alice, id, e.ref(), "get-time", json.RawMessage(`[1,2`)); err == nil {
		t.Error("arguments that are not JSON were accepted")
	}
	_ = e.svc.SetMcpOff(ctx, alice, id, []string{e.ref()})
	refused("a connector switched off in the chat", alice, id, e.ref(), "get-time")
}
