package openai

import (
	"context"
	"strings"

	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/functions"
	"github.com/mudler/LocalAI/pkg/model"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// A tool call LocalAI parses from the model's text, with no grammar behind
// it, must fit the request's tools. The case that prompted this: a model
// called a bash tool whose schema has "script" with {"command": ...}; the
// call went out unchecked and the client's tool server rejected every one.
var _ = Describe("streaming tool calls parsed from text are validated", func() {
	var origInference modelInferenceFunc
	appCfg := config.NewApplicationConfig()

	BeforeEach(func() { origInference = backend.ModelInferenceFunc })
	AfterEach(func() { backend.ModelInferenceFunc = origInference })

	// streamText makes the stub backend stream text through the token
	// callback, as a backend whose parser found no tool call does.
	streamText := func(text string) {
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
					tokenCallback(text, backend.TokenUsage{})
				}
				return backend.LLMResponse{Response: text}, nil
			}, nil
		}
	}

	bashReq := func() *schema.OpenAIRequest {
		ctx, cancel := context.WithCancel(context.Background())
		req := &schema.OpenAIRequest{Context: ctx, Cancel: cancel}
		req.Model = "test-model"
		req.Tools = functions.Tools{{Type: "function", Function: functions.Function{
			Name: "bash",
			Parameters: map[string]any{
				"type":       "object",
				"properties": map[string]any{"script": map[string]any{"type": "string"}},
				"required":   []any{"script"},
			},
		}}}
		return req
	}

	run := func(cfg *config.ModelConfig) (toolCalls []schema.ToolCall, content string) {
		responses := make(chan schema.OpenAIResponse)
		collected := make(chan []schema.OpenAIResponse)
		go func() {
			var all []schema.OpenAIResponse
			for r := range responses {
				all = append(all, r)
			}
			collected <- all
		}()
		var text string
		_, err := processStreamWithTools("answer", "prompt", bashReq(), cfg, nil, appCfg, nil, responses, "req-1", 0, &text)
		Expect(err).ToNot(HaveOccurred())
		var b strings.Builder
		for _, r := range <-collected {
			for _, ch := range r.Choices {
				if ch.Delta == nil {
					continue
				}
				toolCalls = append(toolCalls, ch.Delta.ToolCalls...)
				if s, ok := ch.Delta.Content.(*string); ok && s != nil {
					b.WriteString(*s)
				}
			}
		}
		return toolCalls, b.String()
	}

	It("does not emit a call with an argument the schema does not list", func() {
		streamText(`{"name": "bash", "arguments": {"command": "ls"}}`)
		calls, content := run(&config.ModelConfig{})
		Expect(calls).To(BeEmpty())
		Expect(content).To(ContainSubstring(`"command"`), "the model's text is returned as content")
	})

	It("emits a call that fits the schema", func() {
		streamText(`{"name": "bash", "arguments": {"script": "ls"}}`)
		calls, _ := run(&config.ModelConfig{})
		Expect(calls).ToNot(BeEmpty())
		var args strings.Builder
		for _, c := range calls {
			args.WriteString(c.FunctionCall.Arguments)
		}
		Expect(args.String()).To(ContainSubstring(`"script"`))
	})

	It("keeps the old behavior with disable_tool_call_validation", func() {
		streamText(`{"name": "bash", "arguments": {"command": "ls"}}`)
		cfg := &config.ModelConfig{}
		cfg.FunctionsConfig.DisableToolCallValidation = true
		calls, _ := run(cfg)
		Expect(calls).ToNot(BeEmpty())
	})

	It("does not validate when LocalAI's own grammar constrained the output", func() {
		streamText(`{"name": "bash", "arguments": {"command": "ls"}}`)
		cfg := &config.ModelConfig{}
		cfg.Grammar = "root ::= ..."
		calls, _ := run(cfg)
		Expect(calls).ToNot(BeEmpty())
	})
})
