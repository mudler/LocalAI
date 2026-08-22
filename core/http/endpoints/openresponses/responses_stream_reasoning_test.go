package openresponses

import (
	"github.com/mudler/LocalAI/core/backend"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	reason "github.com/mudler/LocalAI/pkg/reasoning"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// usageWithChatDeltas builds a TokenUsage carrying a single C++ autoparser
// ChatDelta with the given content / reasoning_content split.
func usageWithChatDeltas(content, reasoningContent string) backend.TokenUsage {
	return backend.TokenUsage{
		ChatDeltas: []*pb.ChatDelta{
			{Content: content, ReasoningContent: reasoningContent},
		},
	}
}

// Regression tests for issue #9658: in the /v1/responses streaming handler the
// thinking monologue from a reasoning model was streamed to the client as a
// normal message (msg_ item, output_text.delta) instead of as a reasoning
// item, and was only re-classified into a reasoning item AFTER the stream
// completed.
//
// Root cause: the live reasoning item was gated on extractor.Reasoning(),
// which is only updated by the Go-side raw-tag parser (ProcessToken). When the
// C++ autoparser drives reasoning through reasoning_content ChatDeltas, the
// reasoning is computed via ProcessChatDeltaReasoning into a SEPARATE
// accumulator, so extractor.Reasoning() stays empty and the gate never fires.
var _ = Describe("streamReasoningRouter", func() {
	Context("autoparser drives reasoning via reasoning_content (issue #9658)", func() {
		It("opens a reasoning item during streaming and targets it (not the message)", func() {
			extractor := reason.NewReasoningExtractor("", reason.Config{})
			router := newStreamReasoningRouter(extractor)

			// The raw token is empty: the autoparser carries the reasoning in
			// ChatDelta.ReasoningContent, so the Go-side extractor's
			// Reasoning() stays "" — exactly the state in which the buggy
			// extractor.Reasoning() gate failed to open a reasoning item.
			routing := router.route("", usageWithChatDeltas("", "Let me think about this"))

			Expect(routing.ReasoningDelta).To(Equal("Let me think about this"),
				"the autoparser's reasoning_content must surface as a reasoning delta during streaming")
			Expect(routing.OpenReasoningItem).To(BeTrue(),
				"a reasoning output item must be opened live, not deferred to end-of-stream (#9658)")
			Expect(routing.ContentDelta).To(BeEmpty())
			Expect(routing.OpenMessageItem).To(BeFalse(),
				"reasoning deltas must target the reasoning_ item, never open/route to a msg_ item")
		})

		It("does not re-open the reasoning item on subsequent reasoning deltas", func() {
			extractor := reason.NewReasoningExtractor("", reason.Config{})
			router := newStreamReasoningRouter(extractor)

			_ = router.route("", usageWithChatDeltas("", "first "))
			routing := router.route("", usageWithChatDeltas("", "second"))

			Expect(routing.ReasoningDelta).To(Equal("second"))
			Expect(routing.OpenReasoningItem).To(BeFalse())
		})
	})

	Context("pure content stream", func() {
		It("never opens a reasoning item", func() {
			extractor := reason.NewReasoningExtractor("", reason.Config{})
			router := newStreamReasoningRouter(extractor)

			// Content-only with no reasoning_content: the autoparser is in its
			// pure-content mode, so the router stays on the Go-side extractor,
			// which sees the content via the raw token.
			routing := router.route("hello world", usageWithChatDeltas("hello world", ""))

			Expect(routing.ContentDelta).To(Equal("hello world"))
			Expect(routing.OpenMessageItem).To(BeTrue())
			Expect(routing.OpenReasoningItem).To(BeFalse(),
				"a content-only stream must never open a reasoning item")
			Expect(router.ReasoningStreamed()).To(BeFalse())
		})
	})

	Context("content-only autoparser with embedded <think> (issue #9985 fallback)", func() {
		It("falls back to Go-side extraction instead of leaking <think> into content", func() {
			extractor := reason.NewReasoningExtractor("", reason.Config{})
			router := newStreamReasoningRouter(extractor)

			// The autoparser is in its non-jinja pure-content fallback: it
			// surfaces the whole string as Content with zero reasoning_content,
			// tags and all. The router must NOT trust it (preferAutoparser must
			// stay false) and instead use the Go-side split.
			routing := router.route("<think>reasoning here</think>answer",
				usageWithChatDeltas("<think>reasoning here</think>answer", ""))

			Expect(routing.ContentDelta).To(Equal("answer"),
				"content must be the cleaned answer, not the raw <think>...</think> string")
			Expect(routing.ReasoningDelta).To(Equal("reasoning here"))
			Expect(routing.OpenReasoningItem).To(BeTrue())
		})
	})
})
