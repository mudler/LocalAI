package config_test

import (
	. "github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Backend hooks and parser defaults", func() {
	Context("MatchParserDefaults", func() {
		It("matches Qwen3 family", func() {
			parsers := MatchParserDefaults("Qwen/Qwen3-8B")
			Expect(parsers).NotTo(BeNil())
			Expect(parsers["tool_parser"]).To(Equal("hermes"))
			Expect(parsers["reasoning_parser"]).To(Equal("qwen3"))
		})

		It("matches Qwen3.5 with longest-prefix-first", func() {
			parsers := MatchParserDefaults("Qwen/Qwen3.5-9B")
			Expect(parsers).NotTo(BeNil())
			Expect(parsers["tool_parser"]).To(Equal("qwen3_xml"))
		})

		It("matches Llama-3.3 not Llama-3.2", func() {
			parsers := MatchParserDefaults("meta/Llama-3.3-70B-Instruct")
			Expect(parsers).NotTo(BeNil())
			Expect(parsers["tool_parser"]).To(Equal("llama3_json"))
		})

		It("matches deepseek-r1", func() {
			parsers := MatchParserDefaults("deepseek-ai/DeepSeek-R1")
			Expect(parsers).NotTo(BeNil())
			Expect(parsers["reasoning_parser"]).To(Equal("deepseek_r1"))
			Expect(parsers["tool_parser"]).To(Equal("deepseek_v3"))
		})

		It("returns nil for unknown families", func() {
			Expect(MatchParserDefaults("acme/unknown-model-xyz")).To(BeNil())
		})
	})

	Context("Backend hook registration and execution", func() {
		It("runs registered hook for a backend", func() {
			called := false
			RegisterBackendHook("test-backend-hook", func(cfg *ModelConfig, modelPath string) {
				called = true
				cfg.Description = "modified-by-hook"
			})

			cfg := &ModelConfig{
				Backend: "test-backend-hook",
			}
			// Use the public Prepare path indirectly is heavy; instead exercise via vllmDefaults
			// path, but here just call RegisterBackendHook + we know runBackendHooks is internal.
			// Verify by leveraging Prepare on a fresh ModelConfig with no model path.
			cfg.PredictionOptions = schema.PredictionOptions{}

			// Trigger via Prepare with empty options; this calls runBackendHooks internally.
			cfg.SetDefaults()
			Expect(called).To(BeTrue())
			Expect(cfg.Description).To(Equal("modified-by-hook"))
		})
	})

	Context("vllmDefaults hook", func() {
		It("auto-sets parsers for known model families on vllm backend", func() {
			cfg := &ModelConfig{
				Backend: "vllm",
				PredictionOptions: schema.PredictionOptions{
					BasicModelRequest: schema.BasicModelRequest{
						Model: "Qwen/Qwen3-8B",
					},
				},
			}
			cfg.SetDefaults()

			foundTool := false
			foundReasoning := false
			for _, opt := range cfg.Options {
				if opt == "tool_parser:hermes" {
					foundTool = true
				}
				if opt == "reasoning_parser:qwen3" {
					foundReasoning = true
				}
			}
			Expect(foundTool).To(BeTrue())
			Expect(foundReasoning).To(BeTrue())
		})

		It("does not override user-set tool_parser", func() {
			cfg := &ModelConfig{
				Backend: "vllm",
				Options: []string{"tool_parser:custom"},
				PredictionOptions: schema.PredictionOptions{
					BasicModelRequest: schema.BasicModelRequest{
						Model: "Qwen/Qwen3-8B",
					},
				},
			}
			cfg.SetDefaults()

			count := 0
			for _, opt := range cfg.Options {
				if len(opt) >= len("tool_parser:") && opt[:len("tool_parser:")] == "tool_parser:" {
					count++
				}
			}
			Expect(count).To(Equal(1))
		})

		It("seeds production engine_args defaults", func() {
			cfg := &ModelConfig{Backend: "vllm"}
			cfg.SetDefaults()

			Expect(cfg.EngineArgs).NotTo(BeNil())
			Expect(cfg.EngineArgs["enable_prefix_caching"]).To(Equal(true))
			Expect(cfg.EngineArgs["enable_chunked_prefill"]).To(Equal(true))
		})

		It("does not override user-set engine_args", func() {
			cfg := &ModelConfig{
				Backend: "vllm",
				LLMConfig: LLMConfig{
					EngineArgs: map[string]any{
						"enable_prefix_caching": false,
					},
				},
			}
			cfg.SetDefaults()

			Expect(cfg.EngineArgs["enable_prefix_caching"]).To(Equal(false))
			// chunked_prefill is still seeded since user didn't set it
			Expect(cfg.EngineArgs["enable_chunked_prefill"]).To(Equal(true))
		})
	})
})
