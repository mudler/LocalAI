package config_test

import (
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("InferenceDefaults", func() {
	Describe("MatchModelFamily", func() {
		It("matches qwen3.5 and not qwen3 for Qwen3.5 model", func() {
			family := config.MatchModelFamily("unsloth/Qwen3.5-9B-GGUF")
			Expect(family).ToNot(BeNil())
			Expect(family["temperature"]).To(Equal(0.7))
			Expect(family["top_p"]).To(Equal(0.8))
			Expect(family["top_k"]).To(Equal(float64(20)))
			Expect(family["presence_penalty"]).To(Equal(1.5))
		})

		It("matches llama-3.3 and not llama-3 for Llama-3.3 model", func() {
			family := config.MatchModelFamily("meta-llama/Llama-3.3-70B")
			Expect(family).ToNot(BeNil())
			Expect(family["temperature"]).To(Equal(1.5))
		})

		It("is case insensitive", func() {
			family := config.MatchModelFamily("QWEN3.5-9B")
			Expect(family).ToNot(BeNil())
			Expect(family["temperature"]).To(Equal(0.7))
		})

		It("strips org prefix", func() {
			family := config.MatchModelFamily("someorg/deepseek-r1-7b.gguf")
			Expect(family).ToNot(BeNil())
			Expect(family["temperature"]).To(Equal(0.6))
		})

		It("strips .gguf extension", func() {
			family := config.MatchModelFamily("gemma-3-4b-q4_k_m.gguf")
			Expect(family).ToNot(BeNil())
			Expect(family["temperature"]).To(Equal(1.0))
			Expect(family["top_k"]).To(Equal(float64(64)))
		})

		It("returns nil for unknown model", func() {
			family := config.MatchModelFamily("my-custom-model-v1")
			Expect(family).To(BeNil())
		})

		It("matches qwen3-coder before qwen3", func() {
			family := config.MatchModelFamily("Qwen3-Coder-8B")
			Expect(family).ToNot(BeNil())
			Expect(family["temperature"]).To(Equal(0.7))
			Expect(family["top_p"]).To(Equal(0.8))
		})

		It("matches deepseek-v3", func() {
			family := config.MatchModelFamily("deepseek-v3-base")
			Expect(family).ToNot(BeNil())
			Expect(family["temperature"]).To(Equal(0.6))
		})

		It("matches lfm2 with non-standard params", func() {
			family := config.MatchModelFamily("lfm2-7b")
			Expect(family).ToNot(BeNil())
			Expect(family["temperature"]).To(Equal(0.1))
			Expect(family["top_p"]).To(Equal(0.1))
			Expect(family["min_p"]).To(Equal(0.15))
			Expect(family["repeat_penalty"]).To(Equal(1.05))
		})

		It("includes min_p for llama-3.3", func() {
			family := config.MatchModelFamily("llama-3.3-70b")
			Expect(family).ToNot(BeNil())
			Expect(family["min_p"]).To(Equal(0.1))
		})
	})

	Describe("ApplyInferenceDefaults", func() {
		It("fills nil fields from defaults", func() {
			cfg := &config.ModelConfig{}
			config.ApplyInferenceDefaults(cfg, "gemma-3-8b")

			Expect(cfg.Temperature).ToNot(BeNil())
			Expect(*cfg.Temperature).To(Equal(1.0))
			Expect(cfg.TopP).ToNot(BeNil())
			Expect(*cfg.TopP).To(Equal(0.95))
			Expect(cfg.TopK).ToNot(BeNil())
			Expect(*cfg.TopK).To(Equal(64))
			Expect(cfg.MinP).ToNot(BeNil())
			Expect(*cfg.MinP).To(Equal(0.0))
			Expect(cfg.RepeatPenalty).To(Equal(1.0))
		})

		It("fills min_p with non-zero value", func() {
			cfg := &config.ModelConfig{}
			config.ApplyInferenceDefaults(cfg, "llama-3.3-8b")

			Expect(cfg.MinP).ToNot(BeNil())
			Expect(*cfg.MinP).To(Equal(0.1))
		})

		It("preserves non-nil fields", func() {
			temp := 0.5
			topK := 10
			cfg := &config.ModelConfig{
				PredictionOptions: schema.PredictionOptions{
					Temperature: &temp,
					TopK:        &topK,
				},
			}
			config.ApplyInferenceDefaults(cfg, "gemma-3-8b")

			Expect(*cfg.Temperature).To(Equal(0.5))
			Expect(*cfg.TopK).To(Equal(10))
			// TopP should be filled since it was nil
			Expect(cfg.TopP).ToNot(BeNil())
			Expect(*cfg.TopP).To(Equal(0.95))
		})

		It("preserves non-zero repeat penalty", func() {
			cfg := &config.ModelConfig{
				PredictionOptions: schema.PredictionOptions{
					RepeatPenalty: 1.2,
				},
			}
			config.ApplyInferenceDefaults(cfg, "gemma-3-8b")
			Expect(cfg.RepeatPenalty).To(Equal(1.2))
		})

		It("preserves non-nil min_p", func() {
			minP := 0.05
			cfg := &config.ModelConfig{
				PredictionOptions: schema.PredictionOptions{
					MinP: &minP,
				},
			}
			config.ApplyInferenceDefaults(cfg, "llama-3.3-8b")
			Expect(*cfg.MinP).To(Equal(0.05))
		})

		It("does nothing for unknown model", func() {
			cfg := &config.ModelConfig{}
			config.ApplyInferenceDefaults(cfg, "my-custom-model")

			Expect(cfg.Temperature).To(BeNil())
			Expect(cfg.TopP).To(BeNil())
			Expect(cfg.TopK).To(BeNil())
			Expect(cfg.MinP).To(BeNil())
		})
	})
})
