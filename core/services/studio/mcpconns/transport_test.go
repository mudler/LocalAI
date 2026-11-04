package mcpconns_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/mcpconns"
)

// Regression: agent MCP servers that speak Streamable HTTP were never connected. The
// agent path only tried the legacy SSE transport, so the connection waited for an SSE
// handshake that never came and the agent stalled before its first model call. Connecting
// a connector must reach both transports.
var _ = Describe("MCP connector transports", func() {
	transportServer := func(serve func(get func(*http.Request) *mcp.Server) http.Handler) *httptest.Server {
		srv := mcp.NewServer(&mcp.Implementation{Name: "transport-fixture", Version: "v0.0.1"}, nil)
		mcp.AddTool(srv, &mcp.Tool{Name: "ping", Description: "fixture tool"},
			func(_ context.Context, _ *mcp.CallToolRequest, _ echoIn) (*mcp.CallToolResult, any, error) {
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "pong"}}}, nil, nil
			})
		ts := httptest.NewServer(serve(func(*http.Request) *mcp.Server { return srv }))
		DeferCleanup(func() { ts.CloseClientConnections(); ts.Close() })
		return ts
	}

	resolvedNames := func(e *env, ts *httptest.Server) []string {
		c, err := e.svc.Create(specCtx(), "u1", httpInput("Transport", ts.URL))
		Expect(err).NotTo(HaveOccurred())
		tools, missing := e.svc.Resolve(specCtx(), "u1", []string{c.ID}, false)
		Expect(missing).To(BeEmpty(), "connector did not connect")
		names := make([]string, 0, len(tools))
		for _, t := range tools {
			names = append(names, t.Name)
		}
		return names
	}

	It("connects to a Streamable HTTP MCP server promptly", func() {
		ts := transportServer(func(get func(*http.Request) *mcp.Server) http.Handler {
			return mcp.NewStreamableHTTPHandler(get, nil)
		})
		e := newEnv(func(o *mcpconns.Options) { o.ConnectTimeout = 20 * time.Second })

		start := time.Now()
		names := resolvedNames(e, ts)
		Expect(time.Since(start)).To(BeNumerically("<", 10*time.Second), "connect waited for the SSE handshake")
		Expect(names).To(ContainElement("ping"))
	})

	It("still connects to a legacy SSE MCP server", func() {
		ts := transportServer(func(get func(*http.Request) *mcp.Server) http.Handler {
			return mcp.NewSSEHandler(get, nil)
		})
		e := newEnv(func(o *mcpconns.Options) { o.ConnectTimeout = 20 * time.Second })

		Expect(resolvedNames(e, ts)).To(ContainElement("ping"))
	})
})
