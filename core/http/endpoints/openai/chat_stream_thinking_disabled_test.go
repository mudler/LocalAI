package openai

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/model"
)

// Regression test for issue #12939. qwen3's chat template guards <think> with
// {% if enable_thinking %}, but detection scans the raw template without
// evaluating the condition. With streaming and no tools, prefilling that token
// made the extractor classify the whole answer as reasoning whenever the client
// sent metadata.enable_thinking=false.
var _ = Describe("processStream with thinking disabled (issue #12939)", func() {
	// The marker is only rendered when the template condition is true; the raw
	// string still contains it, which is what detection sees.
	const conditionalThinkTemplate = "{% if enable_thinking %}<think>{% endif %}"

	var origInference modelInferenceFunc

	BeforeEach(func() { origInference = backend.ModelInferenceFunc })
	AfterEach(func() { backend.ModelInferenceFunc = origInference })

	streamAnswer := func(answer string) {
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
				usage := backend.TokenUsage{Prompt: 1, Completion: 1}
				tokenCallback(answer, usage)
				return backend.LLMResponse{Response: answer, Usage: usage}, nil
			}, nil
		}
	}

	run := func(answer string, metadata map[string]string) []schema.OpenAIResponse {
		streamAnswer(answer)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		req := &schema.OpenAIRequest{Cancel: cancel, Context: ctx}
		req.Model = "qwen3-4b"
		cfg := &config.ModelConfig{RequestMetadata: metadata}
		responses := make(chan schema.OpenAIResponse, 16)
		_, err := processStream(conditionalThinkTemplate, req, cfg, nil, config.NewApplicationConfig(), nil, responses, "req-1", 0)
		Expect(err).ToNot(HaveOccurred())
		return drainChannel(responses)
	}

	deltas := func(chunks []schema.OpenAIResponse) (reasoning, content string) {
		for _, chunk := range chunks {
			if len(chunk.Choices) == 0 || chunk.Choices[0].Delta == nil {
				continue
			}
			if chunk.Choices[0].Delta.Reasoning != nil {
				reasoning += *chunk.Choices[0].Delta.Reasoning
			}
			// Message.Content is an any: the streaming workers set it to a
			// *string, while a plain string is also valid.
			switch c := chunk.Choices[0].Delta.Content.(type) {
			case *string:
				if c != nil {
					content += *c
				}
			case string:
				content += c
			}
		}
		return reasoning, content
	}

	It("keeps the answer in content when enable_thinking=false", func() {
		reasoning, content := deltas(run("Hello!", map[string]string{"enable_thinking": "false"}))
		Expect(content).To(Equal("Hello!"))
		Expect(reasoning).To(BeEmpty())
	})

	It("reproduces the misclassification without the request lever (control)", func() {
		reasoning, content := deltas(run("Hello!", nil))
		Expect(reasoning).To(Equal("Hello!"),
			"without enable_thinking=false the synthetic prefill classifies the answer as reasoning; "+
				"this is the behaviour the fix guards against")
		Expect(content).To(BeEmpty())
	})

	It("still extracts real thinking tags when the model ignores the toggle", func() {
		reasoning, content := deltas(run("<think>plan</think>Hello!", map[string]string{"enable_thinking": "false"}))
		Expect(reasoning).To(Equal("plan"))
		Expect(content).To(Equal("Hello!"))
	})
})
