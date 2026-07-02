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
					Content: []any{
						map[string]any{
							"type": "text",
							"text": "Hello",
						},
						map[string]any{
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

		// Regression for mudler/LocalAI#10524: a text part whose inner text is
		// itself a JSON-array string (mealie sends an ingredient list) must
		// flatten to that exact string verbatim. ToProto must NOT escape or
		// restructure it - the C++ backend then treats it as opaque text. This
		// pins the precise Go-side input that produced the "unsupported
		// content[].type" gRPC error before the backend stopped re-parsing it.
		It("flattens a JSON-array-looking text part to the verbatim string (#10524)", func() {
			ingredients := `["1/4 cup brown sugar, packed","1 pound ground beef"]`
			messages := Messages{
				{
					Role: "user",
					Content: []any{
						map[string]any{
							"type": "text",
							"text": ingredients,
						},
					},
				},
			}

			protoMessages := messages.ToProto()

			Expect(protoMessages).To(HaveLen(1))
			Expect(protoMessages[0].Content).To(Equal(ingredients))
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

		It("should serialize ToolCallID and Reasoning fields", func() {
			reasoning := "thinking..."
			messages := Messages{
				{
					Role:       "tool",
					Content:    "result",
					ToolCallID: "call_123",
					Reasoning:  &reasoning,
				},
			}

			protoMessages := messages.ToProto()

			Expect(protoMessages).To(HaveLen(1))
			Expect(protoMessages[0].ToolCallId).To(Equal("call_123"))
			Expect(protoMessages[0].ReasoningContent).To(Equal("thinking..."))
		})

		It("should not leak unset LocalAI-only or cross-endpoint request fields into JSON", func() {
			// OpenAIRequest is a union over chat / completion /
			// embedding / image / whisper. Strict upstream providers
			// (OpenAI, Anthropic) 400 on unknown parameters when
			// cloud-proxy passthrough re-marshals a chat request and
			// whisper's `file`, image's `step`, embedding's `input`,
			// etc. tag along as empty zero values.
			req := OpenAIRequest{}
			req.Model = "gpt-4"
			data, err := json.Marshal(req)
			Expect(err).NotTo(HaveOccurred())
			body := string(data)
			// Anchor with the trailing `:` so e.g. `"stream"` doesn't
			// false-match `"stream_options"` if a future test setup
			// populates the latter.
			for _, key := range []string{
				// LocalAI-only fields
				`"backend":`, `"grammar":`, `"grammar_json_functions":`,
				`"model_base_name":`, `"reasoning_effort":`,
				// Cross-endpoint fields that don't belong on chat
				`"file":`, `"size":`, `"prompt":`, `"instruction":`,
				`"input":`, `"stop":`, `"messages":`, `"functions":`,
				`"function_call":`, `"stream":`, `"quality":`, `"step":`,
				`"metadata":`,
			} {
				Expect(body).NotTo(ContainSubstring(key), "unset field "+key+" must not appear in marshalled JSON")
			}
		})

		It("should not leak internal String* staging fields into JSON", func() {
			// Regression: the request middleware copies decoded
			// Content into StringContent/StringImages/etc. for
			// templating. When cloud-proxy passthrough re-marshals
			// the request, strict providers (Anthropic) 400 with
			// "messages.0.string_content: Extra inputs are not
			// permitted" if these leak.
			msg := Message{
				Role:          "user",
				Content:       "Hello",
				StringContent: "Hello",
				StringImages:  []string{"base64-blob"},
				StringVideos:  []string{"base64-blob"},
				StringAudios:  []string{"base64-blob"},
			}
			data, err := json.Marshal(msg)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(data)).NotTo(ContainSubstring("string_content"))
			Expect(string(data)).NotTo(ContainSubstring("string_images"))
			Expect(string(data)).NotTo(ContainSubstring("string_videos"))
			Expect(string(data)).NotTo(ContainSubstring("string_audios"))
			Expect(string(data)).To(ContainSubstring(`"content":"Hello"`))
		})

		It("should handle message with array content containing non-text parts", func() {
			messages := Messages{
				{
					Role: "user",
					Content: []any{
						map[string]any{
							"type": "text",
							"text": "Hello",
						},
						map[string]any{
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

		// Regression for mudler/LocalAI#10039: ToProto is the path taken by
		// UseTokenizerTemplate backends (e.g. imported GGUFs, where the backend
		// applies the GGUF's jinja template to the raw messages). It reads
		// Content, not StringContent — so a message that only populated
		// StringContent (the shape /v1/responses produced before the fix)
		// reached the backend with empty content. These two cases pin that
		// contract: Content is authoritative, and producers must set it.
		It("emits empty content when only StringContent is set (Content nil)", func() {
			messages := Messages{
				{
					Role:          "user",
					StringContent: "Hello",
				},
			}

			protoMessages := messages.ToProto()

			Expect(protoMessages).To(HaveLen(1))
			Expect(protoMessages[0].Content).To(BeEmpty())
		})

		It("carries Content through to proto regardless of StringContent", func() {
			messages := Messages{
				{
					Role:          "user",
					Content:       "Hello",
					StringContent: "Hello",
				},
			}

			protoMessages := messages.ToProto()

			Expect(protoMessages).To(HaveLen(1))
			Expect(protoMessages[0].Content).To(Equal("Hello"))
		})
	})
})
