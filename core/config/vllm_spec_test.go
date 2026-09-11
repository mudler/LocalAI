package config_test

import (
	. "github.com/mudler/LocalAI/core/config"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("vllm-cpp speculative-decoding auto-defaults", func() {
	Context("HasSafetensorsMTPHead", func() {
		It("detects a top-level mtp_num_hidden_layers", func() {
			n, ok := HasSafetensorsMTPHead([]byte(`{
				"model_type": "qwen3_5_moe",
				"mtp_num_hidden_layers": 1
			}`))
			Expect(ok).To(BeTrue())
			Expect(n).To(Equal(uint32(1)))
		})

		It("detects the head nested under text_config", func() {
			// Multimodal checkpoints nest the language-model config, which is
			// where the MTP depth lives (mirrors the engine's own resolution
			// off config.raw text_config).
			n, ok := HasSafetensorsMTPHead([]byte(`{
				"model_type": "qwen3_5_moe",
				"text_config": {"mtp_num_hidden_layers": 2}
			}`))
			Expect(ok).To(BeTrue())
			Expect(n).To(Equal(uint32(2)))
		})

		It("reports no head when the key is absent", func() {
			n, ok := HasSafetensorsMTPHead([]byte(`{"model_type": "llama"}`))
			Expect(ok).To(BeFalse())
			Expect(n).To(BeZero())
		})

		It("reports no head for a zero depth", func() {
			_, ok := HasSafetensorsMTPHead([]byte(`{"mtp_num_hidden_layers": 0}`))
			Expect(ok).To(BeFalse())
		})

		It("ignores a DFlash draft checkpoint", func() {
			// A DFlash draft is a SEPARATE checkpoint that cannot serve alone:
			// it needs a target to verify against. Same exclusion the GGUF path
			// makes for gemma4-assistant drafts.
			_, ok := HasSafetensorsMTPHead([]byte(`{
				"model_type": "qwen3_dflash",
				"mtp_num_hidden_layers": 1,
				"dflash_config": {"mask_token_id": 151666, "target_layer_ids": [0, 1]}
			}`))
			Expect(ok).To(BeFalse())
		})

		It("reports no head on unparseable JSON", func() {
			_, ok := HasSafetensorsMTPHead([]byte(`{not json`))
			Expect(ok).To(BeFalse())
		})

		It("reports no head on empty input", func() {
			_, ok := HasSafetensorsMTPHead(nil)
			Expect(ok).To(BeFalse())
		})
	})

	Context("IsDFlashDraftConfig", func() {
		It("recognises a draft by its dflash_config block", func() {
			Expect(IsDFlashDraftConfig([]byte(`{
				"dflash_config": {"mask_token_id": 151666, "target_layer_ids": [0]}
			}`))).To(BeTrue())
		})

		It("does not flag an ordinary checkpoint", func() {
			Expect(IsDFlashDraftConfig([]byte(`{"model_type": "qwen3_5_moe"}`))).To(BeFalse())
		})
	})

	Context("ApplyVLLMSpeculativeDefaults", func() {
		It("writes the mtp method into engine_args", func() {
			cfg := &ModelConfig{Name: "qwen"}
			ApplyVLLMSpeculativeDefaults(cfg, 1)
			Expect(cfg.EngineArgs).To(HaveKey("speculative_config"))
			spec, ok := cfg.EngineArgs["speculative_config"].(map[string]any)
			Expect(ok).To(BeTrue())
			Expect(spec["method"]).To(Equal("mtp"))
		})

		It("leaves an existing speculative_config alone", func() {
			cfg := &ModelConfig{
				Name: "qwen",
				LLMConfig: LLMConfig{
					EngineArgs: map[string]any{
						"speculative_config": map[string]any{"method": "ngram", "num_speculative_tokens": 4},
					},
				},
			}
			ApplyVLLMSpeculativeDefaults(cfg, 1)
			spec := cfg.EngineArgs["speculative_config"].(map[string]any)
			Expect(spec["method"]).To(Equal("ngram"))
		})

		It("preserves unrelated engine_args keys", func() {
			cfg := &ModelConfig{
				Name:      "qwen",
				LLMConfig: LLMConfig{EngineArgs: map[string]any{"max_num_seqs": 32}},
			}
			ApplyVLLMSpeculativeDefaults(cfg, 1)
			Expect(cfg.EngineArgs).To(HaveKeyWithValue("max_num_seqs", 32))
			Expect(cfg.EngineArgs).To(HaveKey("speculative_config"))
		})

		It("tolerates a nil config", func() {
			Expect(func() { ApplyVLLMSpeculativeDefaults(nil, 1) }).ToNot(Panic())
		})
	})
})
