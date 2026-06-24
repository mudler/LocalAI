package openai

import (
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	reason "github.com/mudler/LocalAI/pkg/reasoning"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Regression test for the prefill-misclassification artifact surfaced in
// the review of #9991: when LocalAI templates qwen3 with
// use_tokenizer_template (the post-#9985 gallery shape),
// DetectThinkingStartToken finds <think> in the model's jinja chat
// template — without evaluating the surrounding {% if enable_thinking %}
// guard — and the Go-side extractor's PrependThinkingTokenIfNeeded then
// treats every non-thinking output token as reasoning. The autoparser does
// not classify qwen3's tool calls into ChatDelta.ToolCalls (qwen3's tool
// format isn't on llama.cpp's recognized-tool list), so all tokens land in
// ChatDelta.Content while the Go-side extractor silently accumulates a
// "reasoning" string equal to the raw tool-call JSON. End-of-stream this
// is flushed as a trailing `delta.reasoning` chunk to the client.
//
// chooseDeferredReasoning is the gate: when the autoparser was active for
// any chunk (preferAutoparser sticky), we trust its reasoning_content
// classification (usually empty) instead of the polluted Go-side state.
var _ = Describe("chooseDeferredReasoning", func() {
	// Simulate the qwen3-after-#9985 misclassification: build a real
	// extractor with a <think> thinking-start token, then feed it
	// non-thinking content. The extractor will (correctly per its own
	// contract) treat the content as reasoning because
	// PrependThinkingTokenIfNeeded synthesizes a leading <think>.
	pollutedExtractor := func(content string) *reason.ReasoningExtractor {
		e := reason.NewReasoningExtractor("<think>", reason.Config{})
		e.ProcessToken(content)
		Expect(e.Reasoning()).To(Equal(content),
			"sanity: when the thinking-start token is set and content has no real <think>...</think>, "+
				"the extractor classifies all content as reasoning — this is exactly the prefill pollution "+
				"we want chooseDeferredReasoning to guard against")
		return e
	}

	Context("autoparser was active (preferAutoparser=true)", func() {
		It("returns the autoparser's reasoning classification, ignoring the polluted Go-side state", func() {
			toolCallJSON := `{"arguments": {"cmd": "echo hello"}, "name": "exec"}`
			extractor := pollutedExtractor(toolCallJSON)
			// What the C++ autoparser sent: content chunks but no
			// reasoning_content (qwen3 tool calls aren't classified by
			// the upstream PEG parser).
			chatDeltas := []*pb.ChatDelta{
				{Content: toolCallJSON, ReasoningContent: ""},
			}

			got := chooseDeferredReasoning(true, chatDeltas, extractor)

			Expect(got).To(BeEmpty(),
				"chooseDeferredReasoning must NOT return the polluted extractor state "+
					"when the autoparser was active — the autoparser correctly classified zero reasoning")
		})

		It("returns the autoparser's reasoning when it actually did classify reasoning", func() {
			// The other side of the contract: when the autoparser was
			// in jinja-with-recognized-format mode and DID classify
			// reasoning, pass that through verbatim.
			actualReasoning := "Okay, the user asked X. I should call exec."
			extractor := pollutedExtractor("ignored polluted state")
			chatDeltas := []*pb.ChatDelta{
				{Content: "", ReasoningContent: actualReasoning},
			}

			got := chooseDeferredReasoning(true, chatDeltas, extractor)

			Expect(got).To(Equal(actualReasoning))
		})
	})

	Context("autoparser was NOT active (preferAutoparser=false)", func() {
		It("falls back to the Go-side extractor — the right source for vLLM and other autoparser-less backends", func() {
			realReasoning := "Genuine reasoning from a backend without an autoparser"
			extractor := reason.NewReasoningExtractor("<think>", reason.Config{})
			extractor.ProcessToken("<think>" + realReasoning + "</think>final answer")

			got := chooseDeferredReasoning(false, nil, extractor)

			Expect(got).To(Equal(realReasoning))
		})

		It("falls back even when ChatDeltas are present but the autoparser never classified anything", func() {
			// Defensive: chatDeltas could carry vestigial data; if
			// preferAutoparser wasn't flipped, we still use the
			// extractor.
			extractor := reason.NewReasoningExtractor("", reason.Config{})
			extractor.ProcessToken("<think>some thoughts</think>answer")

			got := chooseDeferredReasoning(false, []*pb.ChatDelta{{Content: "answer"}}, extractor)

			Expect(got).To(Equal("some thoughts"))
		})
	})
})
