package config_test

import (
	"context"

	"github.com/mudler/LocalAI/core/config"
	localgrpc "github.com/mudler/LocalAI/pkg/grpc"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/reasoning"

	"github.com/gpustack/gguf-parser-go/util/ptr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
)

// stubModelMetadataBackend is a minimal grpc.Backend that only implements
// ModelMetadata. Any other method call panics via the nil embedded
// interface — intentional, so a regression that starts calling other
// probe methods fails loudly.
type stubModelMetadataBackend struct {
	localgrpc.Backend
	meta *pb.ModelMetadataResponse
}

func (s *stubModelMetadataBackend) ModelMetadata(context.Context, *pb.ModelOptions, ...grpc.CallOption) (*pb.ModelMetadataResponse, error) {
	return s.meta, nil
}

// Regression: when the thinking probe runs (e.g. because the media
// marker is still empty), DetectThinkingSupportFromBackend MUST NOT
// overwrite user-set reasoning.disable / reasoning.disable_reasoning_tag_prefill
// values. The probe gate is in core/backend/llm.go and can fire
// whenever *any* probe slot is empty — meaning a user who saved
// `reasoning.disable: true` in YAML would have it silently flipped
// back to GGUF-derived values on first load.
//
// See: bug report in #<TBD> — qwen_qwen3.5-2b with reasoning.disable=true
// on disk, but GET /api/models/config-json returned disable=false after
// first chat request.
var _ = Describe("DetectThinkingSupportFromBackend reasoning preservation", func() {
	var ctx context.Context
	var opts *pb.ModelOptions

	BeforeEach(func() {
		ctx = context.Background()
		opts = &pb.ModelOptions{}
	})

	It("preserves user-set DisableReasoning=true even when GGUF reports SupportsThinking=true", func() {
		cfg := &config.ModelConfig{
			Name:    "test-model",
			Backend: "llama-cpp",
			TemplateConfig: config.TemplateConfig{
				UseTokenizerTemplate: true,
			},
			ReasoningConfig: reasoning.Config{
				DisableReasoning: ptr.To(true),
			},
		}
		backend := &stubModelMetadataBackend{
			meta: &pb.ModelMetadataResponse{
				SupportsThinking: true,
				RenderedTemplate: "<|im_start|>user\n<|im_end|>\n<|im_start|>assistant\n<think>\n",
			},
		}

		config.DetectThinkingSupportFromBackend(ctx, cfg, backend, opts)

		Expect(cfg.ReasoningConfig.DisableReasoning).ToNot(BeNil())
		Expect(*cfg.ReasoningConfig.DisableReasoning).To(BeTrue(),
			"user's DisableReasoning=true must survive the auto-detection probe")
	})

	It("preserves user-set DisableReasoning=false even when GGUF reports SupportsThinking=false", func() {
		cfg := &config.ModelConfig{
			Name:    "test-model",
			Backend: "llama-cpp",
			TemplateConfig: config.TemplateConfig{
				UseTokenizerTemplate: true,
			},
			ReasoningConfig: reasoning.Config{
				DisableReasoning: ptr.To(false),
			},
		}
		backend := &stubModelMetadataBackend{
			meta: &pb.ModelMetadataResponse{
				SupportsThinking: false,
			},
		}

		config.DetectThinkingSupportFromBackend(ctx, cfg, backend, opts)

		Expect(cfg.ReasoningConfig.DisableReasoning).ToNot(BeNil())
		Expect(*cfg.ReasoningConfig.DisableReasoning).To(BeFalse(),
			"user's DisableReasoning=false must survive the auto-detection probe")
	})

	It("preserves user-set DisableReasoningTagPrefill across the probe", func() {
		cfg := &config.ModelConfig{
			Name:    "test-model",
			Backend: "llama-cpp",
			TemplateConfig: config.TemplateConfig{
				UseTokenizerTemplate: true,
			},
			ReasoningConfig: reasoning.Config{
				DisableReasoningTagPrefill: ptr.To(true),
			},
		}
		backend := &stubModelMetadataBackend{
			meta: &pb.ModelMetadataResponse{
				SupportsThinking: true,
				// non-empty rendered template triggers the :135 assignment branch
				RenderedTemplate: "<|im_start|>assistant\n<think>\n",
			},
		}

		config.DetectThinkingSupportFromBackend(ctx, cfg, backend, opts)

		Expect(cfg.ReasoningConfig.DisableReasoningTagPrefill).ToNot(BeNil())
		Expect(*cfg.ReasoningConfig.DisableReasoningTagPrefill).To(BeTrue(),
			"user's DisableReasoningTagPrefill=true must survive the auto-detection probe")
	})

	It("still fills nil fields from GGUF metadata (no regression in auto-detect)", func() {
		cfg := &config.ModelConfig{
			Name:    "test-model",
			Backend: "llama-cpp",
			TemplateConfig: config.TemplateConfig{
				UseTokenizerTemplate: true,
			},
			// both reasoning fields nil — user never touched them
		}
		backend := &stubModelMetadataBackend{
			meta: &pb.ModelMetadataResponse{
				SupportsThinking: true,
				RenderedTemplate: "<|im_start|>assistant\n<think>\n",
			},
		}

		config.DetectThinkingSupportFromBackend(ctx, cfg, backend, opts)

		Expect(cfg.ReasoningConfig.DisableReasoning).ToNot(BeNil())
		Expect(*cfg.ReasoningConfig.DisableReasoning).To(BeFalse(),
			"SupportsThinking=true should populate DisableReasoning=false when user left it nil")
		Expect(cfg.ReasoningConfig.DisableReasoningTagPrefill).ToNot(BeNil())
		Expect(*cfg.ReasoningConfig.DisableReasoningTagPrefill).To(BeFalse(),
			"rendered template ending with <think> should populate DisableReasoningTagPrefill=false when user left it nil")
	})
})
