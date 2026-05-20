package ollama

import (
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/functions"
	"github.com/mudler/LocalAI/pkg/reasoning"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func boolPtr(b bool) *bool { return &b }

func withKnownUsecases(cfg config.ModelConfig, flags ...string) config.ModelConfig {
	cfg.KnownUsecaseStrings = flags
	cfg.KnownUsecases = config.GetUsecasesFromYAML(flags)
	return cfg
}

var _ = Describe("modelCapabilities", func() {
	DescribeTable("derives Ollama capability strings from a ModelConfig",
		func(cfg config.ModelConfig, expected []string) {
			caps := modelCapabilities(&cfg)
			if len(expected) == 0 {
				Expect(caps).To(BeEmpty())
				return
			}
			Expect(caps).To(ConsistOf(expected))
		},
		Entry("an embedding-only model exposes the embedding capability",
			config.ModelConfig{
				Name:       "embed-model",
				Backend:    "llama-cpp",
				Embeddings: boolPtr(true),
			},
			[]string{"embedding"},
		),
		Entry("a chat-template model exposes the completion capability",
			config.ModelConfig{
				Name:    "chat-model",
				Backend: "llama-cpp",
				TemplateConfig: config.TemplateConfig{
					Chat: "{{ .Input }}",
				},
			},
			[]string{"completion"},
		),
		Entry("a vision-capable chat model exposes completion + vision",
			withKnownUsecases(config.ModelConfig{
				Name:    "vision-model",
				Backend: "llama-cpp",
				TemplateConfig: config.TemplateConfig{
					Chat:       "{{ .Input }}",
					Multimodal: "<__media__>",
				},
			}, "FLAG_CHAT", "FLAG_VISION"),
			[]string{"completion", "vision"},
		),
		Entry("a model with reasoning enabled exposes the thinking capability",
			config.ModelConfig{
				Name:    "thinking-model",
				Backend: "llama-cpp",
				TemplateConfig: config.TemplateConfig{
					Chat: "{{ .Input }}",
				},
				ReasoningConfig: reasoning.Config{
					DisableReasoning: boolPtr(false),
				},
			},
			[]string{"completion", "thinking"},
		),
		Entry("a model with detected tool-format markers exposes the tools capability",
			config.ModelConfig{
				Name:    "tools-model",
				Backend: "llama-cpp",
				TemplateConfig: config.TemplateConfig{
					Chat: "{{ .Input }}",
				},
				FunctionsConfig: functions.FunctionsConfig{
					ToolFormatMarkers: &functions.ToolFormatMarkers{FormatType: "json_native"},
				},
			},
			[]string{"completion", "tools"},
		),
		Entry("a model with an explicit JSON regex match exposes the tools capability",
			config.ModelConfig{
				Name:    "tools-regex-model",
				Backend: "llama-cpp",
				TemplateConfig: config.TemplateConfig{
					Chat: "{{ .Input }}",
				},
				FunctionsConfig: functions.FunctionsConfig{
					JSONRegexMatch: []string{`(?s).*`},
				},
			},
			[]string{"completion", "tools"},
		),
		Entry("a pure backend-only model (no template, no embeddings) reports no capabilities",
			config.ModelConfig{
				Name:    "rerank-model",
				Backend: "rerankers",
			},
			[]string{},
		),
	)
})

var _ = Describe("modelDetailsFromModelConfig", func() {
	It("reports gguf format and llama-cpp family/families for a llama-cpp model", func() {
		cfg := config.ModelConfig{
			Name:    "llama",
			Backend: "llama-cpp",
		}
		details := modelDetailsFromModelConfig(&cfg)
		Expect(details.Format).To(Equal("gguf"))
		Expect(details.Family).To(Equal("llama-cpp"))
		Expect(details.Families).To(ConsistOf("llama-cpp"))
	})

	It("extracts quantization_level from the model filename when present", func() {
		cfg := config.ModelConfig{
			Name:    "qwen-q4",
			Backend: "llama-cpp",
		}
		cfg.Model = "Qwen3-4B-Instruct-Q4_K_M.gguf"
		details := modelDetailsFromModelConfig(&cfg)
		Expect(details.QuantizationLevel).To(Equal("Q4_K_M"))
	})

	It("extracts parameter_size from the model filename when present", func() {
		cfg := config.ModelConfig{
			Name:    "qwen-4b",
			Backend: "llama-cpp",
		}
		cfg.Model = "Qwen3-4B-Instruct-Q4_K_M.gguf"
		details := modelDetailsFromModelConfig(&cfg)
		Expect(details.ParameterSize).To(Equal("4B"))
	})
})
