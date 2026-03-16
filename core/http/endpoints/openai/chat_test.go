package openai

import (
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/functions"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/schema"
)

var _ = Describe("handleQuestion", func() {
	var cfg *config.ModelConfig

	BeforeEach(func() {
		cfg = &config.ModelConfig{}
	})

	Context("with no function results but non-empty result", func() {
		It("should return the result directly", func() {
			result, err := handleQuestion(cfg, nil, "Hello world", "prompt")
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal("Hello world"))
		})
	})

	Context("with no function results and empty result", func() {
		It("should return empty string", func() {
			result, err := handleQuestion(cfg, nil, "", "prompt")
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(BeEmpty())
		})
	})

	Context("with function result containing a message argument", func() {
		It("should extract the message from function arguments", func() {
			funcResults := []functions.FuncCallResults{
				{
					Name:      "answer",
					Arguments: `{"message": "This is the answer"}`,
				},
			}
			result, err := handleQuestion(cfg, funcResults, "", "prompt")
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal("This is the answer"))
		})
	})

	Context("with function result containing empty message", func() {
		It("should return empty string when message is empty", func() {
			funcResults := []functions.FuncCallResults{
				{
					Name:      "answer",
					Arguments: `{"message": ""}`,
				},
			}
			result, err := handleQuestion(cfg, funcResults, "", "prompt")
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(BeEmpty())
		})
	})

	Context("with function result containing invalid JSON arguments", func() {
		It("should return empty string gracefully", func() {
			funcResults := []functions.FuncCallResults{
				{
					Name:      "answer",
					Arguments: "not json",
				},
			}
			result, err := handleQuestion(cfg, funcResults, "", "prompt")
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(BeEmpty())
		})
	})

	Context("with cleaned content (no think tags)", func() {
		It("should return content without think tags", func() {
			// This tests the bug fix: handleQuestion should receive cleaned content,
			// not raw text with <think> tags
			result, err := handleQuestion(cfg, nil, "Just the answer", "prompt")
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal("Just the answer"))
			Expect(result).ToNot(ContainSubstring("<think>"))
		})
	})

	Context("with raw think tags passed as result", func() {
		It("would return content with think tags", func() {
			result, err := handleQuestion(cfg, nil, "<think>reasoning</think>answer", "prompt")
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal("<think>reasoning</think>answer"))
		})
	})
})

var _ = Describe("mergeToolCallDeltas", func() {
	Context("with new tool calls", func() {
		It("should append new tool calls", func() {
			existing := []schema.ToolCall{}
			deltas := []schema.ToolCall{
				{Index: 0, ID: "tc1", Type: "function", FunctionCall: schema.FunctionCall{Name: "search"}},
			}
			result := mergeToolCallDeltas(existing, deltas)
			Expect(result).To(HaveLen(1))
			Expect(result[0].ID).To(Equal("tc1"))
			Expect(result[0].FunctionCall.Name).To(Equal("search"))
		})
	})

	Context("with argument appending", func() {
		It("should append arguments to existing tool call", func() {
			existing := []schema.ToolCall{
				{Index: 0, ID: "tc1", Type: "function", FunctionCall: schema.FunctionCall{Name: "search", Arguments: `{"q":`}},
			}
			deltas := []schema.ToolCall{
				{Index: 0, FunctionCall: schema.FunctionCall{Arguments: `"hello"}`}},
			}
			result := mergeToolCallDeltas(existing, deltas)
			Expect(result).To(HaveLen(1))
			Expect(result[0].FunctionCall.Arguments).To(Equal(`{"q":"hello"}`))
		})
	})

	Context("with multiple tool calls", func() {
		It("should track multiple tool calls by index", func() {
			existing := []schema.ToolCall{}
			deltas1 := []schema.ToolCall{
				{Index: 0, ID: "tc1", Type: "function", FunctionCall: schema.FunctionCall{Name: "search"}},
			}
			result := mergeToolCallDeltas(existing, deltas1)

			deltas2 := []schema.ToolCall{
				{Index: 1, ID: "tc2", Type: "function", FunctionCall: schema.FunctionCall{Name: "browse"}},
			}
			result = mergeToolCallDeltas(result, deltas2)
			Expect(result).To(HaveLen(2))
			Expect(result[0].FunctionCall.Name).To(Equal("search"))
			Expect(result[1].FunctionCall.Name).To(Equal("browse"))
		})
	})

	Context("with ID update on existing tool call", func() {
		It("should update ID when provided in delta", func() {
			existing := []schema.ToolCall{
				{Index: 0, FunctionCall: schema.FunctionCall{Name: "search"}},
			}
			deltas := []schema.ToolCall{
				{Index: 0, ID: "new-id"},
			}
			result := mergeToolCallDeltas(existing, deltas)
			Expect(result).To(HaveLen(1))
			Expect(result[0].ID).To(Equal("new-id"))
			Expect(result[0].FunctionCall.Name).To(Equal("search"))
		})
	})
})
