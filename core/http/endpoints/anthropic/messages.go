package anthropic

import (
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
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
func MessagesEndpoint(cl *config.ModelConfigLoader, ml *model.ModelLoader, evaluator *templates.Evaluator, appConfig *config.ApplicationConfig) echo.HandlerFunc {
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
			return handleAnthropicStream(c, id, input, cfg, ml, cl, appConfig, predInput, openAIReq, funcs, shouldUseFn)
		}

		return handleAnthropicNonStream(c, id, input, cfg, ml, cl, appConfig, predInput, openAIReq, funcs, shouldUseFn)
	}
}

func handleAnthropicNonStream(c echo.Context, id string, input *schema.AnthropicRequest, cfg *config.ModelConfig, ml *model.ModelLoader, cl *config.ModelConfigLoader, appConfig *config.ApplicationConfig, predInput string, openAIReq *schema.OpenAIRequest, funcs functions.Functions, shouldUseFn bool) error {
	images := []string{}
	for _, m := range openAIReq.Messages {
		images = append(images, m.StringImages...)
	}

	toolsJSON := ""
	if len(funcs) > 0 {
		openAITools := make([]functions.Tool, len(funcs))
		for i, f := range funcs {
			openAITools[i] = functions.Tool{Type: "function", Function: f}
		}
		if toolsBytes, err := json.Marshal(openAITools); err == nil {
			toolsJSON = string(toolsBytes)
		}
	}
	toolChoiceJSON := ""
	if input.ToolChoice != nil {
		if toolChoiceBytes, err := json.Marshal(input.ToolChoice); err == nil {
			toolChoiceJSON = string(toolChoiceBytes)
		}
	}

	predFunc, err := backend.ModelInference(
		input.Context, predInput, openAIReq.Messages, images, nil, nil, ml, cfg, cl, appConfig, nil, toolsJSON, toolChoiceJSON, nil, nil, nil)
	if err != nil {
		xlog.Error("Anthropic model inference failed", "error", err)
		return sendAnthropicError(c, 500, "api_error", fmt.Sprintf("model inference failed: %v", err))
	}

	const maxEmptyRetries = 5
	var prediction backend.LLMResponse
	var result string
	for attempt := 0; attempt <= maxEmptyRetries; attempt++ {
		prediction, err = predFunc()
		if err != nil {
			xlog.Error("Anthropic prediction failed", "error", err)
			return sendAnthropicError(c, 500, "api_error", fmt.Sprintf("prediction failed: %v", err))
		}
		result = backend.Finetune(*cfg, predInput, prediction.Response)
		if result != "" || !shouldUseFn {
			break
		}
		xlog.Warn("Anthropic: retrying prediction due to empty backend response", "attempt", attempt+1, "maxRetries", maxEmptyRetries)
	}
	
	// Check if the result contains tool calls
	toolCalls := functions.ParseFunctionCall(result, cfg.FunctionsConfig)
	
	var contentBlocks []schema.AnthropicContentBlock
	var stopReason string
	
	if shouldUseFn && len(toolCalls) > 0 {
		// Model wants to use tools
		stopReason = "tool_use"
		for _, tc := range toolCalls {
			// Parse arguments as JSON
			var inputArgs map[string]interface{}
			if err := json.Unmarshal([]byte(tc.Arguments), &inputArgs); err != nil {
				xlog.Warn("Failed to parse tool call arguments as JSON", "error", err, "args", tc.Arguments)
				inputArgs = map[string]interface{}{"raw": tc.Arguments}
			}
			
			contentBlocks = append(contentBlocks, schema.AnthropicContentBlock{
				Type:  "tool_use",
				ID:    fmt.Sprintf("toolu_%s_%d", id, len(contentBlocks)),
				Name:  tc.Name,
				Input: inputArgs,
			})
		}
		
		// Add any text content before the tool calls
		textContent := functions.ParseTextContent(result, cfg.FunctionsConfig)
		if textContent != "" {
			// Prepend text block
			contentBlocks = append([]schema.AnthropicContentBlock{{Type: "text", Text: textContent}}, contentBlocks...)
		}
	} else {
		// Normal text response
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
			InputTokens:  prediction.Usage.Prompt,
			OutputTokens: prediction.Usage.Completion,
		},
	}

	if respData, err := json.Marshal(resp); err == nil {
		xlog.Debug("Anthropic Response", "response", string(respData))
	}

	return c.JSON(200, resp)
}

func handleAnthropicStream(c echo.Context, id string, input *schema.AnthropicRequest, cfg *config.ModelConfig, ml *model.ModelLoader, cl *config.ModelConfigLoader, appConfig *config.ApplicationConfig, predInput string, openAIReq *schema.OpenAIRequest, funcs functions.Functions, shouldUseFn bool) error {
	c.Response().Header().Set("Content-Type", "text/event-stream")
	c.Response().Header().Set("Cache-Control", "no-cache")
	c.Response().Header().Set("Connection", "keep-alive")

	// Create OpenAI messages for inference
	openAIMessages := openAIReq.Messages

	images := []string{}
	for _, m := range openAIMessages {
		images = append(images, m.StringImages...)
	}

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

	// Stream content deltas
	tokenCallback := func(token string, usage backend.TokenUsage) bool {
		accumulatedContent += token
		
		// If we're using functions, try to detect tool calls incrementally
		if shouldUseFn {
			cleanedResult := functions.CleanupLLMResult(accumulatedContent, cfg.FunctionsConfig)
			
			// Try parsing for tool calls
			toolCalls := functions.ParseFunctionCall(cleanedResult, cfg.FunctionsConfig)
			
			// If we detected new tool calls and haven't emitted them yet
			if len(toolCalls) > toolCallsEmitted {
				// Stop the current text block if we were in one
				if !inToolCall && currentBlockIndex == 0 {
					sendAnthropicSSE(c, schema.AnthropicStreamEvent{
						Type:  "content_block_stop",
						Index: currentBlockIndex,
					})
					currentBlockIndex++
					inToolCall = true
				}
				
				// Emit new tool calls
				for i := toolCallsEmitted; i < len(toolCalls); i++ {
					tc := toolCalls[i]
					
					// Send content_block_start for tool_use
					sendAnthropicSSE(c, schema.AnthropicStreamEvent{
						Type:  "content_block_start",
						Index: currentBlockIndex,
						ContentBlock: &schema.AnthropicContentBlock{
							Type: "tool_use",
							ID:   fmt.Sprintf("toolu_%s_%d", id, i),
							Name: tc.Name,
						},
					})
					
					// Send input_json_delta with the arguments
					sendAnthropicSSE(c, schema.AnthropicStreamEvent{
						Type:  "content_block_delta",
						Index: currentBlockIndex,
						Delta: &schema.AnthropicStreamDelta{
							Type:        "input_json_delta",
							PartialJSON: tc.Arguments,
						},
					})
					
					// Send content_block_stop
					sendAnthropicSSE(c, schema.AnthropicStreamEvent{
						Type:  "content_block_stop",
						Index: currentBlockIndex,
					})
					
					currentBlockIndex++
				}
				toolCallsEmitted = len(toolCalls)
				return true
			}
		}
		
		// Send regular text delta if not in tool call mode
		if !inToolCall {
			delta := schema.AnthropicStreamEvent{
				Type:  "content_block_delta",
				Index: 0,
				Delta: &schema.AnthropicStreamDelta{
					Type: "text_delta",
					Text: token,
				},
			}
			sendAnthropicSSE(c, delta)
		}
		return true
	}

	toolsJSON := ""
	if len(funcs) > 0 {
		openAITools := make([]functions.Tool, len(funcs))
		for i, f := range funcs {
			openAITools[i] = functions.Tool{Type: "function", Function: f}
		}
		if toolsBytes, err := json.Marshal(openAITools); err == nil {
			toolsJSON = string(toolsBytes)
		}
	}
	toolChoiceJSON := ""
	if input.ToolChoice != nil {
		if toolChoiceBytes, err := json.Marshal(input.ToolChoice); err == nil {
			toolChoiceJSON = string(toolChoiceBytes)
		}
	}

	predFunc, err := backend.ModelInference(
		input.Context, predInput, openAIMessages, images, nil, nil, ml, cfg, cl, appConfig, tokenCallback, toolsJSON, toolChoiceJSON, nil, nil, nil)
	if err != nil {
		xlog.Error("Anthropic stream model inference failed", "error", err)
		return sendAnthropicError(c, 500, "api_error", fmt.Sprintf("model inference failed: %v", err))
	}

	prediction, err := predFunc()
	if err != nil {
		xlog.Error("Anthropic stream prediction failed", "error", err)
		return sendAnthropicError(c, 500, "api_error", fmt.Sprintf("prediction failed: %v", err))
	}

	// Send content_block_stop event for last block if we didn't close it yet
	if !inToolCall {
		contentBlockStop := schema.AnthropicStreamEvent{
			Type:  "content_block_stop",
			Index: 0,
		}
		sendAnthropicSSE(c, contentBlockStop)
	}

	// Determine stop reason
	stopReason := "end_turn"
	if toolCallsEmitted > 0 {
		stopReason = "tool_use"
	}

	// Send message_delta event with stop_reason
	messageDelta := schema.AnthropicStreamEvent{
		Type: "message_delta",
		Delta: &schema.AnthropicStreamDelta{
			StopReason: &stopReason,
		},
		Usage: &schema.AnthropicUsage{
			OutputTokens: prediction.Usage.Completion,
		},
	}
	sendAnthropicSSE(c, messageDelta)

	// Send message_stop event
	messageStop := schema.AnthropicStreamEvent{
		Type: "message_stop",
	}
	sendAnthropicSSE(c, messageStop)

	return nil
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
		case []interface{}:
			// Handle array of content blocks
			var textContent string
			var stringImages []string
			var toolCalls []schema.ToolCall
			toolCallIndex := 0

			for _, block := range content {
				if blockMap, ok := block.(map[string]interface{}); ok {
					blockType, _ := blockMap["type"].(string)
					switch blockType {
					case "text":
						if text, ok := blockMap["text"].(string); ok {
							textContent += text
						}
					case "image":
						// Handle image content
						if source, ok := blockMap["source"].(map[string]interface{}); ok {
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
							case []interface{}:
								// Array of content blocks
								for _, cb := range rc {
									if cbMap, ok := cb.(map[string]interface{}); ok {
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
		case map[string]interface{}:
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
