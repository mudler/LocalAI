// SPDX-License-Identifier: MIT

package backend

import (
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/templates"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("tokenizer messages with images", func() {
	DescribeTable("keeps each image on its original message",
		func(backend, marker string, options []string) {
			cfg := &config.ModelConfig{Backend: backend, Options: options}
			messages := schema.Messages{
				{Role: "user", Content: []any{
					map[string]any{"type": "text", "text": "first"},
					map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,image-0"}},
					map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,image-1"}},
				},
					StringContent: "first", StringImages: []string{"image-0", "image-1"}},
				{Role: "assistant", Content: "two pictures"},
				{Role: "user", Content: []schema.Content{{Type: "text", Text: "compare"}},
					StringContent: "compare", StringImages: []string{"image-2"}},
			}
			converted := tokenizerMessages(messages, cfg)
			Expect(converted).To(HaveLen(3))
			Expect(converted[0].Content).To(Equal(marker + marker + "first"))
			Expect(converted[1].Content).To(Equal("two pictures"))
			Expect(converted[2].Content).To(Equal(marker + "compare"))
			Expect(messages[0].StringContent).To(Equal("first"))
		},
		Entry("default marker", "strata", templates.DefaultMultiMediaMarker, nil),
		Entry("development backend", "strata-development", templates.DefaultMultiMediaMarker, nil),
		Entry("hardware variant", "cuda13-strata-development", templates.DefaultMultiMediaMarker, nil),
		Entry("custom marker", "strata", "<image:here>", []string{"media_marker:<image:here>"}),
		Entry("empty marker uses the default", "strata", templates.DefaultMultiMediaMarker, []string{"media_marker:"}),
		Entry("last option wins", "metal-strata", "<image>", []string{"media_marker:old", " media_marker: <image> "}),
	)

	It("handles image-only messages", func() {
		messages := schema.Messages{{Role: "user", StringImages: []string{"image"}}}
		Expect(tokenizerMessages(messages, &config.ModelConfig{Backend: "strata"})[0].Content).
			To(Equal(templates.DefaultMultiMediaMarker))
	})

	It("leaves text and tool calls unchanged without images", func() {
		messages := schema.Messages{
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "", ToolCalls: []schema.ToolCall{
				{ID: "call-1", Type: "function", FunctionCall: schema.FunctionCall{Name: "look", Arguments: "{}"}},
			}},
			{Role: "tool", Content: "done", ToolCallID: "call-1"},
		}
		Expect(tokenizerMessages(messages, &config.ModelConfig{Backend: "strata"})).To(Equal(messages.ToProto()))
	})

	DescribeTable("preserves other tokenizer backends",
		func(backend string) {
			messages := schema.Messages{{Role: "user",
				Content:       []any{map[string]any{"type": "text", "text": "describe"}},
				StringContent: "describe", StringImages: []string{"image"},
			}}
			Expect(tokenizerMessages(messages, &config.ModelConfig{Backend: backend})).To(Equal(messages.ToProto()))
		},
		Entry("llama.cpp", "llama-cpp"),
		Entry("llama.cpp hardware variant", "cuda13-llama-cpp-development"),
		Entry("auto-detection", ""),
		Entry("vLLM", "vllm"),
	)
})
