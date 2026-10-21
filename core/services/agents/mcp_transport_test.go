package agents

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Regression: agent MCP servers that speak Streamable HTTP (the transport used by current
// MCP servers, for example the Loki, NetBox and Odoo gateways) were never connected. The
// agent path only tried the legacy SSE transport, with no deadline, so the connection
// waited for an SSE handshake that never came and the agent job stalled before its first
// model call. setupMCPSessions must connect to both transports.
var _ = Describe("setupMCPSessions transports", func() {
	newFixtureServer := func(serve func(getServer func(*http.Request) *mcp.Server) http.Handler) *httptest.Server {
		srv := mcp.NewServer(&mcp.Implementation{Name: "transport-fixture", Version: "v0.0.1"}, nil)
		srv.AddTool(&mcp.Tool{
			Name:        "ping",
			Description: "fixture tool",
			InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
		}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "pong"}}}, nil
		})
		return httptest.NewServer(serve(func(*http.Request) *mcp.Server { return srv }))
	}

	toolNames := func(sessions []*mcp.ClientSession) []string {
		var names []string
		for _, s := range sessions {
			res, err := s.ListTools(context.Background(), &mcp.ListToolsParams{})
			Expect(err).NotTo(HaveOccurred())
			for _, t := range res.Tools {
				names = append(names, t.Name)
			}
		}
		return names
	}

	It("connects to a Streamable HTTP MCP server promptly", func() {
		ts := newFixtureServer(func(get func(*http.Request) *mcp.Server) http.Handler {
			return mcp.NewStreamableHTTPHandler(get, nil)
		})
		defer ts.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		cfg := &AgentConfig{MCPServers: []MCPServer{{URL: ts.URL}}}

		start := time.Now()
		sessions, cleanup := setupMCPSessions(ctx, cfg)
		defer cleanup()

		Expect(sessions).To(HaveLen(1), "Streamable HTTP server was not connected")
		Expect(time.Since(start)).To(BeNumerically("<", 10*time.Second), "connect waited for the SSE handshake")
		Expect(toolNames(sessions)).To(ContainElement("ping"))
	})

	It("still connects to a legacy SSE MCP server", func() {
		ts := newFixtureServer(func(get func(*http.Request) *mcp.Server) http.Handler {
			return mcp.NewSSEHandler(get, nil)
		})
		defer ts.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		cfg := &AgentConfig{MCPServers: []MCPServer{{URL: ts.URL}}}

		sessions, cleanup := setupMCPSessions(ctx, cfg)
		defer cleanup()

		Expect(sessions).To(HaveLen(1), "legacy SSE server was not connected")
		Expect(toolNames(sessions)).To(ContainElement("ping"))
	})
})
