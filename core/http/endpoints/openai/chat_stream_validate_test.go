package openai

import (
	"context"
	"strings"

	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/functions"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/model"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// A tool call that does not fit the request's tools must not reach the
// client, whether Go-side parsing found it in the text or the C++ autoparser
// returned it. The case that prompted this: a model called a bash tool whose
// schema has "script" with {"command": ...}; the call went out unchecked and
// the client's tool server rejected every one.
var _ = Describe("streaming tool calls are validated", func() {
	var origInference modelInferenceFunc
	appCfg := config.NewApplicationConfig()

	BeforeEach(func() { origInference = backend.ModelInferenceFunc })
	AfterEach(func() { backend.ModelInferenceFunc = origInference })

	// streamWith makes the stub backend stream text through the token
	// callback and return deltas as the C++ autoparser's ChatDeltas. With no
	// deltas it is a backend whose parser found no tool call.
	streamWith := func(text string, deltas []*pb.ChatDelta) {
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
	streamText := func(text string) { streamWith(text, nil) }

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

	It("does not emit an autoparser call with an argument the schema does not list", func() {
		streamWith("", []*pb.ChatDelta{{ToolCalls: []*pb.ToolCallDelta{{Name: "bash", Arguments: `{"command":"ls"}`}}}})
		calls, content := run(&config.ModelConfig{})
		Expect(calls).To(BeEmpty())
		Expect(content).To(ContainSubstring(`"command"`), "the dropped call is answered as text")
	})

	It("emits an autoparser call that fits", func() {
		streamWith("", []*pb.ChatDelta{{ToolCalls: []*pb.ToolCallDelta{{Name: "bash", Arguments: `{"script":"ls"}`}}}})
		calls, _ := run(&config.ModelConfig{})
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
