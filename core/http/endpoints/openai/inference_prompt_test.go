package openai

import (
	"context"

	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/model"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ComputeChoices prompt routing", func() {
	var gotPrompt string

	BeforeEach(func() {
		original := backend.ModelInferenceFunc
		DeferCleanup(func() { backend.ModelInferenceFunc = original })
		gotPrompt = "unset"
		backend.ModelInferenceFunc = func(
			ctx context.Context, prompt string, messages schema.Messages,
			images, videos, audios []string, loader *model.ModelLoader,
			cfg *config.ModelConfig, cl *config.ModelConfigLoader, app *config.ApplicationConfig,
			tokenCallback func(string, backend.TokenUsage) bool, tools, toolChoice string,
			logprobs, topLogprobs *int, logitBias map[string]float64, metadata map[string]string,
		) (func() (backend.LLMResponse, error), error) {
			gotPrompt = prompt
			return func() (backend.LLMResponse, error) {
				return backend.LLMResponse{Response: "ok"}, nil
			}, nil
		}
	})

	compute := func(messages []schema.Message) {
		cfg := &config.ModelConfig{}
		cfg.TemplateConfig.UseTokenizerTemplate = true
		req := &schema.OpenAIRequest{Messages: messages, Context: context.Background()}
		_, _, _, err := ComputeChoices(req, "the prompt", cfg, nil, config.NewApplicationConfig(), nil,
			func(string, *[]schema.Choice) {}, nil)
		Expect(err).NotTo(HaveOccurred())
	}

	It("keeps the prompt of a request without messages", func() {
		compute(nil)
		Expect(gotPrompt).To(Equal("the prompt"))
	})

	It("sends an empty prompt when messages are present", func() {
		compute([]schema.Message{{Role: "user", Content: "hi", StringContent: "hi"}})
		Expect(gotPrompt).To(BeEmpty())
	})
})
