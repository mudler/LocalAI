// SPDX-License-Identifier: MIT
package openresponses

import (
	"context"
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

// A model with template.use_tokenizer_template applies its chat template in
// the backend. The endpoint must send it an empty prompt — a rendered one
// makes the backend continue plain text instead of answering the message.
var _ = Describe("Responses prompt for tokenizer-template models", func() {
	DescribeTable("renders the prompt only when the model does not tokenize itself", func(useTokenizerTemplate bool, wantEmpty bool) {
		var gotPrompt *string
		originalInference := backend.ModelInferenceFunc
		DeferCleanup(func() { backend.ModelInferenceFunc = originalInference })
		backend.ModelInferenceFunc = func(
			ctx context.Context, prompt string, messages schema.Messages,
			images, videos, audios []string, loader *model.ModelLoader,
			cfg *config.ModelConfig, cl *config.ModelConfigLoader, app *config.ApplicationConfig,
			tokenCallback func(string, backend.TokenUsage) bool, tools, toolChoice string,
			logprobs, topLogprobs *int, logitBias map[string]float64, metadata map[string]string,
		) (func() (backend.LLMResponse, error), error) {
			gotPrompt = &prompt
			return func() (backend.LLMResponse, error) {
				return backend.LLMResponse{Response: "hello", Usage: backend.TokenUsage{Prompt: 1, Completion: 1}}, nil
			}, nil
		}

		cfg := &config.ModelConfig{}
		cfg.TemplateConfig.UseTokenizerTemplate = useTokenizerTemplate
		e := echo.New()
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
		c := e.NewContext(request, recorder)
		input := &schema.OpenResponsesRequest{Model: "test-model", Input: "hello"}
		store := false
		input.Store = &store
		c.Set(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST, input)
		c.Set(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG, cfg)

		handler := ResponsesEndpoint(nil, nil, templates.NewEvaluator(""), config.NewApplicationConfig(), nil)
		Expect(handler(c)).To(Succeed())
		Expect(gotPrompt).ToNot(BeNil(), "inference ran")
		if wantEmpty {
			Expect(*gotPrompt).To(BeEmpty(), "a tokenizer-template model gets no rendered prompt")
		} else {
			Expect(strings.TrimSpace(*gotPrompt)).ToNot(BeEmpty(), "a Go-templated model gets its rendered prompt")
		}
	},
		Entry("empty prompt for a native tokenizer template", true, true),
		Entry("rendered prompt for a Go-side template", false, false),
	)
})
