package cli

import (
	"context"

	"github.com/mudler/LocalAI/core/config"
	mcpTools "github.com/mudler/LocalAI/core/http/endpoints/mcp"
	mcpRemote "github.com/mudler/LocalAI/core/services/mcp"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The frontend hands a worker's reply to the model as the tool result, so a
// failure has to come back as a reply with Error set: silence would leave the
// caller waiting for the full request budget.
var _ = Describe("agent worker MCP handlers", func() {
	var tool mcpRemote.ToolHandler = handleMCPToolRequest
	var discovery mcpRemote.DiscoveryHandler = handleMCPDiscoveryRequest

	const model = "agent-worker-mcp-rpc-spec"

	AfterEach(func() {
		mcpTools.CloseMCPSessions(model)
	})

	It("answers a tool no server provides with an error reply", func() {
		resp := tool(context.Background(), mcpRemote.MCPToolRequest{ModelName: model, ToolName: "missing"})
		Expect(resp.Result).To(BeEmpty())
		Expect(resp.Error).To(ContainSubstring(`MCP tool "missing" not found`))
	})

	It("answers a discovery whose server cannot start with that server's error", func() {
		resp := discovery(context.Background(), mcpRemote.MCPDiscoveryRequest{
			ModelName: model,
			StdioServers: config.MCPGenericConfig[config.MCPSTDIOServers]{
				Servers: config.MCPSTDIOServers{
					"broken": {Command: "/nonexistent/localai-spec-mcp-server"},
				},
			},
		})
		Expect(resp.Servers).To(HaveLen(1))
		Expect(resp.Servers[0].Name).To(Equal("broken"))
		Expect(resp.Servers[0].Error).To(ContainSubstring("startup failed"))
		Expect(resp.Tools).To(BeEmpty())
	})
})
