package anthropic

import (
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	mcpTools "github.com/mudler/LocalAI/core/http/endpoints/mcp"
	openaiEndpoint "github.com/mudler/LocalAI/core/http/endpoints/openai"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/templates"
	"github.com/mudler/LocalAI/pkg/functions"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/xlog"
)

// MessagesEndpoint is the Anthropic Messages API endpoint
// https://docs.anthropic.com/claude/reference/messages_post
// @Summary Generate a message response for the given messages and model.
// @Param request body schema.AnthropicRequest true "query params"
// @Success 200 {object} schema.AnthropicResponse "Response"
// @Router /v1/messages [post]
func MessagesEndpoint(cl *config.ModelConfigLoader, ml *model.ModelLoader, evaluator *templates.Evaluator, appConfig *config.ApplicationConfig, natsClient mcpTools.MCPNATSClient) echo.HandlerFunc {
	return func(c echo.Context) error {
		id := uuid.New().String()

		input, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.AnthropicRequest)
		if !ok || input.Model == "" {
			return sendAnthropicError(c, 400, "invalid_request_error", "model is required")
		}

		cfg, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig)
		if !ok || cfg == nil {
			return sendAnthropicError(c, 400, "invalid_request_error", "model configuration not found")
		}

		if input.MaxTokens <= 0 {
			return sendAnthropicError(c, 400, "invalid_request_error", "max_tokens is required and must be greater than 0")
		}

		xlog.Debug("Anthropic Messages endpoint configuration read", "config", cfg)

		// Convert Anthropic messages to OpenAI format for internal processing
		openAIMessages := convertAnthropicToOpenAIMessages(input)

		// Convert Anthropic tools to internal Functions format
		funcs, shouldUseFn := convertAnthropicTools(input, cfg)

		// MCP injection: prompts, resources, and tools
		var mcpExecutor mcpTools.ToolExecutor
		mcpServers := mcpTools.MCPServersFromMetadata(input.Metadata)
		mcpPromptName, mcpPromptArgs := mcpTools.MCPPromptFromMetadata(input.Metadata)
		mcpResourceURIs := mcpTools.MCPResourcesFromMetadata(input.Metadata)

		if (len(mcpServers) > 0 || mcpPromptName != "" || len(mcpResourceURIs) > 0) && (cfg.MCP.Servers != "" || cfg.MCP.Stdio != "") {
			remote, stdio, mcpErr := cfg.MCP.MCPConfigFromYAML()
			if mcpErr == nil {
				mcpExecutor = mcpTools.NewToolExecutor(c.Request().Context(), natsClient, cfg.Name, remote, stdio, mcpServers)

				// Prompt and resource injection (local mode only)
				if natsClient == nil {
					namedSessions, sessErr := mcpTools.NamedSessionsFromMCPConfig(cfg.Name, remote, stdio, mcpServers)
					if sessErr == nil && len(namedSessions) > 0 {
						mcpCtx, _ := mcpTools.InjectMCPContext(c.Request().Context(), namedSessions, mcpPromptName, mcpPromptArgs, mcpResourceURIs)
						if mcpCtx != nil {
							openAIMessages = append(mcpCtx.PromptMessages, openAIMessages...)
							mcpTools.AppendResourceSuffix(openAIMessages, mcpCtx.ResourceSuffix)
						}
					}
				}

				// Tool injection via executor
				if mcpExecutor.HasTools() {
					mcpFuncs, discErr := mcpExecutor.DiscoverTools(c.Request().Context())
					if discErr == nil {
						for _, fn := range mcpFuncs {
							funcs = append(funcs, fn)
						}
						shouldUseFn = len(funcs) > 0 && cfg.ShouldUseFunctions()
						xlog.Debug("Anthropic MCP tools injected", "count", len(mcpFuncs), "total_funcs", len(funcs))
					} else {
						xlog.Error("Failed to discover MCP tools", "error", discErr)
					}
				}
			} else {
				xlog.Error("Failed to parse MCP config", "error", mcpErr)
			}
		}

		// Create an OpenAI-compatible request for internal processing
		openAIReq := &schema.OpenAIRequest{
			PredictionOptions: schema.PredictionOptions{
				BasicModelRequest: schema.BasicModelRequest{Model: input.Model},
				Temperature:       input.Temperature,
				TopK:              input.TopK,
				TopP:              input.TopP,
				Maxtokens:         &input.MaxTokens,
			},
			Messages: openAIMessages,
			Stream:   input.Stream,
			Context:  input.Context,
			Cancel:   input.Cancel,
		}

		// Set stop sequences
		if len(input.StopSequences) > 0 {
			openAIReq.Stop = input.StopSequences
		}

		// Merge config settings
		if input.Temperature != nil {
			cfg.Temperature = input.Temperature
		}
		if input.TopK != nil {
			cfg.TopK = input.TopK
		}
		if input.TopP != nil {
			cfg.TopP = input.TopP
		}
		cfg.Maxtokens = &input.MaxTokens
		if len(input.StopSequences) > 0 {
			cfg.StopWords = append(cfg.StopWords, input.StopSequences...)
		}

		// Template the prompt with tools if available
		predInput := evaluator.TemplateMessages(*openAIReq, openAIReq.Messages, cfg, funcs, shouldUseFn)
		xlog.Debug("Anthropic Messages - Prompt (after templating)", "prompt", predInput)

		if input.Stream {
			return handleAnthropicStream(c, id, input, cfg, ml, cl, appConfig, predInput, openAIReq, funcs, shouldUseFn, mcpExecutor, evaluator)
		}

		return handleAnthropicNonStream(c, id, input, cfg, ml, cl, appConfig, predInput, openAIReq, funcs, shouldUseFn, mcpExecutor, evaluator)
	}
}

func handleAnthropicNonStream(c echo.Context, id string, input *schema.AnthropicRequest, cfg *config.ModelConfig, ml *model.ModelLoader, cl *config.ModelConfigLoader, appConfig *config.ApplicationConfig, predInput string, openAIReq *schema.OpenAIRequest, funcs functions.Functions, shouldUseFn bool, mcpExecutor mcpTools.ToolExecutor, evaluator *templates.Evaluator) error {
	mcpMaxIterations := 10
	if cfg.Agent.MaxIterations > 0 {
		mcpMaxIterations = cfg.Agent.MaxIterations
	}
	hasMCPTools := mcpExecutor != nil && mcpExecutor.HasTools()

	for mcpIteration := 0; mcpIteration <= mcpMaxIterations; mcpIteration++ {
		// Re-template on each MCP iteration since messages may have changed
		if mcpIteration > 0 {
			predInput = evaluator.TemplateMessages(*openAIReq, openAIReq.Messages, cfg, funcs, shouldUseFn)
			xlog.Debug("Anthropic MCP re-templating", "iteration", mcpIteration, "prompt_len", len(predInput))
		}

		// Populate openAIReq fields for ComputeChoices
		openAIReq.Tools = convertFuncsToOpenAITools(funcs)
		openAIReq.ToolsChoice = input.ToolChoice
		openAIReq.Metadata = input.Metadata

		var result string
		cb := func(s string, c *[]schema.Choice) {
			result = s
		}
		_, tokenUsage, chatDeltas, err := openaiEndpoint.ComputeChoices(openAIReq, predInput, cfg, cl, appConfig, ml, cb, nil)
		if err != nil {
			xlog.Error("Anthropic model inference failed", "error", err)
			return sendAnthropicError(c, 500, "api_error", fmt.Sprintf("model inference failed: %v", err))
		}

		// Try pre-parsed tool calls from C++ autoparser first, fall back to text parsing
		var toolCalls []functions.FuncCallResults
		if deltaToolCalls := functions.ToolCallsFromChatDeltas(chatDeltas); len(deltaToolCalls) > 0 {
			xlog.Debug("[ChatDeltas] Anthropic: using pre-parsed tool calls", "count", len(deltaToolCalls))
			toolCalls = deltaToolCalls
		} else {
			xlog.Debug("[ChatDeltas] Anthropic: no pre-parsed tool calls, falling back to Go-side text parsing")
			toolCalls = functions.ParseFunctionCall(result, cfg.FunctionsConfig)
		}

		// MCP server-side tool execution: if any tool calls are MCP tools, execute and loop
		if hasMCPTools && shouldUseFn && len(toolCalls) > 0 {
			var hasMCPCalls bool
			for _, tc := range toolCalls {
				if mcpExecutor != nil && mcpExecutor.IsTool(tc.Name) {
					hasMCPCalls = true
					break
				}
			}
			if hasMCPCalls {
				// Append assistant message with tool_calls to conversation
				assistantMsg := schema.Message{
					Role:    "assistant",
					Content: result,
				}
				for i, tc := range toolCalls {
					toolCallID := tc.ID
					if toolCallID == "" {
						toolCallID = fmt.Sprintf("toolu_%s_%d", id, i)
					}
					assistantMsg.ToolCalls = append(assistantMsg.ToolCalls, schema.ToolCall{
						Index: i,
						ID:    toolCallID,
						Type:  "function",
						FunctionCall: schema.FunctionCall{
							Name:      tc.Name,
							Arguments: tc.Arguments,
						},
					})
				}
				openAIReq.Messages = append(openAIReq.Messages, assistantMsg)

				// Execute each MCP tool call and append results
				for _, tc := range assistantMsg.ToolCalls {
					if mcpExecutor == nil || !mcpExecutor.IsTool(tc.FunctionCall.Name) {
						continue
					}
					xlog.Debug("Executing MCP tool (Anthropic)", "tool", tc.FunctionCall.Name, "iteration", mcpIteration)
					toolResult, toolErr := mcpExecutor.ExecuteTool(
						c.Request().Context(), tc.FunctionCall.Name, tc.FunctionCall.Arguments,
					)
					if toolErr != nil {
						xlog.Error("MCP tool execution failed", "tool", tc.FunctionCall.Name, "error", toolErr)
						toolResult = fmt.Sprintf("Error: %v", toolErr)
					}
					openAIReq.Messages = append(openAIReq.Messages, schema.Message{
						Role:          "tool",
						Content:       toolResult,
						StringContent: toolResult,
						ToolCallID:    tc.ID,
						Name:          tc.FunctionCall.Name,
					})
				}

				xlog.Debug("Anthropic MCP tools executed, re-running inference", "iteration", mcpIteration)
				continue // next MCP iteration
			}
		}

		// No MCP tools to execute, build and return response
		var contentBlocks []schema.AnthropicContentBlock
		var stopReason string

		if shouldUseFn && len(toolCalls) > 0 {
			stopReason = "tool_use"
			for _, tc := range toolCalls {
				var inputArgs map[string]any
				if err := json.Unmarshal([]byte(tc.Arguments), &inputArgs); err != nil {
					xlog.Warn("Failed to parse tool call arguments as JSON", "error", err, "args", tc.Arguments)
					inputArgs = map[string]any{"raw": tc.Arguments}
				}
				contentBlocks = append(contentBlocks, schema.AnthropicContentBlock{
					Type:  "tool_use",
					ID:    fmt.Sprintf("toolu_%s_%d", id, len(contentBlocks)),
					Name:  tc.Name,
					Input: inputArgs,
				})
			}
			textContent := functions.ParseTextContent(result, cfg.FunctionsConfig)
			if textContent != "" {
				contentBlocks = append([]schema.AnthropicContentBlock{{Type: "text", Text: textContent}}, contentBlocks...)
			}
		} else if !shouldUseFn && cfg.FunctionsConfig.AutomaticToolParsingFallback && result != "" {
			// Automatic tool parsing fallback: no tools in request but model emitted tool call markup
			parsed := functions.ParseFunctionCall(result, cfg.FunctionsConfig)
			if len(parsed) > 0 {
				stopReason = "tool_use"
				stripped := functions.StripToolCallMarkup(result)
				if stripped != "" {
					contentBlocks = append(contentBlocks, schema.AnthropicContentBlock{Type: "text", Text: stripped})
				}
				for i, fc := range parsed {
					var inputArgs map[string]any
					if err := json.Unmarshal([]byte(fc.Arguments), &inputArgs); err != nil {
						inputArgs = map[string]any{"raw": fc.Arguments}
					}
					toolCallID := fc.ID
					if toolCallID == "" {
						toolCallID = fmt.Sprintf("toolu_%s_%d", id, i)
					}
					contentBlocks = append(contentBlocks, schema.AnthropicContentBlock{
						Type:  "tool_use",
						ID:    toolCallID,
						Name:  fc.Name,
						Input: inputArgs,
					})
				}
			} else {
				stopReason = "end_turn"
				contentBlocks = []schema.AnthropicContentBlock{{Type: "text", Text: result}}
			}
		} else {
			stopReason = "end_turn"
			contentBlocks = []schema.AnthropicContentBlock{
				{Type: "text", Text: result},
			}
		}

		resp := &schema.AnthropicResponse{
			ID:         fmt.Sprintf("msg_%s", id),
			Type:       "message",
			Role:       "assistant",
			Model:      input.Model,
			StopReason: &stopReason,
			Content:    contentBlocks,
			Usage: schema.AnthropicUsage{
				InputTokens:  tokenUsage.Prompt,
				OutputTokens: tokenUsage.Completion,
			},
		}

		if respData, err := json.Marshal(resp); err == nil {
			xlog.Debug("Anthropic Response", "response", string(respData))
		}

		return c.JSON(200, resp)
	} // end MCP iteration loop

	return sendAnthropicError(c, 500, "api_error", "MCP iteration limit reached")
}

func handleAnthropicStream(c echo.Context, id string, input *schema.AnthropicRequest, cfg *config.ModelConfig, ml *model.ModelLoader, cl *config.ModelConfigLoader, appConfig *config.ApplicationConfig, predInput string, openAIReq *schema.OpenAIRequest, funcs functions.Functions, shouldUseFn bool, mcpExecutor mcpTools.ToolExecutor, evaluator *templates.Evaluator) error {
	c.Response().Header().Set("Content-Type", "text/event-stream")
	c.Response().Header().Set("Cache-Control", "no-cache")
	c.Response().Header().Set("Connection", "keep-alive")

	// Send message_start event
	messageStart := schema.AnthropicStreamEvent{
		Type: "message_start",
		Message: &schema.AnthropicStreamMessage{
			ID:      fmt.Sprintf("msg_%s", id),
			Type:    "message",
			Role:    "assistant",
			Content: []schema.AnthropicContentBlock{},
			Model:   input.Model,
			Usage:   schema.AnthropicUsage{InputTokens: 0, OutputTokens: 0},
		},
	}
	sendAnthropicSSE(c, messageStart)

	mcpMaxIterations := 10
	if cfg.Agent.MaxIterations > 0 {
		mcpMaxIterations = cfg.Agent.MaxIterations
	}
	hasMCPTools := mcpExecutor != nil && mcpExecutor.HasTools()

	for mcpIteration := 0; mcpIteration <= mcpMaxIterations; mcpIteration++ {
		// Re-template on MCP iterations
		if mcpIteration > 0 {
			predInput = evaluator.TemplateMessages(*openAIReq, openAIReq.Messages, cfg, funcs, shouldUseFn)
			xlog.Debug("Anthropic MCP stream re-templating", "iteration", mcpIteration)
		}

		// Track accumulated content for tool call detection
		accumulatedContent := ""
		currentBlockIndex := 0
		inToolCall := false
		toolCallsEmitted := 0

		// Send initial content_block_start event
		contentBlockStart := schema.AnthropicStreamEvent{
			Type:         "content_block_start",
			Index:        currentBlockIndex,
			ContentBlock: &schema.AnthropicContentBlock{Type: "text", Text: ""},
		}
		sendAnthropicSSE(c, contentBlockStart)

		// Collect tool calls for MCP execution
		var collectedToolCalls []functions.FuncCallResults

		tokenCallback := func(token string, usage backend.TokenUsage) bool {
			accumulatedContent += token

			if shouldUseFn {
				cleanedResult := functions.CleanupLLMResult(accumulatedContent, cfg.FunctionsConfig)
				toolCalls := functions.ParseFunctionCall(cleanedResult, cfg.FunctionsConfig)

				if len(toolCalls) > toolCallsEmitted {
					if !inToolCall && currentBlockIndex == 0 {
						sendAnthropicSSE(c, schema.AnthropicStreamEvent{
							Type:  "content_block_stop",
							Index: currentBlockIndex,
						})
						currentBlockIndex++
						inToolCall = true
					}

					for i := toolCallsEmitted; i < len(toolCalls); i++ {
						tc := toolCalls[i]
						sendAnthropicSSE(c, schema.AnthropicStreamEvent{
							Type:  "content_block_start",
							Index: currentBlockIndex,
							ContentBlock: &schema.AnthropicContentBlock{
								Type: "tool_use",
								ID:   fmt.Sprintf("toolu_%s_%d", id, i),
								Name: tc.Name,
							},
						})
						sendAnthropicSSE(c, schema.AnthropicStreamEvent{
							Type:  "content_block_delta",
							Index: currentBlockIndex,
							Delta: &schema.AnthropicStreamDelta{
								Type:        "input_json_delta",
								PartialJSON: tc.Arguments,
							},
						})
						sendAnthropicSSE(c, schema.AnthropicStreamEvent{
							Type:  "content_block_stop",
							Index: currentBlockIndex,
						})
						currentBlockIndex++
					}
					collectedToolCalls = toolCalls
					toolCallsEmitted = len(toolCalls)
					return true
				}
			}

			if !inToolCall {
				sendAnthropicSSE(c, schema.AnthropicStreamEvent{
					Type:  "content_block_delta",
					Index: 0,
					Delta: &schema.AnthropicStreamDelta{
						Type: "text_delta",
						Text: token,
					},
				})
			}
			return true
		}

		// Populate openAIReq fields for ComputeChoices
		openAIReq.Tools = convertFuncsToOpenAITools(funcs)
		openAIReq.ToolsChoice = input.ToolChoice
		openAIReq.Metadata = input.Metadata

		_, tokenUsage, chatDeltas, err := openaiEndpoint.ComputeChoices(openAIReq, predInput, cfg, cl, appConfig, ml, func(s string, c *[]schema.Choice) {}, tokenCallback)
		if err != nil {
			xlog.Error("Anthropic stream model inference failed", "error", err)
			return sendAnthropicError(c, 500, "api_error", fmt.Sprintf("model inference failed: %v", err))
		}

		// Also check chat deltas for tool calls
		if deltaToolCalls := functions.ToolCallsFromChatDeltas(chatDeltas); len(deltaToolCalls) > 0 && len(collectedToolCalls) == 0 {
			collectedToolCalls = deltaToolCalls
		}

		// MCP streaming tool execution: if we collected MCP tool calls, execute and loop
		if hasMCPTools && len(collectedToolCalls) > 0 {
			var hasMCPCalls bool
			for _, tc := range collectedToolCalls {
				if mcpExecutor != nil && mcpExecutor.IsTool(tc.Name) {
					hasMCPCalls = true
					break
				}
			}
			if hasMCPCalls {
				// Append assistant message with tool_calls
				assistantMsg := schema.Message{
					Role:    "assistant",
					Content: accumulatedContent,
				}
				for i, tc := range collectedToolCalls {
					toolCallID := tc.ID
					if toolCallID == "" {
						toolCallID = fmt.Sprintf("toolu_%s_%d", id, i)
					}
					assistantMsg.ToolCalls = append(assistantMsg.ToolCalls, schema.ToolCall{
						Index: i,
						ID:    toolCallID,
						Type:  "function",
						FunctionCall: schema.FunctionCall{
							Name:      tc.Name,
							Arguments: tc.Arguments,
						},
					})
				}
				openAIReq.Messages = append(openAIReq.Messages, assistantMsg)

				// Execute MCP tool calls
				for _, tc := range assistantMsg.ToolCalls {
					if mcpExecutor == nil || !mcpExecutor.IsTool(tc.FunctionCall.Name) {
						continue
					}
					xlog.Debug("Executing MCP tool (Anthropic stream)", "tool", tc.FunctionCall.Name, "iteration", mcpIteration)
					toolResult, toolErr := mcpExecutor.ExecuteTool(
						c.Request().Context(), tc.FunctionCall.Name, tc.FunctionCall.Arguments,
					)
					if toolErr != nil {
						xlog.Error("MCP tool execution failed", "tool", tc.FunctionCall.Name, "error", toolErr)
						toolResult = fmt.Sprintf("Error: %v", toolErr)
					}
					openAIReq.Messages = append(openAIReq.Messages, schema.Message{
						Role:          "tool",
						Content:       toolResult,
						StringContent: toolResult,
						ToolCallID:    tc.ID,
						Name:          tc.FunctionCall.Name,
					})
				}

				xlog.Debug("Anthropic MCP streaming tools executed, re-running inference", "iteration", mcpIteration)
				continue // next MCP iteration
			}
		}

		// Automatic tool parsing fallback for streaming: when no tools were requested
		// but the model emitted tool call markup, parse and emit as tool_use blocks.
		if !shouldUseFn && cfg.FunctionsConfig.AutomaticToolParsingFallback && accumulatedContent != "" && toolCallsEmitted == 0 {
			parsed := functions.ParseFunctionCall(accumulatedContent, cfg.FunctionsConfig)
			if len(parsed) > 0 {
				// Close the text content block
				sendAnthropicSSE(c, schema.AnthropicStreamEvent{
					Type:  "content_block_stop",
					Index: currentBlockIndex,
				})
				currentBlockIndex++
				inToolCall = true

				for i, fc := range parsed {
					toolCallID := fc.ID
					if toolCallID == "" {
						toolCallID = fmt.Sprintf("toolu_%s_%d", id, i)
					}
					sendAnthropicSSE(c, schema.AnthropicStreamEvent{
						Type:  "content_block_start",
						Index: currentBlockIndex,
						ContentBlock: &schema.AnthropicContentBlock{
							Type: "tool_use",
							ID:   toolCallID,
							Name: fc.Name,
						},
					})
					sendAnthropicSSE(c, schema.AnthropicStreamEvent{
						Type:  "content_block_delta",
						Index: currentBlockIndex,
						Delta: &schema.AnthropicStreamDelta{
							Type:        "input_json_delta",
							PartialJSON: fc.Arguments,
						},
					})
					sendAnthropicSSE(c, schema.AnthropicStreamEvent{
						Type:  "content_block_stop",
						Index: currentBlockIndex,
					})
					currentBlockIndex++
					toolCallsEmitted++
				}
			}
		}

		// No MCP tools to execute, close stream
		if !inToolCall {
			sendAnthropicSSE(c, schema.AnthropicStreamEvent{
				Type:  "content_block_stop",
				Index: 0,
			})
		}

		stopReason := "end_turn"
		if toolCallsEmitted > 0 {
			stopReason = "tool_use"
		}

		sendAnthropicSSE(c, schema.AnthropicStreamEvent{
			Type: "message_delta",
			Delta: &schema.AnthropicStreamDelta{
				StopReason: &stopReason,
			},
			Usage: &schema.AnthropicUsage{
				OutputTokens: tokenUsage.Completion,
			},
		})

		sendAnthropicSSE(c, schema.AnthropicStreamEvent{
			Type: "message_stop",
		})

		return nil
	} // end MCP iteration loop

	// Safety fallback
	sendAnthropicSSE(c, schema.AnthropicStreamEvent{
		Type: "message_stop",
	})
	return nil
}

func convertFuncsToOpenAITools(funcs functions.Functions) []functions.Tool {
	tools := make([]functions.Tool, len(funcs))
	for i, f := range funcs {
		tools[i] = functions.Tool{Type: "function", Function: f}
	}
	return tools
}

func sendAnthropicSSE(c echo.Context, event schema.AnthropicStreamEvent) {
	data, err := json.Marshal(event)
	if err != nil {
		xlog.Error("Failed to marshal SSE event", "error", err)
		return
	}
	fmt.Fprintf(c.Response().Writer, "event: %s\ndata: %s\n\n", event.Type, string(data))
	c.Response().Flush()
}

func sendAnthropicError(c echo.Context, statusCode int, errorType, message string) error {
	resp := schema.AnthropicErrorResponse{
		Type: "error",
		Error: schema.AnthropicError{
			Type:    errorType,
			Message: message,
		},
	}
	return c.JSON(statusCode, resp)
}

func convertAnthropicToOpenAIMessages(input *schema.AnthropicRequest) []schema.Message {
	var messages []schema.Message

	// Add system message if present
	if input.System != "" {
		sysStr := string(input.System)
		messages = append(messages, schema.Message{
			Role:          "system",
			StringContent: sysStr,
			Content:       sysStr,
		})
	}

	// Convert Anthropic messages to OpenAI format
	for _, msg := range input.Messages {
		openAIMsg := schema.Message{
			Role: msg.Role,
		}

		// Handle content (can be string or array of content blocks)
		switch content := msg.Content.(type) {
		case string:
			openAIMsg.StringContent = content
			openAIMsg.Content = content
		case []any:
			// Handle array of content blocks
			var textContent string
			var stringImages []string
			var toolCalls []schema.ToolCall
			toolCallIndex := 0

			for _, block := range content {
				if blockMap, ok := block.(map[string]any); ok {
					blockType, _ := blockMap["type"].(string)
					switch blockType {
					case "text":
						if text, ok := blockMap["text"].(string); ok {
							textContent += text
						}
					case "image":
						// Handle image content
						if source, ok := blockMap["source"].(map[string]any); ok {
							if sourceType, ok := source["type"].(string); ok && sourceType == "base64" {
								if data, ok := source["data"].(string); ok {
									mediaType, _ := source["media_type"].(string)
									// Format as data URI
									dataURI := fmt.Sprintf("data:%s;base64,%s", mediaType, data)
									stringImages = append(stringImages, dataURI)
								}
							}
						}
					case "tool_use":
						// Convert tool_use to ToolCall format
						toolID, _ := blockMap["id"].(string)
						toolName, _ := blockMap["name"].(string)
						toolInput := blockMap["input"]
						
						// Serialize input to JSON string
						inputJSON, err := json.Marshal(toolInput)
						if err != nil {
							xlog.Warn("Failed to marshal tool input", "error", err)
							inputJSON = []byte("{}")
						}
						
						toolCalls = append(toolCalls, schema.ToolCall{
							Index: toolCallIndex,
							ID:    toolID,
							Type:  "function",
							FunctionCall: schema.FunctionCall{
								Name:      toolName,
								Arguments: string(inputJSON),
							},
						})
						toolCallIndex++
					case "tool_result":
						// Convert tool_result to a message with role "tool"
						// This is handled by creating a separate message after this block
						// For now, we'll add it as text content
						toolUseID, _ := blockMap["tool_use_id"].(string)
						isError := false
						if isErrorPtr, ok := blockMap["is_error"].(*bool); ok && isErrorPtr != nil {
							isError = *isErrorPtr
						}
						
						var resultText string
						if resultContent, ok := blockMap["content"]; ok {
							switch rc := resultContent.(type) {
							case string:
								resultText = rc
							case []any:
								// Array of content blocks
								for _, cb := range rc {
									if cbMap, ok := cb.(map[string]any); ok {
										if cbMap["type"] == "text" {
											if text, ok := cbMap["text"].(string); ok {
												resultText += text
											}
										}
									}
								}
							}
						}
						
						// Add tool result as a tool role message
						// We need to handle this differently - create a new message
						if msg.Role == "user" {
							// Store tool result info for creating separate message
							prefix := ""
							if isError {
								prefix = "Error: "
							}
							textContent += fmt.Sprintf("\n[Tool Result for %s]: %s%s", toolUseID, prefix, resultText)
						}
					}
				}
			}
			openAIMsg.StringContent = textContent
			openAIMsg.Content = textContent
			openAIMsg.StringImages = stringImages
			
			// Add tool calls if present
			if len(toolCalls) > 0 {
				openAIMsg.ToolCalls = toolCalls
			}
		}

		messages = append(messages, openAIMsg)
	}

	return messages
}

// convertAnthropicTools converts Anthropic tools to internal Functions format
func convertAnthropicTools(input *schema.AnthropicRequest, cfg *config.ModelConfig) (functions.Functions, bool) {
	if len(input.Tools) == 0 {
		return nil, false
	}
	
	var funcs functions.Functions
	for _, tool := range input.Tools {
		f := functions.Function{
			Name:        tool.Name,
			Description: tool.Description,
			Parameters:  tool.InputSchema,
		}
		funcs = append(funcs, f)
	}
	
	// Handle tool_choice
	if input.ToolChoice != nil {
		switch tc := input.ToolChoice.(type) {
		case string:
			// "auto", "any", or "none"
			if tc == "any" {
				// Force the model to use one of the tools
				cfg.SetFunctionCallString("required")
			} else if tc == "none" {
				// Don't use tools
				return nil, false
			}
			// "auto" is the default - let model decide
		case map[string]any:
			// Specific tool selection: {"type": "tool", "name": "tool_name"}
			if tcType, ok := tc["type"].(string); ok && tcType == "tool" {
				if name, ok := tc["name"].(string); ok {
					// Force specific tool
					cfg.SetFunctionCallString(name)
				}
			}
		}
	}
	
	return funcs, len(funcs) > 0 && cfg.ShouldUseFunctions()
}
