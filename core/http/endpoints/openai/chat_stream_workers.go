package openai

import (
	"encoding/json"

	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/functions"
	"github.com/mudler/LocalAI/pkg/model"
	reason "github.com/mudler/LocalAI/pkg/reasoning"
	"github.com/mudler/xlog"
)

// emitJSONToolCallDeltas iterates the JSON tool-call objects produced by the
// streaming tool-call detector and emits SSE chunks for the ones the caller
// hasn't already emitted. It returns the new lastEmittedCount.
//
// Semantics:
//   - Skips entries before lastEmittedCount (already emitted).
//   - Emits one tool_call chunk per consecutive entry that has a usable
//     `name` string.
//   - Stops at the first entry without a name (typically the partial-JSON
//     tail or a healing-marker stub — see issue #9988) so the caller doesn't
//     advance past it. Bumping lastEmittedCount past an unparsed stub
//     permanently gates off content emission for the rest of the stream.
//   - When jsonResults is empty (the autoparser-working case, where the raw
//     text result is cleared and only ChatDeltas carry tool calls), this is
//     a no-op and lastEmittedCount is returned unchanged.
//
// The autoparser-correctly-classifying-tool-calls path is unaffected: it
// delivers tool calls via TokenUsage.ChatDeltas, and the deferred
// end-of-stream block (ToolCallsFromChatDeltas → buildDeferredToolCallChunks)
// emits them; this helper sees an empty jsonResults and emits nothing.
func emitJSONToolCallDeltas(
	jsonResults []map[string]any,
	lastEmittedCount int,
	id, model string,
	created int,
	responses chan<- schema.OpenAIResponse,
) int {
	for i := lastEmittedCount; i < len(jsonResults); i++ {
		jsonObj := jsonResults[i]
		name, ok := jsonObj["name"].(string)
		if !ok || name == "" {
			break
		}
		args := "{}"
		if argsVal, ok := jsonObj["arguments"]; ok {
			if argsStr, ok := argsVal.(string); ok {
				args = argsStr
			} else {
				argsBytes, _ := json.Marshal(argsVal)
				args = string(argsBytes)
			}
		}
		responses <- schema.OpenAIResponse{
			ID:      id,
			Created: created,
			Model:   model,
			Choices: []schema.Choice{{
				Delta: &schema.Message{
					Role: "assistant",
					ToolCalls: []schema.ToolCall{
						{
							Index: i,
							ID:    id,
							Type:  "function",
							FunctionCall: schema.FunctionCall{
								Name:      name,
								Arguments: args,
							},
						},
					},
				},
				Index:        0,
				FinishReason: nil,
			}},
			Object: "chat.completion.chunk",
		}
		lastEmittedCount = i + 1
	}
	return lastEmittedCount
}

// processStream is the streaming worker for chat completions with no
// tool/function calling involved. It pushes SSE-shaped chunks onto
// `responses` and returns the authoritative cumulative TokenUsage from
// the prediction so the caller can populate the include_usage trailer
// without having to peek inside the chunks.
//
// The caller owns the `responses` channel and is expected to read from
// it while this function runs; processStream closes the channel before
// returning.
//
// X-LocalAI-Node attribution (when --expose-node-header is on) is
// handled by middleware.ExposeNodeHeader at the response writer wrapper
// layer; no in-band signal from the worker is needed. The initial
// role=assistant chunk is still emitted from the first token callback
// rather than eagerly here, so the wrapper's lazy lookup against the
// loader runs AFTER ml.Load has stamped the per-modelID node ID.
func processStream(
	s string,
	req *schema.OpenAIRequest,
	cfg *config.ModelConfig,
	cl *config.ModelConfigLoader,
	startupOptions *config.ApplicationConfig,
	loader *model.ModelLoader,
	responses chan schema.OpenAIResponse,
	id string,
	created int,
) (backend.TokenUsage, error) {
	sentInitialRole := false

	// Detect if thinking token is already in prompt or template
	// When UseTokenizerTemplate is enabled, predInput is empty, so we check the template
	var template string
	if cfg.TemplateConfig.UseTokenizerTemplate {
		template = cfg.GetModelTemplate()
	} else {
		template = s
	}
	thinkingStartToken := reason.DetectThinkingStartToken(template, &cfg.ReasoningConfig)
	extractor := reason.NewReasoningExtractor(thinkingStartToken, cfg.ReasoningConfig)

	// preferAutoparser is sticky: once the C++ autoparser has ever classified
	// reasoning_content, we trust it for the rest of the stream. Until then we
	// fall back to Go-side extraction so that a "pure content" autoparser
	// (non-jinja path, issue #9985) does not leak <think>…</think> tokens
	// straight into the OpenAI `content` field.
	preferAutoparser := false

	_, finalUsage, _, err := ComputeChoices(req, s, cfg, cl, startupOptions, loader, func(s string, c *[]schema.Choice) {}, func(s string, tokenUsage backend.TokenUsage) bool {
		var reasoningDelta, contentDelta string

		// Always keep the Go-side extractor in sync with raw tokens so it
		// can serve as fallback for backends without an autoparser (e.g. vLLM).
		goReasoning, goContent := extractor.ProcessToken(s)

		// When C++ autoparser chat deltas are available, prefer them: they
		// handle model-specific formats (Gemma 4, etc.) without Go-side tags.
		// Otherwise fall back to Go-side extraction.
		if tokenUsage.HasChatDeltaContent() {
			rawReasoning, cd := tokenUsage.ChatDeltaReasoningAndContent()
			if rawReasoning != "" {
				preferAutoparser = true
			}
			if preferAutoparser {
				contentDelta = cd
				reasoningDelta = extractor.ProcessChatDeltaReasoning(rawReasoning)
			} else {
				reasoningDelta = goReasoning
				contentDelta = goContent
			}
		} else {
			reasoningDelta = goReasoning
			contentDelta = goContent
		}

		if !sentInitialRole {
			sentInitialRole = true
			responses <- schema.OpenAIResponse{
				ID:      id,
				Created: created,
				Model:   req.Model, // we have to return what the user sent here, due to OpenAI spec.
				Choices: []schema.Choice{{Delta: &schema.Message{Role: "assistant"}, Index: 0, FinishReason: nil}},
				Object:  "chat.completion.chunk",
			}
		}

		delta := &schema.Message{}
		if contentDelta != "" {
			delta.Content = &contentDelta
		}
		if reasoningDelta != "" {
			delta.Reasoning = &reasoningDelta
		}

		responses <- schema.OpenAIResponse{
			ID:      id,
			Created: created,
			Model:   req.Model, // we have to return what the user sent here, due to OpenAI spec.
			Choices: []schema.Choice{{Delta: delta, Index: 0, FinishReason: nil}},
			Object:  "chat.completion.chunk",
		}
		return true
	})
	close(responses)
	return finalUsage, err
}

// processStreamWithTools is the streaming worker for chat completions
// with tools / function calling. Same contract as processStream: pushes
// chunks onto `responses`, closes the channel, returns the cumulative
// TokenUsage.
//
// Returning the TokenUsage as a normal Go value (rather than smuggling
// it on a sentinel chunk) is the fix for issue #9927 — the previous
// implementation discarded the value from ComputeChoices, so the
// include_usage trailer reported zeros whenever `tools` was in play.
func processStreamWithTools(
	noAction string,
	prompt string,
	req *schema.OpenAIRequest,
	cfg *config.ModelConfig,
	cl *config.ModelConfigLoader,
	startupOptions *config.ApplicationConfig,
	loader *model.ModelLoader,
	responses chan schema.OpenAIResponse,
	id string,
	created int,
	textContentToReturn *string,
) (backend.TokenUsage, error) {
	// Detect if thinking token is already in prompt or template
	var template string
	if cfg.TemplateConfig.UseTokenizerTemplate {
		template = cfg.GetModelTemplate()
	} else {
		template = prompt
	}
	thinkingStartToken := reason.DetectThinkingStartToken(template, &cfg.ReasoningConfig)
	extractor := reason.NewReasoningExtractor(thinkingStartToken, cfg.ReasoningConfig)

	result := ""
	lastEmittedCount := 0
	sentInitialRole := false
	sentReasoning := false
	hasChatDeltaToolCalls := false
	hasChatDeltaContent := false

	// X-LocalAI-Node attribution is handled by middleware.ExposeNodeHeader
	// at the wrapper layer; no in-band signalling from this worker.

	_, finalUsage, chatDeltas, err := ComputeChoices(req, prompt, cfg, cl, startupOptions, loader, func(s string, c *[]schema.Choice) {}, func(s string, usage backend.TokenUsage) bool {
		result += s

		// Track whether ChatDeltas from the C++ autoparser contain
		// tool calls or content, so the retry decision can account for them.
		for _, d := range usage.ChatDeltas {
			if len(d.ToolCalls) > 0 {
				hasChatDeltaToolCalls = true
			}
			if d.Content != "" {
				hasChatDeltaContent = true
			}
		}

		var reasoningDelta, contentDelta string

		goReasoning, goContent := extractor.ProcessToken(s)

		if usage.HasChatDeltaContent() {
			rawReasoning, cd := usage.ChatDeltaReasoningAndContent()
			contentDelta = cd
			reasoningDelta = extractor.ProcessChatDeltaReasoning(rawReasoning)
		} else {
			reasoningDelta = goReasoning
			contentDelta = goContent
		}

		// Emit reasoning deltas in their own SSE chunks before any tool-call chunks
		// (OpenAI spec: reasoning and tool_calls never share a delta)
		if reasoningDelta != "" {
			responses <- schema.OpenAIResponse{
				ID:      id,
				Created: created,
				Model:   req.Model,
				Choices: []schema.Choice{{
					Delta: &schema.Message{Reasoning: &reasoningDelta},
					Index: 0,
				}},
				Object: "chat.completion.chunk",
			}
			sentReasoning = true
		}

		// Stream content deltas (cleaned of reasoning tags) while no tool calls
		// have been detected. Once the incremental parser finds tool calls,
		// content stops: per OpenAI spec, content and tool_calls don't mix.
		if lastEmittedCount == 0 && contentDelta != "" {
			if !sentInitialRole {
				responses <- schema.OpenAIResponse{
					ID: id, Created: created, Model: req.Model,
					Choices: []schema.Choice{{Delta: &schema.Message{Role: "assistant"}, Index: 0}},
					Object:  "chat.completion.chunk",
				}
				sentInitialRole = true
			}
			responses <- schema.OpenAIResponse{
				ID: id, Created: created, Model: req.Model,
				Choices: []schema.Choice{{
					Delta: &schema.Message{Content: &contentDelta},
					Index: 0,
				}},
				Object: "chat.completion.chunk",
			}
		}

		// Try incremental XML parsing for streaming support using iterative parser
		// This allows emitting partial tool calls as they're being generated
		cleanedResult := functions.CleanupLLMResult(result, cfg.FunctionsConfig)

		// Determine XML format from config
		var xmlFormat *functions.XMLToolCallFormat
		if cfg.FunctionsConfig.XMLFormat != nil {
			xmlFormat = cfg.FunctionsConfig.XMLFormat
		} else if cfg.FunctionsConfig.XMLFormatPreset != "" {
			xmlFormat = functions.GetXMLFormatPreset(cfg.FunctionsConfig.XMLFormatPreset)
		}

		// Use iterative parser for streaming (partial parsing enabled)
		// Try XML parsing first
		partialResults, parseErr := functions.ParseXMLIterative(cleanedResult, xmlFormat, true)
		if parseErr == nil && len(partialResults) > 0 {
			// Emit new XML tool calls that weren't emitted before
			if len(partialResults) > lastEmittedCount {
				for i := lastEmittedCount; i < len(partialResults); i++ {
					toolCall := partialResults[i]
					initialMessage := schema.OpenAIResponse{
						ID:      id,
						Created: created,
						Model:   req.Model,
						Choices: []schema.Choice{{
							Delta: &schema.Message{
								Role: "assistant",
								ToolCalls: []schema.ToolCall{
									{
										Index: i,
										ID:    id,
										Type:  "function",
										FunctionCall: schema.FunctionCall{
											Name: toolCall.Name,
										},
									},
								},
							},
							Index:        0,
							FinishReason: nil,
						}},
						Object: "chat.completion.chunk",
					}
					select {
					case responses <- initialMessage:
					default:
					}
				}
				lastEmittedCount = len(partialResults)
			}
		} else {
			// Try JSON tool call parsing for streaming.
			// Only emit NEW tool calls (same guard as XML parser above).
			jsonResults, jsonErr := functions.ParseJSONIterative(cleanedResult, true)
			if jsonErr == nil {
				lastEmittedCount = emitJSONToolCallDeltas(
					jsonResults, lastEmittedCount, id, req.Model, created, responses,
				)
			}
		}
		return true
	},
		func(attempt int) bool {
			// After streaming completes: check if we got actionable content
			cleaned := extractor.CleanedContent()
			// Check for tool calls from chat deltas (will be re-checked after ComputeChoices,
			// but we need to know here whether to retry).
			// Also check ChatDelta flags: when the C++ autoparser is active,
			// tool calls and content are delivered via ChatDeltas while the
			// raw message is cleared. Without this check, we'd retry
			// unnecessarily, losing valid results and concatenating output.
			hasToolCalls := lastEmittedCount > 0 || hasChatDeltaToolCalls
			hasContent := cleaned != "" || hasChatDeltaContent
			if !hasContent && !hasToolCalls {
				xlog.Warn("Streaming: backend produced only reasoning, retrying",
					"reasoning_len", len(extractor.Reasoning()), "attempt", attempt+1)
				extractor.ResetAndSuppressReasoning()
				result = ""
				lastEmittedCount = 0
				sentInitialRole = false
				hasChatDeltaToolCalls = false
				hasChatDeltaContent = false
				return true
			}
			return false
		},
	)
	if err != nil {
		return finalUsage, err
	}
	// Try using pre-parsed tool calls from C++ autoparser (chat deltas)
	var functionResults []functions.FuncCallResults
	var reasoning string

	if deltaToolCalls := functions.ToolCallsFromChatDeltas(chatDeltas); len(deltaToolCalls) > 0 {
		xlog.Debug("[ChatDeltas] Using pre-parsed tool calls from C++ autoparser", "count", len(deltaToolCalls))
		functionResults = deltaToolCalls
		// Use content/reasoning from deltas too
		*textContentToReturn = functions.ContentFromChatDeltas(chatDeltas)
		reasoning = functions.ReasoningFromChatDeltas(chatDeltas)
	} else {
		// Fallback: parse tool calls from raw text (no chat deltas from backend)
		xlog.Debug("[ChatDeltas] no pre-parsed tool calls, falling back to Go-side text parsing")
		reasoning = extractor.Reasoning()
		cleanedResult := extractor.CleanedContent()
		*textContentToReturn = functions.ParseTextContent(cleanedResult, cfg.FunctionsConfig)
		cleanedResult = functions.CleanupLLMResult(cleanedResult, cfg.FunctionsConfig)
		functionResults = functions.ParseFunctionCall(cleanedResult, cfg.FunctionsConfig)
	}
	xlog.Debug("[ChatDeltas] final tool call decision", "tool_calls", len(functionResults), "text_content", *textContentToReturn)
	// noAction is a sentinel "just answer" pseudo-function: not a real
	// tool call. Scan the whole slice rather than only index 0 so we
	// don't drop a real tool call that happens to follow a noAction
	// entry, and so the default branch isn't entered with only noAction
	// entries to emit as tool_calls.
	noActionToRun := !hasRealCall(functionResults, noAction)

	switch {
	case noActionToRun:
		// The final usage trailer (when the caller opted in with
		// stream_options.include_usage) is built by the outer streaming
		// loop from the TokenUsage this function returns, not from any
		// chunk on the responses channel.
		var result string
		if !sentInitialRole {
			var hqErr error
			result, hqErr = handleQuestion(cfg, functionResults, extractor.CleanedContent(), prompt)
			if hqErr != nil {
				xlog.Error("error handling question", "error", hqErr)
				return finalUsage, hqErr
			}
		}
		for _, chunk := range buildNoActionFinalChunks(
			id, req.Model, created,
			sentInitialRole, sentReasoning,
			result, reasoning,
		) {
			responses <- chunk
		}

	default:
		for _, chunk := range buildDeferredToolCallChunks(
			id, req.Model, created,
			functionResults, lastEmittedCount,
			sentInitialRole, *textContentToReturn,
			sentReasoning, reasoning,
		) {
			responses <- chunk
		}
	}

	close(responses)
	return finalUsage, err
}
