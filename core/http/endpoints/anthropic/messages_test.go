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
