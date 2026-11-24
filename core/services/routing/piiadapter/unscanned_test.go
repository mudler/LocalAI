package piiadapter

import (
	"github.com/mudler/LocalAI/core/schema"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Unscanned content", func() {
	It("lists OpenAI media block types once, in order", func() {
		req := &schema.OpenAIRequest{Messages: []schema.Message{
			{Role: "user", Content: []any{
				map[string]any{"type": "text", "text": "what is this?"},
				map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,xyz"}},
			}},
			{Role: "user", Content: []any{
				map[string]any{"type": "input_audio", "input_audio": map[string]any{"data": "abc"}},
				map[string]any{"type": "image_url", "image_url": map[string]any{"url": "https://example.com/a.png"}},
				map[string]any{"type": "file", "file": map[string]any{"file_data": "JVBERi0="}},
			}},
		}}
		Expect(OpenAI().Unscanned(req)).To(Equal([]string{"image_url", "input_audio", "file"}))
	})

	It("reports nothing for OpenAI text-only requests", func() {
		req := &schema.OpenAIRequest{Messages: []schema.Message{
			{Role: "system", Content: "be brief"},
			{Role: "user", Content: []any{map[string]any{"type": "text", "text": "hi"}}},
		}}
		Expect(OpenAI().Unscanned(req)).To(BeEmpty())
	})

	It("lists Anthropic image and document blocks but not tool blocks", func() {
		req := &schema.AnthropicRequest{Messages: []schema.AnthropicMessage{
			{Role: "user", Content: []any{
				map[string]any{"type": "text", "text": "read this"},
				map[string]any{"type": "document", "source": map[string]any{"type": "base64"}},
			}},
			{Role: "assistant", Content: []any{map[string]any{"type": "tool_use", "name": "x"}}},
			{Role: "user", Content: []any{
				map[string]any{"type": "tool_result", "content": "ok"},
				map[string]any{"type": "image", "source": map[string]any{"type": "base64"}},
			}},
		}}
		Expect(Anthropic().Unscanned(req)).To(Equal([]string{"document", "image"}))
		textOnly := &schema.AnthropicRequest{Messages: []schema.AnthropicMessage{{Role: "user", Content: "hi"}}}
		Expect(Anthropic().Unscanned(textOnly)).To(BeEmpty())
	})

	It("reports Ollama chat images", func() {
		req := &schema.OllamaChatRequest{Messages: []schema.OllamaMessage{
			{Role: "user", Content: "what is this?", Images: []string{"aGVsbG8="}},
		}}
		Expect(OllamaChat().Unscanned(req)).To(Equal([]string{"images"}))
		textOnly := &schema.OllamaChatRequest{Messages: []schema.OllamaMessage{{Role: "user", Content: "hi"}}}
		Expect(OllamaChat().Unscanned(textOnly)).To(BeEmpty())
	})
})

var _ = Describe("Unscanned content (Open Responses)", func() {
	It("lists media parts inside messages and bare media items", func() {
		req := &schema.OpenResponsesRequest{Input: []any{
			map[string]any{"role": "user", "content": []any{
				map[string]any{"type": "input_text", "text": "what is this?"},
				map[string]any{"type": "input_image", "image_url": "data:image/png;base64,xyz"},
			}},
			map[string]any{"type": "input_file", "file_data": "JVBERi0="},
		}}
		Expect(OpenResponses().Unscanned(req)).To(Equal([]string{"input_image", "input_file"}))
	})

	It("reports nothing for text input, tool output or a plain string", func() {
		req := &schema.OpenResponsesRequest{Instructions: "be brief", Input: []any{
			map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "hi"}}},
			map[string]any{"type": "function_call_output", "call_id": "1", "output": []any{map[string]any{"type": "input_image"}}},
		}}
		Expect(OpenResponses().Unscanned(req)).To(BeEmpty())
		Expect(OpenResponses().Unscanned(&schema.OpenResponsesRequest{Input: "hi"})).To(BeEmpty())
	})
})
