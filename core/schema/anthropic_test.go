package schema_test

import (
	"encoding/json"

	"github.com/mudler/LocalAI/core/schema"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Anthropic Schema", func() {
	Describe("AnthropicRequest", func() {
		It("should unmarshal a valid request", func() {
			jsonData := `{
				"model": "claude-3-sonnet-20240229",
				"max_tokens": 1024,
				"messages": [
					{"role": "user", "content": "Hello, world!"}
				],
				"system": "You are a helpful assistant.",
				"temperature": 0.7
			}`

			var req schema.AnthropicRequest
			err := json.Unmarshal([]byte(jsonData), &req)
			Expect(err).ToNot(HaveOccurred())
			Expect(req.Model).To(Equal("claude-3-sonnet-20240229"))
			Expect(req.MaxTokens).To(Equal(1024))
			Expect(len(req.Messages)).To(Equal(1))
			Expect(req.System).To(Equal("You are a helpful assistant."))
			Expect(*req.Temperature).To(Equal(0.7))
		})

		It("should unmarshal a request with tools", func() {
			jsonData := `{
				"model": "claude-3-sonnet-20240229",
				"max_tokens": 1024,
				"messages": [
					{"role": "user", "content": "What's the weather?"}
				],
				"tools": [
					{
						"name": "get_weather",
						"description": "Get the current weather",
						"input_schema": {
							"type": "object",
							"properties": {
								"location": {"type": "string"}
							}
						}
					}
				],
				"tool_choice": {"type": "tool", "name": "get_weather"}
			}`

			var req schema.AnthropicRequest
			err := json.Unmarshal([]byte(jsonData), &req)
			Expect(err).ToNot(HaveOccurred())
			Expect(len(req.Tools)).To(Equal(1))
			Expect(req.Tools[0].Name).To(Equal("get_weather"))
			Expect(req.Tools[0].Description).To(Equal("Get the current weather"))
			Expect(req.ToolChoice).ToNot(BeNil())
		})

		It("should implement LocalAIRequest interface", func() {
			req := &schema.AnthropicRequest{Model: "test-model"}
			Expect(req.ModelName(nil)).To(Equal("test-model"))

			newModel := "new-model"
			Expect(req.ModelName(&newModel)).To(Equal("new-model"))
			Expect(req.Model).To(Equal("new-model"))
		})
	})

	Describe("AnthropicMessage", func() {
		It("should get string content from string content", func() {
			msg := schema.AnthropicMessage{
				Role:    "user",
				Content: "Hello, world!",
			}
			Expect(msg.GetStringContent()).To(Equal("Hello, world!"))
		})

		It("should get string content from array content", func() {
			msg := schema.AnthropicMessage{
				Role: "user",
				Content: []interface{}{
					map[string]interface{}{"type": "text", "text": "Hello, "},
					map[string]interface{}{"type": "text", "text": "world!"},
				},
			}
			Expect(msg.GetStringContent()).To(Equal("Hello, world!"))
		})

		It("should get content blocks from string content", func() {
			msg := schema.AnthropicMessage{
				Role:    "user",
				Content: "Hello, world!",
			}
			blocks := msg.GetContentBlocks()
			Expect(len(blocks)).To(Equal(1))
			Expect(blocks[0].Type).To(Equal("text"))
			Expect(blocks[0].Text).To(Equal("Hello, world!"))
		})

		It("should get content blocks from array content", func() {
			msg := schema.AnthropicMessage{
				Role: "user",
				Content: []interface{}{
					map[string]interface{}{"type": "text", "text": "Hello"},
					map[string]interface{}{"type": "image", "source": map[string]interface{}{"type": "base64", "data": "abc123"}},
				},
			}
			blocks := msg.GetContentBlocks()
			Expect(len(blocks)).To(Equal(2))
			Expect(blocks[0].Type).To(Equal("text"))
			Expect(blocks[0].Text).To(Equal("Hello"))
		})
	})

	Describe("AnthropicResponse", func() {
		It("should marshal a valid response", func() {
			stopReason := "end_turn"
			resp := schema.AnthropicResponse{
				ID:         "msg_123",
				Type:       "message",
				Role:       "assistant",
				Model:      "claude-3-sonnet-20240229",
				StopReason: &stopReason,
				Content: []schema.AnthropicContentBlock{
					{Type: "text", Text: "Hello!"},
				},
				Usage: schema.AnthropicUsage{
					InputTokens:  10,
					OutputTokens: 5,
				},
			}

			data, err := json.Marshal(resp)
			Expect(err).ToNot(HaveOccurred())

			var result map[string]interface{}
			err = json.Unmarshal(data, &result)
			Expect(err).ToNot(HaveOccurred())

			Expect(result["id"]).To(Equal("msg_123"))
			Expect(result["type"]).To(Equal("message"))
			Expect(result["role"]).To(Equal("assistant"))
			Expect(result["stop_reason"]).To(Equal("end_turn"))
		})

		It("should marshal a response with tool use", func() {
			stopReason := "tool_use"
			resp := schema.AnthropicResponse{
				ID:         "msg_123",
				Type:       "message",
				Role:       "assistant",
				Model:      "claude-3-sonnet-20240229",
				StopReason: &stopReason,
				Content: []schema.AnthropicContentBlock{
					{
						Type: "tool_use",
						ID:   "toolu_123",
						Name: "get_weather",
						Input: map[string]interface{}{
							"location": "San Francisco",
						},
					},
				},
				Usage: schema.AnthropicUsage{
					InputTokens:  10,
					OutputTokens: 5,
				},
			}

			data, err := json.Marshal(resp)
			Expect(err).ToNot(HaveOccurred())

			var result map[string]interface{}
			err = json.Unmarshal(data, &result)
			Expect(err).ToNot(HaveOccurred())

			Expect(result["stop_reason"]).To(Equal("tool_use"))
			content := result["content"].([]interface{})
			Expect(len(content)).To(Equal(1))
			toolUse := content[0].(map[string]interface{})
			Expect(toolUse["type"]).To(Equal("tool_use"))
			Expect(toolUse["id"]).To(Equal("toolu_123"))
			Expect(toolUse["name"]).To(Equal("get_weather"))
		})
	})

	Describe("AnthropicErrorResponse", func() {
		It("should marshal an error response", func() {
			resp := schema.AnthropicErrorResponse{
				Type: "error",
				Error: schema.AnthropicError{
					Type:    "invalid_request_error",
					Message: "max_tokens is required",
				},
			}

			data, err := json.Marshal(resp)
			Expect(err).ToNot(HaveOccurred())

			var result map[string]interface{}
			err = json.Unmarshal(data, &result)
			Expect(err).ToNot(HaveOccurred())

			Expect(result["type"]).To(Equal("error"))
			errorObj := result["error"].(map[string]interface{})
			Expect(errorObj["type"]).To(Equal("invalid_request_error"))
			Expect(errorObj["message"]).To(Equal("max_tokens is required"))
		})
	})
})
