package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/templates"
	"github.com/mudler/LocalAI/pkg/model"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Non-streaming /v1/completions used to report usage 0/0/0 because the
// handler only accumulated timing fields from ComputeChoices and left
// Prompt/Completion at zero. These specs drive the real CompletionEndpoint
// against a stubbed backend so a missing accumulation fails as 0 vs 18.
var _ = Describe("non-streaming /v1/completions usage", func() {
	var origInference modelInferenceFunc

	BeforeEach(func() {
		origInference = backend.ModelInferenceFunc
	})

	AfterEach(func() {
		backend.ModelInferenceFunc = origInference
	})

	stubBackend := func(usage backend.TokenUsage, response string) {
		backend.ModelInferenceFunc = func(
			ctx context.Context, s string, messages schema.Messages,
			images, videos, audios []string,
			loader *model.ModelLoader, c *config.ModelConfig, cl *config.ModelConfigLoader,
			o *config.ApplicationConfig,
			tokenCallback func(string, backend.TokenUsage) bool,
			tools, toolChoice string,
			logprobs, topLogprobs *int,
			logitBias map[string]float64,
			metadata map[string]string,
		) (func() (backend.LLMResponse, error), error) {
			return func() (backend.LLMResponse, error) {
				return backend.LLMResponse{Response: response, Usage: usage}, nil
			}, nil
		}
	}

	run := func() *httptest.ResponseRecorder {
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		input := &schema.OpenAIRequest{
			PredictionOptions: schema.PredictionOptions{
				BasicModelRequest: schema.BasicModelRequest{Model: "test-model"},
			},
			Context: ctx,
			Cancel:  cancel,
		}
		cfg := &config.ModelConfig{}
		cfg.PromptStrings = []string{"hello"}

		e := echo.New()
		rec := httptest.NewRecorder()
		c := e.NewContext(httptest.NewRequest(http.MethodPost, "/v1/completions", strings.NewReader("{}")), rec)
		c.Set(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST, input)
		c.Set(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG, cfg)

		Expect(CompletionEndpoint(nil, nil, templates.NewEvaluator(""), config.NewApplicationConfig())(c)).To(Succeed())
		return rec
	}

	It("returns the backend prompt and completion counts instead of 0/0/0", func() {
		stubBackend(backend.TokenUsage{Prompt: 18, Completion: 213}, "world")

		rec := run()
		var resp schema.OpenAIResponse
		Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed(), rec.Body.String())
		Expect(resp.Usage).ToNot(BeNil())
		Expect(resp.Usage.PromptTokens).To(Equal(18),
			"non-streaming /v1/completions must accumulate Prompt from ComputeChoices")
		Expect(resp.Usage.CompletionTokens).To(Equal(213),
			"non-streaming /v1/completions must accumulate Completion from ComputeChoices")
		Expect(resp.Usage.TotalTokens).To(Equal(231))
	})

	It("sums usage across every prompt string", func() {
		call := 0
		backend.ModelInferenceFunc = func(
			ctx context.Context, s string, messages schema.Messages,
			images, videos, audios []string,
			loader *model.ModelLoader, c *config.ModelConfig, cl *config.ModelConfigLoader,
			o *config.ApplicationConfig,
			tokenCallback func(string, backend.TokenUsage) bool,
			tools, toolChoice string,
			logprobs, topLogprobs *int,
			logitBias map[string]float64,
			metadata map[string]string,
		) (func() (backend.LLMResponse, error), error) {
			return func() (backend.LLMResponse, error) {
				call++
				return backend.LLMResponse{
					Response: "ok",
					Usage:    backend.TokenUsage{Prompt: 10, Completion: 2},
				}, nil
			}, nil
		}

		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		input := &schema.OpenAIRequest{
			PredictionOptions: schema.PredictionOptions{
				BasicModelRequest: schema.BasicModelRequest{Model: "test-model"},
			},
			Context: ctx,
			Cancel:  cancel,
		}
		cfg := &config.ModelConfig{}
		cfg.PromptStrings = []string{"one", "two"}

		e := echo.New()
		rec := httptest.NewRecorder()
		c := e.NewContext(httptest.NewRequest(http.MethodPost, "/v1/completions", strings.NewReader("{}")), rec)
		c.Set(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST, input)
		c.Set(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG, cfg)

		Expect(CompletionEndpoint(nil, nil, templates.NewEvaluator(""), config.NewApplicationConfig())(c)).To(Succeed())

		var resp schema.OpenAIResponse
		Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed(), rec.Body.String())
		Expect(call).To(Equal(2))
		Expect(resp.Usage).ToNot(BeNil())
		Expect(resp.Usage.PromptTokens).To(Equal(20))
		Expect(resp.Usage.CompletionTokens).To(Equal(4))
		Expect(resp.Usage.TotalTokens).To(Equal(24))
	})
})
