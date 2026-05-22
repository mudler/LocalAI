package openai

import (
	"encoding/json"

	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/functions"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// These tests pin LocalAI's streaming chunks to the OpenAI spec for the
// `usage` field. The regression that motivated them (issue #8546) was that
// LocalAI emitted `"usage":{...zeros...}` on every chunk, which made the
// official OpenAI Node SDK consumers (Continue, Kilo Code, Roo Code, Zed,
// IntelliJ Continue) drop every content chunk via the filter at
// continuedev/continue packages/openai-adapters/src/apis/OpenAI.ts:275-288.
//
// Per OpenAI's chat-completion streaming contract:
//   - intermediate chunks MUST NOT carry a `usage` field
//   - usage is only delivered when the request opts in via
//     `stream_options.include_usage: true`, on a final extra chunk whose
//     `choices` is an empty array.

var _ = Describe("streaming usage spec compliance", func() {
	Describe("OpenAIResponse JSON shape", func() {
		It("does not emit a 'usage' key when Usage is unset", func() {
			// A typical intermediate token chunk: no Usage populated.
			content := "hello"
			resp := schema.OpenAIResponse{
				ID:      "req-1",
				Created: 1,
				Model:   "m",
				Object:  "chat.completion.chunk",
				Choices: []schema.Choice{{
					Index: 0,
					Delta: &schema.Message{Content: &content},
				}},
			}
			data, err := json.Marshal(resp)
			Expect(err).ToNot(HaveOccurred())

			var raw map[string]any
			Expect(json.Unmarshal(data, &raw)).To(Succeed())
			_, present := raw["usage"]
			Expect(present).To(BeFalse(),
				"intermediate chunk must not include a 'usage' key; got: %s", string(data))
		})

		It("emits the usage object when Usage is explicitly set", func() {
			usage := &schema.OpenAIUsage{PromptTokens: 11, CompletionTokens: 22, TotalTokens: 33}
			resp := schema.OpenAIResponse{
				ID:      "req-1",
				Created: 1,
				Model:   "m",
				Object:  "chat.completion.chunk",
				Usage:   usage,
			}
			data, err := json.Marshal(resp)
			Expect(err).ToNot(HaveOccurred())

			var raw map[string]any
			Expect(json.Unmarshal(data, &raw)).To(Succeed())
			u, ok := raw["usage"].(map[string]any)
			Expect(ok).To(BeTrue(), "expected 'usage' object, got: %s", string(data))
			Expect(u["prompt_tokens"]).To(BeNumerically("==", 11))
			Expect(u["completion_tokens"]).To(BeNumerically("==", 22))
			Expect(u["total_tokens"]).To(BeNumerically("==", 33))
		})
	})

	Describe("buildNoActionFinalChunks", func() {
		It("returns chunks with no Usage embedded", func() {
			// Whatever the caller is doing, helpers must not bake usage
			// into intermediate or final delta chunks. The usage trailer
			// (when requested via include_usage) is emitted separately.
			chunks := buildNoActionFinalChunks(
				"req-1", "m", 1,
				false, false,
				"hi", "",
			)
			Expect(chunks).ToNot(BeEmpty())
			for i, ch := range chunks {
				Expect(ch.Usage).To(BeNil(),
					"chunk[%d] must not carry Usage; got %+v", i, ch.Usage)
			}
		})

		It("returns chunks with no Usage when only trailing reasoning needs delivery", func() {
			chunks := buildNoActionFinalChunks(
				"req-1", "m", 1,
				true, false,
				"", "autoparser late reasoning",
			)
			Expect(chunks).ToNot(BeEmpty())
			for i, ch := range chunks {
				Expect(ch.Usage).To(BeNil(),
					"chunk[%d] must not carry Usage; got %+v", i, ch.Usage)
			}
		})
	})

	Describe("buildDeferredToolCallChunks", func() {
		It("returns chunks with no Usage embedded", func() {
			calls := []functions.FuncCallResults{{
				Name: "do_thing", Arguments: `{"x":1}`,
			}}
			chunks := buildDeferredToolCallChunks(
				"req-1", "m", 1, calls, 0,
				false, "", false, "",
			)
			Expect(chunks).ToNot(BeEmpty())
			for i, ch := range chunks {
				Expect(ch.Usage).To(BeNil(),
					"chunk[%d] must not carry Usage; got %+v", i, ch.Usage)
			}
		})
	})

	Describe("streamUsageTrailerJSON", func() {
		It("produces JSON matching the OpenAI spec for the trailer chunk", func() {
			// Trailing usage chunk shape (OpenAI streaming spec):
			//   {"id":"...","object":"chat.completion.chunk","created":...,
			//    "model":"...","choices":[],"usage":{...}}
			usage := schema.OpenAIUsage{
				PromptTokens: 18, CompletionTokens: 14, TotalTokens: 32,
			}
			data := streamUsageTrailerJSON("req-1", "m", 1, usage)

			var raw map[string]any
			Expect(json.Unmarshal(data, &raw)).To(Succeed(),
				"trailer must be valid JSON, got: %s", string(data))

			Expect(raw["id"]).To(Equal("req-1"))
			Expect(raw["model"]).To(Equal("m"))
			Expect(raw["object"]).To(Equal("chat.completion.chunk"))
			Expect(raw["created"]).To(BeNumerically("==", 1))

			// `choices` MUST be present as an empty array (not absent, not null).
			rawChoices, present := raw["choices"]
			Expect(present).To(BeTrue(), "choices key must be present, got: %s", string(data))
			choicesArr, ok := rawChoices.([]any)
			Expect(ok).To(BeTrue(), "choices must serialize as an array, got: %s", string(data))
			Expect(choicesArr).To(BeEmpty(), "choices must be empty in usage trailer, got: %s", string(data))

			// `usage` MUST be present and non-null with the populated counts.
			u, ok := raw["usage"].(map[string]any)
			Expect(ok).To(BeTrue(), "usage object must be present, got: %s", string(data))
			Expect(u["prompt_tokens"]).To(BeNumerically("==", 18))
			Expect(u["completion_tokens"]).To(BeNumerically("==", 14))
			Expect(u["total_tokens"]).To(BeNumerically("==", 32))
		})
	})

	// Regression coverage for issue #9927: streaming usage trailer reported
	// zeros when the request included `tools`. processTools (the streaming
	// worker for tool-enabled requests) was discarding the cumulative
	// TokenUsage from ComputeChoices instead of forwarding it to the outer
	// loop. The fix: processTools emits a "usage sentinel" chunk (empty
	// Choices, populated Usage) right before closing the responses channel,
	// and the outer loop captures Usage *before* the empty-Choices skip.
	Describe("streamUsageFromTokenUsage", func() {
		It("converts backend TokenUsage to schema OpenAIUsage", func() {
			tu := backend.TokenUsage{Prompt: 18, Completion: 213}
			u := streamUsageFromTokenUsage(tu, false)
			Expect(u.PromptTokens).To(Equal(18))
			Expect(u.CompletionTokens).To(Equal(213))
			Expect(u.TotalTokens).To(Equal(231))
			Expect(u.TimingTokenGeneration).To(BeZero())
			Expect(u.TimingPromptProcessing).To(BeZero())
		})
		It("includes timings when extraUsage is true", func() {
			tu := backend.TokenUsage{
				Prompt: 10, Completion: 20,
				TimingPromptProcessing: 0.5,
				TimingTokenGeneration:  1.5,
			}
			u := streamUsageFromTokenUsage(tu, true)
			Expect(u.TimingPromptProcessing).To(Equal(0.5))
			Expect(u.TimingTokenGeneration).To(Equal(1.5))
		})
	})

	Describe("usageSentinelChunk", func() {
		It("carries Usage but empty Choices so the outer streaming loop can capture totals without emitting on the wire", func() {
			tu := backend.TokenUsage{Prompt: 18, Completion: 213}
			ch := usageSentinelChunk("req-1", "Qwen3.6", 100, tu, false)

			Expect(ch.Choices).To(BeEmpty(),
				"sentinel must have no Choices so the outer loop's empty-Choices skip prevents wire emission")
			Expect(ch.Usage).ToNot(BeNil(),
				"sentinel must carry Usage so the outer loop can populate the include_usage trailer")
			Expect(ch.Usage.PromptTokens).To(Equal(18))
			Expect(ch.Usage.CompletionTokens).To(Equal(213))
			Expect(ch.Usage.TotalTokens).To(Equal(231))
			Expect(ch.ID).To(Equal("req-1"))
			Expect(ch.Model).To(Equal("Qwen3.6"))
			Expect(ch.Created).To(Equal(100))
			Expect(ch.Object).To(Equal("chat.completion.chunk"))
		})
	})

	Describe("applyChunkToUsage", func() {
		It("updates running usage from a sentinel chunk with empty Choices", func() {
			running := &schema.OpenAIUsage{}
			sentinel := schema.OpenAIResponse{
				Usage: &schema.OpenAIUsage{PromptTokens: 18, CompletionTokens: 213, TotalTokens: 231},
			}
			updated := applyChunkToUsage(running, sentinel)
			Expect(updated.PromptTokens).To(Equal(18))
			Expect(updated.CompletionTokens).To(Equal(213))
			Expect(updated.TotalTokens).To(Equal(231))
		})
		It("keeps running usage when chunk has no Usage", func() {
			running := &schema.OpenAIUsage{PromptTokens: 1, CompletionTokens: 2, TotalTokens: 3}
			chunk := schema.OpenAIResponse{
				Choices: []schema.Choice{{Delta: &schema.Message{}, Index: 0}},
			}
			updated := applyChunkToUsage(running, chunk)
			Expect(updated.PromptTokens).To(Equal(1))
			Expect(updated.CompletionTokens).To(Equal(2))
			Expect(updated.TotalTokens).To(Equal(3))
		})
	})

	// Flow-level contract: this test mirrors the outer streaming loop's
	// per-chunk handling block exactly. It documents the chain by which the
	// tools-streaming path delivers token counts to the include_usage trailer.
	// Any future change that breaks this contract (reordering the skip, or
	// dropping the sentinel-emission step in processTools) will surface as a
	// behavioral regression of issue #9927.
	Describe("tools-flow usage capture through outer-loop sequence", func() {
		It("captures usage from a tool-call streaming sequence that ends in a sentinel chunk", func() {
			hello := "Hello"
			args := `{"x":1}`
			toolID := "call_1"
			feed := []schema.OpenAIResponse{
				// Role chunk emitted on first content token, no Usage.
				{
					Object:  "chat.completion.chunk",
					Choices: []schema.Choice{{Delta: &schema.Message{Role: "assistant"}, Index: 0}},
				},
				// Content delta chunk, no Usage.
				{
					Object:  "chat.completion.chunk",
					Choices: []schema.Choice{{Delta: &schema.Message{Content: &hello}, Index: 0}},
				},
				// Tool call deltas (XML/JSON iterative parser path), no Usage.
				{
					Object: "chat.completion.chunk",
					Choices: []schema.Choice{{Delta: &schema.Message{
						Role: "assistant",
						ToolCalls: []schema.ToolCall{{
							Index: 0, ID: toolID, Type: "function",
							FunctionCall: schema.FunctionCall{Name: "do_thing", Arguments: args},
						}},
					}, Index: 0}},
				},
				// Final usage sentinel forwarded from ComputeChoices' returned
				// TokenUsage. Empty Choices, populated Usage.
				usageSentinelChunk("req-1", "m", 0,
					backend.TokenUsage{Prompt: 18, Completion: 213}, false),
			}

			// Mirror the outer loop body in chat.go ChatEndpoint exactly.
			usage := &schema.OpenAIUsage{}
			wireChunks := 0
			for _, ev := range feed {
				usage = applyChunkToUsage(usage, ev)
				if len(ev.Choices) == 0 {
					continue
				}
				wireChunks++
			}

			Expect(wireChunks).To(Equal(3),
				"the 3 real delta chunks must reach the wire; the sentinel must not")
			Expect(usage.PromptTokens).To(Equal(18),
				"trailer must report the prompt tokens from the sentinel (issue #9927)")
			Expect(usage.CompletionTokens).To(Equal(213),
				"trailer must report the completion tokens from the sentinel (issue #9927)")
			Expect(usage.TotalTokens).To(Equal(231))
		})

		It("would still report zeros without a sentinel, demonstrating the original bug shape", func() {
			// Same sequence as the previous test minus the sentinel. This
			// pins the contract that processTools is responsible for adding
			// the sentinel: without it, the trailer cannot recover the
			// counts because the deferred-final-chunk helpers deliberately
			// omit Usage (regression test from issue #8546).
			hello := "Hello"
			feed := []schema.OpenAIResponse{
				{
					Object:  "chat.completion.chunk",
					Choices: []schema.Choice{{Delta: &schema.Message{Role: "assistant"}, Index: 0}},
				},
				{
					Object:  "chat.completion.chunk",
					Choices: []schema.Choice{{Delta: &schema.Message{Content: &hello}, Index: 0}},
				},
			}

			usage := &schema.OpenAIUsage{}
			for _, ev := range feed {
				usage = applyChunkToUsage(usage, ev)
				if len(ev.Choices) == 0 {
					continue
				}
			}

			Expect(usage.PromptTokens).To(Equal(0))
			Expect(usage.CompletionTokens).To(Equal(0))
			Expect(usage.TotalTokens).To(Equal(0))
		})
	})

	Describe("OpenAIRequest.StreamOptions", func() {
		It("parses stream_options.include_usage=true", func() {
			body := []byte(`{
                "model": "m",
                "stream": true,
                "stream_options": {"include_usage": true},
                "messages": []
            }`)
			var req schema.OpenAIRequest
			Expect(json.Unmarshal(body, &req)).To(Succeed())
			Expect(req.StreamOptions).ToNot(BeNil())
			Expect(req.StreamOptions.IncludeUsage).To(BeTrue())
		})

		It("defaults IncludeUsage to false when stream_options is absent", func() {
			body := []byte(`{"model":"m","stream":true,"messages":[]}`)
			var req schema.OpenAIRequest
			Expect(json.Unmarshal(body, &req)).To(Succeed())
			// Either a nil StreamOptions or one with IncludeUsage=false is acceptable.
			if req.StreamOptions != nil {
				Expect(req.StreamOptions.IncludeUsage).To(BeFalse())
			}
		})
	})
})
