package mcpconns_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mudler/LocalAI/core/services/studio/mcpconns"
)

const appToken = "tok-app-secret-9"

// appServer is an MCP server over HTTP that has a view, and that fails to read
// a resource with a message that holds the bearer token it was given.
func appServer() *httptest.Server {
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
	DeferCleanup(func() { ts.CloseClientConnections(); ts.Close() })
	return ts
}

func appConnector(e *env, owner string) mcpconns.Connector {
	ts := appServer()
	in := httpInput("Clock", ts.URL)
	in.Token = str(appToken)
	c, err := e.svc.Create(specCtx(), owner, in)
	Expect(err).NotTo(HaveOccurred())
	return c
}

var _ = Describe("MCP apps", func() {
	It("resolves the UI meta and runs a tool for an app", func() {
		e := newEnv()
		ctx := specCtx()
		c := appConnector(e, "u1")
		tools, missing := e.svc.Resolve(ctx, "u1", []string{c.ID}, false)
		Expect(missing).To(BeEmpty())
		Expect(tools).To(HaveLen(1))
		ui, _ := tools[0].Meta["ui"].(map[string]any)
		Expect(ui["resourceUri"]).To(Equal("ui://clock/app.html"), "meta = %v", tools[0].Meta)
		out, err := tools[0].RunApp(ctx, json.RawMessage(`{"text":"x"}`))
		Expect(err).NotTo(HaveOccurred(), "app call")
		Expect(out.Text).To(Equal("12:00"))
		var got map[string]any
		Expect(json.Unmarshal(out.Result, &got)).To(Succeed())
		Expect(got["structuredContent"]).NotTo(BeNil(), "the full result must carry the structured content: %s", out.Result)
	})

	It("reads the page and never a leaked secret", func() {
		e := newEnv()
		ctx := specCtx()
		c := appConnector(e, "u1")
		res, err := e.svc.ReadResource(ctx, "u1", c.ID, "ui://clock/app.html")
		Expect(err).NotTo(HaveOccurred())
		Expect(res.HTML).To(Equal("<p>clock</p>"))
		// A server that puts the credential in its error must not get it through.
		_, err = e.svc.ReadResource(ctx, "u1", c.ID, "ui://broken/app.html")
		Expect(err).To(HaveOccurred(), "a failing read must fail")
		Expect(err.Error()).NotTo(ContainSubstring(appToken), "the error leaks the token")
	})

	It("refuses to read what a user may not use", func() {
		e := newEnv()
		ctx := specCtx()
		c := appConnector(e, "u1")
		_, err := e.svc.ReadResource(ctx, "u2", c.ID, "ui://clock/app.html")
		Expect(err).To(MatchError(mcpconns.ErrNotFound), "another user's connector")
		_, err = e.svc.ReadResource(ctx, "u1", c.ID, "file:///etc/passwd")
		Expect(err).To(HaveOccurred(), "a URI that is not ui:// was read")
		_, err = e.svc.SetEnabled(ctx, "u1", c.ID, false)
		Expect(err).NotTo(HaveOccurred())
		_, err = e.svc.ReadResource(ctx, "u1", c.ID, "ui://clock/app.html")
		Expect(err).To(HaveOccurred(), "a connector that is switched off was read")
		_, err = e.svc.SetEnabled(ctx, "u1", c.ID, true)
		Expect(err).NotTo(HaveOccurred())
		e.permit.Store(false)
		_, err = e.svc.ReadResource(ctx, "u1", c.ID, "ui://clock/app.html")
		Expect(err).To(MatchError(mcpconns.ErrForbidden), "without the MCP feature")
	})
})
