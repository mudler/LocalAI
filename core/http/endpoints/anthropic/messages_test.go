package anthropic

import (
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/types"
)

var _ = Describe("Anthropic thinking inbound", func() {
	It("maps an assistant thinking block to Message.Reasoning", func() {
		req := &schema.AnthropicRequest{
			Messages: []schema.AnthropicMessage{
				{Role: "assistant", Content: []any{
					map[string]any{"type": "thinking", "thinking": "I should call get_weather", "signature": "sig_1"},
					map[string]any{"type": "text", "text": "Checking."},
				}},
			},
		}
		msgs := convertAnthropicToOpenAIMessages(req)
		Expect(msgs).To(HaveLen(1))
		Expect(msgs[0].Reasoning).NotTo(BeNil())
		Expect(*msgs[0].Reasoning).To(Equal("I should call get_weather"))
	})
})

var _ = Describe("Anthropic thinking outbound (non-stream)", func() {
	It("emits a thinking block before tool_use when reasoning is present", func() {
		blocks := buildAnthropicContentBlocks(buildParams{
			reasoning:       "I need the weather",
			thinkingEnabled: true,
			text:            "",
			toolCalls: []schema.ToolCall{{ID: "call_1", Type: "function",
				FunctionCall: schema.FunctionCall{Name: "get_weather", Arguments: `{"city":"Rome"}`}}},
			id: "abc",
		})
		Expect(blocks[0].Type).To(Equal("thinking"))
		Expect(blocks[0].Thinking).To(Equal("I need the weather"))
		Expect(blocks[0].Signature).NotTo(BeEmpty())
		Expect(blocks[1].Type).To(Equal("tool_use"))
	})

	It("omits the thinking block when thinking is not enabled", func() {
		blocks := buildAnthropicContentBlocks(buildParams{
			reasoning: "hidden", thinkingEnabled: false, text: "hi", id: "abc",
		})
		for _, b := range blocks {
			Expect(b.Type).NotTo(Equal("thinking"))
		}
	})
})

var _ = Describe("Anthropic thinking outbound (stream)", func() {
	It("sequences a thinking block before tool_use in streaming order", func() {
		seq := anthropicStreamSequence(streamInput{
			reasoningDeltas: []string{"think ", "more"},
			thinkingEnabled: true,
			toolCalls: []schema.ToolCall{{ID: "call_1", Type: "function",
				FunctionCall: schema.FunctionCall{Name: "f", Arguments: "{}"}}},
		})
		types := eventTypes(seq)
		Expect(types).To(ContainElements(
			"content_block_start", "thinking_delta", "signature_delta", "content_block_stop"))
		Expect(indexOf(types, "content_block_stop")).To(BeNumerically("<", indexOf(types, "tool_use_start")))
	})

	It("omits the thinking sequence when thinking is not enabled", func() {
		seq := anthropicStreamSequence(streamInput{
			reasoningDeltas: []string{"hidden"},
			thinkingEnabled: false,
			toolCalls: []schema.ToolCall{{ID: "call_1", Type: "function",
				FunctionCall: schema.FunctionCall{Name: "f", Arguments: "{}"}}},
		})
		types := eventTypes(seq)
		Expect(types).NotTo(ContainElement("thinking_delta"))
		Expect(types).NotTo(ContainElement("signature_delta"))
	})
})

// eventTypes maps each streaming event to a stable logical label so ordering
// assertions read naturally: content_block_start of a tool_use block is
// surfaced as "tool_use_start", and delta events surface their delta type.
func eventTypes(events []schema.AnthropicStreamEvent) []string {
	out := make([]string, 0, len(events))
	for _, e := range events {
		switch e.Type {
		case "content_block_start":
			if e.ContentBlock != nil && e.ContentBlock.Type == "tool_use" {
				out = append(out, "tool_use_start")
				continue
			}
			out = append(out, e.Type)
		case "content_block_delta":
			if e.Delta != nil {
				out = append(out, e.Delta.Type)
				continue
			}
			out = append(out, e.Type)
		default:
			out = append(out, e.Type)
		}
	}
	return out
}

func indexOf(items []string, target string) int {
	for i, s := range items {
		if s == target {
			return i
		}
	}
	return -1
}

var _ = Describe("Typed inbound conversion", func() {
	It("preserves ordered typed text images and tool errors without mutation", func() {
		yes := true
		blocks := []schema.AnthropicContentBlock{{Type: "text", Text: "first"}, {Type: "image", Source: &schema.AnthropicImageSource{Type: "base64", MediaType: "image/png", Data: "AA=="}}, {Type: "text", Text: "second"}, {Type: "image", Source: &schema.AnthropicImageSource{Type: "base64", MediaType: "image/jpeg", Data: "AQ=="}}, {Type: "tool_result", ToolUseID: "id", Content: "failed", IsError: &yes}}
		req := &schema.AnthropicRequest{Messages: []schema.AnthropicMessage{{Role: "user", Content: blocks}}}
		msgs := convertAnthropicToOpenAIMessages(req)
		Expect(msgs[0].StringContent).To(Equal("firstsecond\n[Tool Result for id]: Error: failed"))
		Expect(msgs[0].StringImages).To(Equal([]string{"data:image/png;base64,AA==", "data:image/jpeg;base64,AQ=="}))
		Expect(req.Messages[0].Content).To(Equal(blocks))
	})
})

var _ = Describe("Anthropic tool_choice", func() {
	tools := []schema.AnthropicTool{{Name: "get_weather"}}

	DescribeTable("convertAnthropicTools",
		func(toolChoice any, expectTools bool, expectCall string) {
			cfg := &config.ModelConfig{}
			funcs, useFn := convertAnthropicTools(&schema.AnthropicRequest{Tools: tools, ToolChoice: toolChoice}, cfg)
			Expect(useFn).To(Equal(expectTools))
			Expect(funcs != nil).To(Equal(expectTools))
			Expect(cfg.FunctionToCall()).To(Equal(expectCall))
		},
		Entry("absent", nil, true, ""),
		Entry("auto object", map[string]any{"type": "auto"}, true, ""),
		Entry("any object", map[string]any{"type": "any"}, true, "required"),
		Entry("none object", map[string]any{"type": "none"}, false, ""),
		Entry("tool object", map[string]any{"type": "tool", "name": "get_weather"}, true, "get_weather"),
		Entry("any string", "any", true, "required"),
		Entry("none string", "none", false, ""),
	)

	DescribeTable("openAIToolChoice",
		func(toolChoice any, expected types.GomegaMatcher) {
			Expect(openAIToolChoice(toolChoice)).To(expected)
		},
		Entry("absent", nil, BeNil()),
		Entry("auto", map[string]any{"type": "auto"}, Equal("auto")),
		Entry("any", map[string]any{"type": "any", "disable_parallel_tool_use": true}, Equal("required")),
		Entry("none", map[string]any{"type": "none"}, Equal("none")),
		Entry("tool", map[string]any{"type": "tool", "name": "get_weather"},
			Equal(map[string]any{"type": "function", "function": map[string]any{"name": "get_weather"}})),
		Entry("tool without a name", map[string]any{"type": "tool"}, BeNil()),
		Entry("any string", "any", Equal("required")),
	)
})
