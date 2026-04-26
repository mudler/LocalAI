package openai

import (
	"fmt"

	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/functions"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// contentOf extracts the string payload from a chunk's delta.Content,
// transparently handling both *string and string underlying types so
// assertions don't have to care which one the helper produced.
func contentOf(ch schema.OpenAIResponse) string {
	if len(ch.Choices) == 0 || ch.Choices[0].Delta == nil {
		return ""
	}
	switch v := ch.Choices[0].Delta.Content.(type) {
	case *string:
		if v == nil {
			return ""
		}
		return *v
	case string:
		return v
	default:
		return ""
	}
}

// reasoningOf mirrors contentOf for the delta.Reasoning field, which is a
// *string on schema.Message.
func reasoningOf(ch schema.OpenAIResponse) string {
	if len(ch.Choices) == 0 || ch.Choices[0].Delta == nil {
		return ""
	}
	r := ch.Choices[0].Delta.Reasoning
	if r == nil {
		return ""
	}
	return *r
}

// toolCallsOf returns the ToolCalls slice of a chunk's delta, or nil.
func toolCallsOf(ch schema.OpenAIResponse) []schema.ToolCall {
	if len(ch.Choices) == 0 || ch.Choices[0].Delta == nil {
		return nil
	}
	return ch.Choices[0].Delta.ToolCalls
}

// expectSpecCompliant enforces the invariants on every chunk:
//   - Object == "chat.completion.chunk"
//   - Exactly one Choice with Index==0
//   - No delta ever carries both non-empty Content and non-empty ToolCalls
//   - No delta ever carries both non-empty Reasoning and non-empty ToolCalls
func expectSpecCompliant(chunks []schema.OpenAIResponse) {
	for i, ch := range chunks {
		Expect(ch.Object).To(Equal("chat.completion.chunk"), "chunk[%d] Object", i)
		Expect(ch.Choices).To(HaveLen(1), "chunk[%d] Choices length", i)
		Expect(ch.Choices[0].Index).To(Equal(0), "chunk[%d] Choices[0].Index", i)

		hasContent := contentOf(ch) != ""
		hasReasoning := reasoningOf(ch) != ""
		hasToolCalls := len(toolCallsOf(ch)) > 0

		if hasContent && hasToolCalls {
			Fail(fmt.Sprintf("chunk[%d] violates spec: Content and ToolCalls in same delta", i))
		}
		if hasReasoning && hasToolCalls {
			Fail(fmt.Sprintf("chunk[%d] violates spec: Reasoning and ToolCalls in same delta", i))
		}
	}
}

// expectMetadata asserts every chunk carries the same id/model/created.
func expectMetadata(chunks []schema.OpenAIResponse, id, model string, created int) {
	for i, ch := range chunks {
		Expect(ch.ID).To(Equal(id), "chunk[%d] ID", i)
		Expect(ch.Model).To(Equal(model), "chunk[%d] Model", i)
		Expect(ch.Created).To(Equal(created), "chunk[%d] Created", i)
	}
}

var _ = Describe("buildDeferredToolCallChunks", func() {
	const (
		testID      = "req"
		testModel   = "test-model"
		testCreated = 1700000000
	)

	Describe("Case A — primary bug: content already streamed, 1 deferred call", func() {
		It("emits only the tool_call chunks, no Content anywhere", func() {
			results := []functions.FuncCallResults{
				{Name: "search", Arguments: `{"q":"x"}`, ID: "tc1"},
			}
			chunks := buildDeferredToolCallChunks(
				testID, testModel, testCreated,
				results, 0,
				true, "Let me search…",
				true, "",
			)

			expectSpecCompliant(chunks)
			Expect(chunks).To(HaveLen(2), "two chunks: name, args")

			// Name chunk
			tc0 := toolCallsOf(chunks[0])
			Expect(tc0).To(HaveLen(1))
			Expect(tc0[0].Index).To(Equal(0))
			Expect(tc0[0].ID).To(Equal("tc1"))
			Expect(tc0[0].FunctionCall.Name).To(Equal("search"))
			Expect(tc0[0].FunctionCall.Arguments).To(BeEmpty())
			Expect(contentOf(chunks[0])).To(BeEmpty())

			// Args chunk — MUST NOT carry Content
			tc1 := toolCallsOf(chunks[1])
			Expect(tc1).To(HaveLen(1))
			Expect(tc1[0].FunctionCall.Name).To(BeEmpty())
			Expect(tc1[0].FunctionCall.Arguments).To(Equal(`{"q":"x"}`))
			Expect(contentOf(chunks[1])).To(BeEmpty(),
				"args chunk must not duplicate already-streamed content")
		})
	})

	Describe("Case B — autoparser / content not streamed", func() {
		It("emits role, content, then name+args", func() {
			results := []functions.FuncCallResults{
				{Name: "do", Arguments: "{}", ID: "tc1"},
			}
			chunks := buildDeferredToolCallChunks(
				testID, testModel, testCreated,
				results, 0,
				false, "Here is my plan…",
				true, "",
			)

			expectSpecCompliant(chunks)
			Expect(chunks).To(HaveLen(4), "role, content, name, args")

			// Role chunk
			Expect(chunks[0].Choices[0].Delta.Role).To(Equal("assistant"))
			Expect(contentOf(chunks[0])).To(BeEmpty())
			Expect(toolCallsOf(chunks[0])).To(BeEmpty())

			// Content chunk
			Expect(contentOf(chunks[1])).To(Equal("Here is my plan…"))
			Expect(toolCallsOf(chunks[1])).To(BeEmpty())

			// Name + args chunks
			Expect(toolCallsOf(chunks[2])).To(HaveLen(1))
			Expect(toolCallsOf(chunks[2])[0].FunctionCall.Name).To(Equal("do"))
			Expect(toolCallsOf(chunks[3])).To(HaveLen(1))
			Expect(toolCallsOf(chunks[3])[0].FunctionCall.Arguments).To(Equal("{}"))
		})
	})

	Describe("Case C — multiple deferred calls, content already streamed", func() {
		It("emits (name, args) × 3 with no Content anywhere", func() {
			results := []functions.FuncCallResults{
				{Name: "a", Arguments: "{}", ID: "tcA"},
				{Name: "b", Arguments: "{}", ID: "tcB"},
				{Name: "c", Arguments: "{}", ID: "tcC"},
			}
			chunks := buildDeferredToolCallChunks(
				testID, testModel, testCreated,
				results, 0,
				true, "some narration",
				true, "",
			)

			expectSpecCompliant(chunks)
			Expect(chunks).To(HaveLen(6))

			for i := 0; i < 3; i++ {
				Expect(contentOf(chunks[2*i])).To(BeEmpty(),
					"call #%d name chunk must not carry Content", i)
				Expect(contentOf(chunks[2*i+1])).To(BeEmpty(),
					"call #%d args chunk must not carry Content", i)
				Expect(toolCallsOf(chunks[2*i])[0].Index).To(Equal(i))
				Expect(toolCallsOf(chunks[2*i+1])[0].Index).To(Equal(i))
			}
			Expect(toolCallsOf(chunks[0])[0].FunctionCall.Name).To(Equal("a"))
			Expect(toolCallsOf(chunks[2])[0].FunctionCall.Name).To(Equal("b"))
			Expect(toolCallsOf(chunks[4])[0].FunctionCall.Name).To(Equal("c"))
		})
	})

	Describe("Case D — partial incremental emission", func() {
		It("emits only the deferred tail (call #1), skipping #0", func() {
			results := []functions.FuncCallResults{
				{Name: "a", Arguments: "{}", ID: "tc0"},
				{Name: "b", Arguments: "{}", ID: "tc1"},
			}
			chunks := buildDeferredToolCallChunks(
				testID, testModel, testCreated,
				results, 1,
				true, "narration",
				true, "",
			)

			expectSpecCompliant(chunks)
			Expect(chunks).To(HaveLen(2))
			Expect(toolCallsOf(chunks[0])[0].Index).To(Equal(1))
			Expect(toolCallsOf(chunks[0])[0].FunctionCall.Name).To(Equal("b"))
			Expect(toolCallsOf(chunks[1])[0].Index).To(Equal(1))
			Expect(toolCallsOf(chunks[1])[0].FunctionCall.Arguments).To(Equal("{}"))
		})
	})

	Describe("Case E — all calls already emitted incrementally", func() {
		It("emits nothing", func() {
			results := []functions.FuncCallResults{
				{Name: "a", Arguments: "{}", ID: "tc0"},
				{Name: "b", Arguments: "{}", ID: "tc1"},
			}
			chunks := buildDeferredToolCallChunks(
				testID, testModel, testCreated,
				results, 2,
				true, "narration",
				true, "",
			)

			expectSpecCompliant(chunks)
			Expect(chunks).To(BeEmpty())
		})
	})

	Describe("Case F — content not streamed but textContent empty", func() {
		It("emits only the tool call chunks, no leading role/content", func() {
			results := []functions.FuncCallResults{
				{Name: "x", Arguments: "{}", ID: "tcX"},
			}
			chunks := buildDeferredToolCallChunks(
				testID, testModel, testCreated,
				results, 0,
				false, "",
				true, "",
			)

			expectSpecCompliant(chunks)
			Expect(chunks).To(HaveLen(2))
			Expect(toolCallsOf(chunks[0])[0].FunctionCall.Name).To(Equal("x"))
			Expect(toolCallsOf(chunks[1])[0].FunctionCall.Arguments).To(Equal("{}"))
		})
	})

	Describe("Case G — empty ss.ID falls back to a unique per-index ID", func() {
		It("emits a deterministic per-index fallback", func() {
			results := []functions.FuncCallResults{
				{Name: "x", Arguments: "{}", ID: ""},
			}
			chunks := buildDeferredToolCallChunks(
				testID, testModel, testCreated,
				results, 0,
				true, "narration",
				true, "",
			)

			expectSpecCompliant(chunks)
			Expect(chunks).To(HaveLen(2))
			expectedID := fmt.Sprintf("%s-%d", testID, 0)
			Expect(toolCallsOf(chunks[0])[0].ID).To(Equal(expectedID))
			Expect(toolCallsOf(chunks[1])[0].ID).To(Equal(expectedID))
		})
	})

	Describe("Case G2 — multiple empty IDs get distinct fallbacks", func() {
		It("avoids the collision bug where every empty-ID call shared the request id", func() {
			results := []functions.FuncCallResults{
				{Name: "a", Arguments: "{}", ID: ""},
				{Name: "b", Arguments: "{}", ID: ""},
				{Name: "c", Arguments: "{}", ID: ""},
			}
			chunks := buildDeferredToolCallChunks(
				testID, testModel, testCreated,
				results, 0,
				true, "narration",
				true, "",
			)

			expectSpecCompliant(chunks)
			Expect(chunks).To(HaveLen(6))

			ids := map[string]int{}
			for _, ch := range chunks {
				for _, tc := range toolCallsOf(ch) {
					ids[tc.ID]++
				}
			}
			// Each call yields a name chunk + args chunk → each distinct ID
			// should appear in exactly two chunks. Three distinct IDs
			// overall.
			Expect(ids).To(HaveLen(3), "three distinct per-index fallback IDs")
			for id, n := range ids {
				Expect(n).To(Equal(2), "ID %q should appear in exactly 2 chunks", id)
			}
		})
	})

	Describe("Case H — indices preserved across skip with multiple calls", func() {
		It("emits Index fields matching functionResults positions", func() {
			results := []functions.FuncCallResults{
				{Name: "a", Arguments: "{}", ID: "tc0"},
				{Name: "b", Arguments: "{}", ID: "tc1"},
				{Name: "c", Arguments: "{}", ID: "tc2"},
			}
			chunks := buildDeferredToolCallChunks(
				testID, testModel, testCreated,
				results, 1,
				true, "narration",
				true, "",
			)

			expectSpecCompliant(chunks)
			Expect(chunks).To(HaveLen(4))

			Expect(toolCallsOf(chunks[0])[0].Index).To(Equal(1))
			Expect(toolCallsOf(chunks[1])[0].Index).To(Equal(1))
			Expect(toolCallsOf(chunks[2])[0].Index).To(Equal(2))
			Expect(toolCallsOf(chunks[3])[0].Index).To(Equal(2))
		})
	})

	Describe("Case I — explicit non-empty ID is preserved", func() {
		It("does not touch ss.ID when it's already set", func() {
			results := []functions.FuncCallResults{
				{Name: "x", Arguments: "{}", ID: "abc123"},
			}
			chunks := buildDeferredToolCallChunks(
				testID, testModel, testCreated,
				results, 0,
				true, "narration",
				true, "",
			)

			expectSpecCompliant(chunks)
			Expect(chunks).To(HaveLen(2))
			Expect(toolCallsOf(chunks[0])[0].ID).To(Equal("abc123"))
			Expect(toolCallsOf(chunks[1])[0].ID).To(Equal("abc123"))
		})
	})

	Describe("Case J — chunk-shape sanity", func() {
		It("splits Name into the first chunk and Arguments into the second", func() {
			results := []functions.FuncCallResults{
				{Name: "x", Arguments: `{"k":"v"}`, ID: "tcX"},
			}
			chunks := buildDeferredToolCallChunks(
				testID, testModel, testCreated,
				results, 0,
				true, "narration",
				true, "",
			)

			expectSpecCompliant(chunks)
			Expect(chunks).To(HaveLen(2))

			Expect(toolCallsOf(chunks[0])[0].FunctionCall.Name).To(Equal("x"))
			Expect(toolCallsOf(chunks[0])[0].FunctionCall.Arguments).To(BeEmpty())

			Expect(toolCallsOf(chunks[1])[0].FunctionCall.Name).To(BeEmpty())
			Expect(toolCallsOf(chunks[1])[0].FunctionCall.Arguments).To(Equal(`{"k":"v"}`))
		})
	})

	Describe("Case K — metadata propagation", func() {
		It("stamps every chunk with the same id/model/created", func() {
			results := []functions.FuncCallResults{
				{Name: "a", Arguments: "{}", ID: "tcA"},
				{Name: "b", Arguments: "{}", ID: "tcB"},
			}
			chunks := buildDeferredToolCallChunks(
				testID, testModel, testCreated,
				results, 0,
				false, "hello",
				true, "",
			)

			expectSpecCompliant(chunks)
			expectMetadata(chunks, testID, testModel, testCreated)
		})
	})

	Describe("Case L — Choices[0].Index == 0 invariant", func() {
		It("is upheld across every branch the helper can take", func() {
			scenarios := []struct {
				name                  string
				functionResults       []functions.FuncCallResults
				lastEmittedCount      int
				contentStreamed       bool
				text                  string
				reasoningStreamed     bool
				reasoning             string
			}{
				{"streamed-content-deferred-call",
					[]functions.FuncCallResults{{Name: "a", Arguments: "{}"}},
					0, true, "hi", true, ""},
				{"unstreamed-content-deferred-call",
					[]functions.FuncCallResults{{Name: "a", Arguments: "{}"}},
					0, false, "hello", true, ""},
				{"unstreamed-reasoning-and-content",
					[]functions.FuncCallResults{{Name: "a", Arguments: "{}"}},
					0, false, "hello", false, "thinking…"},
				{"partial-incremental",
					[]functions.FuncCallResults{
						{Name: "a", Arguments: "{}"},
						{Name: "b", Arguments: "{}"}},
					1, true, "hi", true, ""},
			}
			for _, sc := range scenarios {
				chunks := buildDeferredToolCallChunks(
					testID, testModel, testCreated,
					sc.functionResults, sc.lastEmittedCount,
					sc.contentStreamed, sc.text,
					sc.reasoningStreamed, sc.reasoning,
				)
				for i, ch := range chunks {
					Expect(ch.Choices[0].Index).To(Equal(0),
						"scenario %q chunk[%d] Choices[0].Index", sc.name, i)
				}
			}
		})
	})

	Describe("Case M — spec compliance across every scenario", func() {
		It("never mixes Content or Reasoning with ToolCalls in a single delta", func() {
			scenarios := []struct {
				name                  string
				functionResults       []functions.FuncCallResults
				lastEmittedCount      int
				contentStreamed       bool
				text                  string
				reasoningStreamed     bool
				reasoning             string
			}{
				{"A", []functions.FuncCallResults{{Name: "a", Arguments: "{}", ID: "tc"}},
					0, true, "already-streamed", true, ""},
				{"C", []functions.FuncCallResults{
					{Name: "a", Arguments: "{}", ID: "tc0"},
					{Name: "b", Arguments: "{}", ID: "tc1"}},
					0, true, "already-streamed", true, ""},
				{"B", []functions.FuncCallResults{{Name: "a", Arguments: "{}", ID: "tc"}},
					0, false, "plan", true, ""},
				{"Reasoning-deferred", []functions.FuncCallResults{{Name: "a", Arguments: "{}", ID: "tc"}},
					0, false, "plan", false, "thinking…"},
			}
			for _, sc := range scenarios {
				chunks := buildDeferredToolCallChunks(
					testID, testModel, testCreated,
					sc.functionResults, sc.lastEmittedCount,
					sc.contentStreamed, sc.text,
					sc.reasoningStreamed, sc.reasoning,
				)
				for i, ch := range chunks {
					hasContent := contentOf(ch) != ""
					hasReasoning := reasoningOf(ch) != ""
					hasToolCalls := len(toolCallsOf(ch)) > 0
					Expect(hasContent && hasToolCalls).To(BeFalse(),
						"scenario %q chunk[%d] mixes Content with ToolCalls", sc.name, i)
					Expect(hasReasoning && hasToolCalls).To(BeFalse(),
						"scenario %q chunk[%d] mixes Reasoning with ToolCalls", sc.name, i)
				}
			}
		})
	})

	Describe("Case N — empty functionResults", func() {
		It("emits nothing, including no leading role/content/reasoning", func() {
			chunks := buildDeferredToolCallChunks(
				testID, testModel, testCreated,
				nil, 0,
				false, "ignored",
				false, "ignored",
			)
			Expect(chunks).To(BeEmpty())
		})
	})

	Describe("Case O — content not streamed but all calls already emitted", func() {
		It("emits nothing, not even a standalone content chunk", func() {
			results := []functions.FuncCallResults{
				{Name: "a", Arguments: "{}", ID: "tc0"},
				{Name: "b", Arguments: "{}", ID: "tc1"},
			}
			chunks := buildDeferredToolCallChunks(
				testID, testModel, testCreated,
				results, 2,
				false, "narration",
				false, "thinking…",
			)
			Expect(chunks).To(BeEmpty(),
				"no tool_calls to trigger on, so no leading role/content/reasoning either")
		})
	})

	Describe("Reasoning — autoparser delivered reasoning only at end", func() {
		It("emits a leading reasoning chunk when !reasoningAlreadyStreamed", func() {
			results := []functions.FuncCallResults{
				{Name: "a", Arguments: "{}", ID: "tc"},
			}
			chunks := buildDeferredToolCallChunks(
				testID, testModel, testCreated,
				results, 0,
				true, "streamed content",
				false, "model's private thoughts",
			)

			expectSpecCompliant(chunks)
			Expect(chunks).To(HaveLen(3), "reasoning, name, args")

			Expect(reasoningOf(chunks[0])).To(Equal("model's private thoughts"))
			Expect(contentOf(chunks[0])).To(BeEmpty())
			Expect(toolCallsOf(chunks[0])).To(BeEmpty())

			// The following two are the tool_call name + args chunks.
			Expect(toolCallsOf(chunks[1])[0].FunctionCall.Name).To(Equal("a"))
			Expect(toolCallsOf(chunks[2])[0].FunctionCall.Arguments).To(Equal("{}"))
		})

		It("emits reasoning before role+content when neither was streamed", func() {
			results := []functions.FuncCallResults{
				{Name: "a", Arguments: "{}", ID: "tc"},
			}
			chunks := buildDeferredToolCallChunks(
				testID, testModel, testCreated,
				results, 0,
				false, "final plan",
				false, "private thoughts",
			)

			expectSpecCompliant(chunks)
			Expect(chunks).To(HaveLen(5), "reasoning, role, content, name, args")

			Expect(reasoningOf(chunks[0])).To(Equal("private thoughts"))
			Expect(chunks[1].Choices[0].Delta.Role).To(Equal("assistant"))
			Expect(contentOf(chunks[2])).To(Equal("final plan"))
			Expect(toolCallsOf(chunks[3])[0].FunctionCall.Name).To(Equal("a"))
			Expect(toolCallsOf(chunks[4])[0].FunctionCall.Arguments).To(Equal("{}"))
		})

		It("does not re-emit reasoning that was already streamed", func() {
			results := []functions.FuncCallResults{
				{Name: "a", Arguments: "{}", ID: "tc"},
			}
			chunks := buildDeferredToolCallChunks(
				testID, testModel, testCreated,
				results, 0,
				true, "streamed",
				true, "already-sent reasoning",
			)

			expectSpecCompliant(chunks)
			Expect(chunks).To(HaveLen(2), "only name + args; no reasoning re-emission")
			for _, ch := range chunks {
				Expect(reasoningOf(ch)).To(BeEmpty())
			}
		})
	})
})

var _ = Describe("hasRealCall", func() {
	const noAction = "answer"

	It("returns false for nil and empty slices", func() {
		Expect(hasRealCall(nil, noAction)).To(BeFalse())
		Expect(hasRealCall([]functions.FuncCallResults{}, noAction)).To(BeFalse())
	})

	It("returns false when every entry is the noAction sentinel", func() {
		results := []functions.FuncCallResults{
			{Name: noAction, Arguments: `{"message":"hi"}`},
			{Name: noAction, Arguments: `{"message":"hello"}`},
		}
		Expect(hasRealCall(results, noAction)).To(BeFalse())
	})

	It("returns true when only one entry is a real call", func() {
		results := []functions.FuncCallResults{
			{Name: "search", Arguments: "{}"},
		}
		Expect(hasRealCall(results, noAction)).To(BeTrue())
	})

	It("returns true when a real call follows a noAction entry", func() {
		// This is the regression the follow-up fixes: the old
		// functionResults[0].Name == noAction check would declare this
		// noActionToRun and drop the real call entirely.
		results := []functions.FuncCallResults{
			{Name: noAction, Arguments: `{"message":"hi"}`},
			{Name: "search", Arguments: "{}"},
		}
		Expect(hasRealCall(results, noAction)).To(BeTrue())
	})

	It("returns true when a real call precedes a noAction entry", func() {
		results := []functions.FuncCallResults{
			{Name: "search", Arguments: "{}"},
			{Name: noAction, Arguments: `{"message":"hi"}`},
		}
		Expect(hasRealCall(results, noAction)).To(BeTrue())
	})
})

var _ = Describe("buildNoActionFinalChunks", func() {
	const (
		testID      = "req"
		testModel   = "test-model"
		testCreated = 1700000000
	)
	usage := schema.OpenAIUsage{PromptTokens: 5, CompletionTokens: 7, TotalTokens: 12}

	Describe("Content streamed — trailing usage chunk", func() {
		It("emits just one chunk with usage, no content, no reasoning when reasoning was streamed", func() {
			chunks := buildNoActionFinalChunks(
				testID, testModel, testCreated,
				true, true,
				"", "already-streamed-reasoning", usage,
			)

			Expect(chunks).To(HaveLen(1))
			Expect(chunks[0].Usage.TotalTokens).To(Equal(12))
			Expect(contentOf(chunks[0])).To(BeEmpty())
			Expect(reasoningOf(chunks[0])).To(BeEmpty(),
				"reasoning must not be re-emitted once it was streamed via the callback")
		})

		It("emits a trailing reasoning delivery when reasoning came only at end", func() {
			chunks := buildNoActionFinalChunks(
				testID, testModel, testCreated,
				true, false,
				"", "autoparser final reasoning", usage,
			)

			Expect(chunks).To(HaveLen(1))
			Expect(reasoningOf(chunks[0])).To(Equal("autoparser final reasoning"))
			Expect(contentOf(chunks[0])).To(BeEmpty())
			Expect(chunks[0].Usage.TotalTokens).To(Equal(12))
		})

		It("omits reasoning when it's empty regardless of streamed flag", func() {
			chunks := buildNoActionFinalChunks(
				testID, testModel, testCreated,
				true, false,
				"", "", usage,
			)

			Expect(chunks).To(HaveLen(1))
			Expect(reasoningOf(chunks[0])).To(BeEmpty())
		})
	})

	Describe("Content not streamed — role, then content+usage", func() {
		It("emits role chunk then content chunk without reasoning when reasoning was streamed", func() {
			chunks := buildNoActionFinalChunks(
				testID, testModel, testCreated,
				false, true,
				"the answer", "already-streamed-reasoning", usage,
			)

			Expect(chunks).To(HaveLen(2))
			Expect(chunks[0].Choices[0].Delta.Role).To(Equal("assistant"))
			Expect(contentOf(chunks[0])).To(BeEmpty())

			Expect(contentOf(chunks[1])).To(Equal("the answer"))
			Expect(reasoningOf(chunks[1])).To(BeEmpty(),
				"reasoning must not be re-emitted if it was streamed earlier")
			Expect(chunks[1].Usage.TotalTokens).To(Equal(12))
		})

		It("emits role, then content+reasoning when reasoning was not streamed", func() {
			chunks := buildNoActionFinalChunks(
				testID, testModel, testCreated,
				false, false,
				"the answer", "autoparser final reasoning", usage,
			)

			Expect(chunks).To(HaveLen(2))
			Expect(chunks[0].Choices[0].Delta.Role).To(Equal("assistant"))

			Expect(contentOf(chunks[1])).To(Equal("the answer"))
			Expect(reasoningOf(chunks[1])).To(Equal("autoparser final reasoning"))
			Expect(chunks[1].Usage.TotalTokens).To(Equal(12))
		})

		It("still emits content even when reasoning is empty", func() {
			chunks := buildNoActionFinalChunks(
				testID, testModel, testCreated,
				false, false,
				"just an answer", "", usage,
			)

			Expect(chunks).To(HaveLen(2))
			Expect(contentOf(chunks[1])).To(Equal("just an answer"))
			Expect(reasoningOf(chunks[1])).To(BeEmpty())
		})
	})

	Describe("Metadata and shape invariants", func() {
		It("stamps every chunk with the same id/model/created and object", func() {
			chunks := buildNoActionFinalChunks(
				testID, testModel, testCreated,
				false, false,
				"hi", "reasoning", usage,
			)
			for i, ch := range chunks {
				Expect(ch.ID).To(Equal(testID), "chunk[%d] ID", i)
				Expect(ch.Model).To(Equal(testModel), "chunk[%d] Model", i)
				Expect(ch.Created).To(Equal(testCreated), "chunk[%d] Created", i)
				Expect(ch.Object).To(Equal("chat.completion.chunk"), "chunk[%d] Object", i)
				Expect(ch.Choices).To(HaveLen(1))
				Expect(ch.Choices[0].Index).To(Equal(0))
			}
		})
	})
})
