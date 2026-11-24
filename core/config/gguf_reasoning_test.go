package config

import (
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/reasoning"

	"github.com/gpustack/gguf-parser-go/util/ptr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("GGUF backend metadata reasoning defaults", func() {
	It("fills reasoning defaults when unset", func() {
		cfg := &ModelConfig{
			TemplateConfig: TemplateConfig{UseTokenizerTemplate: true},
		}

		applyDetectedThinkingConfig(cfg, &pb.ModelMetadataResponse{
			SupportsThinking: true,
			RenderedTemplate: "{{ bos_token }}<think>",
		})

		Expect(cfg.ReasoningConfig.DisableReasoning).ToNot(BeNil())
		Expect(*cfg.ReasoningConfig.DisableReasoning).To(BeFalse())
		Expect(cfg.ReasoningConfig.DisableReasoningTagPrefill).ToNot(BeNil())
		Expect(*cfg.ReasoningConfig.DisableReasoningTagPrefill).To(BeFalse())
	})

	It("preserves fully explicit reasoning settings", func() {
		cfg := &ModelConfig{
			TemplateConfig: TemplateConfig{UseTokenizerTemplate: true},
			ReasoningConfig: reasoning.Config{
				DisableReasoning:           ptr.To(true),
				DisableReasoningTagPrefill: ptr.To(true),
			},
		}

		applyDetectedThinkingConfig(cfg, &pb.ModelMetadataResponse{
			SupportsThinking: true,
			RenderedTemplate: "{{ bos_token }}<think>",
		})

		Expect(cfg.ReasoningConfig.DisableReasoning).ToNot(BeNil())
		Expect(*cfg.ReasoningConfig.DisableReasoning).To(BeTrue())
		Expect(cfg.ReasoningConfig.DisableReasoningTagPrefill).ToNot(BeNil())
		Expect(*cfg.ReasoningConfig.DisableReasoningTagPrefill).To(BeTrue())
	})

	It("preserves explicit disable while still inferring missing prefill", func() {
		cfg := &ModelConfig{
			TemplateConfig: TemplateConfig{UseTokenizerTemplate: true},
			ReasoningConfig: reasoning.Config{
				DisableReasoning: ptr.To(true),
			},
		}

		applyDetectedThinkingConfig(cfg, &pb.ModelMetadataResponse{
			SupportsThinking: true,
			RenderedTemplate: "{{ bos_token }}<think>",
		})

		Expect(cfg.ReasoningConfig.DisableReasoning).ToNot(BeNil())
		Expect(*cfg.ReasoningConfig.DisableReasoning).To(BeTrue())
		Expect(cfg.ReasoningConfig.DisableReasoningTagPrefill).ToNot(BeNil())
		Expect(*cfg.ReasoningConfig.DisableReasoningTagPrefill).To(BeFalse())
	})

	It("preserves explicit prefill while still inferring missing disable flag", func() {
		cfg := &ModelConfig{
			TemplateConfig: TemplateConfig{UseTokenizerTemplate: true},
			ReasoningConfig: reasoning.Config{
				DisableReasoningTagPrefill: ptr.To(true),
			},
		}

		applyDetectedThinkingConfig(cfg, &pb.ModelMetadataResponse{
			SupportsThinking: true,
			RenderedTemplate: "{{ bos_token }}<think>",
		})

		Expect(cfg.ReasoningConfig.DisableReasoning).ToNot(BeNil())
		Expect(*cfg.ReasoningConfig.DisableReasoning).To(BeFalse())
		Expect(cfg.ReasoningConfig.DisableReasoningTagPrefill).ToNot(BeNil())
		Expect(*cfg.ReasoningConfig.DisableReasoningTagPrefill).To(BeTrue())
	})

	It("defaults to disabling reasoning when backend does not support thinking", func() {
		cfg := &ModelConfig{
			TemplateConfig: TemplateConfig{UseTokenizerTemplate: true},
		}

		applyDetectedThinkingConfig(cfg, &pb.ModelMetadataResponse{
			SupportsThinking: false,
		})

		Expect(cfg.ReasoningConfig.DisableReasoning).ToNot(BeNil())
		Expect(*cfg.ReasoningConfig.DisableReasoning).To(BeTrue())
		Expect(cfg.ReasoningConfig.DisableReasoningTagPrefill).ToNot(BeNil())
		Expect(*cfg.ReasoningConfig.DisableReasoningTagPrefill).To(BeTrue())
	})
})
