package openai

import (
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/functions"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/xlog"
)

// nonStreamToolCalls is what the non-streaming chat path takes from one
// inference: the tool calls to run, the content and reasoning to send, and
// the raw text (cleaned when it was parsed) that handleQuestion falls back
// to when no call is left.
type nonStreamToolCalls struct {
	calls     []functions.FuncCallResults
	content   string
	reasoning string
	raw       string
}

// resolveNonStreamToolCalls picks the tool calls of a non-streaming chat
// completion: the C++ autoparser's (ChatDeltas) when it returned any, else
// the ones Go-side parsing finds in raw.
//
// Unless LocalAI sent its own grammar, calls from either source are checked
// against the declared functions (see functions.ValidatesToolCalls) and the
// ones that do not fit are dropped. When none remain, the caller answers
// with text: raw, which holds calls parsed from it, or for autoparser calls
// (whose source text is not in raw) the content followed by the calls.
func resolveNonStreamToolCalls(chatDeltas []*pb.ChatDelta, raw, reasoning string, cfg *config.ModelConfig, declared functions.Functions, noAction string) nonStreamToolCalls {
	out := nonStreamToolCalls{raw: raw, reasoning: reasoning}
	parsedFromText := false

	// Try pre-parsed tool calls from C++ autoparser first
	if deltaToolCalls := functions.ToolCallsFromChatDeltas(chatDeltas); len(deltaToolCalls) > 0 {
		xlog.Debug("[ChatDeltas] non-SSE: using C++ autoparser tool calls, skipping Go-side parsing", "count", len(deltaToolCalls))
		out.calls = deltaToolCalls
		out.content = functions.ContentFromChatDeltas(chatDeltas)
		out.reasoning = functions.ReasoningFromChatDeltas(chatDeltas)
	} else if deltaContent := functions.ContentFromChatDeltas(chatDeltas); len(chatDeltas) > 0 && deltaContent != "" {
		// ChatDeltas have content but no tool calls — model answered without using tools.
		// This happens with thinking models (e.g. Gemma 4) where the Go-side reasoning
		// extraction misclassifies clean content as reasoning, leaving raw empty.
		xlog.Debug("[ChatDeltas] non-SSE: using C++ autoparser content (no tool calls)", "content_len", len(deltaContent))
		out.content = deltaContent
		out.reasoning = functions.ReasoningFromChatDeltas(chatDeltas)
	} else {
		// Fallback: parse tool calls from raw text
		xlog.Debug("[ChatDeltas] non-SSE: no chat deltas, falling back to Go-side text parsing")
		out.content = functions.ParseTextContent(out.raw, cfg.FunctionsConfig)
		out.raw = functions.CleanupLLMResult(out.raw, cfg.FunctionsConfig)
		out.calls = functions.ParseFunctionCall(out.raw, cfg.FunctionsConfig)
		parsedFromText = true
	}

	// Content-based tool call fallback: if no tool calls were found,
	// try parsing the raw result — ParseFunctionCall handles detection internally.
	if len(out.calls) == 0 {
		contentFuncResults := functions.ParseFunctionCall(out.raw, cfg.FunctionsConfig)
		if len(contentFuncResults) > 0 {
			out.calls = contentFuncResults
			out.content = functions.StripToolCallMarkup(out.raw)
			parsedFromText = true
		}
	}

	if len(out.calls) > 0 && functions.ValidatesToolCalls(cfg.Grammar, cfg.FunctionsConfig, declared) {
		valid, dropped := functions.SplitFuncCalls(out.calls, declared, noAction)
		out.calls = valid
		if len(dropped) > 0 && !hasRealCall(valid, noAction) {
			if parsedFromText {
				// Empty content makes the caller answer with raw.
				out.content = ""
			} else {
				out.content = functions.AnswerText(out.content, dropped, true)
			}
		}
	}
	return out
}
