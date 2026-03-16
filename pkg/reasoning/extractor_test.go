package reasoning_test

import (
	. "github.com/mudler/LocalAI/pkg/reasoning"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ReasoningExtractor", func() {
	Context("basic streaming with <think> tags", func() {
		It("should extract reasoning and content deltas incrementally", func() {
			ext := NewReasoningExtractor("<think>", Config{})

			// Simulate tokens arriving one at a time
			tokens := []string{"<think>", "I need", " to think", "</think>", "Hello", " world"}
			var allReasoningDeltas, allContentDeltas string

			for _, tok := range tokens {
				rDelta, cDelta := ext.ProcessToken(tok)
				allReasoningDeltas += rDelta
				allContentDeltas += cDelta
			}

			Expect(ext.Reasoning()).To(Equal("I need to think"))
			Expect(ext.CleanedContent()).To(Equal("Hello world"))
			Expect(allReasoningDeltas).To(Equal("I need to think"))
			Expect(allContentDeltas).To(Equal("Hello world"))
		})
	})

	Context("no reasoning tags", func() {
		It("should pass all content through as content deltas", func() {
			ext := NewReasoningExtractor("", Config{})

			rDelta1, cDelta1 := ext.ProcessToken("Hello")
			rDelta2, cDelta2 := ext.ProcessToken(" world")

			Expect(rDelta1).To(BeEmpty())
			Expect(cDelta1).To(Equal("Hello"))
			Expect(rDelta2).To(BeEmpty())
			Expect(cDelta2).To(Equal(" world"))
			Expect(ext.Reasoning()).To(BeEmpty())
			Expect(ext.CleanedContent()).To(Equal("Hello world"))
		})
	})

	Context("unclosed thinking tags", func() {
		It("should treat content after unclosed tag as reasoning", func() {
			ext := NewReasoningExtractor("<think>", Config{})

			ext.ProcessToken("<think>")
			ext.ProcessToken("still thinking")
			// No closing tag - reasoning is extracted from unclosed tag

			Expect(ext.Reasoning()).To(Equal("still thinking"))
			Expect(ext.CleanedContent()).To(BeEmpty())
		})
	})

	Context("empty tokens", func() {
		It("should handle empty tokens gracefully", func() {
			ext := NewReasoningExtractor("", Config{})

			rDelta, cDelta := ext.ProcessToken("")
			Expect(rDelta).To(BeEmpty())
			Expect(cDelta).To(BeEmpty())

			rDelta, cDelta = ext.ProcessToken("Hello")
			Expect(rDelta).To(BeEmpty())
			Expect(cDelta).To(Equal("Hello"))
		})
	})

	Context("Reset", func() {
		It("should clear all state", func() {
			ext := NewReasoningExtractor("<think>", Config{})

			ext.ProcessToken("<think>reason</think>content")
			Expect(ext.Reasoning()).ToNot(BeEmpty())
			Expect(ext.CleanedContent()).ToNot(BeEmpty())

			ext.Reset()
			Expect(ext.Reasoning()).To(BeEmpty())
			Expect(ext.CleanedContent()).To(BeEmpty())
			Expect(ext.Accumulated()).To(BeEmpty())
		})
	})

	Context("disabled reasoning", func() {
		It("should pass all content through when reasoning is disabled", func() {
			disabled := true
			ext := NewReasoningExtractor("<think>", Config{DisableReasoning: &disabled})

			rDelta, cDelta := ext.ProcessToken("<think>reason</think>content")
			Expect(rDelta).To(BeEmpty())
			Expect(cDelta).To(Equal("<think>reason</think>content"))
			Expect(ext.Reasoning()).To(BeEmpty())
		})
	})

	Context("split tags across tokens", func() {
		It("should handle tags split across multiple tokens", func() {
			ext := NewReasoningExtractor("<think>", Config{})

			// Tag arrives in pieces
			ext.ProcessToken("<thi")
			ext.ProcessToken("nk>reasoning here</thi")
			ext.ProcessToken("nk>final answer")

			Expect(ext.Reasoning()).To(Equal("reasoning here"))
			Expect(ext.CleanedContent()).To(Equal("final answer"))
		})
	})

	Context("ResetAndSuppressReasoning", func() {
		It("should suppress reasoning deltas but still extract reasoning internally", func() {
			ext := NewReasoningExtractor("<think>", Config{})

			// First pass: reasoning is emitted normally
			rDelta1, cDelta1 := ext.ProcessToken("<think>first reasoning</think>first content")
			Expect(rDelta1).To(Equal("first reasoning"))
			Expect(cDelta1).To(Equal("first content"))
			Expect(ext.Suppressed()).To(BeFalse())

			// Simulate retry: suppress reasoning
			ext.ResetAndSuppressReasoning()
			Expect(ext.Suppressed()).To(BeTrue())
			Expect(ext.Reasoning()).To(BeEmpty())
			Expect(ext.CleanedContent()).To(BeEmpty())
			Expect(ext.Accumulated()).To(BeEmpty())

			// Second pass: reasoning deltas suppressed, content still works
			rDelta2, cDelta2 := ext.ProcessToken("<think>retry reasoning</think>retry content")
			Expect(rDelta2).To(BeEmpty(), "reasoning delta should be suppressed after ResetAndSuppressReasoning")
			Expect(cDelta2).To(Equal("retry content"))

			// Internal state still tracks reasoning (for CleanedContent to work)
			Expect(ext.Reasoning()).To(Equal("retry reasoning"))
			Expect(ext.CleanedContent()).To(Equal("retry content"))
		})

		It("should suppress reasoning across multiple streaming tokens", func() {
			ext := NewReasoningExtractor("<think>", Config{})
			ext.ResetAndSuppressReasoning()

			tokens := []string{"<think>", "suppressed", " thought", "</think>", "visible", " answer"}
			var allReasoningDeltas, allContentDeltas string

			for _, tok := range tokens {
				rDelta, cDelta := ext.ProcessToken(tok)
				allReasoningDeltas += rDelta
				allContentDeltas += cDelta
			}

			Expect(allReasoningDeltas).To(BeEmpty(), "no reasoning deltas should be emitted when suppressed")
			Expect(allContentDeltas).To(Equal("visible answer"))
			Expect(ext.Reasoning()).To(Equal("suppressed thought"))
			Expect(ext.CleanedContent()).To(Equal("visible answer"))
		})
	})

	Context("Accumulated", func() {
		It("should return all raw tokens concatenated", func() {
			ext := NewReasoningExtractor("<think>", Config{})

			ext.ProcessToken("<think>reason</think>")
			ext.ProcessToken("content")

			Expect(ext.Accumulated()).To(Equal("<think>reason</think>content"))
		})
	})

	Context("with thinking start token prefill", func() {
		It("should prepend thinking token when prefill is not disabled", func() {
			ext := NewReasoningExtractor("<think>", Config{})

			// Content without explicit <think> tag - extractor should prepend it
			ext.ProcessToken("I am thinking")
			ext.ProcessToken("</think>")
			ext.ProcessToken("Answer here")

			Expect(ext.Reasoning()).To(Equal("I am thinking"))
			Expect(ext.CleanedContent()).To(Equal("Answer here"))
		})
	})

	Context("strip reasoning only", func() {
		It("should strip reasoning from content but not return it", func() {
			strip := true
			ext := NewReasoningExtractor("<think>", Config{StripReasoningOnly: &strip})

			ext.ProcessToken("<think>secret reasoning</think>visible content")

			Expect(ext.Reasoning()).To(BeEmpty())
			Expect(ext.CleanedContent()).To(Equal("visible content"))
		})
	})
})
