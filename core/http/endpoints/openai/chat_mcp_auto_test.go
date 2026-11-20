package openai

import (
	"context"

	"github.com/mudler/LocalAI/core/config"
	mcpTools "github.com/mudler/LocalAI/core/http/endpoints/mcp"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/functions"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// mcp.auto lets plain OpenAI clients, which cannot send metadata.mcp_servers,
// reach the model's server-side tools on /v1/chat/completions.
var _ = Describe("selectMCPServers", func() {
	withServers := func(auto bool) *config.ModelConfig {
		cfg := &config.ModelConfig{}
		cfg.MCP.Servers = `{"mcpServers": {"web": {"url": "http://web.invalid/mcp"}}}`
		cfg.MCP.Stdio = `{"mcpServers": {"notes": {"command": "notes-mcp"}}}`
		cfg.MCP.Auto = auto
		return cfg
	}

	It("enables every configured server when mcp.auto is set and the request names none", func() {
		Expect(selectMCPServers(withServers(true), nil, false, nil)).To(Equal([]string{"notes", "web"}))
	})

	It("keeps the servers the request selected", func() {
		Expect(selectMCPServers(withServers(true), []string{"web"}, false, nil)).To(Equal([]string{"web"}))
	})

	It("enables nothing without mcp.auto, as before", func() {
		Expect(selectMCPServers(withServers(false), nil, false, nil)).To(BeEmpty())
	})

	It("leaves the assistant modality alone", func() {
		Expect(selectMCPServers(withServers(true), nil, true, nil)).To(BeEmpty())
	})

	It("enables nothing when no servers are configured", func() {
		cfg := &config.ModelConfig{}
		cfg.MCP.Auto = true
		Expect(selectMCPServers(cfg, nil, false, nil)).To(BeEmpty())
	})
})

var _ = Describe("requestedToolNames", func() {
	It("collects both explicit functions and OpenAI tools", func() {
		input := &schema.OpenAIRequest{
			Functions: []functions.Function{{Name: "legacy_tool"}},
			Tools: []functions.Tool{
				{Type: "function", Function: functions.Function{Name: "tool"}},
			},
		}
		Expect(requestedToolNames(input)).To(Equal(map[string]struct{}{
			"legacy_tool": {},
			"tool":        {},
		}))
	})
})

// staticToolExecutor serves a fixed tool list, as the server-side MCP executor
// would after discovery.
type staticToolExecutor struct {
	tools []functions.Function
}

func (f staticToolExecutor) DiscoverTools(context.Context) ([]functions.Function, error) {
	return f.tools, nil
}

func (f staticToolExecutor) IsTool(name string) bool {
	for _, t := range f.tools {
		if t.Name == name {
			return true
		}
	}
	return false
}

func (f staticToolExecutor) ExecuteTool(_ context.Context, name, _ string) (string, error) {
	return "ran " + name, nil
}

func (f staticToolExecutor) HasTools() bool { return len(f.tools) > 0 }

var _ = Describe("dropOfferedTools", func() {
	var exec mcpTools.ToolExecutor

	BeforeEach(func() {
		exec = staticToolExecutor{tools: []functions.Function{{Name: "x"}, {Name: "y"}}}
	})

	It("hides a server-side tool the request already offers", func() {
		input := &schema.OpenAIRequest{
			Tools: []functions.Tool{{Type: "function", Function: functions.Function{Name: "x"}}},
		}
		filtered := dropOfferedTools(exec, input)

		discovered, err := filtered.DiscoverTools(context.Background())
		Expect(err).ToNot(HaveOccurred())
		Expect(discovered).To(HaveLen(1))
		Expect(discovered[0].Name).To(Equal("y"))
		Expect(filtered.IsTool("x")).To(BeFalse())
		Expect(filtered.IsTool("y")).To(BeTrue())

		_, err = filtered.ExecuteTool(context.Background(), "x", "{}")
		Expect(err).To(HaveOccurred())
		out, err := filtered.ExecuteTool(context.Background(), "y", "{}")
		Expect(err).ToNot(HaveOccurred())
		Expect(out).To(Equal("ran y"))
	})

	It("leaves the executor unchanged when the request offers no tools", func() {
		filtered := dropOfferedTools(exec, &schema.OpenAIRequest{})

		discovered, err := filtered.DiscoverTools(context.Background())
		Expect(err).ToNot(HaveOccurred())
		Expect(discovered).To(HaveLen(2))
		Expect(filtered.IsTool("x")).To(BeTrue())
		Expect(filtered.IsTool("y")).To(BeTrue())
	})
})
