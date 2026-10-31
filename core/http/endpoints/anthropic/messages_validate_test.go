package anthropic

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/model"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// A tool call that does not fit the request's tools must not reach the
// client, whether Go-side parsing found it in the text or the C++ autoparser
// returned it. The case that prompted this: a model called a bash tool whose
// schema has "script" with {"command": ...}.
var _ = Describe("Anthropic tool calls are validated", func() {
	origInference := backend.ModelInferenceFunc
	AfterEach(func() { backend.ModelInferenceFunc = origInference })

	// modelSaysWith stubs the backend: it streams text through the token
	// callback (when there is one) and returns it with deltas as the C++
	// autoparser's ChatDeltas. With no deltas it is a backend whose parser
	// found no tool call.
	modelSaysWith := func(text string, deltas []*pb.ChatDelta) {
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
				if tokenCallback != nil {
					tokenCallback(text, backend.TokenUsage{ChatDeltas: deltas})
				}
				return backend.LLMResponse{Response: text, ChatDeltas: deltas}, nil
			}, nil
		}
	}
	modelSays := func(text string) { modelSaysWith(text, nil) }
	autoparserCall := func(args string) []*pb.ChatDelta {
		return []*pb.ChatDelta{{ToolCalls: []*pb.ToolCallDelta{{Name: "bash", Arguments: args}}}}
	}

	input := &schema.AnthropicRequest{
		Model: "test-model",
		Tools: []schema.AnthropicTool{{
			Name: "bash",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"script": map[string]any{"type": "string"}},
				"required":   []any{"script"},
			},
		}},
	}

	run := func(stream bool, cfg *config.ModelConfig) string {
		funcs, _ := convertAnthropicTools(input, cfg)
		Expect(funcs).To(HaveLen(1))
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		openAIReq := &schema.OpenAIRequest{Context: ctx, Cancel: cancel}
		openAIReq.Model = input.Model

		rec := httptest.NewRecorder()
		c := echo.New().NewContext(httptest.NewRequest(http.MethodPost, "/v1/messages", nil), rec)
		var err error
		if stream {
			err = handleAnthropicStream(c, "id1", input, cfg, nil, nil, config.NewApplicationConfig(), "prompt", openAIReq, funcs, true, nil, nil)
		} else {
			err = handleAnthropicNonStream(c, "id1", input, cfg, nil, nil, config.NewApplicationConfig(), "prompt", openAIReq, funcs, true, nil, nil)
		}
		Expect(err).ToNot(HaveOccurred())
		return rec.Body.String()
	}

	nonStream := func(cfg *config.ModelConfig) schema.AnthropicResponse {
		var resp schema.AnthropicResponse
		Expect(json.Unmarshal([]byte(run(false, cfg)), &resp)).To(Succeed())
		return resp
	}

	const badCall = `{"name": "bash", "arguments": {"command": "ls"}}`

	It("non-stream: answers with the text instead of a call that does not fit", func() {
		modelSays(badCall)
		resp := nonStream(&config.ModelConfig{})
		Expect(*resp.StopReason).To(Equal("end_turn"))
		Expect(resp.Content).To(HaveLen(1))
		Expect(resp.Content[0].Type).To(Equal("text"))
		Expect(resp.Content[0].Text).To(ContainSubstring(`"command"`))
	})

	It("non-stream: keeps a call that fits", func() {
		modelSays(`{"name": "bash", "arguments": {"script": "ls"}}`)
		resp := nonStream(&config.ModelConfig{})
		Expect(*resp.StopReason).To(Equal("tool_use"))
	})

	It("non-stream: keeps the old behavior with disable_tool_call_validation", func() {
		modelSays(badCall)
		cfg := &config.ModelConfig{}
		cfg.FunctionsConfig.DisableToolCallValidation = true
		Expect(*nonStream(cfg).StopReason).To(Equal("tool_use"))
	})

	It("stream: sends the text and no tool_use block for a call that does not fit", func() {
		modelSays(badCall)
		body := run(true, &config.ModelConfig{})
		Expect(body).ToNot(ContainSubstring(`"tool_use"`))
		Expect(body).To(ContainSubstring(`"text_delta"`))
		Expect(body).To(ContainSubstring(`\"command\"`))
	})

	It("stream: emits a tool_use block for a call that fits", func() {
		modelSays(`{"name": "bash", "arguments": {"script": "ls"}}`)
		Expect(run(true, &config.ModelConfig{})).To(ContainSubstring(`"tool_use"`))
	})

	It("stream: keeps the old behavior with disable_tool_call_validation", func() {
		modelSays(badCall)
		cfg := &config.ModelConfig{}
		cfg.FunctionsConfig.DisableToolCallValidation = true
		Expect(strings.Contains(run(true, cfg), `"tool_use"`)).To(BeTrue())
	})

	It("non-stream: answers with the autoparser's call as text when it does not fit", func() {
		modelSaysWith("", autoparserCall(`{"command":"ls"}`))
		resp := nonStream(&config.ModelConfig{})
		Expect(*resp.StopReason).To(Equal("end_turn"))
		Expect(resp.Content).To(HaveLen(1))
		Expect(resp.Content[0].Type).To(Equal("text"))
		Expect(resp.Content[0].Text).To(ContainSubstring(`"command"`))
	})

	It("stream: sends the autoparser's call as text when it does not fit", func() {
		modelSaysWith("", autoparserCall(`{"command":"ls"}`))
		body := run(true, &config.ModelConfig{})
		Expect(body).ToNot(ContainSubstring(`"tool_use"`))
		Expect(body).To(ContainSubstring(`\"command\"`))
	})

	It("stream: emits the autoparser's call when it fits", func() {
		modelSaysWith("", autoparserCall(`{"script":"ls"}`))
		Expect(run(true, &config.ModelConfig{})).To(ContainSubstring(`"tool_use"`))
	})
})
