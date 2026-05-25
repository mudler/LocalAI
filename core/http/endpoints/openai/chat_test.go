package openai

import (
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/functions"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	reason "github.com/mudler/LocalAI/pkg/reasoning"
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

var _ = Describe("applyAutoparserOverride", func() {
	// Regression test for https://github.com/mudler/LocalAI/issues/9985.
	// When LocalAI templates a <think>-style reasoning model outside of jinja
	// (e.g. the gallery qwen3 entry), the llama.cpp autoparser falls back to
	// the "pure content" PEG parser which dumps the entire raw response,
	// including <think>…</think>, into ChatDelta.Content and leaves
	// ChatDelta.ReasoningContent empty. The Go side previously trusted that
	// content verbatim and clobbered the tokenCallback's correctly-split
	// reasoning, so <think> blocks leaked into the OpenAI `content` field.
	Context("autoparser delivered content with embedded <think> tags and empty reasoning (issue #9985)", func() {
		It("splits <think>…</think> out of content into the reasoning field", func() {
			raw := "<think>\nOkay, the user said \"Hello\". I should reply warmly.\n</think>\n\nHello! How can I assist you today? 😊"
			chatDeltas := []*pb.ChatDelta{
				{Content: raw, ReasoningContent: ""},
			}

			result := applyAutoparserOverride(chatDeltas, "", reason.Config{}, nil)

			Expect(result).To(HaveLen(1))
			Expect(result[0].Message).ToNot(BeNil())
			Expect(result[0].Message.Content).ToNot(BeNil())

			content := *(result[0].Message.Content.(*string))
			Expect(content).ToNot(ContainSubstring("<think>"),
				"raw <think> tag must not leak into OpenAI content field")
			Expect(content).ToNot(ContainSubstring("</think>"),
				"raw </think> tag must not leak into OpenAI content field")
			Expect(content).To(ContainSubstring("Hello! How can I assist you today?"),
				"the model's actual answer must still be in content")

			Expect(result[0].Message.Reasoning).ToNot(BeNil(),
				"reasoning extracted from <think>…</think> must populate Reasoning")
			Expect(*result[0].Message.Reasoning).To(ContainSubstring("Okay, the user said"))
		})

		It("does not run extraction when the autoparser already populated reasoning", func() {
			// When the autoparser actually classified reasoning, leave its
			// content/reasoning split untouched.
			content := "Hello! How can I assist you today?"
			reasoning := "Already split by the C++ autoparser."
			chatDeltas := []*pb.ChatDelta{
				{Content: content, ReasoningContent: reasoning},
			}

			result := applyAutoparserOverride(chatDeltas, "", reason.Config{}, nil)

			Expect(result).To(HaveLen(1))
			Expect(*(result[0].Message.Content.(*string))).To(Equal(content))
			Expect(result[0].Message.Reasoning).ToNot(BeNil())
			Expect(*result[0].Message.Reasoning).To(Equal(reasoning))
		})

		It("passes plain content through unchanged when no reasoning tags are present", func() {
			content := "Just a normal answer with no reasoning at all."
			chatDeltas := []*pb.ChatDelta{
				{Content: content, ReasoningContent: ""},
			}

			result := applyAutoparserOverride(chatDeltas, "", reason.Config{}, nil)

			Expect(result).To(HaveLen(1))
			Expect(*(result[0].Message.Content.(*string))).To(Equal(content))
			Expect(result[0].Message.Reasoning).To(BeNil())
		})

		It("strips an empty <think></think> block (qwen3 /no_think mode)", func() {
			// qwen3 with the /no_think directive still emits an empty thinking
			// block. The Go-side fallback must strip it from content rather than
			// pass <think></think> through verbatim. No reasoning is set because
			// the block has no body.
			raw := "<think>\n\n</think>\n\nHello! How can I assist you today?"
			chatDeltas := []*pb.ChatDelta{
				{Content: raw, ReasoningContent: ""},
			}

			result := applyAutoparserOverride(chatDeltas, "", reason.Config{}, nil)

			Expect(result).To(HaveLen(1))
			content := *(result[0].Message.Content.(*string))
			Expect(content).ToNot(ContainSubstring("<think>"))
			Expect(content).ToNot(ContainSubstring("</think>"))
			Expect(content).To(ContainSubstring("Hello! How can I assist you today?"))
		})

		It("returns the existing result when chatDeltas is empty", func() {
			existing := []schema.Choice{{Index: 7}}
			result := applyAutoparserOverride(nil, "", reason.Config{}, existing)
			Expect(result).To(Equal(existing))
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
