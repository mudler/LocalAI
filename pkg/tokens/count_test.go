package tokens

import (
	"github.com/mudler/LocalAI/core/schema"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Token counting", func() {
	It("includes roles, content, and tool payloads", func() {
		messages := []schema.Message{
			{Role: "system", Content: "keep this"},
			{Role: "assistant", Content: "", ToolCalls: []schema.ToolCall{{ID: "call-1", Type: "function", FunctionCall: schema.FunctionCall{Name: "lookup", Arguments: `{"id":42}`}}}},
			{Role: "tool", ToolCallID: "call-1", Content: "https://example.com/result"},
		}

		got, err := CountMessages(messages)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(BeNumerically(">=", 15))
	})

	It("rejects unsupported content", func() {
		_, err := CountMessages([]schema.Message{{Role: "user", Content: make(chan int)}})
		Expect(err).To(HaveOccurred())
	})
})
