package schema_test

import (
	"encoding/json"

	. "github.com/mudler/LocalAI/core/schema"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("LLM tests", func() {

	Context("ToProtoMessages conversion", func() {
		It("should convert basic message with string content", func() {
			messages := Messages{
				{
					Role:    "user",
					Content: "Hello, world!",
				},
			}

			protoMessages := messages.ToProto()

			Expect(protoMessages).To(HaveLen(1))
			Expect(protoMessages[0].Role).To(Equal("user"))
			Expect(protoMessages[0].Content).To(Equal("Hello, world!"))
			Expect(protoMessages[0].Name).To(BeEmpty())
			Expect(protoMessages[0].ToolCalls).To(BeEmpty())
		})

		It("should convert message with nil content to empty string", func() {
			messages := Messages{
				{
					Role:    "assistant",
					Content: nil,
				},
			}

			protoMessages := messages.ToProto()

			Expect(protoMessages).To(HaveLen(1))
			Expect(protoMessages[0].Role).To(Equal("assistant"))
			Expect(protoMessages[0].Content).To(Equal(""))
		})

		It("should convert message with array content (multimodal)", func() {
			messages := Messages{
				{
					Role: "user",
					Content: []interface{}{
						map[string]interface{}{
							"type": "text",
							"text": "Hello",
						},
						map[string]interface{}{
							"type": "text",
							"text": " World",
						},
					},
				},
			}

			protoMessages := messages.ToProto()

			Expect(protoMessages).To(HaveLen(1))
			Expect(protoMessages[0].Role).To(Equal("user"))
			Expect(protoMessages[0].Content).To(Equal("Hello World"))
		})

		It("should convert message with tool_calls", func() {
			messages := Messages{
				{
					Role:    "assistant",
					Content: "I'll call a function",
					ToolCalls: []ToolCall{
						{
							Index: 0,
							ID:    "call_123",
							Type:  "function",
							FunctionCall: FunctionCall{
								Name:      "get_weather",
								Arguments: `{"location": "San Francisco"}`,
							},
						},
					},
				},
			}

			protoMessages := messages.ToProto()

			Expect(protoMessages).To(HaveLen(1))
			Expect(protoMessages[0].Role).To(Equal("assistant"))
			Expect(protoMessages[0].Content).To(Equal("I'll call a function"))
			Expect(protoMessages[0].ToolCalls).NotTo(BeEmpty())

			// Verify tool_calls JSON is valid
			var toolCalls []ToolCall
			err := json.Unmarshal([]byte(protoMessages[0].ToolCalls), &toolCalls)
			Expect(err).NotTo(HaveOccurred())
			Expect(toolCalls).To(HaveLen(1))
			Expect(toolCalls[0].ID).To(Equal("call_123"))
			Expect(toolCalls[0].FunctionCall.Name).To(Equal("get_weather"))
		})

		It("should convert message with name field", func() {
			messages := Messages{
				{
					Role:    "tool",
					Content: "Function result",
					Name:    "get_weather",
				},
			}

			protoMessages := messages.ToProto()

			Expect(protoMessages).To(HaveLen(1))
			Expect(protoMessages[0].Role).To(Equal("tool"))
			Expect(protoMessages[0].Content).To(Equal("Function result"))
			Expect(protoMessages[0].Name).To(Equal("get_weather"))
		})

		It("should convert message with tool_calls and nil content", func() {
			messages := Messages{
				{
					Role:    "assistant",
					Content: nil,
					ToolCalls: []ToolCall{
						{
							Index: 0,
							ID:    "call_456",
							Type:  "function",
							FunctionCall: FunctionCall{
								Name:      "search",
								Arguments: `{"query": "test"}`,
							},
						},
					},
				},
			}

			protoMessages := messages.ToProto()

			Expect(protoMessages).To(HaveLen(1))
			Expect(protoMessages[0].Role).To(Equal("assistant"))
			Expect(protoMessages[0].Content).To(Equal(""))
			Expect(protoMessages[0].ToolCalls).NotTo(BeEmpty())

			var toolCalls []ToolCall
			err := json.Unmarshal([]byte(protoMessages[0].ToolCalls), &toolCalls)
			Expect(err).NotTo(HaveOccurred())
			Expect(toolCalls).To(HaveLen(1))
			Expect(toolCalls[0].FunctionCall.Name).To(Equal("search"))
		})

		It("should convert multiple messages", func() {
			messages := Messages{
				{
					Role:    "user",
					Content: "Hello",
				},
				{
					Role:    "assistant",
					Content: "Hi there!",
				},
				{
					Role:    "user",
					Content: "How are you?",
				},
			}

			protoMessages := messages.ToProto()

			Expect(protoMessages).To(HaveLen(3))
			Expect(protoMessages[0].Role).To(Equal("user"))
			Expect(protoMessages[0].Content).To(Equal("Hello"))
			Expect(protoMessages[1].Role).To(Equal("assistant"))
			Expect(protoMessages[1].Content).To(Equal("Hi there!"))
			Expect(protoMessages[2].Role).To(Equal("user"))
			Expect(protoMessages[2].Content).To(Equal("How are you?"))
		})

		It("should handle empty messages slice", func() {
			messages := Messages{}

			protoMessages := messages.ToProto()

			Expect(protoMessages).To(HaveLen(0))
		})

		It("should handle message with all optional fields", func() {
			messages := Messages{
				{
					Role:    "assistant",
					Content: "I'll help you",
					Name:    "test_tool",
					ToolCalls: []ToolCall{
						{
							Index: 0,
							ID:    "call_789",
							Type:  "function",
							FunctionCall: FunctionCall{
								Name:      "test_function",
								Arguments: `{"param": "value"}`,
							},
						},
					},
				},
			}

			protoMessages := messages.ToProto()

			Expect(protoMessages).To(HaveLen(1))
			Expect(protoMessages[0].Role).To(Equal("assistant"))
			Expect(protoMessages[0].Content).To(Equal("I'll help you"))
			Expect(protoMessages[0].Name).To(Equal("test_tool"))
			Expect(protoMessages[0].ToolCalls).NotTo(BeEmpty())

			var toolCalls []ToolCall
			err := json.Unmarshal([]byte(protoMessages[0].ToolCalls), &toolCalls)
			Expect(err).NotTo(HaveOccurred())
			Expect(toolCalls).To(HaveLen(1))
		})

		It("should handle message with empty string content", func() {
			messages := Messages{
				{
					Role:    "user",
					Content: "",
				},
			}

			protoMessages := messages.ToProto()

			Expect(protoMessages).To(HaveLen(1))
			Expect(protoMessages[0].Role).To(Equal("user"))
			Expect(protoMessages[0].Content).To(Equal(""))
		})

		It("should handle message with array content containing non-text parts", func() {
			messages := Messages{
				{
					Role: "user",
					Content: []interface{}{
						map[string]interface{}{
							"type": "text",
							"text": "Hello",
						},
						map[string]interface{}{
							"type": "image",
							"url":  "https://example.com/image.jpg",
						},
					},
				},
			}

			protoMessages := messages.ToProto()

			Expect(protoMessages).To(HaveLen(1))
			Expect(protoMessages[0].Role).To(Equal("user"))
			// Should only extract text parts
			Expect(protoMessages[0].Content).To(Equal("Hello"))
		})
	})
})
