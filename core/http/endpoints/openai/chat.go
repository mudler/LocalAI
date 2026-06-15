package openai

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	mcpTools "github.com/mudler/LocalAI/core/http/endpoints/mcp"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/cloudproxy"
	"github.com/mudler/LocalAI/core/services/routing/pii"
	"github.com/mudler/LocalAI/pkg/functions"
	reason "github.com/mudler/LocalAI/pkg/reasoning"

	"github.com/mudler/LocalAI/core/templates"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/model"

	"github.com/mudler/xlog"
)

// hasSystemMessage reports whether the message slice already contains a
// system-role message — used to avoid clobbering a caller-supplied system
// prompt when the LocalAI Assistant modality is on.
func hasSystemMessage(messages []schema.Message) bool {
	for _, m := range messages {
		if m.Role == "system" {
			return true
		}
	}
	return false
}

// mergeToolCallDeltas merges streaming tool call deltas into complete tool calls.
// In SSE streaming, a single tool call arrives as multiple chunks sharing the same Index:
// the first chunk carries the ID, Type, and Name; subsequent chunks append to Arguments.
func mergeToolCallDeltas(existing []schema.ToolCall, deltas []schema.ToolCall) []schema.ToolCall {
	byIndex := make(map[int]int, len(existing)) // tool call Index -> position in slice
	for i, tc := range existing {
		byIndex[tc.Index] = i
	}
	for _, d := range deltas {
		pos, found := byIndex[d.Index]
		if !found {
			byIndex[d.Index] = len(existing)
			existing = append(existing, d)
			continue
		}
		// Merge into existing entry
		tc := &existing[pos]
		if d.ID != "" {
			tc.ID = d.ID
		}
		if d.Type != "" {
			tc.Type = d.Type
		}
		if d.FunctionCall.Name != "" {
			tc.FunctionCall.Name = d.FunctionCall.Name
		}
		tc.FunctionCall.Arguments += d.FunctionCall.Arguments
	}
	return existing
}

// applyAutoparserOverride replaces the Go-side reasoning-extraction result with
// the C++ autoparser's classified ChatDeltas when those deltas contain
// actionable content or reasoning. It preserves the original logprobs.
//
// When the autoparser did not classify any reasoning (deltaReasoning == "") but
// deltaContent still carries an unparsed reasoning tag pair (e.g. the
// non-jinja "pure content" fallback path on a <think> model — issue #9985),
// the Go-side reasoning extractor is run on deltaContent as a defensive
// fallback so <think>…</think> blocks do not leak into the OpenAI `content`
// field.
func applyAutoparserOverride(
	chatDeltas []*pb.ChatDelta,
	thinkingStartToken string,
	reasoningConfig reason.Config,
	existing []schema.Choice,
) []schema.Choice {
	if len(chatDeltas) == 0 {
		return existing
	}
	deltaContent := functions.ContentFromChatDeltas(chatDeltas)
	deltaReasoning := functions.ReasoningFromChatDeltas(chatDeltas)
	if deltaContent == "" && deltaReasoning == "" {
		return existing
	}
	// Fallback for non-jinja models (issue #9985): when the C++ autoparser
	// did not classify reasoning but the raw content still contains a known
	// reasoning tag pair, run Go-side extraction on the content so that the
	// <think>…</think> block does not leak into the OpenAI `content` field.
	// When the autoparser DID populate ReasoningContent, leave its
	// content/reasoning split alone — trust the parser. We replace
	// deltaContent unconditionally because ExtractReasoningWithConfig is a
	// no-op when no tag pair matches; this also strips empty thinking
	// blocks like "<think></think>" that some models emit when reasoning
	// is disabled.
	if deltaReasoning == "" && deltaContent != "" {
		// Complete-response extraction: only honor a prefilled <think> start
		// token when deltaContent actually closes the reasoning block. Without
		// it the model answered directly and the whole answer must stay in
		// content rather than be swallowed as unclosed reasoning. See
		// reason.ExtractReasoningComplete.
		deltaReasoning, deltaContent = reason.ExtractReasoningComplete(deltaContent, thinkingStartToken, reasoningConfig)
	}
	xlog.Debug("[ChatDeltas] non-SSE no-tools: overriding result with C++ autoparser deltas",
		"content_len", len(deltaContent), "reasoning_len", len(deltaReasoning))
	stopReason := FinishReasonStop
	message := &schema.Message{Role: "assistant", Content: &deltaContent}
	if deltaReasoning != "" {
		message.Reasoning = &deltaReasoning
	}
	newChoice := schema.Choice{FinishReason: &stopReason, Index: 0, Message: message}
	if len(existing) > 0 && existing[0].Logprobs != nil {
		newChoice.Logprobs = existing[0].Logprobs
	}
	return []schema.Choice{newChoice}
}

// ChatEndpoint is the OpenAI Completion API endpoint https://platform.openai.com/docs/api-reference/chat/create
// @Summary Generate a chat completions for a given prompt and model.
// @Tags inference
// @Param request body schema.OpenAIRequest true "query params"
// @Success 200 {object} schema.OpenAIResponse "Response"
// @Router /v1/chat/completions [post]
func ChatEndpoint(cl *config.ModelConfigLoader, ml *model.ModelLoader, evaluator *templates.Evaluator, startupOptions *config.ApplicationConfig, natsClient mcpTools.MCPNATSClient, assistantHolder *mcpTools.LocalAIAssistantHolder, piiRedactor *pii.Redactor, piiEvents pii.EventStore) echo.HandlerFunc {
	return func(c echo.Context) error {
		var textContentToReturn string
		id := uuid.New().String()
		created := int(time.Now().Unix())

		input, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.OpenAIRequest)
		if !ok || input.Model == "" {
			return echo.ErrBadRequest
		}

		extraUsage := c.Request().Header.Get("Extra-Usage") != ""

		config, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig)
		if !ok || config == nil {
			return echo.ErrBadRequest
		}

		xlog.Debug("Chat endpoint configuration read", "config", config)

		// Cloud-proxy bail. Bypasses the local pipeline (templating,
		// MCP injection, gRPC backend) and forwards via the cloud-
		// proxy backend, which does the outbound HTTP. The streaming
		// PII filter still runs because its input is per-token text
		// extracted from the wire envelope, not the envelope itself.
		if config.IsCloudProxyBackendPassthrough() {
			return forwardCloudProxyOpenAIViaBackend(c, config, input, piiRedactor, piiEvents, ml, startupOptions)
		}

		funcs := input.Functions
		shouldUseFn := len(input.Functions) > 0 && config.ShouldUseFunctions()
		strictMode := false

		// MCP tool injection: when mcp_servers is set in metadata and model has MCP config
		var mcpExecutor mcpTools.ToolExecutor
		mcpServers := mcpTools.MCPServersFromMetadata(input.Metadata)

		// LocalAI Assistant modality: an admin opted into the in-process MCP
		// admin tool surface. Runs *before* the regular MCP block — when both
		// are set, the assistant tools win (the admin cannot mix them with
		// per-model MCP servers in the same chat session by design).
		assistantMode := mcpTools.LocalAIAssistantFromMetadata(input.Metadata)
		if assistantMode {
			if err := requireAssistantAccess(c, startupOptions.Auth.Enabled); err != nil {
				return err
			}
			// Read the disable flag live: an admin can flip it via /api/settings
			// and the next request must see the change without a restart.
			if startupOptions.DisableLocalAIAssistant {
				return echo.NewHTTPError(http.StatusServiceUnavailable, "LocalAI Assistant is disabled on this server")
			}
			if assistantHolder == nil || !assistantHolder.HasTools() {
				return echo.NewHTTPError(http.StatusServiceUnavailable, "LocalAI Assistant is not available on this server")
			}
			mcpExecutor = assistantHolder.Executor()
			mcpFuncs, discErr := mcpExecutor.DiscoverTools(c.Request().Context())
			if discErr != nil {
				xlog.Error("Failed to discover LocalAI Assistant tools", "error", discErr)
				return echo.NewHTTPError(http.StatusInternalServerError, "discover assistant tools: "+discErr.Error())
			}
			for _, fn := range mcpFuncs {
				funcs = append(funcs, fn)
				input.Tools = append(input.Tools, functions.Tool{Type: "function", Function: fn})
			}
			shouldUseFn = len(funcs) > 0 && config.ShouldUseFunctions()

			// Prepend the embedded system prompt unless the caller supplied
			// their own system message. Why: the prompt is what teaches the
			// model the safety rules and recipes. If a caller already has a
			// system message they're responsible for keeping the assistant
			// safe, so we leave it alone.
			if !hasSystemMessage(input.Messages) {
				input.Messages = append([]schema.Message{{Role: "system", StringContent: assistantHolder.SystemPrompt()}}, input.Messages...)
			}

			xlog.Debug("LocalAI Assistant tools injected", "count", len(mcpFuncs))
		}

		// MCP prompt and resource injection (extracted before tool injection)
		mcpPromptName, mcpPromptArgs := mcpTools.MCPPromptFromMetadata(input.Metadata)
		mcpResourceURIs := mcpTools.MCPResourcesFromMetadata(input.Metadata)

		if (len(mcpServers) > 0 || mcpPromptName != "" || len(mcpResourceURIs) > 0) && (config.MCP.Servers != "" || config.MCP.Stdio != "") {
			remote, stdio, mcpErr := config.MCP.MCPConfigFromYAML()
			if mcpErr == nil {
				mcpExecutor = mcpTools.NewToolExecutor(c.Request().Context(), natsClient, config.Name, remote, stdio, mcpServers)

				// Prompt and resource injection (pre-processing step — resolves locally regardless of distributed mode)
				namedSessions, sessErr := mcpTools.NamedSessionsFromMCPConfig(config.Name, remote, stdio, mcpServers)
				if sessErr == nil && len(namedSessions) > 0 {
					mcpCtx, _ := mcpTools.InjectMCPContext(c.Request().Context(), namedSessions, mcpPromptName, mcpPromptArgs, mcpResourceURIs)
					if mcpCtx != nil {
						input.Messages = append(mcpCtx.PromptMessages, input.Messages...)
						mcpTools.AppendResourceSuffix(input.Messages, mcpCtx.ResourceSuffix)
					}
				}

				// Tool injection via executor
				if mcpExecutor.HasTools() {
					mcpFuncs, discErr := mcpExecutor.DiscoverTools(c.Request().Context())
					if discErr == nil {
						for _, fn := range mcpFuncs {
							funcs = append(funcs, fn)
							input.Tools = append(input.Tools, functions.Tool{Type: "function", Function: fn})
						}
						shouldUseFn = len(funcs) > 0 && config.ShouldUseFunctions()
						xlog.Debug("MCP tools injected", "count", len(mcpFuncs), "total_funcs", len(funcs))
					} else {
						xlog.Error("Failed to discover MCP tools", "error", discErr)
					}
				}
			} else {
				xlog.Error("Failed to parse MCP config", "error", mcpErr)
			}
		}

		xlog.Debug("Tool call routing decision",
			"shouldUseFn", shouldUseFn,
			"len(input.Functions)", len(input.Functions),
			"len(input.Tools)", len(input.Tools),
			"config.ShouldUseFunctions()", config.ShouldUseFunctions(),
			"config.FunctionToCall()", config.FunctionToCall(),
		)

		for _, f := range input.Functions {
			if f.Strict {
				strictMode = true
				break
			}
		}

		// Allow the user to set custom actions via config file
		// to be "embedded" in each model
		noActionName := "answer"
		noActionDescription := "use this action to answer without performing any action"

		if config.FunctionsConfig.NoActionFunctionName != "" {
			noActionName = config.FunctionsConfig.NoActionFunctionName
		}
		if config.FunctionsConfig.NoActionDescriptionName != "" {
			noActionDescription = config.FunctionsConfig.NoActionDescriptionName
		}

		// If we are using a response format, we need to generate a grammar for it
		if config.ResponseFormatMap != nil {
			d := schema.ChatCompletionResponseFormat{}
			dat, err := json.Marshal(config.ResponseFormatMap)
			if err != nil {
				return err
			}
			err = json.Unmarshal(dat, &d)
			if err != nil {
				return err
			}

			switch d.Type {
			case "json_object":
				input.Grammar = functions.JSONBNF
			case "json_schema":
				d := schema.JsonSchemaRequest{}
				dat, err := json.Marshal(config.ResponseFormatMap)
				if err != nil {
					return err
				}
				err = json.Unmarshal(dat, &d)
				if err != nil {
					return err
				}
				fs := &functions.JSONFunctionStructure{
					AnyOf: []functions.Item{d.JsonSchema.Schema},
				}
				g, err := fs.Grammar(config.FunctionsConfig.GrammarOptions()...)
				if err == nil {
					input.Grammar = g
				} else {
					xlog.Error("Failed generating grammar", "error", err)
				}
			}
		}

		config.Grammar = input.Grammar

		if shouldUseFn {
			xlog.Debug("Response needs to process functions")
		}

		switch {
		// Generates grammar with internal's LocalAI engine
		case (!config.FunctionsConfig.GrammarConfig.NoGrammar || strictMode) && shouldUseFn:
			noActionGrammar := functions.Function{
				Name:        noActionName,
				Description: noActionDescription,
				Parameters: map[string]any{
					"properties": map[string]any{
						"message": map[string]any{
							"type":        "string",
							"description": "The message to reply the user with",
						}},
				},
			}

			// Append the no action function
			if !config.FunctionsConfig.DisableNoAction && !strictMode {
				funcs = append(funcs, noActionGrammar)
			}

			// Force picking one of the functions by the request
			if config.FunctionToCall() != "" {
				funcs = funcs.Select(config.FunctionToCall())
			}

			// Update input grammar or json_schema based on use_llama_grammar option
			jsStruct := funcs.ToJSONStructure(config.FunctionsConfig.FunctionNameKey, config.FunctionsConfig.FunctionNameKey)
			g, err := jsStruct.Grammar(config.FunctionsConfig.GrammarOptions()...)
			if err == nil {
				config.Grammar = g
			} else {
				xlog.Error("Failed generating grammar", "error", err)
			}
		case input.JSONFunctionGrammarObject != nil:
			g, err := input.JSONFunctionGrammarObject.Grammar(config.FunctionsConfig.GrammarOptions()...)
			if err == nil {
				config.Grammar = g
			} else {
				xlog.Error("Failed generating grammar", "error", err)
			}

		default:
			// Force picking one of the functions by the request
			if config.FunctionToCall() != "" {
				funcs = funcs.Select(config.FunctionToCall())
			}
		}

		// process functions if we have any defined or if we have a function call string

		// functions are not supported in stream mode (yet?)
		toStream := input.Stream

		xlog.Debug("Parameters", "config", config)

		var predInput string

		// If we are using the tokenizer template, we don't need to process the messages
		// unless we are processing functions
		if !config.TemplateConfig.UseTokenizerTemplate {
			predInput = evaluator.TemplateMessages(*input, input.Messages, config, funcs, shouldUseFn)

			xlog.Debug("Prompt (after templating)", "prompt", predInput)
			if config.Grammar != "" {
				xlog.Debug("Grammar", "grammar", config.Grammar)
			}
		}

		switch {
		case toStream:

			xlog.Debug("Stream request received")
			c.Response().Header().Set("Content-Type", "text/event-stream")
			c.Response().Header().Set("Cache-Control", "no-cache")
			c.Response().Header().Set("Connection", "keep-alive")
			c.Response().Header().Set("X-Correlation-ID", id)

			// Per-stream PII filter: when the resolved model has PII
			// enabled, wrap the response content so values spanning
			// chunk boundaries still get masked. Shared with the
			// cloud-proxy bail below via cloudproxy.BuildStreamFilter
			// so both paths apply the same per-model gate and override
			// rules.
			streamPIIFilter := cloudproxy.BuildStreamFilter(c, config, true, piiRedactor, piiEvents, id)

			mcpStreamMaxIterations := 10
			if config.Agent.MaxIterations > 0 {
				mcpStreamMaxIterations = config.Agent.MaxIterations
			}
			hasMCPToolsStream := mcpExecutor != nil && mcpExecutor.HasTools()

			for mcpStreamIter := 0; mcpStreamIter <= mcpStreamMaxIterations; mcpStreamIter++ {
				// Re-template on MCP iterations
				if mcpStreamIter > 0 && !config.TemplateConfig.UseTokenizerTemplate {
					predInput = evaluator.TemplateMessages(*input, input.Messages, config, funcs, shouldUseFn)
					xlog.Debug("MCP stream re-templating", "iteration", mcpStreamIter)
				}

				responses := make(chan schema.OpenAIResponse)
				ended := make(chan streamWorkerResult, 1)

				go func() {
					if !shouldUseFn {
						u, err := processStream(predInput, input, config, cl, startupOptions, ml, responses, id, created)
						ended <- streamWorkerResult{usage: u, err: err}
					} else {
						u, err := processStreamWithTools(noActionName, predInput, input, config, cl, startupOptions, ml, responses, id, created, &textContentToReturn)
						ended <- streamWorkerResult{usage: u, err: err}
					}
				}()

				var finalUsage backend.TokenUsage
				toolsCalled := false
				var collectedToolCalls []schema.ToolCall
				var collectedContent string

			LOOP:
				for {
					select {
					case <-input.Context.Done():
						// Context was cancelled (client disconnected or request cancelled)
						xlog.Debug("Request context cancelled, stopping stream")
						input.Cancel()
						break LOOP
					case ev := <-responses:
						if len(ev.Choices) == 0 {
							xlog.Debug("No choices in the response, skipping")
							continue
						}
						if len(ev.Choices[0].Delta.ToolCalls) > 0 {
							toolsCalled = true
							// Collect and merge tool call deltas for MCP execution
							if hasMCPToolsStream {
								collectedToolCalls = mergeToolCallDeltas(collectedToolCalls, ev.Choices[0].Delta.ToolCalls)
							}
						}
						// Extract the raw content delta string once per chunk;
						// both the MCP collector and the PII filter need it
						// and the type-switch is otherwise duplicated.
						var rawContent string
						haveContent := false
						if ev.Choices[0].Delta != nil && ev.Choices[0].Delta.Content != nil {
							switch v := ev.Choices[0].Delta.Content.(type) {
							case string:
								rawContent = v
								haveContent = true
							case *string:
								if v != nil {
									rawContent = *v
									haveContent = true
								}
							}
						}
						// Collect content for MCP conversation history and automatic tool parsing fallback.
						// We collect the RAW (unfiltered) content so the model's tool-call
						// markup keeps parsing correctly even when PII redaction would mask
						// substrings.
						if (hasMCPToolsStream || config.FunctionsConfig.AutomaticToolParsingFallback) && haveContent {
							collectedContent += rawContent
						}
						// Stream-side PII filter: feed the content delta
						// through the buffered-emit filter. The filter
						// holds back a tail to handle pattern boundaries
						// across chunks, so a Push may legitimately
						// return "" — drop the chunk in that case rather
						// than emitting an empty Delta to the wire.
						if streamPIIFilter != nil && haveContent {
							filtered := streamPIIFilter.Push(rawContent)
							if filtered == "" {
								// Fully buffered — skip this chunk's
								// content. Still emit non-content chunks
								// (role, tool_calls). When this delta is
								// content-only and we buffer it, drop the
								// whole event to avoid a vestigial
								// {"delta":{}} on the wire.
								if ev.Choices[0].Delta.Role == "" && len(ev.Choices[0].Delta.ToolCalls) == 0 && ev.Choices[0].Delta.Reasoning == nil {
									continue
								}
								// Mixed delta — strip content, keep the rest.
								ev.Choices[0].Delta.Content = nil
							} else {
								ev.Choices[0].Delta.Content = filtered
							}
						}
						respData, err := json.Marshal(ev)
						if err != nil {
							xlog.Debug("Failed to marshal response", "error", err)
							input.Cancel()
							continue
						}
						xlog.Debug("Sending chunk", "chunk", string(respData))
						_, err = fmt.Fprintf(c.Response().Writer, "data: %s\n\n", string(respData))
						if err != nil {
							xlog.Debug("Sending chunk failed", "error", err)
							input.Cancel()
							return err
						}
						c.Response().Flush()
					case res := <-ended:
						if res.err == nil {
							finalUsage = res.usage
							break LOOP
						}
						xlog.Error("Stream ended with error", "error", res.err)

						errorResp := schema.ErrorResponse{
							Error: &schema.APIError{
								Message: res.err.Error(),
								Type:    "server_error",
								Code:    "server_error",
							},
						}
						respData, marshalErr := json.Marshal(errorResp)
						if marshalErr != nil {
							xlog.Error("Failed to marshal error response", "error", marshalErr)
							fmt.Fprintf(c.Response().Writer, "data: {\"error\":{\"message\":\"Internal error\",\"type\":\"server_error\"}}\n\n")
						} else {
							fmt.Fprintf(c.Response().Writer, "data: %s\n\n", respData)
						}
						fmt.Fprintf(c.Response().Writer, "data: [DONE]\n\n")
						c.Response().Flush()

						return nil
					}
				}

				// Drain responses channel to unblock the background goroutine if it's
				// still trying to send (e.g., after client disconnect). The goroutine
				// calls close(responses) when done, which terminates the drain.
				if input.Context.Err() != nil {
					go func() {
						for range responses {
						}
					}()
					<-ended
				}

				// MCP streaming tool execution: if we collected MCP tool calls, execute and loop
				if hasMCPToolsStream && toolsCalled && len(collectedToolCalls) > 0 {
					var hasMCPCalls bool
					for _, tc := range collectedToolCalls {
						if mcpExecutor != nil && mcpExecutor.IsTool(tc.FunctionCall.Name) {
							hasMCPCalls = true
							break
						}
					}
					if hasMCPCalls {
						// Append assistant message with tool_calls
						assistantMsg := schema.Message{
							Role:      "assistant",
							Content:   collectedContent,
							ToolCalls: collectedToolCalls,
						}
						input.Messages = append(input.Messages, assistantMsg)

						// Execute MCP tool calls and stream results as tool_result events
						for _, tc := range collectedToolCalls {
							if mcpExecutor == nil || !mcpExecutor.IsTool(tc.FunctionCall.Name) {
								continue
							}
							xlog.Debug("Executing MCP tool (stream)", "tool", tc.FunctionCall.Name, "iteration", mcpStreamIter)
							toolResult, toolErr := mcpExecutor.ExecuteTool(c.Request().Context(), tc.FunctionCall.Name, tc.FunctionCall.Arguments)
							if toolErr != nil {
								xlog.Error("MCP tool execution failed", "tool", tc.FunctionCall.Name, "error", toolErr)
								toolResult = fmt.Sprintf("Error: %v", toolErr)
							}
							input.Messages = append(input.Messages, schema.Message{
								Role:          "tool",
								Content:       toolResult,
								StringContent: toolResult,
								ToolCallID:    tc.ID,
								Name:          tc.FunctionCall.Name,
							})

							// Stream tool result event to client
							mcpEvent := map[string]any{
								"type":   "mcp_tool_result",
								"name":   tc.FunctionCall.Name,
								"result": toolResult,
							}
							if mcpEventData, err := json.Marshal(mcpEvent); err == nil {
								fmt.Fprintf(c.Response().Writer, "data: %s\n\n", mcpEventData)
								c.Response().Flush()
							}
						}

						xlog.Debug("MCP streaming tools executed, re-running inference", "iteration", mcpStreamIter)
						continue // next MCP stream iteration
					}
				}

				// Automatic tool parsing fallback for streaming: when no tools were
				// requested but the model emitted tool call markup, parse and emit them.
				if !shouldUseFn && config.FunctionsConfig.AutomaticToolParsingFallback && collectedContent != "" && !toolsCalled {
					parsed := functions.ParseFunctionCall(collectedContent, config.FunctionsConfig)
					for i, fc := range parsed {
						toolCallID := fc.ID
						if toolCallID == "" {
							toolCallID = id
						}
						toolCallMsg := schema.OpenAIResponse{
							ID:      id,
							Created: created,
							Model:   input.Model,
							Choices: []schema.Choice{{
								Delta: &schema.Message{
									Role: "assistant",
									ToolCalls: []schema.ToolCall{{
										Index: i,
										ID:    toolCallID,
										Type:  "function",
										FunctionCall: schema.FunctionCall{
											Name:      fc.Name,
											Arguments: fc.Arguments,
										},
									}},
								},
								Index: 0,
							}},
							Object: "chat.completion.chunk",
						}
						respData, _ := json.Marshal(toolCallMsg)
						fmt.Fprintf(c.Response().Writer, "data: %s\n\n", respData)
						c.Response().Flush()
						toolsCalled = true
					}
				}

				// Drain the per-stream PII filter before the stop chunk
				// so any text held back by the buffered-emit invariant
				// reaches the client as a regular content delta. We
				// emit it as a chunk WITHOUT a finish_reason so the
				// next "stop" chunk still terminates the stream.
				if streamPIIFilter != nil {
					residual := streamPIIFilter.Drain()
					if residual != "" {
						drainResp := &schema.OpenAIResponse{
							ID:      id,
							Created: created,
							Model:   input.Model,
							Choices: []schema.Choice{{
								Delta: &schema.Message{Content: residual},
								Index: 0,
							}},
							Object: "chat.completion.chunk",
						}
						if drainBytes, err := json.Marshal(drainResp); err == nil {
							_, _ = fmt.Fprintf(c.Response().Writer, "data: %s\n\n", drainBytes)
							c.Response().Flush()
						}
					}
				}

				// No MCP tools to execute, send final stop message
				finishReason := FinishReasonStop
				if toolsCalled && len(input.Tools) > 0 {
					finishReason = FinishReasonToolCalls
				} else if toolsCalled {
					finishReason = FinishReasonFunctionCall
				}

				// Final delta chunk: empty delta with finish_reason set. Per
				// OpenAI streaming spec this chunk does NOT carry usage —
				// the optional trailer (below) does, gated on include_usage.
				resp := &schema.OpenAIResponse{
					ID:      id,
					Created: created,
					Model:   input.Model, // we have to return what the user sent here, due to OpenAI spec.
					Choices: []schema.Choice{
						{
							FinishReason: &finishReason,
							Index:        0,
							Delta:        &schema.Message{},
						}},
					Object: "chat.completion.chunk",
				}
				respData, _ := json.Marshal(resp)

				middleware.StampUsage(c, input.Model, finalUsage.Prompt, finalUsage.Completion)

				fmt.Fprintf(c.Response().Writer, "data: %s\n\n", respData)

				// Trailing usage chunk per OpenAI spec: emit only when the
				// caller opted in via stream_options.include_usage. Shape:
				// {"choices":[],"usage":{...},"object":"chat.completion.chunk",...}
				//
				// finalUsage is the authoritative TokenUsage returned by the
				// worker function (process / processTools) via the `ended`
				// channel. The worker reads it from ComputeChoices' return
				// value, which is the cumulative count produced by the backend
				// over the whole prediction. Issue #9927 was caused by the
				// tools-path worker not surfacing this value at all.
				if input.StreamOptions != nil && input.StreamOptions.IncludeUsage {
					trailerUsage := streamUsageFromTokenUsage(finalUsage, extraUsage)
					trailer := streamUsageTrailerJSON(id, input.Model, created, trailerUsage)
					_, _ = fmt.Fprintf(c.Response().Writer, "data: %s\n\n", trailer)
				}

				fmt.Fprintf(c.Response().Writer, "data: [DONE]\n\n")
				c.Response().Flush()
				xlog.Debug("Stream ended")
				return nil
			} // end MCP stream iteration loop

			// Safety fallback
			fmt.Fprintf(c.Response().Writer, "data: [DONE]\n\n")
			c.Response().Flush()
			return nil

		// no streaming mode
		default:
			mcpMaxIterations := 10
			if config.Agent.MaxIterations > 0 {
				mcpMaxIterations = config.Agent.MaxIterations
			}
			hasMCPTools := mcpExecutor != nil && mcpExecutor.HasTools()

			for mcpIteration := 0; mcpIteration <= mcpMaxIterations; mcpIteration++ {
				// Re-template on each MCP iteration since messages may have changed
				if mcpIteration > 0 && !config.TemplateConfig.UseTokenizerTemplate {
					predInput = evaluator.TemplateMessages(*input, input.Messages, config, funcs, shouldUseFn)
					xlog.Debug("MCP re-templating", "iteration", mcpIteration, "prompt_len", len(predInput))
				}

				// Detect if thinking token is already in prompt or template
				var template string
				if config.TemplateConfig.UseTokenizerTemplate {
					template = config.GetModelTemplate() // Uses raw template text; parsed jinja would be a future improvement
				} else {
					template = predInput
				}
				thinkingStartToken := reason.DetectThinkingStartToken(template, &config.ReasoningConfig)

				xlog.Debug("Thinking start token", "thinkingStartToken", thinkingStartToken, "template", template)

				// When shouldUseFn, the callback just stores the raw text — tool parsing
				// is deferred to after ComputeChoices so we can check chat deltas first
				// and avoid redundant Go-side parsing.
				var cbRawResult, cbReasoning string

				tokenCallback := func(s string, c *[]schema.Choice) {
					reasoning, s := reason.ExtractReasoningWithConfig(s, thinkingStartToken, config.ReasoningConfig)

					if !shouldUseFn {
						stopReason := FinishReasonStop
						message := &schema.Message{Role: "assistant", Content: &s}
						if reasoning != "" {
							message.Reasoning = &reasoning
						}
						*c = append(*c, schema.Choice{FinishReason: &stopReason, Index: 0, Message: message})
						return
					}

					// Store raw text for deferred tool parsing
					cbRawResult = s
					cbReasoning = reasoning
				}

				var result []schema.Choice
				var tokenUsage backend.TokenUsage
				var err error

				var chatDeltas []*pb.ChatDelta
				result, tokenUsage, chatDeltas, err = ComputeChoices(
					input,
					predInput,
					config,
					cl,
					startupOptions,
					ml,
					tokenCallback,
					nil,
					func(attempt int) bool {
						if !shouldUseFn {
							return false
						}
						// Retry when backend produced only reasoning and no content/tool calls.
						// Full tool parsing is deferred until after ComputeChoices returns
						// (when chat deltas are available), but we can detect the empty case here.
						if cbRawResult == "" && textContentToReturn == "" {
							xlog.Warn("Backend produced reasoning without actionable content, retrying",
								"reasoning_len", len(cbReasoning), "attempt", attempt+1)
							cbRawResult = ""
							cbReasoning = ""
							textContentToReturn = ""
							return true
						}
						return false
					},
				)
				if err != nil {
					return err
				}

				// For non-tool requests: prefer C++ autoparser chat deltas over
				// Go-side tag extraction (which can mangle output when thinkingStartToken
				// differs from the model's actual reasoning tags, e.g. Gemma 4).
				if !shouldUseFn {
					result = applyAutoparserOverride(chatDeltas, thinkingStartToken, config.ReasoningConfig, result)
				}

				// Tool parsing is deferred here (only when shouldUseFn) so chat deltas are available
				if shouldUseFn {
					var funcResults []functions.FuncCallResults

					// Try pre-parsed tool calls from C++ autoparser first
					if deltaToolCalls := functions.ToolCallsFromChatDeltas(chatDeltas); len(deltaToolCalls) > 0 {
						xlog.Debug("[ChatDeltas] non-SSE: using C++ autoparser tool calls, skipping Go-side parsing", "count", len(deltaToolCalls))
						funcResults = deltaToolCalls
						textContentToReturn = functions.ContentFromChatDeltas(chatDeltas)
						cbReasoning = functions.ReasoningFromChatDeltas(chatDeltas)
					} else if deltaContent := functions.ContentFromChatDeltas(chatDeltas); len(chatDeltas) > 0 && deltaContent != "" {
						// ChatDeltas have content but no tool calls — model answered without using tools.
						// This happens with thinking models (e.g. Gemma 4) where the Go-side reasoning
						// extraction misclassifies clean content as reasoning, leaving cbRawResult empty.
						xlog.Debug("[ChatDeltas] non-SSE: using C++ autoparser content (no tool calls)", "content_len", len(deltaContent))
						textContentToReturn = deltaContent
						cbReasoning = functions.ReasoningFromChatDeltas(chatDeltas)
					} else {
						// Fallback: parse tool calls from raw text
						xlog.Debug("[ChatDeltas] non-SSE: no chat deltas, falling back to Go-side text parsing")
						textContentToReturn = functions.ParseTextContent(cbRawResult, config.FunctionsConfig)
						cbRawResult = functions.CleanupLLMResult(cbRawResult, config.FunctionsConfig)
						funcResults = functions.ParseFunctionCall(cbRawResult, config.FunctionsConfig)
					}

					// Content-based tool call fallback: if no tool calls were found,
					// try parsing the raw result — ParseFunctionCall handles detection internally.
					if len(funcResults) == 0 {
						contentFuncResults := functions.ParseFunctionCall(cbRawResult, config.FunctionsConfig)
						if len(contentFuncResults) > 0 {
							funcResults = contentFuncResults
							textContentToReturn = functions.StripToolCallMarkup(cbRawResult)
						}
					}

					noActionsToRun := len(funcResults) > 0 && funcResults[0].Name == noActionName || len(funcResults) == 0

					switch {
					case noActionsToRun:
						// Use textContentToReturn if available (e.g. from ChatDeltas),
						// otherwise fall back to cbRawResult for legacy Go-side parsing.
						questionInput := cbRawResult
						if textContentToReturn != "" {
							questionInput = textContentToReturn
						}
						qResult, qErr := handleQuestion(config, funcResults, questionInput, predInput)
						if qErr != nil {
							xlog.Error("error handling question", "error", qErr)
						}

						stopReason := FinishReasonStop
						message := &schema.Message{Role: "assistant", Content: &qResult}
						if cbReasoning != "" {
							message.Reasoning = &cbReasoning
						}
						result = append(result, schema.Choice{
							FinishReason: &stopReason,
							Message:      message,
						})
					default:
						toolCallsReason := FinishReasonToolCalls
						toolChoice := schema.Choice{
							FinishReason: &toolCallsReason,
							Message: &schema.Message{
								Role: "assistant",
							},
						}
						if cbReasoning != "" {
							toolChoice.Message.Reasoning = &cbReasoning
						}

						for _, ss := range funcResults {
							name, args := ss.Name, ss.Arguments
							toolCallID := ss.ID
							if toolCallID == "" {
								toolCallID = id
							}
							if len(input.Tools) > 0 {
								toolChoice.Message.Content = textContentToReturn
								toolChoice.Message.ToolCalls = append(toolChoice.Message.ToolCalls,
									schema.ToolCall{
										ID:   toolCallID,
										Type: "function",
										FunctionCall: schema.FunctionCall{
											Name:      name,
											Arguments: args,
										},
									},
								)
							} else {
								// Deprecated function_call format
								functionCallReason := FinishReasonFunctionCall
								message := &schema.Message{
									Role:    "assistant",
									Content: &textContentToReturn,
									FunctionCall: map[string]any{
										"name":      name,
										"arguments": args,
									},
								}
								if cbReasoning != "" {
									message.Reasoning = &cbReasoning
								}
								result = append(result, schema.Choice{
									FinishReason: &functionCallReason,
									Message:      message,
								})
							}
						}

						if len(input.Tools) > 0 {
							result = append(result, toolChoice)
						}
					}
				}

				// Automatic tool parsing fallback: when no tools/functions were in the
				// request but the model emitted tool call markup, parse and surface them.
				if !shouldUseFn && config.FunctionsConfig.AutomaticToolParsingFallback && len(result) > 0 {
					for i, choice := range result {
						if choice.Message == nil || choice.Message.Content == nil {
							continue
						}
						contentStr, ok := choice.Message.Content.(string)
						if !ok || contentStr == "" {
							continue
						}
						parsed := functions.ParseFunctionCall(contentStr, config.FunctionsConfig)
						if len(parsed) == 0 {
							continue
						}
						stripped := functions.StripToolCallMarkup(contentStr)
						toolCallsReason := FinishReasonToolCalls
						result[i].FinishReason = &toolCallsReason
						if stripped != "" {
							result[i].Message.Content = &stripped
						} else {
							result[i].Message.Content = nil
						}
						for _, fc := range parsed {
							toolCallID := fc.ID
							if toolCallID == "" {
								toolCallID = id
							}
							result[i].Message.ToolCalls = append(result[i].Message.ToolCalls,
								schema.ToolCall{
									ID:   toolCallID,
									Type: "function",
									FunctionCall: schema.FunctionCall{
										Name:      fc.Name,
										Arguments: fc.Arguments,
									},
								},
							)
						}
					}
				}

				// MCP server-side tool execution loop:
				// If we have MCP tools and the model returned tool_calls, execute MCP tools
				// and re-run inference with the results appended to the conversation.
				if hasMCPTools && len(result) > 0 {
					var mcpCallsExecuted bool
					for _, choice := range result {
						if choice.Message == nil || len(choice.Message.ToolCalls) == 0 {
							continue
						}
						// Check if any tool calls are MCP tools
						var hasMCPCalls bool
						for _, tc := range choice.Message.ToolCalls {
							if mcpExecutor != nil && mcpExecutor.IsTool(tc.FunctionCall.Name) {
								hasMCPCalls = true
								break
							}
						}
						if !hasMCPCalls {
							continue
						}

						// Append assistant message with tool_calls to conversation
						assistantContent := ""
						if choice.Message.Content != nil {
							if s, ok := choice.Message.Content.(string); ok {
								assistantContent = s
							} else if sp, ok := choice.Message.Content.(*string); ok && sp != nil {
								assistantContent = *sp
							}
						}
						assistantMsg := schema.Message{
							Role:      "assistant",
							Content:   assistantContent,
							ToolCalls: choice.Message.ToolCalls,
						}
						input.Messages = append(input.Messages, assistantMsg)

						// Execute each MCP tool call and append results
						for _, tc := range choice.Message.ToolCalls {
							if mcpExecutor == nil || !mcpExecutor.IsTool(tc.FunctionCall.Name) {
								continue
							}
							xlog.Debug("Executing MCP tool", "tool", tc.FunctionCall.Name, "arguments", tc.FunctionCall.Arguments, "iteration", mcpIteration)
							toolResult, toolErr := mcpExecutor.ExecuteTool(c.Request().Context(), tc.FunctionCall.Name, tc.FunctionCall.Arguments)
							if toolErr != nil {
								xlog.Error("MCP tool execution failed", "tool", tc.FunctionCall.Name, "error", toolErr)
								toolResult = fmt.Sprintf("Error: %v", toolErr)
							}
							input.Messages = append(input.Messages, schema.Message{
								Role:          "tool",
								Content:       toolResult,
								StringContent: toolResult,
								ToolCallID:    tc.ID,
								Name:          tc.FunctionCall.Name,
							})
							mcpCallsExecuted = true
						}
					}

					if mcpCallsExecuted {
						xlog.Debug("MCP tools executed, re-running inference", "iteration", mcpIteration, "messages", len(input.Messages))
						continue // next MCP iteration
					}
				}

				// No MCP tools to execute (or no MCP tools configured), return response
				usage := schema.OpenAIUsage{
					PromptTokens:     tokenUsage.Prompt,
					CompletionTokens: tokenUsage.Completion,
					TotalTokens:      tokenUsage.Prompt + tokenUsage.Completion,
				}
				if extraUsage {
					usage.TimingTokenGeneration = tokenUsage.TimingTokenGeneration
					usage.TimingPromptProcessing = tokenUsage.TimingPromptProcessing
				}

				resp := &schema.OpenAIResponse{
					ID:      id,
					Created: created,
					Model:   input.Model, // we have to return what the user sent here, due to OpenAI spec.
					Choices: result,
					Object:  "chat.completion",
					Usage:   &usage,
				}
				respData, _ := json.Marshal(resp)
				xlog.Debug("Response", "response", string(respData))

				middleware.StampUsage(c, input.Model, usage.PromptTokens, usage.CompletionTokens)

				// Return the prediction in the response body
				return c.JSON(200, resp)
			} // end MCP iteration loop

			// Should not reach here, but safety fallback
			return fmt.Errorf("MCP iteration limit reached")
		}
	}
}

func handleQuestion(config *config.ModelConfig, funcResults []functions.FuncCallResults, result, prompt string) (string, error) {

	if len(funcResults) == 0 && result != "" {
		xlog.Debug("nothing function results but we had a message from the LLM")

		return result, nil
	}

	xlog.Debug("nothing to do, computing a reply")
	arg := ""
	if len(funcResults) > 0 {
		arg = funcResults[0].Arguments
	}
	// If there is a message that the LLM already sends as part of the JSON reply, use it
	arguments := map[string]any{}
	if err := json.Unmarshal([]byte(arg), &arguments); err != nil {
		xlog.Debug("handleQuestion: function result did not contain a valid JSON object")
	}
	m, exists := arguments["message"]
	if exists {
		switch message := m.(type) {
		case string:
			if message != "" {
				xlog.Debug("Reply received from LLM", "message", message)
				message = backend.Finetune(*config, prompt, message)
				xlog.Debug("Reply received from LLM(finetuned)", "message", message)

				return message, nil
			}
		}
	}

	xlog.Debug("No action received from LLM, without a message, computing a reply")

	return "", nil
}

// forwardCloudProxyOpenAIViaBackend marshals the OpenAI request,
// constructs the streaming PII filter (when this model has PII
// enabled), and hands off to the cloud-proxy gRPC backend which does
// the outbound HTTP. The chat endpoint owns the body+filter
// construction because it's the only place the request lands as a
// parsed *schema.OpenAIRequest.
func forwardCloudProxyOpenAIViaBackend(c echo.Context, cfg *config.ModelConfig, input *schema.OpenAIRequest, piiRedactor *pii.Redactor, piiEvents pii.EventStore, ml *model.ModelLoader, appConfig *config.ApplicationConfig) error {
	body, err := json.Marshal(input)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "cloudproxy: marshal request: "+err.Error())
	}

	correlationID := c.Response().Header().Get("X-Correlation-ID")
	streamFilter := cloudproxy.BuildStreamFilter(c, cfg, input.Stream, piiRedactor, piiEvents, correlationID)
	return cloudproxy.ForwardViaBackend(c, cfg, body, streamFilter, ml, appConfig)
}
