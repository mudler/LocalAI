package openai

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	mcpTools "github.com/mudler/LocalAI/core/http/endpoints/mcp"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/functions"
	reason "github.com/mudler/LocalAI/pkg/reasoning"

	"github.com/mudler/LocalAI/core/templates"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/model"

	"github.com/mudler/xlog"
)

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

// ChatEndpoint is the OpenAI Completion API endpoint https://platform.openai.com/docs/api-reference/chat/create
// @Summary Generate a chat completions for a given prompt and model.
// @Param request body schema.OpenAIRequest true "query params"
// @Success 200 {object} schema.OpenAIResponse "Response"
// @Router /v1/chat/completions [post]
func ChatEndpoint(cl *config.ModelConfigLoader, ml *model.ModelLoader, evaluator *templates.Evaluator, startupOptions *config.ApplicationConfig, natsClient mcpTools.MCPNATSClient) echo.HandlerFunc {
	var id, textContentToReturn string
	var created int

	process := func(s string, req *schema.OpenAIRequest, config *config.ModelConfig, loader *model.ModelLoader, responses chan schema.OpenAIResponse, extraUsage bool) error {
		initialMessage := schema.OpenAIResponse{
			ID:      id,
			Created: created,
			Model:   req.Model, // we have to return what the user sent here, due to OpenAI spec.
			Choices: []schema.Choice{{Delta: &schema.Message{Role: "assistant"}, Index: 0, FinishReason: nil}},
			Object:  "chat.completion.chunk",
		}
		responses <- initialMessage

		// Detect if thinking token is already in prompt or template
		// When UseTokenizerTemplate is enabled, predInput is empty, so we check the template
		var template string
		if config.TemplateConfig.UseTokenizerTemplate {
			template = config.GetModelTemplate()
		} else {
			template = s
		}
		thinkingStartToken := reason.DetectThinkingStartToken(template, &config.ReasoningConfig)
		extractor := reason.NewReasoningExtractor(thinkingStartToken, config.ReasoningConfig)

		_, _, _, err := ComputeChoices(req, s, config, cl, startupOptions, loader, func(s string, c *[]schema.Choice) {}, func(s string, tokenUsage backend.TokenUsage) bool {
			reasoningDelta, contentDelta := extractor.ProcessToken(s)

			usage := schema.OpenAIUsage{
				PromptTokens:     tokenUsage.Prompt,
				CompletionTokens: tokenUsage.Completion,
				TotalTokens:      tokenUsage.Prompt + tokenUsage.Completion,
			}
			if extraUsage {
				usage.TimingTokenGeneration = tokenUsage.TimingTokenGeneration
				usage.TimingPromptProcessing = tokenUsage.TimingPromptProcessing
			}

			delta := &schema.Message{}
			if contentDelta != "" {
				delta.Content = &contentDelta
			}
			if reasoningDelta != "" {
				delta.Reasoning = &reasoningDelta
			}

			resp := schema.OpenAIResponse{
				ID:      id,
				Created: created,
				Model:   req.Model, // we have to return what the user sent here, due to OpenAI spec.
				Choices: []schema.Choice{{Delta: delta, Index: 0, FinishReason: nil}},
				Object:  "chat.completion.chunk",
				Usage:   usage,
			}

			responses <- resp
			return true
		})
		close(responses)
		return err
	}
	processTools := func(noAction string, prompt string, req *schema.OpenAIRequest, config *config.ModelConfig, loader *model.ModelLoader, responses chan schema.OpenAIResponse, extraUsage bool) error {
		// Detect if thinking token is already in prompt or template
		var template string
		if config.TemplateConfig.UseTokenizerTemplate {
			template = config.GetModelTemplate()
		} else {
			template = prompt
		}
		thinkingStartToken := reason.DetectThinkingStartToken(template, &config.ReasoningConfig)
		extractor := reason.NewReasoningExtractor(thinkingStartToken, config.ReasoningConfig)

		result := ""
		lastEmittedCount := 0
		sentInitialRole := false

		_, tokenUsage, chatDeltas, err := ComputeChoices(req, prompt, config, cl, startupOptions, loader, func(s string, c *[]schema.Choice) {}, func(s string, usage backend.TokenUsage) bool {
			result += s
			reasoningDelta, contentDelta := extractor.ProcessToken(s)

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
			}

			// Stream content deltas (cleaned of reasoning tags) while no tool calls
			// have been detected. Once the incremental parser finds tool calls,
			// content stops — per OpenAI spec, content and tool_calls don't mix.
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
			cleanedResult := functions.CleanupLLMResult(result, config.FunctionsConfig)

			// Determine XML format from config
			var xmlFormat *functions.XMLToolCallFormat
			if config.FunctionsConfig.XMLFormat != nil {
				xmlFormat = config.FunctionsConfig.XMLFormat
			} else if config.FunctionsConfig.XMLFormatPreset != "" {
				xmlFormat = functions.GetXMLFormatPreset(config.FunctionsConfig.XMLFormatPreset)
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
				// Try JSON tool call parsing for streaming
				// Check if the result looks like JSON tool calls
				jsonResults, jsonErr := functions.ParseJSONIterative(cleanedResult, true)
				if jsonErr == nil && len(jsonResults) > 0 {
					// Check if these are tool calls (have "name" and optionally "arguments")
					for _, jsonObj := range jsonResults {
						if name, ok := jsonObj["name"].(string); ok && name != "" {
							// This looks like a tool call
							args := "{}"
							if argsVal, ok := jsonObj["arguments"]; ok {
								if argsStr, ok := argsVal.(string); ok {
									args = argsStr
								} else {
									argsBytes, _ := json.Marshal(argsVal)
									args = string(argsBytes)
								}
							}
							// Emit tool call
							initialMessage := schema.OpenAIResponse{
								ID:      id,
								Created: created,
								Model:   req.Model,
								Choices: []schema.Choice{{
									Delta: &schema.Message{
										Role: "assistant",
										ToolCalls: []schema.ToolCall{
											{
												Index: lastEmittedCount,
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
							select {
							case responses <- initialMessage:
							default:
							}
							lastEmittedCount++
						}
					}
				}
			}
			return true
		},
			func(attempt int) bool {
				// After streaming completes: check if we got actionable content
				cleaned := extractor.CleanedContent()
				// Check for tool calls from chat deltas (will be re-checked after ComputeChoices,
				// but we need to know here whether to retry)
				hasToolCalls := lastEmittedCount > 0
				if cleaned == "" && !hasToolCalls {
					xlog.Warn("Streaming: backend produced only reasoning, retrying",
						"reasoning_len", len(extractor.Reasoning()), "attempt", attempt+1)
					extractor.ResetAndSuppressReasoning()
					result = ""
					lastEmittedCount = 0
					sentInitialRole = false
					return true
				}
				return false
			},
		)
		if err != nil {
			return err
		}
		// Try using pre-parsed tool calls from C++ autoparser (chat deltas)
		var functionResults []functions.FuncCallResults
		var reasoning string

		if deltaToolCalls := functions.ToolCallsFromChatDeltas(chatDeltas); len(deltaToolCalls) > 0 {
			xlog.Debug("[ChatDeltas] Using pre-parsed tool calls from C++ autoparser", "count", len(deltaToolCalls))
			functionResults = deltaToolCalls
			// Use content/reasoning from deltas too
			textContentToReturn = functions.ContentFromChatDeltas(chatDeltas)
			reasoning = functions.ReasoningFromChatDeltas(chatDeltas)
		} else {
			// Fallback: parse tool calls from raw text (no chat deltas from backend)
			xlog.Debug("[ChatDeltas] no pre-parsed tool calls, falling back to Go-side text parsing")
			reasoning = extractor.Reasoning()
			cleanedResult := extractor.CleanedContent()
			textContentToReturn = functions.ParseTextContent(cleanedResult, config.FunctionsConfig)
			cleanedResult = functions.CleanupLLMResult(cleanedResult, config.FunctionsConfig)
			functionResults = functions.ParseFunctionCall(cleanedResult, config.FunctionsConfig)
		}
		xlog.Debug("[ChatDeltas] final tool call decision", "tool_calls", len(functionResults), "text_content", textContentToReturn)
		noActionToRun := len(functionResults) > 0 && functionResults[0].Name == noAction || len(functionResults) == 0

		switch {
		case noActionToRun:
			usage := schema.OpenAIUsage{
				PromptTokens:     tokenUsage.Prompt,
				CompletionTokens: tokenUsage.Completion,
				TotalTokens:      tokenUsage.Prompt + tokenUsage.Completion,
			}
			if extraUsage {
				usage.TimingTokenGeneration = tokenUsage.TimingTokenGeneration
				usage.TimingPromptProcessing = tokenUsage.TimingPromptProcessing
			}

			if sentInitialRole {
				// Content was already streamed during the callback — just emit usage.
				delta := &schema.Message{}
				if reasoning != "" && extractor.Reasoning() == "" {
					delta.Reasoning = &reasoning
				}
				responses <- schema.OpenAIResponse{
					ID: id, Created: created, Model: req.Model,
					Choices: []schema.Choice{{Delta: delta, Index: 0}},
					Object:  "chat.completion.chunk",
					Usage:   usage,
				}
			} else {
				// Content was NOT streamed — send everything at once (fallback).
				responses <- schema.OpenAIResponse{
					ID: id, Created: created, Model: req.Model,
					Choices: []schema.Choice{{Delta: &schema.Message{Role: "assistant"}, Index: 0}},
					Object:  "chat.completion.chunk",
				}

				result, err := handleQuestion(config, functionResults, extractor.CleanedContent(), prompt)
				if err != nil {
					xlog.Error("error handling question", "error", err)
					return err
				}

				delta := &schema.Message{Content: &result}
				if reasoning != "" {
					delta.Reasoning = &reasoning
				}
				responses <- schema.OpenAIResponse{
					ID: id, Created: created, Model: req.Model,
					Choices: []schema.Choice{{Delta: delta, Index: 0}},
					Object:  "chat.completion.chunk",
					Usage:   usage,
				}
			}

		default:
			for i, ss := range functionResults {
				name, args := ss.Name, ss.Arguments
				toolCallID := ss.ID
				if toolCallID == "" {
					toolCallID = id
				}

				initialMessage := schema.OpenAIResponse{
					ID:      id,
					Created: created,
					Model:   req.Model, // we have to return what the user sent here, due to OpenAI spec.
					Choices: []schema.Choice{{
						Delta: &schema.Message{
							Role: "assistant",
							ToolCalls: []schema.ToolCall{
								{
									Index: i,
									ID:    toolCallID,
									Type:  "function",
									FunctionCall: schema.FunctionCall{
										Name: name,
									},
								},
							},
						},
						Index:        0,
						FinishReason: nil,
					}},
					Object: "chat.completion.chunk",
				}
				responses <- initialMessage

				responses <- schema.OpenAIResponse{
					ID:      id,
					Created: created,
					Model:   req.Model, // we have to return what the user sent here, due to OpenAI spec.
					Choices: []schema.Choice{{
						Delta: &schema.Message{
							Role:    "assistant",
							Content: &textContentToReturn,
							ToolCalls: []schema.ToolCall{
								{
									Index: i,
									ID:    toolCallID,
									Type:  "function",
									FunctionCall: schema.FunctionCall{
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
			}
		}

		close(responses)
		return err
	}

	return func(c echo.Context) error {
		textContentToReturn = ""
		id = uuid.New().String()
		created = int(time.Now().Unix())

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

		funcs := input.Functions
		shouldUseFn := len(input.Functions) > 0 && config.ShouldUseFunctions()
		strictMode := false

		// MCP tool injection: when mcp_servers is set in metadata and model has MCP config
		var mcpExecutor mcpTools.ToolExecutor
		mcpServers := mcpTools.MCPServersFromMetadata(input.Metadata)

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
			ended := make(chan error, 1)

			go func() {
				if !shouldUseFn {
					ended <- process(predInput, input, config, ml, responses, extraUsage)
				} else {
					ended <- processTools(noActionName, predInput, input, config, ml, responses, extraUsage)
				}
			}()

			usage := &schema.OpenAIUsage{}
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
					usage = &ev.Usage // Copy a pointer to the latest usage chunk so that the stop message can reference it
					if len(ev.Choices[0].Delta.ToolCalls) > 0 {
						toolsCalled = true
						// Collect and merge tool call deltas for MCP execution
						if hasMCPToolsStream {
							collectedToolCalls = mergeToolCallDeltas(collectedToolCalls, ev.Choices[0].Delta.ToolCalls)
						}
					}
					// Collect content for MCP conversation history and automatic tool parsing fallback
					if (hasMCPToolsStream || config.FunctionsConfig.AutomaticToolParsingFallback) && ev.Choices[0].Delta != nil && ev.Choices[0].Delta.Content != nil {
						if s, ok := ev.Choices[0].Delta.Content.(string); ok {
							collectedContent += s
						} else if sp, ok := ev.Choices[0].Delta.Content.(*string); ok && sp != nil {
							collectedContent += *sp
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
				case err := <-ended:
					if err == nil {
						break LOOP
					}
					xlog.Error("Stream ended with error", "error", err)

					errorResp := schema.ErrorResponse{
						Error: &schema.APIError{
							Message: err.Error(),
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

			// No MCP tools to execute, send final stop message
			finishReason := FinishReasonStop
			if toolsCalled && len(input.Tools) > 0 {
				finishReason = FinishReasonToolCalls
			} else if toolsCalled {
				finishReason = FinishReasonFunctionCall
			}

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
				Usage:  *usage,
			}
			respData, _ := json.Marshal(resp)

			fmt.Fprintf(c.Response().Writer, "data: %s\n\n", respData)
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
				template = config.GetModelTemplate() // TODO: this should be the parsed jinja template. But for now this is the best we can do.
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

			// Tool parsing is deferred here (only when shouldUseFn) so chat deltas are available
			if shouldUseFn {
				var funcResults []functions.FuncCallResults

				// Try pre-parsed tool calls from C++ autoparser first
				if deltaToolCalls := functions.ToolCallsFromChatDeltas(chatDeltas); len(deltaToolCalls) > 0 {
					xlog.Debug("[ChatDeltas] non-SSE: using C++ autoparser tool calls, skipping Go-side parsing", "count", len(deltaToolCalls))
					funcResults = deltaToolCalls
					textContentToReturn = functions.ContentFromChatDeltas(chatDeltas)
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
					qResult, qErr := handleQuestion(config, funcResults, cbRawResult, predInput)
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
				Usage:   usage,
			}
			respData, _ := json.Marshal(resp)
			xlog.Debug("Response", "response", string(respData))

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
