package openai

import (
	"github.com/mudler/LocalAI/core/config"
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
		Expect(selectMCPServers(withServers(true), nil, false)).To(Equal([]string{"notes", "web"}))
	})

	It("keeps the servers the request selected", func() {
		Expect(selectMCPServers(withServers(true), []string{"web"}, false)).To(Equal([]string{"web"}))
	})

	It("enables nothing without mcp.auto, as before", func() {
		Expect(selectMCPServers(withServers(false), nil, false)).To(BeEmpty())
	})

	It("leaves the assistant modality alone", func() {
		Expect(selectMCPServers(withServers(true), nil, true)).To(BeEmpty())
	})

	It("enables nothing when no servers are configured", func() {
		cfg := &config.ModelConfig{}
		cfg.MCP.Auto = true
		Expect(selectMCPServers(cfg, nil, false)).To(BeEmpty())
	})
})
