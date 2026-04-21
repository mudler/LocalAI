package openai

import (
	"fmt"

	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/functions"
)

// hasRealCall reports whether functionResults contains at least one
// entry whose Name is something other than the noAction sentinel.
// Used by processTools to decide between the "answer the question"
// path and the real tool-call flush.
func hasRealCall(functionResults []functions.FuncCallResults, noAction string) bool {
	for _, fc := range functionResults {
		if fc.Name != noAction {
			return true
		}
	}
	return false
}

// buildNoActionFinalChunks produces the closing SSE chunks for the
// noActionToRun branch of processTools (i.e. the model chose the "answer"
// pseudo-function or emitted no tool calls at all).
//
// When content was already streamed (contentAlreadyStreamed=true) the
// helper emits a single trailing usage chunk, optionally carrying
// reasoning that was produced but not streamed incrementally. When
// content was not streamed it emits a role chunk followed by a
// content+reasoning+usage chunk — the "send everything at once" fallback.
//
// Reasoning re-emission is guarded by reasoningAlreadyStreamed, not by
// probing the extractor's Go-side state: the C++ autoparser delivers
// reasoning through ProcessChatDeltaReasoning which populates a
// separate accumulator that extractor.Reasoning() does not expose.
// Without this guard the callback would stream reasoning incrementally
// and the final chunk would duplicate it.
func buildNoActionFinalChunks(
	id, model string,
	created int,
	contentAlreadyStreamed bool,
	reasoningAlreadyStreamed bool,
	content string,
	reasoning string,
	usage schema.OpenAIUsage,
) []schema.OpenAIResponse {
	var out []schema.OpenAIResponse

	if contentAlreadyStreamed {
		delta := &schema.Message{}
		if reasoning != "" && !reasoningAlreadyStreamed {
			r := reasoning
			delta.Reasoning = &r
		}
		out = append(out, schema.OpenAIResponse{
			ID: id, Created: created, Model: model,
			Choices: []schema.Choice{{Delta: delta, Index: 0}},
			Object:  "chat.completion.chunk",
			Usage:   usage,
		})
		return out
	}

	// Content was not streamed — send role, then content (+reasoning) + usage.
	out = append(out, schema.OpenAIResponse{
		ID: id, Created: created, Model: model,
		Choices: []schema.Choice{{
			Delta: &schema.Message{Role: "assistant"},
			Index: 0,
		}},
		Object: "chat.completion.chunk",
	})

	c := content
	delta := &schema.Message{Content: &c}
	if reasoning != "" && !reasoningAlreadyStreamed {
		r := reasoning
		delta.Reasoning = &r
	}
	out = append(out, schema.OpenAIResponse{
		ID: id, Created: created, Model: model,
		Choices: []schema.Choice{{Delta: delta, Index: 0}},
		Object:  "chat.completion.chunk",
		Usage:   usage,
	})
	return out
}

// buildDeferredToolCallChunks produces the SSE chunks for tool calls that
// were discovered only during final parsing (i.e. after the streaming
// callback finished). The caller forwards every returned chunk to the
// responses channel.
//
// Guarantees:
//   - tool calls with i < lastEmittedCount are skipped (already streamed)
//   - each emitted call yields two chunks: name-only, then args-only
//   - no chunk ever carries both non-empty Content and non-empty ToolCalls
//   - no chunk ever carries both non-empty Reasoning and non-empty ToolCalls
//   - if !reasoningAlreadyStreamed && reasoningContent != "",
//     a reasoning chunk is emitted first
//   - if !contentAlreadyStreamed && textContent != "",
//     a role chunk followed by a content chunk is emitted (after reasoning)
//   - chunks order: [reasoning?] [role+content?] (name, args)+
//   - fallback IDs for empty ss.ID are unique per index so a client can
//     match tool_result messages back to the right call
func buildDeferredToolCallChunks(
	id, model string,
	created int,
	functionResults []functions.FuncCallResults,
	lastEmittedCount int,
	contentAlreadyStreamed bool,
	textContent string,
	reasoningAlreadyStreamed bool,
	reasoningContent string,
) []schema.OpenAIResponse {
	// If every call was already emitted incrementally there's nothing to
	// flush — and no reason to emit a standalone reasoning/content chunk.
	hasDeferred := false
	for i := range functionResults {
		if i >= lastEmittedCount {
			hasDeferred = true
			break
		}
	}
	if !hasDeferred {
		return nil
	}

	var out []schema.OpenAIResponse

	// Reasoning first — the callback path at processTools emits reasoning
	// incrementally in its own chunks, but when the C++ autoparser only
	// surfaces reasoning as a final aggregate the callback never sees it.
	// Recover it here (no duplication: contentAlreadyStreamed and
	// reasoningAlreadyStreamed track what the callback already sent).
	if !reasoningAlreadyStreamed && reasoningContent != "" {
		r := reasoningContent
		out = append(out, schema.OpenAIResponse{
			ID: id, Created: created, Model: model,
			Choices: []schema.Choice{{
				Delta: &schema.Message{Reasoning: &r},
				Index: 0,
			}},
			Object: "chat.completion.chunk",
		})
	}

	// Then content, when it wasn't streamed via the callback. Emit role
	// and content in separate deltas — the OpenAI streaming contract
	// forbids bundling content alongside tool_calls in one delta.
	if !contentAlreadyStreamed && textContent != "" {
		out = append(out, schema.OpenAIResponse{
			ID: id, Created: created, Model: model,
			Choices: []schema.Choice{{
				Delta: &schema.Message{Role: "assistant"},
				Index: 0,
			}},
			Object: "chat.completion.chunk",
		})
		c := textContent
		out = append(out, schema.OpenAIResponse{
			ID: id, Created: created, Model: model,
			Choices: []schema.Choice{{
				Delta: &schema.Message{Content: &c},
				Index: 0,
			}},
			Object: "chat.completion.chunk",
		})
	}

	for i, ss := range functionResults {
		if i < lastEmittedCount {
			// Already streamed by the incremental JSON/XML parser during
			// the token callback — skip to avoid a duplicate emission.
			continue
		}

		toolCallID := ss.ID
		if toolCallID == "" {
			// Unique per-index fallback so multiple empty-ID calls don't
			// collide on the same request ID (clients match tool results
			// back by tool_call_id).
			toolCallID = fmt.Sprintf("%s-%d", id, i)
		}

		// Name chunk.
		out = append(out, schema.OpenAIResponse{
			ID: id, Created: created, Model: model,
			Choices: []schema.Choice{{
				Delta: &schema.Message{
					Role: "assistant",
					ToolCalls: []schema.ToolCall{{
						Index: i,
						ID:    toolCallID,
						Type:  "function",
						FunctionCall: schema.FunctionCall{
							Name: ss.Name,
						},
					}},
				},
				Index:        0,
				FinishReason: nil,
			}},
			Object: "chat.completion.chunk",
		})

		// Args chunk — no Content here. Either it was streamed through
		// the token callback earlier, or the role+content pair above
		// already delivered it.
		out = append(out, schema.OpenAIResponse{
			ID: id, Created: created, Model: model,
			Choices: []schema.Choice{{
				Delta: &schema.Message{
					Role: "assistant",
					ToolCalls: []schema.ToolCall{{
						Index: i,
						ID:    toolCallID,
						Type:  "function",
						FunctionCall: schema.FunctionCall{
							Arguments: ss.Arguments,
						},
					}},
				},
				Index:        0,
				FinishReason: nil,
			}},
			Object: "chat.completion.chunk",
		})
	}

	return out
}
