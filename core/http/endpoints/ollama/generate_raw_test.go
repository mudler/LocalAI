package ollama_test

import (
	"context"
	"net/http/httptest"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/endpoints/ollama"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/templates"
	"github.com/mudler/LocalAI/pkg/model"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// A model with a native (tokenizer) chat template receives an empty prompt
// and structured messages. Raw generate requests must bypass that template,
// so the backend must get the prompt text and no messages.
var _ = Describe("Ollama generate with a native chat template", func() {
	var (
		gotPrompt   string
		gotMessages schema.Messages
	)

	BeforeEach(func() {
		original := backend.ModelInferenceFunc
		DeferCleanup(func() { backend.ModelInferenceFunc = original })
		gotPrompt, gotMessages = "", nil
		backend.ModelInferenceFunc = func(
			ctx context.Context, prompt string, messages schema.Messages,
			images, videos, audios []string, loader *model.ModelLoader,
			cfg *config.ModelConfig, cl *config.ModelConfigLoader, app *config.ApplicationConfig,
			tokenCallback func(string, backend.TokenUsage) bool, tools, toolChoice string,
			logprobs, topLogprobs *int, logitBias map[string]float64, metadata map[string]string,
		) (func() (backend.LLMResponse, error), error) {
			gotPrompt, gotMessages = prompt, messages
			return func() (backend.LLMResponse, error) {
				if tokenCallback != nil {
					tokenCallback("ok", backend.TokenUsage{})
				}
				return backend.LLMResponse{Response: "ok"}, nil
			}, nil
		}
	})

	generate := func(req *schema.OllamaGenerateRequest) *httptest.ResponseRecorder {
		cfg := &config.ModelConfig{}
		cfg.TemplateConfig.UseTokenizerTemplate = true
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest("POST", "/api/generate", nil)
		c := echo.New().NewContext(request, recorder)
		req.Model = "test-model"
		req.Ctx = request.Context()
		c.Set(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST, req)
		c.Set(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG, cfg)
		handler := ollama.GenerateEndpoint(nil, nil, templates.NewEvaluator(GinkgoT().TempDir()), config.NewApplicationConfig())
		Expect(handler(c)).To(Succeed())
		Expect(recorder.Code).To(Equal(200))
		return recorder
	}

	DescribeTable("sends a raw prompt to the backend unchanged, with no messages",
		func(stream bool) {
			const raw = "[INST] Say hi [/INST]"
			generate(&schema.OllamaGenerateRequest{Prompt: raw, System: "ignored", Raw: true, Stream: &stream})
			Expect(gotPrompt).To(Equal(raw))
			Expect(gotMessages).To(BeEmpty())
		},
		Entry("buffered", false),
		Entry("streaming", true),
	)

	It("sends the prompt as a user message when raw is not set", func() {
		stream := false
		generate(&schema.OllamaGenerateRequest{Prompt: "Say hi", Stream: &stream})
		Expect(gotPrompt).To(BeEmpty())
		Expect(gotMessages).To(HaveLen(1))
		Expect(gotMessages[0].Role).To(Equal("user"))
		Expect(gotMessages[0].Content).To(Equal("Say hi"))
	})
})
