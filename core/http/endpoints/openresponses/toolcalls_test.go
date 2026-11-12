// SPDX-License-Identifier: MIT
package openresponses

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/functions"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/model"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Tool calls that do not fit the request's tools must not reach the client,
// whether Go-side parsing found them in the text or llama.cpp's autoparser
// returned them (it parses the arguments as any JSON when no grammar was
// built). The case that prompted this: bash called with {"command": ...}
// where the schema has "script".
var _ = Describe("Responses tool calls are validated", func() {
	funcs := functions.Functions{{
		Name: "bash",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{"script": map[string]any{"type": "string"}},
			"required":   []any{"script"},
		},
	}}
	const badCall = `{"name": "bash", "arguments": {"command": "ls"}}`
	const goodCall = `{"name": "bash", "arguments": {"script": "ls"}}`

	// modelSays stubs the backend: text streams through the token callback
	// (when there is one) and deltas, if any, come back as the autoparser's.
	modelSays := func(text string, deltas []*pb.ChatDelta) {
		orig := backend.ModelInferenceFunc
		DeferCleanup(func() { backend.ModelInferenceFunc = orig })
		backend.ModelInferenceFunc = func(
			ctx context.Context, prompt string, messages schema.Messages,
			images, videos, audios []string, loader *model.ModelLoader,
			cfg *config.ModelConfig, cl *config.ModelConfigLoader, app *config.ApplicationConfig,
			tokenCallback func(string, backend.TokenUsage) bool, tools, toolChoice string,
			logprobs, topLogprobs *int, logitBias map[string]float64, metadata map[string]string,
		) (func() (backend.LLMResponse, error), error) {
			return func() (backend.LLMResponse, error) {
				if tokenCallback != nil {
					tokenCallback(text, backend.TokenUsage{ChatDeltas: deltas})
				}
				return backend.LLMResponse{Response: text, ChatDeltas: deltas}, nil
			}, nil
		}
	}
	autoparserCall := func(args string) []*pb.ChatDelta {
		return []*pb.ChatDelta{{ToolCalls: []*pb.ToolCallDelta{{Name: "bash", Arguments: args}}}}
	}

	input := &schema.OpenResponsesRequest{Model: "test-model", Input: "hello"}

	nonStream := func(cfg *config.ModelConfig) *schema.ORResponseResource {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/v1/responses", nil)
		c := echo.New().NewContext(req, rec)
		Expect(handleOpenResponsesNonStream(c, "resp_test", 1, input, cfg, nil, nil, config.NewApplicationConfig(), "hello",
			&schema.OpenAIRequest{Context: req.Context()}, funcs, true, false, nil, nil, 0)).To(Succeed())
		var resp schema.ORResponseResource
		Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
		return &resp
	}

	// stream returns the completed response and the streamed message text.
	stream := func(cfg *config.ModelConfig) (*schema.ORResponseResource, string) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/v1/responses", nil)
		c := echo.New().NewContext(req, rec)
		Expect(handleOpenResponsesStream(c, "resp_test", 1, input, cfg, nil, nil, config.NewApplicationConfig(), "hello",
			&schema.OpenAIRequest{Context: req.Context()}, funcs, true, false, nil, nil)).To(Succeed())
		var completed *schema.ORResponseResource
		var deltas strings.Builder
		for _, line := range strings.Split(rec.Body.String(), "\n") {
			if !strings.HasPrefix(line, "data: ") || line == "data: [DONE]" {
				continue
			}
			var ev schema.ORStreamEvent
			Expect(json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev)).To(Succeed())
			switch ev.Type {
			case "response.output_text.delta":
				if ev.Delta != nil {
					deltas.WriteString(*ev.Delta)
				}
			case "response.completed":
				completed = ev.Response
			}
		}
		Expect(completed).NotTo(BeNil())
		return completed, deltas.String()
	}

	types := func(r *schema.ORResponseResource) []string {
		var out []string
		for _, item := range r.Output {
			out = append(out, item.Type)
		}
		return out
	}
	messageText := func(r *schema.ORResponseResource) string {
		var b strings.Builder
		for _, item := range r.Output {
			if item.Type != "message" {
				continue
			}
			raw, _ := json.Marshal(item.Content)
			var parts []schema.ORContentPart
			if json.Unmarshal(raw, &parts) == nil {
				for _, p := range parts {
					b.WriteString(p.Text)
				}
			}
		}
		return b.String()
	}

	Context("non-stream", func() {
		It("answers with the text instead of a parsed call that does not fit", func() {
			modelSays(badCall, nil)
			resp := nonStream(&config.ModelConfig{})
			Expect(types(resp)).NotTo(ContainElement("function_call"))
			Expect(messageText(resp)).To(ContainSubstring(`"command"`))
		})

		It("answers with the call as text when the autoparser's call does not fit", func() {
			modelSays("", autoparserCall(`{"command":"ls"}`))
			resp := nonStream(&config.ModelConfig{})
			Expect(types(resp)).NotTo(ContainElement("function_call"))
			Expect(messageText(resp)).To(ContainSubstring(`"command"`))
		})

		It("keeps a call that fits", func() {
			modelSays(goodCall, nil)
			Expect(types(nonStream(&config.ModelConfig{}))).To(ContainElement("function_call"))
		})

		It("keeps the old behavior with disable_tool_call_validation", func() {
			modelSays(badCall, nil)
			cfg := &config.ModelConfig{}
			cfg.FunctionsConfig.DisableToolCallValidation = true
			Expect(types(nonStream(cfg))).To(ContainElement("function_call"))
		})
	})

	Context("stream", func() {
		It("streams the text instead of a parsed call that does not fit", func() {
			modelSays(badCall, nil)
			resp, text := stream(&config.ModelConfig{})
			Expect(types(resp)).NotTo(ContainElement("function_call"))
			Expect(messageText(resp)).To(ContainSubstring(`"command"`))
			Expect(text).To(ContainSubstring(`"command"`), "the text must arrive as output_text deltas too")
		})

		It("streams the call as text when the autoparser's call does not fit", func() {
			modelSays("", autoparserCall(`{"command":"ls"}`))
			resp, text := stream(&config.ModelConfig{})
			Expect(types(resp)).NotTo(ContainElement("function_call"))
			Expect(text).To(ContainSubstring(`"command"`))
		})

		It("emits a call that fits", func() {
			modelSays(goodCall, nil)
			resp, _ := stream(&config.ModelConfig{})
			Expect(types(resp)).To(ContainElement("function_call"))
		})

		It("keeps the old behavior with disable_tool_call_validation", func() {
			modelSays(badCall, nil)
			cfg := &config.ModelConfig{}
			cfg.FunctionsConfig.DisableToolCallValidation = true
			resp, _ := stream(cfg)
			Expect(types(resp)).To(ContainElement("function_call"))
		})
	})
})
