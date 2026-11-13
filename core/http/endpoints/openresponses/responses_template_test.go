// SPDX-License-Identifier: MIT
package openresponses

import (
	"context"
	"net/http/httptest"

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

var _ = Describe("Responses native chat template routing", func() {
	DescribeTable("keeps input and instructions as role-bearing messages", func(input any, stream bool, native bool) {
		original := backend.ModelInferenceFunc
		DeferCleanup(func() { backend.ModelInferenceFunc = original })
		called := false
		backend.ModelInferenceFunc = func(
			ctx context.Context, prompt string, messages schema.Messages,
			images, videos, audios []string, loader *model.ModelLoader,
			cfg *config.ModelConfig, cl *config.ModelConfigLoader, app *config.ApplicationConfig,
			tokenCallback func(string, backend.TokenUsage) bool, tools, toolChoice string,
			logprobs, topLogprobs *int, logitBias map[string]float64, metadata map[string]string,
		) (func() (backend.LLMResponse, error), error) {
			called = true
			if native {
				Expect(prompt).To(BeEmpty(), "a nonempty prompt bypasses the backend's chat template")
			} else {
				Expect(prompt).To(ContainSubstring("你好"))
			}
			Expect(messages).To(HaveLen(2))
			Expect(messages[0].Role).To(Equal("system"))
			Expect(messages[0].Content).To(Equal("请简短回答。"))
			Expect(messages[1].Role).To(Equal("user"))
			Expect(messages[1].Content).To(Equal("你好"))
			return func() (backend.LLMResponse, error) {
				usage := backend.TokenUsage{Prompt: 20, Completion: 3}
				if tokenCallback != nil {
					tokenCallback("你好！", usage)
				}
				return backend.LLMResponse{Response: "你好！", Usage: usage}, nil
			}, nil
		}

		cfg := &config.ModelConfig{}
		cfg.TemplateConfig.UseTokenizerTemplate = native
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest("POST", "/v1/responses", nil)
		c := echo.New().NewContext(request, recorder)
		store := false
		c.Set(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST, &schema.OpenResponsesRequest{
			Model: "test-model", Input: input, Instructions: "请简短回答。",
			Stream: stream, Store: &store, Context: request.Context(),
		})
		c.Set(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG, cfg)
		err := ResponsesEndpoint(nil, nil, templates.NewEvaluator(GinkgoT().TempDir()), config.NewApplicationConfig(), nil)(c)
		Expect(err).NotTo(HaveOccurred())
		Expect(called).To(BeTrue())
		Expect(recorder.Code).To(Equal(200))
		if stream {
			Expect(recorder.Body.String()).To(ContainSubstring("response.completed"))
		} else {
			Expect(recorder.Body.String()).To(ContainSubstring("你好！"))
		}
	},
		Entry("string input", "你好", false, true),
		Entry("streaming string input", "你好", true, true),
		Entry("message array", []any{map[string]any{"role": "user", "content": "你好"}}, false, true),
		Entry("streaming message array", []any{map[string]any{"role": "user", "content": "你好"}}, true, true),
		Entry("Go-side template fallback", "你好", false, false),
	)
})
