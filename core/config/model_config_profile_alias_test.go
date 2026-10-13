package config_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
)

// A profile alias carries an opt-in profile block (system_prompt, mcp,
// client_tools) that is applied on top of its target while the target's loader
// identity is kept, so several assistant personas share one loaded model
// instead of each loading the weights again.
var _ = Describe("Profile alias overlay", func() {
	const target = `name: base-llm
backend: llama-cpp
system_prompt: base prompt
parameters:
  model: base-llm.gguf
mcp:
  remote: |
    {"mcpServers": {"base-tools": {"url": "http://base.invalid/mcp"}}}
`
	const profile = `name: researcher
alias: base-llm
profile:
  system_prompt: You are a research assistant.
  client_tools: drop
  mcp:
    auto: true
    remote: |
      {"mcpServers": {"web": {"url": "http://web.invalid/mcp"}}}
    stdio: |
      {"mcpServers": {"notes": {"command": "notes-mcp"}}}
`
	const plain = `name: gpt-4
alias: base-llm
`
	// Top-level fields on an alias were always ignored; without a profile
	// block they must stay ignored, so existing aliases never change.
	const legacy = `name: legacy
alias: base-llm
system_prompt: ignored prompt
client_tools: drop
mcp:
  auto: true
  remote: |
    {"mcpServers": {"ignored": {"url": "http://ignored.invalid/mcp"}}}
`

	var cl *config.ModelConfigLoader

	BeforeEach(func() {
		dir := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(dir, "base-llm.yaml"), []byte(target), 0644)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(dir, "researcher.yaml"), []byte(profile), 0644)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(dir, "gpt-4.yaml"), []byte(plain), 0644)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(dir, "legacy.yaml"), []byte(legacy), 0644)).To(Succeed())
		cl = config.NewModelConfigLoader(dir)
		Expect(cl.LoadModelConfigsFromPath(dir)).To(Succeed())
	})

	resolve := func(name string) *config.ModelConfig {
		cfg, ok := cl.GetModelConfig(name)
		Expect(ok).To(BeTrue(), name)
		resolved, wasAlias, err := cl.ResolveAlias(&cfg)
		Expect(err).NotTo(HaveOccurred())
		Expect(wasAlias).To(BeTrue())
		return resolved
	}

	It("applies the alias's system prompt, MCP servers and tool policy", func() {
		resolved := resolve("researcher")

		Expect(resolved.SystemPrompt).To(Equal("You are a research assistant."))
		Expect(resolved.DropsClientTools()).To(BeTrue())
		Expect(resolved.MCP.Auto).To(BeTrue())
		names, err := resolved.MCP.ServerNames()
		Expect(err).NotTo(HaveOccurred())
		// The alias's servers replace the target's; nothing of the target's leaks in.
		Expect(names).To(Equal([]string{"notes", "web"}))
		// Sessions are cached apart from the target's, which has other servers.
		Expect(resolved.MCPSessionKey()).To(Equal("researcher"))
	})

	It("keeps the target's loader identity, so no second model load happens", func() {
		base, ok := cl.GetModelConfig("base-llm")
		Expect(ok).To(BeTrue())
		resolved := resolve("researcher")

		// ModelID, Backend, Model and the persisted revision are what the
		// model loader and the controller key a backend process on.
		Expect(resolved.ModelID()).To(Equal("base-llm"))
		Expect(resolved.Backend).To(Equal(base.Backend))
		Expect(resolved.Model).To(Equal(base.Model))
		Expect(resolved.PersistedConfigRevision()).NotTo(BeEmpty())
		Expect(resolved.PersistedConfigRevision()).To(Equal(base.PersistedConfigRevision()))
		Expect(resolve("gpt-4").ModelID()).To(Equal(resolved.ModelID()))
	})

	It("does not leak the overlay into the stored target", func() {
		_ = resolve("researcher")

		base, ok := cl.GetModelConfig("base-llm")
		Expect(ok).To(BeTrue())
		Expect(base.SystemPrompt).To(Equal("base prompt"))
		Expect(base.DropsClientTools()).To(BeFalse())
		Expect(base.MCP.Auto).To(BeFalse())
		Expect(base.MCPSessionKey()).To(Equal("base-llm"))
		names, err := base.MCP.ServerNames()
		Expect(err).NotTo(HaveOccurred())
		Expect(names).To(Equal([]string{"base-tools"}))
	})

	It("resolves an alias without overlay fields to exactly the target", func() {
		base, ok := cl.GetModelConfig("base-llm")
		Expect(ok).To(BeTrue())

		resolved := resolve("gpt-4")
		Expect(*resolved).To(Equal(base))
		Expect(resolved.MCPSessionKey()).To(Equal("base-llm"))
	})

	It("ignores top-level system_prompt, mcp and client_tools on an alias, as before", func() {
		base, ok := cl.GetModelConfig("base-llm")
		Expect(ok).To(BeTrue())

		resolved := resolve("legacy")
		Expect(*resolved).To(Equal(base))
		Expect(resolved.SystemPrompt).To(Equal("base prompt"))
		Expect(resolved.DropsClientTools()).To(BeFalse())
		Expect(resolved.MCP.Auto).To(BeFalse())
		Expect(resolved.MCPSessionKey()).To(Equal("base-llm"))
	})

	It("keeps the target's servers and session key when the profile only sets mcp.auto", func() {
		dir := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(dir, "base-llm.yaml"), []byte(target), 0644)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(dir, "auto.yaml"), []byte("name: auto\nalias: base-llm\nprofile:\n  mcp:\n    auto: true\n"), 0644)).To(Succeed())
		cl = config.NewModelConfigLoader(dir)
		Expect(cl.LoadModelConfigsFromPath(dir)).To(Succeed())

		resolved := resolve("auto")
		Expect(resolved.MCP.Auto).To(BeTrue())
		names, err := resolved.MCP.ServerNames()
		Expect(err).NotTo(HaveOccurred())
		Expect(names).To(Equal([]string{"base-tools"}))
		Expect(resolved.MCPSessionKey()).To(Equal("base-llm"))
	})

	Context("validation", func() {
		It("rejects an unknown profile.client_tools value", func() {
			cfg := config.ModelConfig{Name: "p", Alias: "base-llm"}
			cfg.Profile.ClientTools = "ignore"
			ok, err := cfg.Validate()
			Expect(ok).To(BeFalse())
			Expect(err).To(MatchError(ContainSubstring("profile.client_tools")))
		})

		It("rejects a profile whose MCP config does not parse", func() {
			cfg := config.ModelConfig{Name: "p", Alias: "base-llm"}
			cfg.Profile.MCP.Servers = "{not yaml: ["
			ok, err := cfg.Validate()
			Expect(ok).To(BeFalse())
			Expect(err).To(MatchError(ContainSubstring("invalid MCP configuration")))
		})

		It("rejects a profile block on a model that is not an alias", func() {
			cfg := config.ModelConfig{Name: "m", Backend: "llama-cpp"}
			cfg.Profile.SystemPrompt = "persona"
			ok, err := cfg.Validate()
			Expect(ok).To(BeFalse())
			Expect(err).To(MatchError(ContainSubstring("only valid on an alias")))
		})

		It("accepts a well-formed profile alias and a plain alias", func() {
			cfg := config.ModelConfig{Name: "p", Alias: "base-llm"}
			cfg.Profile.SystemPrompt = "persona"
			cfg.Profile.ClientTools = config.ClientToolsDrop
			cfg.Profile.MCP.Servers = `{"mcpServers": {"web": {"url": "http://web.invalid/mcp"}}}`
			ok, err := cfg.Validate()
			Expect(err).NotTo(HaveOccurred())
			Expect(ok).To(BeTrue())

			plainCfg := config.ModelConfig{Name: "gpt-4", Alias: "base-llm"}
			ok, err = plainCfg.Validate()
			Expect(err).NotTo(HaveOccurred())
			Expect(ok).To(BeTrue())
		})
	})
})
