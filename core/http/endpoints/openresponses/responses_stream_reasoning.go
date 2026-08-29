package openresponses

import (
	"github.com/mudler/LocalAI/core/backend"
	reason "github.com/mudler/LocalAI/pkg/reasoning"
)

// streamTokenRouting describes how a single streamed token's deltas should be
// routed to Open Responses output items: the reasoning/content split and
// whether a new reasoning or message output item must be opened before the
// corresponding delta can be emitted.
type streamTokenRouting struct {
	ReasoningDelta string
	ContentDelta   string
	// OpenReasoningItem is true when a reasoning output item must be created
	// before emitting ReasoningDelta (the first reasoning delta of the stream).
	OpenReasoningItem bool
	// OpenMessageItem is true when a message output item must be created before
	// emitting ContentDelta (the first content delta of the stream).
	OpenMessageItem bool
}

// streamReasoningRouter classifies streamed tokens into reasoning vs message
// deltas and tracks which output items have been opened, so the SSE-emitting
// code in handleOpenResponsesStream becomes a thin shell over a unit-testable
// decision.
//
// It mirrors the sticky-preferAutoparser logic in the OpenAI chat streaming
// worker (core/http/endpoints/openai/chat_stream_workers.go, processStream):
// once the C++ autoparser has surfaced reasoning_content, we trust its
// classification for the rest of the stream; until then we fall back to the
// Go-side reasoning extractor so a pure-content autoparser (the non-jinja PEG
// fallback, issue #9985) does not leak <think>...</think> tokens into content.
//
// Crucially, the decision to open and target a reasoning item keys off the
// per-token reasoningDelta, NOT extractor.Reasoning(): the autoparser path
// computes reasoning through ProcessChatDeltaReasoning, which updates a
// separate accumulator that extractor.Reasoning() never exposes. Gating on
// extractor.Reasoning() (issue #9658) dropped live reasoning whenever the
// autoparser drove it via reasoning_content, surfacing it only after the
// stream completed and mis-routing earlier deltas onto the msg_ item.
type streamReasoningRouter struct {
	extractor        *reason.ReasoningExtractor
	preferAutoparser bool
	reasoningOpened  bool
	messageOpened    bool
}

func newStreamReasoningRouter(extractor *reason.ReasoningExtractor) *streamReasoningRouter {
	return &streamReasoningRouter{extractor: extractor}
}

// classify splits a token into reasoning/content deltas using the sticky
// preferAutoparser preference. Once the C++ autoparser has surfaced
// reasoning_content we trust it for the rest of the stream; until then we fall
// back to the Go-side extractor so a pure-content autoparser (zero
// reasoning_content, issue #9985) does not leak <think>...</think> tokens into
// content.
func (r *streamReasoningRouter) classify(token string, usage backend.TokenUsage) (reasoningDelta, contentDelta string) {
	goReasoning, goContent := r.extractor.ProcessToken(token)
	if usage.HasChatDeltaContent() {
		rawReasoning, cd := usage.ChatDeltaReasoningAndContent()
		if rawReasoning != "" {
			r.preferAutoparser = true
		}
		if r.preferAutoparser {
			contentDelta = cd
			reasoningDelta = r.extractor.ProcessChatDeltaReasoning(rawReasoning)
		} else {
			reasoningDelta = goReasoning
			contentDelta = goContent
		}
	} else {
		reasoningDelta = goReasoning
		contentDelta = goContent
	}
	return reasoningDelta, contentDelta
}

// route classifies a token and decides which output items its deltas target,
// flipping the opened-flags as items are created.
//
// The reasoning gate keys off reasoningDelta, NOT extractor.Reasoning(): the
// autoparser path computes reasoning via ProcessChatDeltaReasoning into a
// separate accumulator that extractor.Reasoning() never reflects (issue #9658).
func (r *streamReasoningRouter) route(token string, usage backend.TokenUsage) streamTokenRouting {
	reasoningDelta, contentDelta := r.classify(token, usage)
	out := streamTokenRouting{ReasoningDelta: reasoningDelta, ContentDelta: contentDelta}
	if reasoningDelta != "" && !r.reasoningOpened {
		out.OpenReasoningItem = true
		r.reasoningOpened = true
	}
	if contentDelta != "" && !r.messageOpened {
		out.OpenMessageItem = true
		r.messageOpened = true
	}
	return out
}

// resetForIteration clears the per-stream routing state for an MCP re-inference
// iteration, mirroring extractor.Reset() on the underlying extractor.
func (r *streamReasoningRouter) resetForIteration() {
	r.preferAutoparser = false
	r.reasoningOpened = false
	r.messageOpened = false
	r.extractor.Reset()
}

// ReasoningStreamed reports whether a reasoning output item was opened during
// the stream. The end-of-stream closing blocks key off this rather than a
// reasoning-id string so the ordering (reasoning before message) is explicit.
func (r *streamReasoningRouter) ReasoningStreamed() bool {
	return r.reasoningOpened
}
