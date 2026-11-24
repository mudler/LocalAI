package application

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/mcpconns"
)

type slowIn struct {
	Text string `json:"text"`
}

// slowMCPServer serves one tool, "slow", that answers after d.
func slowMCPServer(d time.Duration) *httptest.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: "slow", Version: "1"}, nil)
	mcp.AddTool(srv, &mcp.Tool{Name: "slow", Description: "sleeps"}, func(ctx context.Context, _ *mcp.CallToolRequest, _ slowIn) (*mcp.CallToolResult, any, error) {
		select {
		case <-time.After(d):
		case <-ctx.Done():
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "done"}}}, nil, nil
	})
	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil)
	ts := httptest.NewServer(h)
	DeferCleanup(func() { ts.CloseClientConnections(); ts.Close() })
	return ts
}

// connectorApp builds an Application with the connector service on JSON
// stores, with the given MCP tool timeout (zero keeps the default).
func connectorApp(toolTimeout time.Duration) *Application {
	dir := GinkgoT().TempDir()
	ms, err := mcpconns.NewJSON(filepath.Join(dir, "mcp"))
	Expect(err).NotTo(HaveOccurred())
	cs, err := connectors.NewJSON(filepath.Join(dir, "connectors"))
	Expect(err).NotTo(HaveOccurred())
	cfg := &config.ApplicationConfig{MCPConnectorsAllowPrivate: true}
	cfg.Distributed.MCPToolTimeout = toolTimeout
	a := &Application{applicationConfig: cfg, studioMCPStore: ms, studioConnStore: cs}
	a.initStudioMCPConnectors()
	Expect(a.StudioMCPConnectors()).NotTo(BeNil())
	DeferCleanup(a.StudioMCPConnectors().Close)
	return a
}

func runSlow(a *Application, url string) (string, error) {
	ctx := context.Background()
	_, err := a.StudioMCPConnectors().Create(ctx, "u1", mcpconns.Input{Name: "Slow", Transport: mcpconns.TransportHTTP, URL: url, Enabled: true})
	Expect(err).NotTo(HaveOccurred())
	tools, _ := a.StudioMCPConnectors().Resolve(ctx, "u1", nil, true)
	for _, tool := range tools {
		if tool.Name == "slow" {
			return tool.Run(ctx, json.RawMessage(`{"text":"x"}`))
		}
	}
	Fail("the connector offers no tool slow")
	return "", nil
}

var _ = Describe("MCP connector call timeout", func() {
	It("defaults to the MCP tool timeout, not the connector default", func() {
		Expect(connectorApp(0).studioMCPCallTimeout()).To(Equal(config.DefaultMCPToolTimeout))
		Expect(config.DefaultMCPToolTimeout).To(BeNumerically(">", mcpconns.DefaultCallTimeout))
	})

	It("lets a tool finish within the configured MCP tool timeout", func() {
		// The tool takes 1.2 s and completes within the configured 3 s timeout.
		a := connectorApp(3 * time.Second)
		out, err := runSlow(a, slowMCPServer(1200*time.Millisecond).URL)
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("done"))
	})

	It("still stops a tool that runs past the MCP tool timeout", func() {
		a := connectorApp(300 * time.Millisecond)
		start := time.Now()
		_, err := runSlow(a, slowMCPServer(5*time.Second).URL)
		Expect(err).To(HaveOccurred())
		Expect(time.Since(start)).To(BeNumerically("<", 3*time.Second))
	})
})
