package openresponses

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/templates"
	"github.com/mudler/LocalAI/pkg/functions"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/utils"
	"github.com/mudler/xlog"
)

// ResponsesEndpoint is the Open Responses API endpoint
// https://www.openresponses.org/specification
// @Summary Create a response using the Open Responses API
// @Param request body schema.OpenResponsesRequest true "Request body"
// @Success 200 {object} schema.ORResponseResource "Response"
// @Router /v1/responses [post]
func ResponsesEndpoint(cl *config.ModelConfigLoader, ml *model.ModelLoader, evaluator *templates.Evaluator, appConfig *config.ApplicationConfig) echo.HandlerFunc {
	return func(c echo.Context) error {
		createdAt := time.Now().Unix()
		responseID := fmt.Sprintf("resp_%s", uuid.New().String())

		input, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.OpenResponsesRequest)
		if !ok || input.Model == "" {
			return sendOpenResponsesError(c, 400, "invalid_request", "model is required", "")
		}

		cfg, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig)
		if !ok || cfg == nil {
			return sendOpenResponsesError(c, 400, "invalid_request", "model configuration not found", "")
		}

		// Convert Open Responses input to internal Messages
		messages, err := convertORInputToMessages(input.Input, cfg)
		if err != nil {
			return sendOpenResponsesError(c, 400, "invalid_request", fmt.Sprintf("failed to parse input: %v", err), "")
		}

		// Add instructions as system message if provided
		if input.Instructions != "" {
			messages = append([]schema.Message{{Role: "system", StringContent: input.Instructions}}, messages...)
		}

		// Handle tools
		var funcs functions.Functions
		var shouldUseFn bool
		var useMCP bool

		if len(input.Tools) > 0 {
			// User-provided tools
			funcs, shouldUseFn = convertORToolsToFunctions(input, cfg)
		} else if cfg.MCP.Servers != "" || cfg.MCP.Stdio != "" {
			// MCP tools (internal)
			useMCP = true
		}

		// Create OpenAI-compatible request for internal processing
		openAIReq := &schema.OpenAIRequest{
			PredictionOptions: schema.PredictionOptions{
				BasicModelRequest: schema.BasicModelRequest{Model: input.Model},
				Temperature:       input.Temperature,
				TopP:              input.TopP,
				Maxtokens:         input.MaxOutputTokens,
			},
			Messages: messages,
			Stream:   input.Stream,
			Context:  input.Context,
			Cancel:   input.Cancel,
			Functions: funcs,
		}

		// Template the prompt
		predInput := evaluator.TemplateMessages(*openAIReq, openAIReq.Messages, cfg, funcs, shouldUseFn)
		xlog.Debug("Open Responses - Prompt (after templating)", "prompt", predInput)

		if useMCP {
			// Use MCP agentic loop
			return handleMCPResponse(c, responseID, createdAt, input, cfg, ml, predInput, openAIReq, appConfig)
		}

		if input.Stream {
			return handleOpenResponsesStream(c, responseID, createdAt, input, cfg, ml, predInput, openAIReq, funcs, shouldUseFn)
		}

		return handleOpenResponsesNonStream(c, responseID, createdAt, input, cfg, ml, predInput, openAIReq, funcs, shouldUseFn)
	}
}

// convertORInputToMessages converts Open Responses input to internal Messages
func convertORInputToMessages(input interface{}, cfg *config.ModelConfig) ([]schema.Message, error) {
	var messages []schema.Message

	switch v := input.(type) {
	case string:
		// Simple string = user message
		return []schema.Message{{Role: "user", StringContent: v}}, nil
	case []interface{}:
		// Array of items
		for _, itemRaw := range v {
			itemMap, ok := itemRaw.(map[string]interface{})
			if !ok {
				continue
			}

			itemType, _ := itemMap["type"].(string)
			switch itemType {
			case "message":
				msg, err := convertORMessageItem(itemMap, cfg)
				if err != nil {
					return nil, err
				}
				messages = append(messages, msg)
			case "function_call_output":
				// Convert function call output to tool role message
				callID, _ := itemMap["call_id"].(string)
				output := itemMap["output"]
				var outputStr string
				if str, ok := output.(string); ok {
					outputStr = str
				} else {
					// Convert to JSON string
					outputBytes, _ := json.Marshal(output)
					outputStr = string(outputBytes)
				}
				// For tool messages, we use the Name field to store the call ID
				messages = append(messages, schema.Message{
					Role:         "tool",
					Name:         callID,
					Content:      outputStr,
					StringContent: outputStr,
				})
			case "item_reference":
				// TODO: Handle item references (would need to load from previous response)
				xlog.Warn("item_reference not yet implemented")
			}
		}
		return messages, nil
	default:
		return nil, fmt.Errorf("unsupported input type: %T", input)
	}
}

// convertORMessageItem converts an Open Responses message item to internal Message
func convertORMessageItem(itemMap map[string]interface{}, cfg *config.ModelConfig) (schema.Message, error) {
	role, _ := itemMap["role"].(string)
	msg := schema.Message{Role: role}

	content := itemMap["content"]
	switch contentVal := content.(type) {
	case string:
		msg.StringContent = contentVal
		msg.Content = contentVal
	case []interface{}:
		// Array of content parts
		var textContent string
		var stringImages []string
		var stringVideos []string
		var stringAudios []string

		for _, partRaw := range contentVal {
			partMap, ok := partRaw.(map[string]interface{})
			if !ok {
				continue
			}

			partType, _ := partMap["type"].(string)
			switch partType {
			case "input_text":
				if text, ok := partMap["text"].(string); ok {
					textContent += text
				}
			case "input_image":
				if imageURL, ok := partMap["image_url"].(string); ok {
					// Convert to base64 data URI
					base64, err := utils.GetContentURIAsBase64(imageURL)
					if err != nil {
						xlog.Error("Failed encoding image", "error", err)
						continue
					}
					stringImages = append(stringImages, base64)
				}
			case "input_file":
				if fileURL, ok := partMap["file_url"].(string); ok {
					// Convert to base64
					base64, err := utils.GetContentURIAsBase64(fileURL)
					if err != nil {
						xlog.Error("Failed encoding file", "error", err)
						continue
					}
					// For now, treat files as text content
					textContent += base64
				} else if fileData, ok := partMap["file_data"].(string); ok {
					// Already base64
					textContent += fileData
				}
			}
		}

		msg.StringContent = textContent
		msg.Content = textContent
		msg.StringImages = stringImages
		msg.StringVideos = stringVideos
		msg.StringAudios = stringAudios

		// Template multimodal content
		if len(stringImages) > 0 || len(stringVideos) > 0 || len(stringAudios) > 0 {
			msg.StringContent, _ = templates.TemplateMultiModal(cfg.TemplateConfig.Multimodal, templates.MultiModalOptions{
				TotalImages:     len(stringImages),
				TotalVideos:     len(stringVideos),
				TotalAudios:     len(stringAudios),
				ImagesInMessage: len(stringImages),
				VideosInMessage: len(stringVideos),
				AudiosInMessage: len(stringAudios),
			}, textContent)
		}
	}

	return msg, nil
}

// convertORToolsToFunctions converts Open Responses tools to internal Functions
func convertORToolsToFunctions(input *schema.OpenResponsesRequest, cfg *config.ModelConfig) (functions.Functions, bool) {
	if len(input.Tools) == 0 {
		return nil, false
	}

	var funcs functions.Functions
	for _, tool := range input.Tools {
		if tool.Type == "function" {
			f := functions.Function{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  tool.Parameters,
			}
			funcs = append(funcs, f)
		}
	}

	// Handle tool_choice
	if input.ToolChoice != nil {
		switch tc := input.ToolChoice.(type) {
		case string:
			if tc == "required" {
				cfg.SetFunctionCallString("required")
			} else if tc == "none" {
				return nil, false
			}
		case map[string]interface{}:
			if tcType, ok := tc["type"].(string); ok && tcType == "function" {
				if name, ok := tc["name"].(string); ok {
					cfg.SetFunctionCallString(name)
				}
			}
		}
	}

	return funcs, len(funcs) > 0 && cfg.ShouldUseFunctions()
}

// handleOpenResponsesNonStream handles non-streaming responses
func handleOpenResponsesNonStream(c echo.Context, responseID string, createdAt int64, input *schema.OpenResponsesRequest, cfg *config.ModelConfig, ml *model.ModelLoader, predInput string, openAIReq *schema.OpenAIRequest, funcs functions.Functions, shouldUseFn bool) error {
	images := []string{}
	for _, m := range openAIReq.Messages {
		images = append(images, m.StringImages...)
	}

	predFunc, err := backend.ModelInference(
		input.Context, predInput, openAIReq.Messages, images, nil, nil, ml, cfg, nil, nil, nil, "", "", nil, nil, nil)
	if err != nil {
		xlog.Error("Open Responses model inference failed", "error", err)
		return sendOpenResponsesError(c, 500, "model_error", fmt.Sprintf("model inference failed: %v", err), "")
	}

	prediction, err := predFunc()
	if err != nil {
		xlog.Error("Open Responses prediction failed", "error", err)
		return sendOpenResponsesError(c, 500, "model_error", fmt.Sprintf("prediction failed: %v", err), "")
	}

	result := backend.Finetune(*cfg, predInput, prediction.Response)

	// Parse tool calls if using functions
	var outputItems []schema.ORItemField
	var toolCalls []schema.ToolCall

	if shouldUseFn {
		funcCallResults := functions.ParseFunctionCall(result, cfg.FunctionsConfig)
		textContent := functions.ParseTextContent(result, cfg.FunctionsConfig)

		// Convert FuncCallResults to ToolCall
		for i, fc := range funcCallResults {
			toolCalls = append(toolCalls, schema.ToolCall{
				Index: i,
				ID:    fmt.Sprintf("fc_%s", uuid.New().String()),
				Type:  "function",
				FunctionCall: schema.FunctionCall{
					Name:      fc.Name,
					Arguments: fc.Arguments,
				},
			})
		}

		// Add message item with text content
		if textContent != "" {
			outputItems = append(outputItems, schema.ORItemField{
				Type:   "message",
				ID:     fmt.Sprintf("msg_%s", uuid.New().String()),
				Status: "completed",
				Role:   "assistant",
				Content: []schema.ORContentPart{{
					Type: "output_text",
					Text: textContent,
				}},
			})
		}

		// Add function call items
		for _, tc := range toolCalls {
			outputItems = append(outputItems, schema.ORItemField{
				Type:      "function_call",
				ID:        fmt.Sprintf("fc_%s", uuid.New().String()),
				Status:    "completed",
				CallID:    tc.ID,
				Name:      tc.FunctionCall.Name,
				Arguments: tc.FunctionCall.Arguments,
			})
		}
	} else {
		// Simple text response
		outputItems = []schema.ORItemField{
			{
				Type:   "message",
				ID:     fmt.Sprintf("msg_%s", uuid.New().String()),
				Status: "completed",
				Role:   "assistant",
				Content: []schema.ORContentPart{{
					Type: "output_text",
					Text: result,
				}},
			},
		}
	}

	// Build response
	response := &schema.ORResponseResource{
		ID:        responseID,
		Object:    "response",
		CreatedAt: createdAt,
		Status:    "completed",
		Model:     input.Model,
		Output:    outputItems,
		Usage: &schema.ORUsage{
			InputTokens:  prediction.Usage.Prompt,
			OutputTokens: prediction.Usage.Completion,
			TotalTokens:  prediction.Usage.Prompt + prediction.Usage.Completion,
		},
	}

	if input.Temperature != nil {
		response.Temperature = *input.Temperature
	}
	if input.TopP != nil {
		response.TopP = *input.TopP
	}
	if input.MaxOutputTokens != nil {
		response.MaxOutputTokens = input.MaxOutputTokens
	}
	if input.Tools != nil {
		response.Tools = input.Tools
	}
	if input.ToolChoice != nil {
		response.ToolChoice = input.ToolChoice
	}
	if input.Truncation != "" {
		response.Truncation = input.Truncation
	}
	if input.Metadata != nil {
		response.Metadata = input.Metadata
	}

	now := time.Now().Unix()
	response.CompletedAt = &now

	return c.JSON(200, response)
}

// handleOpenResponsesStream handles streaming responses
func handleOpenResponsesStream(c echo.Context, responseID string, createdAt int64, input *schema.OpenResponsesRequest, cfg *config.ModelConfig, ml *model.ModelLoader, predInput string, openAIReq *schema.OpenAIRequest, funcs functions.Functions, shouldUseFn bool) error {
	c.Response().Header().Set("Content-Type", "text/event-stream")
	c.Response().Header().Set("Cache-Control", "no-cache")
	c.Response().Header().Set("Connection", "keep-alive")

	sequenceNumber := 0

	// Emit response.created
	responseCreated := &schema.ORResponseResource{
		ID:        responseID,
		Object:    "response",
		CreatedAt: createdAt,
		Status:    "in_progress",
		Model:     input.Model,
		Output:    []schema.ORItemField{},
	}
	sendSSEEvent(c, &schema.ORStreamEvent{
		Type:           "response.created",
		SequenceNumber: sequenceNumber,
		Response:       responseCreated,
	})
	sequenceNumber++

	// Emit response.in_progress
	sendSSEEvent(c, &schema.ORStreamEvent{
		Type:           "response.in_progress",
		SequenceNumber: sequenceNumber,
		Response:       responseCreated,
	})
	sequenceNumber++

	images := []string{}
	for _, m := range openAIReq.Messages {
		images = append(images, m.StringImages...)
	}

	// Track state for streaming
	var currentMessageID string
	var currentContentIndex int
	var accumulatedText string
	var lastEmittedToolCallCount int
	outputIndex := 0
	inToolCallMode := false

	if shouldUseFn {
		// For tool calls, we need to track accumulated result and parse incrementally
		// We'll handle this differently - track the full result and parse tool calls
		accumulatedResult := ""
		tokenCallback := func(token string, tokenUsage backend.TokenUsage) bool {
			accumulatedResult += token
			accumulatedText += token

			// Try to parse tool calls incrementally
			cleanedResult := functions.CleanupLLMResult(accumulatedResult, cfg.FunctionsConfig)

			// Determine XML format from config
			var xmlFormat *functions.XMLToolCallFormat
			if cfg.FunctionsConfig.XMLFormat != nil {
				xmlFormat = cfg.FunctionsConfig.XMLFormat
			} else if cfg.FunctionsConfig.XMLFormatPreset != "" {
				xmlFormat = functions.GetXMLFormatPreset(cfg.FunctionsConfig.XMLFormatPreset)
			}

			// Try XML parsing first
			partialResults, parseErr := functions.ParseXMLIterative(cleanedResult, xmlFormat, true)
			if parseErr == nil && len(partialResults) > lastEmittedToolCallCount {
				// New tool calls detected
				if !inToolCallMode && currentMessageID != "" {
					// Close the current message content part
					sendSSEEvent(c, &schema.ORStreamEvent{
						Type:           "response.content_part.done",
						SequenceNumber: sequenceNumber,
						ItemID:         currentMessageID,
						OutputIndex:    &outputIndex,
						ContentIndex:   &currentContentIndex,
						Part: &schema.ORContentPart{
							Type: "output_text",
							Text: functions.ParseTextContent(cleanedResult, cfg.FunctionsConfig),
						},
					})
					sequenceNumber++
					inToolCallMode = true
				}

				// Emit new tool calls
				for i := lastEmittedToolCallCount; i < len(partialResults); i++ {
					tc := partialResults[i]
					toolCallID := fmt.Sprintf("fc_%s", uuid.New().String())
					outputIndex++

					// Emit function_call item added
					functionCallItem := &schema.ORItemField{
						Type:      "function_call",
						ID:        toolCallID,
						Status:    "in_progress",
						CallID:    toolCallID,
						Name:      tc.Name,
						Arguments: "",
					}
					sendSSEEvent(c, &schema.ORStreamEvent{
						Type:           "response.output_item.added",
						SequenceNumber: sequenceNumber,
						OutputIndex:    &outputIndex,
						Item:           functionCallItem,
					})
					sequenceNumber++

					// Emit arguments delta
					if tc.Arguments != "" {
						sendSSEEvent(c, &schema.ORStreamEvent{
							Type:           "response.function_call_arguments.delta",
							SequenceNumber: sequenceNumber,
							ItemID:         toolCallID,
							OutputIndex:    &outputIndex,
							Delta:          tc.Arguments,
						})
						sequenceNumber++

						// Emit arguments done
						sendSSEEvent(c, &schema.ORStreamEvent{
							Type:           "response.function_call_arguments.done",
							SequenceNumber: sequenceNumber,
							ItemID:         toolCallID,
							OutputIndex:    &outputIndex,
							Arguments:      tc.Arguments,
						})
						sequenceNumber++

						// Emit function_call item done
						functionCallItem.Status = "completed"
						functionCallItem.Arguments = tc.Arguments
						sendSSEEvent(c, &schema.ORStreamEvent{
							Type:           "response.output_item.done",
							SequenceNumber: sequenceNumber,
							OutputIndex:    &outputIndex,
							Item:           functionCallItem,
						})
						sequenceNumber++
					}
				}
				lastEmittedToolCallCount = len(partialResults)
				c.Response().Flush()
				return true
			}

			// Try JSON parsing as fallback
			jsonResults, jsonErr := functions.ParseJSONIterative(cleanedResult, true)
			if jsonErr == nil && len(jsonResults) > lastEmittedToolCallCount {
				for i := lastEmittedToolCallCount; i < len(jsonResults); i++ {
					jsonObj := jsonResults[i]
					if name, ok := jsonObj["name"].(string); ok && name != "" {
						args := "{}"
						if argsVal, ok := jsonObj["arguments"]; ok {
							if argsStr, ok := argsVal.(string); ok {
								args = argsStr
							} else {
								argsBytes, _ := json.Marshal(argsVal)
								args = string(argsBytes)
							}
						}

						toolCallID := fmt.Sprintf("fc_%s", uuid.New().String())
						outputIndex++

						functionCallItem := &schema.ORItemField{
							Type:      "function_call",
							ID:        toolCallID,
							Status:    "completed",
							CallID:    toolCallID,
							Name:      name,
							Arguments: args,
						}
						sendSSEEvent(c, &schema.ORStreamEvent{
							Type:           "response.output_item.added",
							SequenceNumber: sequenceNumber,
							OutputIndex:    &outputIndex,
							Item:           functionCallItem,
						})
						sequenceNumber++

						sendSSEEvent(c, &schema.ORStreamEvent{
							Type:           "response.output_item.done",
							SequenceNumber: sequenceNumber,
							OutputIndex:    &outputIndex,
							Item:           functionCallItem,
						})
						sequenceNumber++
					}
				}
				lastEmittedToolCallCount = len(jsonResults)
				c.Response().Flush()
				return true
			}

			// If no tool calls detected yet, emit text delta
			if !inToolCallMode {
				if currentMessageID == "" {
					// Emit output_item.added for message
					currentMessageID = fmt.Sprintf("msg_%s", uuid.New().String())
					messageItem := &schema.ORItemField{
						Type:   "message",
						ID:     currentMessageID,
						Status: "in_progress",
						Role:   "assistant",
						Content: []schema.ORContentPart{},
					}
					sendSSEEvent(c, &schema.ORStreamEvent{
						Type:           "response.output_item.added",
						SequenceNumber: sequenceNumber,
						OutputIndex:    &outputIndex,
						Item:           messageItem,
					})
					sequenceNumber++

					// Emit content_part.added
					currentContentIndex = 0
					sendSSEEvent(c, &schema.ORStreamEvent{
						Type:           "response.content_part.added",
						SequenceNumber: sequenceNumber,
						ItemID:         currentMessageID,
						OutputIndex:    &outputIndex,
						ContentIndex:   &currentContentIndex,
						Part: &schema.ORContentPart{
							Type: "output_text",
							Text: "",
						},
					})
					sequenceNumber++
				}

				// Emit text delta
				sendSSEEvent(c, &schema.ORStreamEvent{
					Type:           "response.output_text.delta",
					SequenceNumber: sequenceNumber,
					ItemID:         currentMessageID,
					OutputIndex:    &outputIndex,
					ContentIndex:   &currentContentIndex,
					Delta:          token,
					Logprobs:       []schema.ORLogProb{},
				})
				sequenceNumber++
				c.Response().Flush()
			}
			return true
		}

		predFunc, err := backend.ModelInference(
			input.Context, predInput, openAIReq.Messages, images, nil, nil, ml, cfg, nil, nil, tokenCallback, "", "", nil, nil, nil)
		if err != nil {
			xlog.Error("Open Responses stream model inference failed", "error", err)
			sendSSEEvent(c, &schema.ORStreamEvent{
				Type:           "error",
				SequenceNumber: sequenceNumber,
				Error: &schema.ORErrorPayload{
					Type:    "model_error",
					Message: fmt.Sprintf("model inference failed: %v", err),
				},
			})
			sequenceNumber++
			responseFailed := responseCreated
			responseFailed.Status = "failed"
			sendSSEEvent(c, &schema.ORStreamEvent{
				Type:           "response.failed",
				SequenceNumber: sequenceNumber,
				Response:       responseFailed,
			})
			return nil
		}

		prediction, err := predFunc()
		if err != nil {
			xlog.Error("Open Responses stream prediction failed", "error", err)
			sendSSEEvent(c, &schema.ORStreamEvent{
				Type:           "error",
				SequenceNumber: sequenceNumber,
				Error: &schema.ORErrorPayload{
					Type:    "model_error",
					Message: fmt.Sprintf("prediction failed: %v", err),
				},
			})
			sequenceNumber++
			responseFailed := responseCreated
			responseFailed.Status = "failed"
			sendSSEEvent(c, &schema.ORStreamEvent{
				Type:           "response.failed",
				SequenceNumber: sequenceNumber,
				Response:       responseFailed,
			})
			return nil
		}

		result := backend.Finetune(*cfg, predInput, prediction.Response)
		cleanedResult := functions.CleanupLLMResult(result, cfg.FunctionsConfig)
		toolCalls := functions.ParseFunctionCall(cleanedResult, cfg.FunctionsConfig)
		textContent := functions.ParseTextContent(cleanedResult, cfg.FunctionsConfig)

		// Close message if we have text content
		if currentMessageID != "" && textContent != "" && !inToolCallMode {
			// Emit output_text.done
			sendSSEEvent(c, &schema.ORStreamEvent{
				Type:           "response.output_text.done",
				SequenceNumber: sequenceNumber,
				ItemID:         currentMessageID,
				OutputIndex:    &outputIndex,
				ContentIndex:   &currentContentIndex,
				Text:           textContent,
				Logprobs:       []schema.ORLogProb{},
			})
			sequenceNumber++

			// Emit content_part.done
			sendSSEEvent(c, &schema.ORStreamEvent{
				Type:           "response.content_part.done",
				SequenceNumber: sequenceNumber,
				ItemID:         currentMessageID,
				OutputIndex:    &outputIndex,
				ContentIndex:   &currentContentIndex,
				Part: &schema.ORContentPart{
					Type: "output_text",
					Text: textContent,
				},
			})
			sequenceNumber++

			// Emit output_item.done for message
			messageItem := &schema.ORItemField{
				Type:   "message",
				ID:     currentMessageID,
				Status: "completed",
				Role:   "assistant",
				Content: []schema.ORContentPart{{
					Type: "output_text",
					Text: textContent,
				}},
			}
			sendSSEEvent(c, &schema.ORStreamEvent{
				Type:           "response.output_item.done",
				SequenceNumber: sequenceNumber,
				OutputIndex:    &outputIndex,
				Item:           messageItem,
			})
			sequenceNumber++
		}

		// Emit any remaining tool calls that weren't streamed
		for i := lastEmittedToolCallCount; i < len(toolCalls); i++ {
			tc := toolCalls[i]
			toolCallID := fmt.Sprintf("fc_%s", uuid.New().String())
			outputIndex++

			functionCallItem := &schema.ORItemField{
				Type:      "function_call",
				ID:        toolCallID,
				Status:    "completed",
				CallID:    toolCallID,
				Name:      tc.Name,
				Arguments: tc.Arguments,
			}
			sendSSEEvent(c, &schema.ORStreamEvent{
				Type:           "response.output_item.added",
				SequenceNumber: sequenceNumber,
				OutputIndex:    &outputIndex,
				Item:           functionCallItem,
			})
			sequenceNumber++

			sendSSEEvent(c, &schema.ORStreamEvent{
				Type:           "response.output_item.done",
				SequenceNumber: sequenceNumber,
				OutputIndex:    &outputIndex,
				Item:           functionCallItem,
			})
			sequenceNumber++
		}

		// Build final response with all items
		var allOutputItems []schema.ORItemField
		if currentMessageID != "" && textContent != "" {
			allOutputItems = append(allOutputItems, schema.ORItemField{
				Type:   "message",
				ID:     currentMessageID,
				Status: "completed",
				Role:   "assistant",
				Content: []schema.ORContentPart{{
					Type: "output_text",
					Text: textContent,
				}},
			})
		}
		for _, tc := range toolCalls {
			toolCallID := fmt.Sprintf("fc_%s", uuid.New().String())
			allOutputItems = append(allOutputItems, schema.ORItemField{
				Type:      "function_call",
				ID:        toolCallID,
				Status:    "completed",
				CallID:    toolCallID,
				Name:      tc.Name,
				Arguments: tc.Arguments,
			})
		}

		// Emit response.completed
		now := time.Now().Unix()
		responseCompleted := responseCreated
		responseCompleted.Status = "completed"
		responseCompleted.CompletedAt = &now
		responseCompleted.Output = allOutputItems
		responseCompleted.Usage = &schema.ORUsage{
			InputTokens:  prediction.Usage.Prompt,
			OutputTokens: prediction.Usage.Completion,
			TotalTokens:  prediction.Usage.Prompt + prediction.Usage.Completion,
		}
		sendSSEEvent(c, &schema.ORStreamEvent{
			Type:           "response.completed",
			SequenceNumber: sequenceNumber,
			Response:       responseCompleted,
		})

		// Send [DONE]
		fmt.Fprintf(c.Response().Writer, "data: [DONE]\n\n")
		c.Response().Flush()

		return nil
	}

	// Non-tool-call streaming path
	// Emit output_item.added for message
	currentMessageID = fmt.Sprintf("msg_%s", uuid.New().String())
	messageItem := &schema.ORItemField{
		Type:   "message",
		ID:     currentMessageID,
		Status: "in_progress",
		Role:   "assistant",
		Content: []schema.ORContentPart{},
	}
	sendSSEEvent(c, &schema.ORStreamEvent{
		Type:           "response.output_item.added",
		SequenceNumber: sequenceNumber,
		OutputIndex:    &outputIndex,
		Item:           messageItem,
	})
	sequenceNumber++

	// Emit content_part.added
	currentContentIndex = 0
	sendSSEEvent(c, &schema.ORStreamEvent{
		Type:           "response.content_part.added",
		SequenceNumber: sequenceNumber,
		ItemID:         currentMessageID,
		OutputIndex:    &outputIndex,
		ContentIndex:   &currentContentIndex,
		Part: &schema.ORContentPart{
			Type: "output_text",
			Text: "",
		},
	})
	sequenceNumber++

	// Stream text deltas
	tokenCallback := func(token string, tokenUsage backend.TokenUsage) bool {
		accumulatedText += token

		// Emit text delta
		sendSSEEvent(c, &schema.ORStreamEvent{
			Type:           "response.output_text.delta",
			SequenceNumber: sequenceNumber,
			ItemID:         currentMessageID,
			OutputIndex:    &outputIndex,
			ContentIndex:   &currentContentIndex,
			Delta:          token,
			Logprobs:       []schema.ORLogProb{},
		})
		sequenceNumber++
		c.Response().Flush()
		return true
	}

	predFunc, err := backend.ModelInference(
		input.Context, predInput, openAIReq.Messages, images, nil, nil, ml, cfg, nil, nil, tokenCallback, "", "", nil, nil, nil)
	if err != nil {
		xlog.Error("Open Responses stream model inference failed", "error", err)
		sendSSEEvent(c, &schema.ORStreamEvent{
			Type:           "error",
			SequenceNumber: sequenceNumber,
			Error: &schema.ORErrorPayload{
				Type:    "model_error",
				Message: fmt.Sprintf("model inference failed: %v", err),
			},
		})
		sequenceNumber++
		responseFailed := responseCreated
		responseFailed.Status = "failed"
		sendSSEEvent(c, &schema.ORStreamEvent{
			Type:           "response.failed",
			SequenceNumber: sequenceNumber,
			Response:       responseFailed,
		})
		return nil
	}

	prediction, err := predFunc()
	if err != nil {
		xlog.Error("Open Responses stream prediction failed", "error", err)
		sendSSEEvent(c, &schema.ORStreamEvent{
			Type:           "error",
			SequenceNumber: sequenceNumber,
			Error: &schema.ORErrorPayload{
				Type:    "model_error",
				Message: fmt.Sprintf("prediction failed: %v", err),
			},
		})
		sequenceNumber++
		responseFailed := responseCreated
		responseFailed.Status = "failed"
		sendSSEEvent(c, &schema.ORStreamEvent{
			Type:           "response.failed",
			SequenceNumber: sequenceNumber,
			Response:       responseFailed,
		})
		return nil
	}

	result := backend.Finetune(*cfg, predInput, prediction.Response)

	// Emit output_text.done
	sendSSEEvent(c, &schema.ORStreamEvent{
		Type:           "response.output_text.done",
		SequenceNumber: sequenceNumber,
		ItemID:         currentMessageID,
		OutputIndex:    &outputIndex,
		ContentIndex:   &currentContentIndex,
		Text:           result,
		Logprobs:       []schema.ORLogProb{},
	})
	sequenceNumber++

	// Emit content_part.done
	sendSSEEvent(c, &schema.ORStreamEvent{
		Type:           "response.content_part.done",
		SequenceNumber: sequenceNumber,
		ItemID:         currentMessageID,
		OutputIndex:    &outputIndex,
		ContentIndex:   &currentContentIndex,
		Part: &schema.ORContentPart{
			Type: "output_text",
			Text: result,
		},
	})
	sequenceNumber++

	// Emit output_item.done
	messageItem.Status = "completed"
	messageItem.Content = []schema.ORContentPart{{
		Type: "output_text",
		Text: result,
	}}
	sendSSEEvent(c, &schema.ORStreamEvent{
		Type:           "response.output_item.done",
		SequenceNumber: sequenceNumber,
		OutputIndex:    &outputIndex,
		Item:           messageItem,
	})
	sequenceNumber++

	// Emit response.completed
	now := time.Now().Unix()
	responseCompleted := responseCreated
	responseCompleted.Status = "completed"
	responseCompleted.CompletedAt = &now
	responseCompleted.Output = []schema.ORItemField{*messageItem}
	responseCompleted.Usage = &schema.ORUsage{
		InputTokens:  prediction.Usage.Prompt,
		OutputTokens: prediction.Usage.Completion,
		TotalTokens:  prediction.Usage.Prompt + prediction.Usage.Completion,
	}
	sendSSEEvent(c, &schema.ORStreamEvent{
		Type:           "response.completed",
		SequenceNumber: sequenceNumber,
		Response:       responseCompleted,
	})

	// Send [DONE]
	fmt.Fprintf(c.Response().Writer, "data: [DONE]\n\n")
	c.Response().Flush()

	return nil
}

// handleMCPResponse handles responses using MCP agentic loop
func handleMCPResponse(c echo.Context, responseID string, createdAt int64, input *schema.OpenResponsesRequest, cfg *config.ModelConfig, ml *model.ModelLoader, predInput string, openAIReq *schema.OpenAIRequest, appConfig *config.ApplicationConfig) error {
	// This follows the pattern from localai/mcp.go
	// For now, return an error indicating MCP support is coming
	// TODO: Implement full MCP integration
	return sendOpenResponsesError(c, 501, "server_error", "MCP integration not yet implemented", "")
}

// sendSSEEvent sends a Server-Sent Event
func sendSSEEvent(c echo.Context, event *schema.ORStreamEvent) {
	data, err := json.Marshal(event)
	if err != nil {
		xlog.Error("Failed to marshal SSE event", "error", err)
		return
	}
	fmt.Fprintf(c.Response().Writer, "event: %s\ndata: %s\n\n", event.Type, string(data))
}

// sendOpenResponsesError sends an error response
func sendOpenResponsesError(c echo.Context, statusCode int, errorType, message, param string) error {
	errorResp := map[string]interface{}{
		"error": map[string]interface{}{
			"type":    errorType,
			"message": message,
		},
	}
	if param != "" {
		errorResp["error"].(map[string]interface{})["param"] = param
	}
	return c.JSON(statusCode, errorResp)
}
