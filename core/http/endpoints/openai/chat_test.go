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

	// Regression tests for the prefilled-thinking-token path (thinkingStartToken
	// != ""). This is the configuration the gallery qwen3 family runs in: the
	// chat template injects <think> into the prompt, so DetectThinkingStartToken
	// returns "<think>" and the model's output begins *inside* a reasoning block
	// — it emits a closing </think> but no opening tag.
	//
	// The defensive Go-side fallback prepends the start token so the standard
	// extractor can pair it with the model's </think>. But on a *complete*
	// response that contains NO closing tag (the model answered directly with no
	// reasoning at all), prepending <think> manufactures an unclosed block that
	// swallows the entire answer into reasoning, leaving content empty. That is
	// the bug: short/direct answers (session names, JSON summaries) come back
	// with an empty content field.
	Context("autoparser delivered content with empty reasoning and a prefilled thinking token", func() {
		const startToken = "<think>"

		It("keeps a tag-less direct answer as content instead of swallowing it as reasoning", func() {
			// Model answered directly: no <think>, no </think> anywhere.
			chatDeltas := []*pb.ChatDelta{
				{Content: "hello", ReasoningContent: ""},
			}

			result := applyAutoparserOverride(chatDeltas, startToken, reason.Config{}, nil)

			Expect(result).To(HaveLen(1))
			Expect(result[0].Message.Content).ToNot(BeNil())
			Expect(*(result[0].Message.Content.(*string))).To(Equal("hello"),
				"a complete answer with no closing reasoning tag must stay in content")
			Expect(result[0].Message.Reasoning).To(BeNil(),
				"no reasoning block was emitted, so Reasoning must not be set")
		})

		It("keeps a tag-less JSON answer as content (the summary case)", func() {
			raw := `{"short":"Tests pass","long":"go test ./... succeeded."}`
			chatDeltas := []*pb.ChatDelta{
				{Content: raw, ReasoningContent: ""},
			}

			result := applyAutoparserOverride(chatDeltas, startToken, reason.Config{}, nil)

			Expect(result).To(HaveLen(1))
			Expect(*(result[0].Message.Content.(*string))).To(Equal(raw))
			Expect(result[0].Message.Reasoning).To(BeNil())
		})

		It("still splits reasoning when the model emits the closing tag (prefill paired with </think>)", func() {
			// The legitimate prefill case: <think> was in the prompt, so the
			// output carries only the closing tag. The closing tag is the proof
			// that a reasoning block exists, so extraction must run.
			raw := "The user wants a greeting.\n</think>\n\nHello there!"
			chatDeltas := []*pb.ChatDelta{
				{Content: raw, ReasoningContent: ""},
			}

			result := applyAutoparserOverride(chatDeltas, startToken, reason.Config{}, nil)

			Expect(result).To(HaveLen(1))
			content := *(result[0].Message.Content.(*string))
			Expect(content).To(ContainSubstring("Hello there!"))
			Expect(content).ToNot(ContainSubstring("</think>"))
			Expect(content).ToNot(ContainSubstring("The user wants a greeting"))
			Expect(result[0].Message.Reasoning).ToNot(BeNil())
			Expect(*result[0].Message.Reasoning).To(ContainSubstring("The user wants a greeting"))
		})

		It("still splits a fully-tagged <think>…</think> block with a prefill token set", func() {
			raw := "<think>Reasoning here.</think>Final answer."
			chatDeltas := []*pb.ChatDelta{
				{Content: raw, ReasoningContent: ""},
			}

			result := applyAutoparserOverride(chatDeltas, startToken, reason.Config{}, nil)

			Expect(result).To(HaveLen(1))
			Expect(*(result[0].Message.Content.(*string))).To(Equal("Final answer."))
			Expect(result[0].Message.Reasoning).ToNot(BeNil())
			Expect(*result[0].Message.Reasoning).To(ContainSubstring("Reasoning here"))
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
