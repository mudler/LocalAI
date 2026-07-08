package anthropic

import (
	"github.com/mudler/LocalAI/core/schema"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
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
