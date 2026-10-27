package mcpconns_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mudler/LocalAI/core/services/studio/mcpconns"
)

const appToken = "tok-app-secret-9"

// appServer is an MCP server over HTTP that has a view, and that fails to read
// a resource with a message that holds the bearer token it was given.
func appServer(t *testing.T) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	last := ""
	srv := mcp.NewServer(&mcp.Implementation{Name: "apps", Version: "1"}, nil)
	mcp.AddTool(srv, &mcp.Tool{Name: "get-time", Meta: mcp.Meta{"ui": map[string]any{"resourceUri": "ui://clock/app.html"}}},
		func(_ context.Context, _ *mcp.CallToolRequest, in echoIn) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "12:00"}}, StructuredContent: map[string]any{"time": "12:00"}}, nil, nil
		})
	srv.AddResource(&mcp.Resource{URI: "ui://clock/app.html", Name: "clock", MIMEType: "text/html;profile=mcp-app"},
		func(_ context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: req.Params.URI, MIMEType: "text/html;profile=mcp-app", Text: "<p>clock</p>"}}}, nil
		})
	srv.AddResource(&mcp.Resource{URI: "ui://broken/app.html", Name: "broken", MIMEType: "text/html"},
		func(_ context.Context, _ *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			mu.Lock()
			defer mu.Unlock()
			return nil, errors.New("backend said no to " + last)
		})
	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		last = r.Header.Get("Authorization")
		mu.Unlock()
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(func() { ts.CloseClientConnections(); ts.Close() })
	return ts
}

func appConnector(t *testing.T, e *env, owner string) mcpconns.Connector {
	t.Helper()
	ts := appServer(t)
	in := httpInput("Clock", ts.URL)
	in.Token = str(appToken)
	c, err := e.svc.Create(t.Context(), owner, in)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestResolveCarriesTheUIMetaAndRunsATool_ForAnApp(t *testing.T) {
	e := newEnv(t)
	c := appConnector(t, e, "u1")
	tools, missing := e.svc.Resolve(t.Context(), "u1", []string{c.ID}, false)
	if len(missing) != 0 || len(tools) != 1 {
		t.Fatalf("tools %v missing %v", tools, missing)
	}
	ui, _ := tools[0].Meta["ui"].(map[string]any)
	if ui["resourceUri"] != "ui://clock/app.html" {
		t.Fatalf("meta = %v", tools[0].Meta)
	}
	out, err := tools[0].RunApp(t.Context(), json.RawMessage(`{"text":"x"}`))
	if err != nil || out.Text != "12:00" {
		t.Fatalf("app call: %+v %v", out, err)
	}
	var got map[string]any
	if err := json.Unmarshal(out.Result, &got); err != nil || got["structuredContent"] == nil {
		t.Fatalf("the full result must carry the structured content: %s", out.Result)
	}
}

func TestReadResourceReturnsThePageAndNeverALeakedSecret(t *testing.T) {
	e := newEnv(t)
	c := appConnector(t, e, "u1")
	res, err := e.svc.ReadResource(t.Context(), "u1", c.ID, "ui://clock/app.html")
	if err != nil || res.HTML != "<p>clock</p>" {
		t.Fatalf("read: %+v %v", res, err)
	}
	// A server that puts the credential in its error must not get it through.
	_, err = e.svc.ReadResource(t.Context(), "u1", c.ID, "ui://broken/app.html")
	if err == nil {
		t.Fatal("a failing read must fail")
	}
	if strings.Contains(err.Error(), appToken) {
		t.Fatalf("the error leaks the token: %v", err)
	}
}

func TestReadResourceRefusesWhatAUserMayNotUse(t *testing.T) {
	e := newEnv(t)
	c := appConnector(t, e, "u1")
	if _, err := e.svc.ReadResource(t.Context(), "u2", c.ID, "ui://clock/app.html"); !errors.Is(err, mcpconns.ErrNotFound) {
		t.Fatalf("another user's connector: %v", err)
	}
	if _, err := e.svc.ReadResource(t.Context(), "u1", c.ID, "file:///etc/passwd"); err == nil {
		t.Fatal("a URI that is not ui:// was read")
	}
	if _, err := e.svc.SetEnabled(t.Context(), "u1", c.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.ReadResource(t.Context(), "u1", c.ID, "ui://clock/app.html"); err == nil {
		t.Fatal("a connector that is switched off was read")
	}
	if _, err := e.svc.SetEnabled(t.Context(), "u1", c.ID, true); err != nil {
		t.Fatal(err)
	}
	e.permit.Store(false)
	if _, err := e.svc.ReadResource(t.Context(), "u1", c.ID, "ui://clock/app.html"); !errors.Is(err, mcpconns.ErrForbidden) {
		t.Fatalf("without the MCP feature: %v", err)
	}
}
