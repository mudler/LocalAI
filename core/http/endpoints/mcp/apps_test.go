package mcp

import (
	"context"
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type timeIn struct {
	Zone string `json:"zone"`
}

// appSession is a connected in-memory MCP server that offers one tool with a
// view, one tool only the app may call, and one plain tool.
func appSession(name string) NamedSession {
	srv := mcp.NewServer(&mcp.Implementation{Name: name, Version: "1"}, nil)
	mcp.AddTool(srv, &mcp.Tool{Name: "get-time", Description: "time", Meta: mcp.Meta{"ui": map[string]any{"resourceUri": "ui://clock/app.html"}}},
		func(_ context.Context, _ *mcp.CallToolRequest, in timeIn) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "12:00 " + in.Zone}}, StructuredContent: map[string]any{"time": "12:00"}}, nil, nil
		})
	mcp.AddTool(srv, &mcp.Tool{Name: "refresh", Meta: mcp.Meta{"ui": map[string]any{"visibility": []any{"app"}}}},
		func(_ context.Context, _ *mcp.CallToolRequest, _ timeIn) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil, nil
		})
	mcp.AddTool(srv, &mcp.Tool{Name: "plain"},
		func(_ context.Context, _ *mcp.CallToolRequest, _ timeIn) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "plain"}}}, nil, nil
		})
	srv.AddResource(&mcp.Resource{URI: "ui://clock/app.html", Name: "clock", MIMEType: "text/html;profile=mcp-app"},
		func(_ context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: req.Params.URI, MIMEType: "text/html;profile=mcp-app", Text: "<p>clock</p>"}}}, nil
		})
	ct, st := mcp.NewInMemoryTransports()
	_, err := srv.Connect(context.Background(), st, nil)
	Expect(err).NotTo(HaveOccurred())
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).Connect(context.Background(), ct, nil)
	Expect(err).NotTo(HaveOccurred())
	return NamedSession{Name: name, Type: "remote", Session: cs}
}

var _ = Describe("MCP Apps on the local executor", func() {
	var exec *LocalToolExecutor
	BeforeEach(func() {
		exec = NewLocalToolExecutor(context.Background(), []NamedSession{appSession("clock")})
	})

	It("lists the view and the visibility of each tool with its server", func() {
		apps := exec.AppTools()
		byName := map[string]AppTool{}
		for _, a := range apps {
			byName[a.Name] = a
		}
		Expect(byName).To(HaveKey("get-time"))
		Expect(byName["get-time"].Server).To(Equal("clock"))
		Expect(byName["get-time"].UI.ResourceURI).To(Equal("ui://clock/app.html"))
		Expect(byName["get-time"].UI.ForApp()).To(BeTrue())
		Expect(byName["refresh"].UI.ForModel()).To(BeFalse())
		Expect(byName["plain"].UI.ResourceURI).To(BeEmpty())
	})

	It("reads a resource of a named server", func() {
		res, err := exec.ReadAppResource(context.Background(), "clock", "ui://clock/app.html")
		Expect(err).NotTo(HaveOccurred())
		Expect(res.HTML).To(Equal("<p>clock</p>"))
	})

	It("refuses another server, a URI that is not ui:// and a missing resource", func() {
		_, err := exec.ReadAppResource(context.Background(), "other", "ui://clock/app.html")
		Expect(err).To(HaveOccurred())
		_, err = exec.ReadAppResource(context.Background(), "clock", "file:///etc/passwd")
		Expect(err).To(HaveOccurred())
		_, err = exec.ReadAppResource(context.Background(), "clock", "ui://nope")
		Expect(err).To(HaveOccurred())
	})

	It("runs a tool for an app and returns the text and the structured result", func() {
		out, err := exec.ExecuteAppTool(context.Background(), "clock", "get-time", json.RawMessage(`{"zone":"UTC"}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(out.Text).To(Equal("12:00 UTC"))
		var got map[string]any
		Expect(json.Unmarshal(out.Result, &got)).To(Succeed())
		Expect(got["structuredContent"]).To(HaveKeyWithValue("time", "12:00"))
	})

	It("refuses an add-on tool name, an unknown tool and another server", func() {
		_, err := exec.ExecuteAppTool(context.Background(), "clock", "addon__x__y", nil)
		Expect(err).To(HaveOccurred())
		_, err = exec.ExecuteAppTool(context.Background(), "clock", "nope", nil)
		Expect(err).To(HaveOccurred())
		_, err = exec.ExecuteAppTool(context.Background(), "other", "get-time", nil)
		Expect(err).To(HaveOccurred())
	})
})
