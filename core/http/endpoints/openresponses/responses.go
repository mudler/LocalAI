package openresponses

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	mcpTools "github.com/mudler/LocalAI/core/http/endpoints/mcp"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/templates"
	"github.com/mudler/LocalAI/pkg/functions"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/utils"
	"github.com/mudler/cogito"
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

		// Initialize store with TTL from appConfig
		store := GetGlobalStore()
		if appConfig.OpenResponsesStoreTTL > 0 {
			store.SetTTL(appConfig.OpenResponsesStoreTTL)
		}

		// Check if storage is disabled for this request
		shouldStore := true
		if input.Store != nil && !*input.Store {
			shouldStore = false
		}

		// Handle previous_response_id if provided
		var previousResponse *schema.ORResponseResource
		var messages []schema.Message
		if input.PreviousResponseID != "" {
			stored, err := store.Get(input.PreviousResponseID)
			if err != nil {
				return sendOpenResponsesError(c, 404, "not_found", fmt.Sprintf("previous response not found: %s", input.PreviousResponseID), "previous_response_id")
			}
			previousResponse = stored.Response

			// Also convert previous response input to messages
			previousInputMessages, err := convertORInputToMessages(stored.Request.Input, cfg)
			if err != nil {
				return sendOpenResponsesError(c, 400, "invalid_request", fmt.Sprintf("failed to convert previous input: %v", err), "")
			}

			// Convert previous response output items to messages
			previousOutputMessages, err := convertOROutputItemsToMessages(previousResponse.Output)
			if err != nil {
				return sendOpenResponsesError(c, 400, "invalid_request", fmt.Sprintf("failed to convert previous response: %v", err), "")
			}

			// Concatenate: previous_input + previous_output + new_input
			// Start with previous input messages
			messages = previousInputMessages
			// Add previous output as assistant messages
			messages = append(messages, previousOutputMessages...)
		}

		// Convert Open Responses input to internal Messages
		newMessages, err := convertORInputToMessages(input.Input, cfg)
		if err != nil {
			return sendOpenResponsesError(c, 400, "invalid_request", fmt.Sprintf("failed to parse input: %v", err), "")
		}
		// Append new input messages
		messages = append(messages, newMessages...)

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
			Messages:  messages,
			Stream:    input.Stream,
			Context:   input.Context,
			Cancel:    input.Cancel,
			Functions: funcs,
		}

		// Handle text_format -> response_format conversion
		if input.TextFormat != nil {
			openAIReq.ResponseFormat = convertTextFormatToResponseFormat(input.TextFormat)
		}

		// Template the prompt
		predInput := evaluator.TemplateMessages(*openAIReq, openAIReq.Messages, cfg, funcs, shouldUseFn)
		xlog.Debug("Open Responses - Prompt (after templating)", "prompt", predInput)

		if useMCP {
			// Use MCP agentic loop
			return handleMCPResponse(c, responseID, createdAt, input, cfg, ml, predInput, openAIReq, appConfig, shouldStore)
		}

		if input.Stream {
			return handleOpenResponsesStream(c, responseID, createdAt, input, cfg, ml, cl, appConfig, predInput, openAIReq, funcs, shouldUseFn, shouldStore)
		}

		return handleOpenResponsesNonStream(c, responseID, createdAt, input, cfg, ml, cl, appConfig, predInput, openAIReq, funcs, shouldUseFn, shouldStore)
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
					Role:          "tool",
					Name:          callID,
					Content:       outputStr,
					StringContent: outputStr,
				})
			case "item_reference":
				// Handle item references - look up item in stored responses
				// According to spec, item_reference uses "id" field, not "item_id"
				itemID, ok := itemMap["id"].(string)
				if !ok || itemID == "" {
					return nil, fmt.Errorf("item_reference missing id")
				}

				store := GetGlobalStore()
				item, responseID, err := store.FindItem(itemID)
				if err != nil {
					return nil, fmt.Errorf("item not found: %s (from response %s): %w", itemID, responseID, err)
				}

				// Log item reference resolution for debugging
				xlog.Debug("Resolved item reference", "item_id", itemID, "response_id", responseID, "item_type", item.Type)

				// Convert referenced item to message based on its type
				msg, err := convertORItemToMessage(item, responseID)
				if err != nil {
					return nil, fmt.Errorf("failed to convert referenced item %s from response %s: %w", itemID, responseID, err)
				}
				messages = append(messages, msg)
			}
		}
		return messages, nil
	default:
		return nil, fmt.Errorf("unsupported input type: %T", input)
	}
}

// convertORItemToMessage converts a single ORItemField to a Message
// responseID is the ID of the response where this item was found (for logging/debugging)
func convertORItemToMessage(item *schema.ORItemField, responseID string) (schema.Message, error) {
	switch item.Type {
	case "message":
		// Convert message item to message
		var textContent string
		if contentParts, ok := item.Content.([]schema.ORContentPart); ok {
			for _, part := range contentParts {
				if part.Type == "output_text" || part.Type == "input_text" {
					textContent += part.Text
				}
			}
		} else if str, ok := item.Content.(string); ok {
			textContent = str
		}
		return schema.Message{
			Role:          item.Role,
			StringContent: textContent,
			Content:       textContent,
		}, nil
	case "function_call_output":
		// Convert function call output to tool role message
		var outputStr string
		if str, ok := item.Output.(string); ok {
			outputStr = str
		} else {
			// Convert to JSON string
			outputBytes, _ := json.Marshal(item.Output)
			outputStr = string(outputBytes)
		}
		return schema.Message{
			Role:          "tool",
			Name:          item.CallID,
			Content:       outputStr,
			StringContent: outputStr,
		}, nil
	default:
		return schema.Message{}, fmt.Errorf("unsupported item type for conversion: %s (from response %s)", item.Type, responseID)
	}
}

// convertOROutputItemsToMessages converts Open Responses output items to internal Messages
func convertOROutputItemsToMessages(outputItems []schema.ORItemField) ([]schema.Message, error) {
	var messages []schema.Message

	for _, item := range outputItems {
		switch item.Type {
		case "message":
			// Convert message item to assistant message
			var textContent string
			if contentParts, ok := item.Content.([]schema.ORContentPart); ok && len(contentParts) > 0 {
				for _, part := range contentParts {
					if part.Type == "output_text" {
						textContent += part.Text
					}
				}
			}
			messages = append(messages, schema.Message{
				Role:          item.Role,
				StringContent: textContent,
				Content:       textContent,
			})
		case "function_call":
			// Function calls are handled separately - they become tool calls in the next turn
			// For now, we skip them as they're part of the model's output, not input
		case "function_call_output":
			// Convert function call output to tool role message
			var outputStr string
			if str, ok := item.Output.(string); ok {
				outputStr = str
			} else {
				// Convert to JSON string
				outputBytes, _ := json.Marshal(item.Output)
				outputStr = string(outputBytes)
			}
			messages = append(messages, schema.Message{
				Role:          "tool",
				Name:          item.CallID,
				Content:       outputStr,
				StringContent: outputStr,
			})
		}
	}

	return messages, nil
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
			case "input_video":
				if videoURL, ok := partMap["video_url"].(string); ok {
					// Convert to base64 data URI
					base64, err := utils.GetContentURIAsBase64(videoURL)
					if err != nil {
						xlog.Error("Failed encoding video", "error", err)
						continue
					}
					stringVideos = append(stringVideos, base64)
				}
			case "input_audio":
				if audioURL, ok := partMap["audio_url"].(string); ok {
					// Convert to base64 data URI
					base64, err := utils.GetContentURIAsBase64(audioURL)
					if err != nil {
						xlog.Error("Failed encoding audio", "error", err)
						continue
					}
					stringAudios = append(stringAudios, base64)
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

	// Build allowed tools set if specified
	allowedSet := make(map[string]bool)
	if len(input.AllowedTools) > 0 {
		for _, name := range input.AllowedTools {
			allowedSet[name] = true
		}
	}

	var funcs functions.Functions
	for _, tool := range input.Tools {
		if tool.Type == "function" {
			// Skip if not in allowed list (when allowed_tools is specified)
			if len(allowedSet) > 0 && !allowedSet[tool.Name] {
				continue
			}
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
			} else if tc == "auto" {
				// "auto" is the default - let model decide whether to use tools
				// Tools are available but not forced
			}
			// If not "required", "none", or "auto", treat as "auto" (default behavior)
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

// convertTextFormatToResponseFormat converts Open Responses text_format to OpenAI response_format
func convertTextFormatToResponseFormat(textFormat interface{}) interface{} {
	switch tf := textFormat.(type) {
	case map[string]interface{}:
		if tfType, ok := tf["type"].(string); ok {
			if tfType == "json_schema" {
				return map[string]interface{}{
					"type":        "json_schema",
					"json_schema": tf,
				}
			}
			return map[string]interface{}{"type": tfType}
		}
	case string:
		return map[string]interface{}{"type": tf}
	}
	return nil
}

// handleOpenResponsesNonStream handles non-streaming responses
func handleOpenResponsesNonStream(c echo.Context, responseID string, createdAt int64, input *schema.OpenResponsesRequest, cfg *config.ModelConfig, ml *model.ModelLoader, cl *config.ModelConfigLoader, appConfig *config.ApplicationConfig, predInput string, openAIReq *schema.OpenAIRequest, funcs functions.Functions, shouldUseFn bool, shouldStore bool) error {
	images := []string{}
	videos := []string{}
	audios := []string{}
	for _, m := range openAIReq.Messages {
		images = append(images, m.StringImages...)
		videos = append(videos, m.StringVideos...)
		audios = append(audios, m.StringAudios...)
	}

	// Serialize tools and tool_choice to JSON strings
	toolsJSON := ""
	if len(input.Tools) > 0 {
		toolsBytes, err := json.Marshal(input.Tools)
		if err == nil {
			toolsJSON = string(toolsBytes)
		}
	}
	toolChoiceJSON := ""
	if input.ToolChoice != nil {
		toolChoiceBytes, err := json.Marshal(input.ToolChoice)
		if err == nil {
			toolChoiceJSON = string(toolChoiceBytes)
		}
	}

	predFunc, err := backend.ModelInference(
		input.Context, predInput, openAIReq.Messages, images, videos, audios, ml, cfg, cl, appConfig, nil, toolsJSON, toolChoiceJSON, nil, nil, nil)
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

	// Set previous_response_id if provided
	if input.PreviousResponseID != "" {
		response.PreviousResponseID = input.PreviousResponseID
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
	if input.Instructions != "" {
		response.Instructions = input.Instructions
	}
	if input.Reasoning != nil {
		response.Reasoning = &schema.ORReasoning{
			Effort:  input.Reasoning.Effort,
			Summary: input.Reasoning.Summary,
		}
	}

	now := time.Now().Unix()
	response.CompletedAt = &now

	// Store response for future reference (if enabled)
	if shouldStore {
		store := GetGlobalStore()
		store.Store(responseID, input, response)
	}

	return c.JSON(200, response)
}

// handleOpenResponsesStream handles streaming responses
func handleOpenResponsesStream(c echo.Context, responseID string, createdAt int64, input *schema.OpenResponsesRequest, cfg *config.ModelConfig, ml *model.ModelLoader, cl *config.ModelConfigLoader, appConfig *config.ApplicationConfig, predInput string, openAIReq *schema.OpenAIRequest, funcs functions.Functions, shouldUseFn bool, shouldStore bool) error {
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

	// Set previous_response_id if provided
	if input.PreviousResponseID != "" {
		responseCreated.PreviousResponseID = input.PreviousResponseID
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
	videos := []string{}
	audios := []string{}
	for _, m := range openAIReq.Messages {
		images = append(images, m.StringImages...)
		videos = append(videos, m.StringVideos...)
		audios = append(audios, m.StringAudios...)
	}

	// Serialize tools and tool_choice to JSON strings
	toolsJSON := ""
	if len(input.Tools) > 0 {
		toolsBytes, err := json.Marshal(input.Tools)
		if err == nil {
			toolsJSON = string(toolsBytes)
		}
	}
	toolChoiceJSON := ""
	if input.ToolChoice != nil {
		toolChoiceBytes, err := json.Marshal(input.ToolChoice)
		if err == nil {
			toolChoiceJSON = string(toolChoiceBytes)
		}
	}

	// Track state for streaming
	var currentMessageID string
	var currentContentIndex int
	var accumulatedText string
	var lastEmittedToolCallCount int
	outputIndex := 0
	inToolCallMode := false

	// Collect all output items for storage
	var collectedOutputItems []schema.ORItemField

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

						// Collect item for storage
						collectedOutputItems = append(collectedOutputItems, *functionCallItem)
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
						Type:    "message",
						ID:      currentMessageID,
						Status:  "in_progress",
						Role:    "assistant",
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
			input.Context, predInput, openAIReq.Messages, images, videos, audios, ml, cfg, cl, appConfig, tokenCallback, toolsJSON, toolChoiceJSON, nil, nil, nil)
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
			// Send [DONE] even on error
			fmt.Fprintf(c.Response().Writer, "data: [DONE]\n\n")
			c.Response().Flush()
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
			// Send [DONE] even on error
			fmt.Fprintf(c.Response().Writer, "data: [DONE]\n\n")
			c.Response().Flush()
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

			// Collect message item for storage
			collectedOutputItems = append(collectedOutputItems, *messageItem)
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

			// Collect function call item for storage
			collectedOutputItems = append(collectedOutputItems, *functionCallItem)
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

		// Echo request parameters in response
		if input.PreviousResponseID != "" {
			responseCompleted.PreviousResponseID = input.PreviousResponseID
		}
		if input.Temperature != nil {
			responseCompleted.Temperature = *input.Temperature
		}
		if input.TopP != nil {
			responseCompleted.TopP = *input.TopP
		}
		if input.MaxOutputTokens != nil {
			responseCompleted.MaxOutputTokens = input.MaxOutputTokens
		}
		if input.Tools != nil {
			responseCompleted.Tools = input.Tools
		}
		if input.ToolChoice != nil {
			responseCompleted.ToolChoice = input.ToolChoice
		}
		if input.Truncation != "" {
			responseCompleted.Truncation = input.Truncation
		}
		if input.Metadata != nil {
			responseCompleted.Metadata = input.Metadata
		}
		if input.Instructions != "" {
			responseCompleted.Instructions = input.Instructions
		}
		if input.Reasoning != nil {
			responseCompleted.Reasoning = &schema.ORReasoning{
				Effort:  input.Reasoning.Effort,
				Summary: input.Reasoning.Summary,
			}
		}

		sendSSEEvent(c, &schema.ORStreamEvent{
			Type:           "response.completed",
			SequenceNumber: sequenceNumber,
			Response:       responseCompleted,
		})

		// Store response for future reference (if enabled)
		if shouldStore {
			store := GetGlobalStore()
			store.Store(responseID, input, responseCompleted)
		}

		// Send [DONE]
		fmt.Fprintf(c.Response().Writer, "data: [DONE]\n\n")
		c.Response().Flush()

		return nil
	}

	// Non-tool-call streaming path
	// Emit output_item.added for message
	currentMessageID = fmt.Sprintf("msg_%s", uuid.New().String())
	messageItem := &schema.ORItemField{
		Type:    "message",
		ID:      currentMessageID,
		Status:  "in_progress",
		Role:    "assistant",
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
		input.Context, predInput, openAIReq.Messages, images, videos, audios, ml, cfg, cl, appConfig, tokenCallback, toolsJSON, toolChoiceJSON, nil, nil, nil)
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
		// Send [DONE] even on error
		fmt.Fprintf(c.Response().Writer, "data: [DONE]\n\n")
		c.Response().Flush()
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
		// Send [DONE] even on error
		fmt.Fprintf(c.Response().Writer, "data: [DONE]\n\n")
		c.Response().Flush()
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

	// Collect final output items (use collected items if available, otherwise use messageItem)
	var finalOutputItems []schema.ORItemField
	if len(collectedOutputItems) > 0 {
		finalOutputItems = collectedOutputItems
	} else {
		finalOutputItems = []schema.ORItemField{*messageItem}
	}
	responseCompleted.Output = finalOutputItems
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

	// Store response for future reference
	store := GetGlobalStore()
	store.Store(responseID, input, responseCompleted)

	// Send [DONE]
	fmt.Fprintf(c.Response().Writer, "data: [DONE]\n\n")
	c.Response().Flush()

	return nil
}

// handleMCPResponse handles responses using MCP agentic loop
func handleMCPResponse(c echo.Context, responseID string, createdAt int64, input *schema.OpenResponsesRequest, cfg *config.ModelConfig, ml *model.ModelLoader, predInput string, openAIReq *schema.OpenAIRequest, appConfig *config.ApplicationConfig, shouldStore bool) error {
	ctx := input.Context
	if ctx == nil {
		ctx = c.Request().Context()
	}

	// Validate MCP config
	if cfg.MCP.Servers == "" && cfg.MCP.Stdio == "" {
		return sendOpenResponsesError(c, 400, "invalid_request", "no MCP servers configured", "")
	}

	// Get MCP config from model config
	remote, stdio, err := cfg.MCP.MCPConfigFromYAML()
	if err != nil {
		return sendOpenResponsesError(c, 500, "server_error", fmt.Sprintf("failed to get MCP config: %v", err), "")
	}

	// Get MCP sessions
	sessions, err := mcpTools.SessionsFromMCPConfig(cfg.Name, remote, stdio)
	if err != nil {
		return sendOpenResponsesError(c, 500, "server_error", fmt.Sprintf("failed to get MCP sessions: %v", err), "")
	}

	if len(sessions) == 0 {
		return sendOpenResponsesError(c, 500, "server_error", "no working MCP servers found", "")
	}

	// Build fragment from messages
	fragment := cogito.NewEmptyFragment()
	for _, message := range openAIReq.Messages {
		fragment = fragment.AddMessage(message.Role, message.StringContent)
	}
	fragmentPtr := &fragment

	// Get API address and key
	_, port, err := net.SplitHostPort(appConfig.APIAddress)
	if err != nil {
		return sendOpenResponsesError(c, 500, "server_error", fmt.Sprintf("failed to parse API address: %v", err), "")
	}
	apiKey := ""
	if len(appConfig.ApiKeys) > 0 {
		apiKey = appConfig.ApiKeys[0]
	}

	ctxWithCancellation, cancel := context.WithCancel(ctx)
	defer cancel()

	// Create OpenAI LLM client
	defaultLLM := cogito.NewOpenAILLM(cfg.Name, apiKey, "http://127.0.0.1:"+port)

	// Build cogito options
	cogitoOpts := cfg.BuildCogitoOptions()
	cogitoOpts = append(
		cogitoOpts,
		cogito.WithContext(ctxWithCancellation),
		cogito.WithMCPs(sessions...),
	)

	if input.Stream {
		return handleMCPStream(c, responseID, createdAt, input, cfg, defaultLLM, fragmentPtr, cogitoOpts, ctxWithCancellation, cancel, shouldStore)
	}

	// Non-streaming mode
	return handleMCPNonStream(c, responseID, createdAt, input, cfg, defaultLLM, fragmentPtr, cogitoOpts, ctxWithCancellation, shouldStore)
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

// handleMCPNonStream handles non-streaming MCP responses
func handleMCPNonStream(c echo.Context, responseID string, createdAt int64, input *schema.OpenResponsesRequest, cfg *config.ModelConfig, defaultLLM cogito.LLM, fragment *cogito.Fragment, cogitoOpts []cogito.Option, ctx context.Context, shouldStore bool) error {
	frag := *fragment
	// Set up callbacks for logging
	cogitoOpts = append(
		cogitoOpts,
		cogito.WithStatusCallback(func(s string) {
			xlog.Debug("[Open Responses MCP] Status", "model", cfg.Name, "status", s)
		}),
		cogito.WithReasoningCallback(func(s string) {
			xlog.Debug("[Open Responses MCP] Reasoning", "model", cfg.Name, "reasoning", s)
		}),
		cogito.WithToolCallBack(func(t *cogito.ToolChoice, state *cogito.SessionState) cogito.ToolCallDecision {
			xlog.Debug("[Open Responses MCP] Tool call", "model", cfg.Name, "tool", t.Name, "reasoning", t.Reasoning, "arguments", t.Arguments)
			return cogito.ToolCallDecision{
				Approved: true,
			}
		}),
		cogito.WithToolCallResultCallback(func(t cogito.ToolStatus) {
			xlog.Debug("[Open Responses MCP] Tool call result", "model", cfg.Name, "tool", t.Name, "result", t.Result, "tool_arguments", t.ToolArguments)
		}),
	)

	// Execute tools
	f, err := cogito.ExecuteTools(defaultLLM, frag, cogitoOpts...)
	if err != nil && !errors.Is(err, cogito.ErrNoToolSelected) {
		return sendOpenResponsesError(c, 500, "model_error", fmt.Sprintf("failed to execute tools: %v", err), "")
	}

	// Get final response
	f, err = defaultLLM.Ask(ctx, f)
	if err != nil {
		return sendOpenResponsesError(c, 500, "model_error", fmt.Sprintf("failed to get response: %v", err), "")
	}

	// Convert fragment to Open Responses format
	fPtr := &f
	outputItems := convertCogitoFragmentToORItems(fPtr)

	// Build response
	now := time.Now().Unix()
	response := &schema.ORResponseResource{
		ID:          responseID,
		Object:      "response",
		CreatedAt:   createdAt,
		CompletedAt: &now,
		Status:      "completed",
		Model:       input.Model,
		Output:      outputItems,
	}

	if input.PreviousResponseID != "" {
		response.PreviousResponseID = input.PreviousResponseID
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
	if input.Metadata != nil {
		response.Metadata = input.Metadata
	}

	// Store response (if enabled)
	if shouldStore {
		store := GetGlobalStore()
		store.Store(responseID, input, response)
	}

	return c.JSON(200, response)
}

// handleMCPStream handles streaming MCP responses
func handleMCPStream(c echo.Context, responseID string, createdAt int64, input *schema.OpenResponsesRequest, cfg *config.ModelConfig, defaultLLM cogito.LLM, fragment *cogito.Fragment, cogitoOpts []cogito.Option, ctx context.Context, cancel context.CancelFunc, shouldStore bool) error {
	frag := *fragment
	// Set SSE headers
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
	if input.PreviousResponseID != "" {
		responseCreated.PreviousResponseID = input.PreviousResponseID
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

	// Create channels for streaming events
	events := make(chan interface{})
	ended := make(chan error, 1)
	var collectedOutputItems []schema.ORItemField
	outputIndex := 0

	// Set up callbacks
	statusCallback := func(s string) {
		events <- map[string]interface{}{
			"type":    "status",
			"message": s,
		}
	}

	reasoningCallback := func(s string) {
		itemID := fmt.Sprintf("reasoning_%s", uuid.New().String())
		outputIndex++
		item := &schema.ORItemField{
			Type:   "reasoning",
			ID:     itemID,
			Status: "in_progress",
		}
		collectedOutputItems = append(collectedOutputItems, *item)

		events <- map[string]interface{}{
			"type":         "reasoning",
			"item_id":      itemID,
			"output_index": outputIndex,
			"content":      s,
		}
	}

	toolCallCallback := func(t *cogito.ToolChoice, state *cogito.SessionState) cogito.ToolCallDecision {
		toolCallID := fmt.Sprintf("fc_%s", uuid.New().String())
		outputIndex++
		item := &schema.ORItemField{
			Type:      "function_call",
			ID:        toolCallID,
			Status:    "in_progress",
			CallID:    toolCallID,
			Name:      t.Name,
			Arguments: "",
		}
		collectedOutputItems = append(collectedOutputItems, *item)

		events <- map[string]interface{}{
			"type":         "tool_call",
			"item_id":      toolCallID,
			"output_index": outputIndex,
			"name":         t.Name,
			"arguments":    t.Arguments,
			"reasoning":    t.Reasoning,
		}
		return cogito.ToolCallDecision{
			Approved: true,
		}
	}

	toolCallResultCallback := func(t cogito.ToolStatus) {
		outputIndex++
		callID := fmt.Sprintf("fc_%s", uuid.New().String())
		item := schema.ORItemField{
			Type:   "function_call_output",
			ID:     fmt.Sprintf("fco_%s", uuid.New().String()),
			Status: "completed",
			CallID: callID,
			Output: t.Result,
		}
		collectedOutputItems = append(collectedOutputItems, item)

		events <- map[string]interface{}{
			"type":         "tool_result",
			"item_id":      item.ID,
			"output_index": outputIndex,
			"name":         t.Name,
			"result":       t.Result,
		}
	}

	cogitoOpts = append(cogitoOpts,
		cogito.WithStatusCallback(statusCallback),
		cogito.WithReasoningCallback(reasoningCallback),
		cogito.WithToolCallBack(toolCallCallback),
		cogito.WithToolCallResultCallback(toolCallResultCallback),
	)

	// Execute tools in goroutine
	go func() {
		defer close(events)

		f, err := cogito.ExecuteTools(defaultLLM, frag, cogitoOpts...)
		if err != nil && !errors.Is(err, cogito.ErrNoToolSelected) {
			events <- map[string]interface{}{
				"type":    "error",
				"message": fmt.Sprintf("Failed to execute tools: %v", err),
			}
			ended <- err
			return
		}

		// Get final response
		f, err = defaultLLM.Ask(ctx, f)
		if err != nil {
			events <- map[string]interface{}{
				"type":    "error",
				"message": fmt.Sprintf("Failed to get response: %v", err),
			}
			ended <- err
			return
		}

		// Stream final assistant message
		content := f.LastMessage().Content
		messageID := fmt.Sprintf("msg_%s", uuid.New().String())
		outputIndex++
		item := schema.ORItemField{
			Type:   "message",
			ID:     messageID,
			Status: "completed",
			Role:   "assistant",
			Content: []schema.ORContentPart{{
				Type: "output_text",
				Text: content,
			}},
		}
		collectedOutputItems = append(collectedOutputItems, item)

		events <- map[string]interface{}{
			"type":         "assistant",
			"item_id":      messageID,
			"output_index": outputIndex,
			"content":      content,
		}

		ended <- nil
	}()

	// Stream events to client
LOOP:
	for {
		select {
		case <-ctx.Done():
			cancel()
			break LOOP
		case event := <-events:
			if event == nil {
				break LOOP
			}
			// Convert event to Open Responses format and send
			if err := sendMCPEventAsOR(c, event, &sequenceNumber); err != nil {
				cancel()
				return err
			}
			c.Response().Flush()
		case err := <-ended:
			if err == nil {
				// Emit response.completed
				now := time.Now().Unix()
				responseCompleted := responseCreated
				responseCompleted.Status = "completed"
				responseCompleted.CompletedAt = &now
				responseCompleted.Output = collectedOutputItems
				sendSSEEvent(c, &schema.ORStreamEvent{
					Type:           "response.completed",
					SequenceNumber: sequenceNumber,
					Response:       responseCompleted,
				})
				sequenceNumber++

				// Store response
				store := GetGlobalStore()
				store.Store(responseID, input, responseCompleted)

				// Send [DONE]
				fmt.Fprintf(c.Response().Writer, "data: [DONE]\n\n")
				c.Response().Flush()
				break LOOP
			}
			// Send error
			sendSSEEvent(c, &schema.ORStreamEvent{
				Type:           "error",
				SequenceNumber: sequenceNumber,
				Error: &schema.ORErrorPayload{
					Type:    "model_error",
					Message: err.Error(),
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
			fmt.Fprintf(c.Response().Writer, "data: [DONE]\n\n")
			c.Response().Flush()
			return nil
		}
	}

	return nil
}

// convertCogitoFragmentToORItems converts a cogito fragment to Open Responses items
func convertCogitoFragmentToORItems(f *cogito.Fragment) []schema.ORItemField {
	var items []schema.ORItemField

	// Get the last message (assistant response)
	lastMsg := f.LastMessage()
	if lastMsg != nil && lastMsg.Content != "" {
		items = append(items, schema.ORItemField{
			Type:   "message",
			ID:     fmt.Sprintf("msg_%s", uuid.New().String()),
			Status: "completed",
			Role:   "assistant",
			Content: []schema.ORContentPart{{
				Type: "output_text",
				Text: lastMsg.Content,
			}},
		})
	}

	return items
}

// sendMCPEventAsOR converts MCP events to Open Responses format and sends them
func sendMCPEventAsOR(c echo.Context, event interface{}, sequenceNumber *int) error {
	eventMap, ok := event.(map[string]interface{})
	if !ok {
		return nil
	}

	eventType, _ := eventMap["type"].(string)
	switch eventType {
	case "status":
		// Status events are informational, skip for now
		return nil
	case "reasoning":
		itemID, _ := eventMap["item_id"].(string)
		outputIndex, _ := eventMap["output_index"].(int)

		item := &schema.ORItemField{
			Type:   "reasoning",
			ID:     itemID,
			Status: "in_progress",
		}
		sendSSEEvent(c, &schema.ORStreamEvent{
			Type:           "response.output_item.added",
			SequenceNumber: *sequenceNumber,
			OutputIndex:    &outputIndex,
			Item:           item,
		})
		*sequenceNumber++
		// Note: reasoning content streaming would go here
		return nil
	case "tool_call":
		itemID, _ := eventMap["item_id"].(string)
		outputIndex, _ := eventMap["output_index"].(int)
		name, _ := eventMap["name"].(string)
		arguments, _ := eventMap["arguments"].(string)

		item := &schema.ORItemField{
			Type:      "function_call",
			ID:        itemID,
			Status:    "in_progress",
			CallID:    itemID,
			Name:      name,
			Arguments: "",
		}
		sendSSEEvent(c, &schema.ORStreamEvent{
			Type:           "response.output_item.added",
			SequenceNumber: *sequenceNumber,
			OutputIndex:    &outputIndex,
			Item:           item,
		})
		*sequenceNumber++

		// Emit arguments
		if arguments != "" {
			sendSSEEvent(c, &schema.ORStreamEvent{
				Type:           "response.function_call_arguments.delta",
				SequenceNumber: *sequenceNumber,
				ItemID:         itemID,
				OutputIndex:    &outputIndex,
				Delta:          arguments,
			})
			*sequenceNumber++

			item.Status = "completed"
			item.Arguments = arguments
			sendSSEEvent(c, &schema.ORStreamEvent{
				Type:           "response.function_call_arguments.done",
				SequenceNumber: *sequenceNumber,
				ItemID:         itemID,
				OutputIndex:    &outputIndex,
				Arguments:      arguments,
			})
			*sequenceNumber++

			sendSSEEvent(c, &schema.ORStreamEvent{
				Type:           "response.output_item.done",
				SequenceNumber: *sequenceNumber,
				OutputIndex:    &outputIndex,
				Item:           item,
			})
			*sequenceNumber++
		}
		return nil
	case "tool_result":
		itemID, _ := eventMap["item_id"].(string)
		outputIndex, _ := eventMap["output_index"].(int)
		result, _ := eventMap["result"].(string)

		item := &schema.ORItemField{
			Type:   "function_call_output",
			ID:     itemID,
			Status: "completed",
			Output: result,
		}
		sendSSEEvent(c, &schema.ORStreamEvent{
			Type:           "response.output_item.added",
			SequenceNumber: *sequenceNumber,
			OutputIndex:    &outputIndex,
			Item:           item,
		})
		*sequenceNumber++
		sendSSEEvent(c, &schema.ORStreamEvent{
			Type:           "response.output_item.done",
			SequenceNumber: *sequenceNumber,
			OutputIndex:    &outputIndex,
			Item:           item,
		})
		*sequenceNumber++
		return nil
	case "assistant":
		itemID, _ := eventMap["item_id"].(string)
		outputIndex, _ := eventMap["output_index"].(int)
		content, _ := eventMap["content"].(string)

		item := &schema.ORItemField{
			Type:    "message",
			ID:      itemID,
			Status:  "in_progress",
			Role:    "assistant",
			Content: []schema.ORContentPart{},
		}
		sendSSEEvent(c, &schema.ORStreamEvent{
			Type:           "response.output_item.added",
			SequenceNumber: *sequenceNumber,
			OutputIndex:    &outputIndex,
			Item:           item,
		})
		*sequenceNumber++

		// Emit content part
		sendSSEEvent(c, &schema.ORStreamEvent{
			Type:           "response.content_part.added",
			SequenceNumber: *sequenceNumber,
			ItemID:         itemID,
			OutputIndex:    &outputIndex,
			ContentIndex:   func() *int { i := 0; return &i }(),
			Part: &schema.ORContentPart{
				Type: "output_text",
				Text: "",
			},
		})
		*sequenceNumber++

		// Emit text done
		sendSSEEvent(c, &schema.ORStreamEvent{
			Type:           "response.output_text.done",
			SequenceNumber: *sequenceNumber,
			ItemID:         itemID,
			OutputIndex:    &outputIndex,
			ContentIndex:   func() *int { i := 0; return &i }(),
			Text:           content,
			Logprobs:       []schema.ORLogProb{},
		})
		*sequenceNumber++

		// Emit content part done
		sendSSEEvent(c, &schema.ORStreamEvent{
			Type:           "response.content_part.done",
			SequenceNumber: *sequenceNumber,
			ItemID:         itemID,
			OutputIndex:    &outputIndex,
			ContentIndex:   func() *int { i := 0; return &i }(),
			Part: &schema.ORContentPart{
				Type: "output_text",
				Text: content,
			},
		})
		*sequenceNumber++

		// Emit item done
		item.Status = "completed"
		item.Content = []schema.ORContentPart{{
			Type: "output_text",
			Text: content,
		}}
		sendSSEEvent(c, &schema.ORStreamEvent{
			Type:           "response.output_item.done",
			SequenceNumber: *sequenceNumber,
			OutputIndex:    &outputIndex,
			Item:           item,
		})
		*sequenceNumber++
		return nil
	case "error":
		message, _ := eventMap["message"].(string)
		sendSSEEvent(c, &schema.ORStreamEvent{
			Type:           "error",
			SequenceNumber: *sequenceNumber,
			Error: &schema.ORErrorPayload{
				Type:    "model_error",
				Message: message,
			},
		})
		*sequenceNumber++
		return nil
	}

	return nil
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
