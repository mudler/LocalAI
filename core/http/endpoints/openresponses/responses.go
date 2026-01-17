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

		// Generate grammar for function calling (similar to OpenAI chat endpoint)
		if shouldUseFn && !cfg.FunctionsConfig.GrammarConfig.NoGrammar {
			// Add no-action function to allow model to respond without calling a tool
			noActionName := "answer"
			noActionDescription := "use this action to answer without performing any action"
			if cfg.FunctionsConfig.NoActionFunctionName != "" {
				noActionName = cfg.FunctionsConfig.NoActionFunctionName
			}
			if cfg.FunctionsConfig.NoActionDescriptionName != "" {
				noActionDescription = cfg.FunctionsConfig.NoActionDescriptionName
			}

			noActionGrammar := functions.Function{
				Name:        noActionName,
				Description: noActionDescription,
				Parameters: map[string]interface{}{
					"properties": map[string]interface{}{
						"message": map[string]interface{}{
							"type":        "string",
							"description": "The message to reply the user with",
						},
					},
				},
			}

			// Make a copy of funcs to avoid modifying the original
			funcsWithNoAction := make(functions.Functions, len(funcs))
			copy(funcsWithNoAction, funcs)

			// Append no-action function unless disabled
			if !cfg.FunctionsConfig.DisableNoAction {
				funcsWithNoAction = append(funcsWithNoAction, noActionGrammar)
			}

			// Force picking one of the functions by the request
			if cfg.FunctionToCall() != "" {
				funcsWithNoAction = funcsWithNoAction.Select(cfg.FunctionToCall())
			}

			// Generate grammar to constrain model output to valid function calls
			jsStruct := funcsWithNoAction.ToJSONStructure(cfg.FunctionsConfig.FunctionNameKey, cfg.FunctionsConfig.FunctionNameKey)
			g, err := jsStruct.Grammar(cfg.FunctionsConfig.GrammarOptions()...)
			if err == nil {
				cfg.Grammar = g
				xlog.Debug("Open Responses - Generated grammar for function calling")
			} else {
				xlog.Error("Open Responses - Failed generating grammar for function calling", "error", err)
			}
		}

		// Template the prompt
		predInput := evaluator.TemplateMessages(*openAIReq, openAIReq.Messages, cfg, funcs, shouldUseFn)
		xlog.Debug("Open Responses - Prompt (after templating)", "prompt", predInput)

		// Handle background mode
		isBackground := input.Background != nil && *input.Background
		if isBackground {
			// Background mode requires storage
			if !shouldStore {
				return sendOpenResponsesError(c, 400, "invalid_request_error", "background=true requires store=true", "background")
			}

			// Create initial response with "queued" status
			queuedResponse := buildORResponse(responseID, createdAt, nil, schema.ORStatusQueued, input, []schema.ORItemField{}, nil, true)

			// Create cancellable context for background execution
			bgCtx, bgCancel := context.WithCancel(context.Background())

			// Store the background response
			store.StoreBackground(responseID, input, queuedResponse, bgCancel, input.Stream)

			// Start background processing goroutine
			go func() {
				defer bgCancel()

				// Update status to in_progress
				store.UpdateStatus(responseID, schema.ORStatusInProgress, nil)

				var finalResponse *schema.ORResponseResource
				var bgErr error

				if useMCP {
					// Background MCP processing
					finalResponse, bgErr = handleBackgroundMCPResponse(bgCtx, store, responseID, createdAt, input, cfg, ml, predInput, openAIReq, appConfig)
				} else if input.Stream {
					// Background streaming processing (buffer events)
					finalResponse, bgErr = handleBackgroundStream(bgCtx, store, responseID, createdAt, input, cfg, ml, cl, appConfig, predInput, openAIReq, funcs, shouldUseFn)
				} else {
					// Background non-streaming processing
					finalResponse, bgErr = handleBackgroundNonStream(bgCtx, store, responseID, createdAt, input, cfg, ml, cl, appConfig, predInput, openAIReq, funcs, shouldUseFn)
				}

				if bgErr != nil {
					xlog.Error("Background response failed", "response_id", responseID, "error", bgErr)
					now := time.Now().Unix()
					store.UpdateStatus(responseID, schema.ORStatusFailed, &now)
					return
				}

				// Update final response in store
				if finalResponse != nil {
					store.UpdateResponse(responseID, finalResponse)
				}
			}()

			// Return immediately with queued response
			return c.JSON(200, queuedResponse)
		}

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

// handleBackgroundNonStream handles background non-streaming responses
func handleBackgroundNonStream(ctx context.Context, store *ResponseStore, responseID string, createdAt int64, input *schema.OpenResponsesRequest, cfg *config.ModelConfig, ml *model.ModelLoader, cl *config.ModelConfigLoader, appConfig *config.ApplicationConfig, predInput string, openAIReq *schema.OpenAIRequest, funcs functions.Functions, shouldUseFn bool) (*schema.ORResponseResource, error) {
	images := []string{}
	videos := []string{}
	audios := []string{}
	for _, m := range openAIReq.Messages {
		images = append(images, m.StringImages...)
		videos = append(videos, m.StringVideos...)
		audios = append(audios, m.StringAudios...)
	}

	toolsJSON := serializeToolsForBackend(input.Tools)
	toolChoiceJSON := ""
	if input.ToolChoice != nil {
		toolChoiceBytes, err := json.Marshal(input.ToolChoice)
		if err == nil {
			toolChoiceJSON = string(toolChoiceBytes)
		}
	}

	var logprobs *int
	if input.TopLogprobs != nil && *input.TopLogprobs > 0 {
		logprobs = input.TopLogprobs
	}

	predFunc, err := backend.ModelInference(
		ctx, predInput, openAIReq.Messages, images, videos, audios, ml, cfg, cl, appConfig, nil, toolsJSON, toolChoiceJSON, logprobs, input.TopLogprobs, input.LogitBias)
	if err != nil {
		return nil, fmt.Errorf("model inference failed: %w", err)
	}

	// Check for cancellation
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	prediction, err := predFunc()
	if err != nil {
		return nil, fmt.Errorf("prediction failed: %w", err)
	}

	result := backend.Finetune(*cfg, predInput, prediction.Response)

	// Parse tool calls if using functions (same logic as regular handler)
	var outputItems []schema.ORItemField
	var toolCalls []schema.ToolCall

	if shouldUseFn {
		cleanedResult := functions.CleanupLLMResult(result, cfg.FunctionsConfig)
		funcCallResults := functions.ParseFunctionCall(cleanedResult, cfg.FunctionsConfig)
		textContent := functions.ParseTextContent(cleanedResult, cfg.FunctionsConfig)

		noActionName := "answer"
		if cfg.FunctionsConfig.NoActionFunctionName != "" {
			noActionName = cfg.FunctionsConfig.NoActionFunctionName
		}

		for i, fc := range funcCallResults {
			if fc.Name == noActionName {
				if fc.Arguments != "" {
					var args map[string]interface{}
					if err := json.Unmarshal([]byte(fc.Arguments), &args); err == nil {
						if msg, ok := args["message"].(string); ok && msg != "" {
							textContent = msg
						}
					}
				}
				continue
			}
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

		if textContent != "" {
			outputItems = append(outputItems, schema.ORItemField{
				Type:    "message",
				ID:      fmt.Sprintf("msg_%s", uuid.New().String()),
				Status:  "completed",
				Role:    "assistant",
				Content: []schema.ORContentPart{makeOutputTextPartWithLogprobs(textContent, prediction.Logprobs)},
			})
		}

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

		if len(outputItems) == 0 && result != "" {
			outputItems = append(outputItems, schema.ORItemField{
				Type:    "message",
				ID:      fmt.Sprintf("msg_%s", uuid.New().String()),
				Status:  "completed",
				Role:    "assistant",
				Content: []schema.ORContentPart{makeOutputTextPartWithLogprobs(result, prediction.Logprobs)},
			})
		}
	} else {
		outputItems = append(outputItems, schema.ORItemField{
			Type:    "message",
			ID:      fmt.Sprintf("msg_%s", uuid.New().String()),
			Status:  "completed",
			Role:    "assistant",
			Content: []schema.ORContentPart{makeOutputTextPartWithLogprobs(result, prediction.Logprobs)},
		})
	}

	now := time.Now().Unix()
	response := buildORResponse(responseID, createdAt, &now, schema.ORStatusCompleted, input, outputItems, &schema.ORUsage{
		InputTokens:  prediction.Usage.Prompt,
		OutputTokens: prediction.Usage.Completion,
		TotalTokens:  prediction.Usage.Prompt + prediction.Usage.Completion,
	}, true)

	return response, nil
}

// handleBackgroundStream handles background streaming responses with event buffering
func handleBackgroundStream(ctx context.Context, store *ResponseStore, responseID string, createdAt int64, input *schema.OpenResponsesRequest, cfg *config.ModelConfig, ml *model.ModelLoader, cl *config.ModelConfigLoader, appConfig *config.ApplicationConfig, predInput string, openAIReq *schema.OpenAIRequest, funcs functions.Functions, shouldUseFn bool) (*schema.ORResponseResource, error) {
	images := []string{}
	videos := []string{}
	audios := []string{}
	for _, m := range openAIReq.Messages {
		images = append(images, m.StringImages...)
		videos = append(videos, m.StringVideos...)
		audios = append(audios, m.StringAudios...)
	}

	toolsJSON := serializeToolsForBackend(input.Tools)
	toolChoiceJSON := ""
	if input.ToolChoice != nil {
		toolChoiceBytes, err := json.Marshal(input.ToolChoice)
		if err == nil {
			toolChoiceJSON = string(toolChoiceBytes)
		}
	}

	sequenceNumber := 0

	// Emit response.created
	responseCreated := buildORResponse(responseID, createdAt, nil, schema.ORStatusInProgress, input, []schema.ORItemField{}, nil, true)
	bufferEvent(store, responseID, &schema.ORStreamEvent{
		Type:           "response.created",
		SequenceNumber: sequenceNumber,
		Response:       responseCreated,
	})
	sequenceNumber++

	// Emit response.in_progress
	bufferEvent(store, responseID, &schema.ORStreamEvent{
		Type:           "response.in_progress",
		SequenceNumber: sequenceNumber,
		Response:       responseCreated,
	})
	sequenceNumber++

	var accumulatedText string
	var collectedOutputItems []schema.ORItemField
	outputIndex := 0
	currentMessageID := fmt.Sprintf("msg_%s", uuid.New().String())

	// Emit output_item.added
	messageItem := &schema.ORItemField{
		Type:    "message",
		ID:      currentMessageID,
		Status:  "in_progress",
		Role:    "assistant",
		Content: []schema.ORContentPart{},
	}
	bufferEvent(store, responseID, &schema.ORStreamEvent{
		Type:           "response.output_item.added",
		SequenceNumber: sequenceNumber,
		OutputIndex:    &outputIndex,
		Item:           messageItem,
	})
	sequenceNumber++

	// Emit content_part.added
	currentContentIndex := 0
	emptyPart := makeOutputTextPart("")
	bufferEvent(store, responseID, &schema.ORStreamEvent{
		Type:           "response.content_part.added",
		SequenceNumber: sequenceNumber,
		ItemID:         currentMessageID,
		OutputIndex:    &outputIndex,
		ContentIndex:   &currentContentIndex,
		Part:           &emptyPart,
	})
	sequenceNumber++

	// Token callback for streaming
	tokenCallback := func(token string, tokenUsage backend.TokenUsage) bool {
		select {
		case <-ctx.Done():
			return false
		default:
		}

		accumulatedText += token

		// Buffer text delta
		bufferEvent(store, responseID, &schema.ORStreamEvent{
			Type:           "response.output_text.delta",
			SequenceNumber: sequenceNumber,
			ItemID:         currentMessageID,
			OutputIndex:    &outputIndex,
			ContentIndex:   &currentContentIndex,
			Delta:          strPtr(token),
			Logprobs:       emptyLogprobs(),
		})
		sequenceNumber++
		return true
	}

	var streamLogprobs *int
	if input.TopLogprobs != nil && *input.TopLogprobs > 0 {
		streamLogprobs = input.TopLogprobs
	}

	predFunc, err := backend.ModelInference(
		ctx, predInput, openAIReq.Messages, images, videos, audios, ml, cfg, cl, appConfig, tokenCallback, toolsJSON, toolChoiceJSON, streamLogprobs, input.TopLogprobs, input.LogitBias)
	if err != nil {
		return nil, fmt.Errorf("model inference failed: %w", err)
	}

	prediction, err := predFunc()
	if err != nil {
		return nil, fmt.Errorf("prediction failed: %w", err)
	}

	// Emit output_text.done
	streamEventLogprobs := convertLogprobsForStreaming(prediction.Logprobs)
	bufferEvent(store, responseID, &schema.ORStreamEvent{
		Type:           "response.output_text.done",
		SequenceNumber: sequenceNumber,
		ItemID:         currentMessageID,
		OutputIndex:    &outputIndex,
		ContentIndex:   &currentContentIndex,
		Text:           strPtr(accumulatedText),
		Logprobs:       logprobsPtr(streamEventLogprobs),
	})
	sequenceNumber++

	// Emit content_part.done
	textPart := makeOutputTextPartWithLogprobs(accumulatedText, prediction.Logprobs)
	bufferEvent(store, responseID, &schema.ORStreamEvent{
		Type:           "response.content_part.done",
		SequenceNumber: sequenceNumber,
		ItemID:         currentMessageID,
		OutputIndex:    &outputIndex,
		ContentIndex:   &currentContentIndex,
		Part:           &textPart,
	})
	sequenceNumber++

	// Emit output_item.done
	completedMessageItem := &schema.ORItemField{
		Type:    "message",
		ID:      currentMessageID,
		Status:  "completed",
		Role:    "assistant",
		Content: []schema.ORContentPart{makeOutputTextPartWithLogprobs(accumulatedText, prediction.Logprobs)},
	}
	bufferEvent(store, responseID, &schema.ORStreamEvent{
		Type:           "response.output_item.done",
		SequenceNumber: sequenceNumber,
		OutputIndex:    &outputIndex,
		Item:           completedMessageItem,
	})
	sequenceNumber++
	collectedOutputItems = append(collectedOutputItems, *completedMessageItem)

	// Build final response
	now := time.Now().Unix()
	response := buildORResponse(responseID, createdAt, &now, schema.ORStatusCompleted, input, collectedOutputItems, &schema.ORUsage{
		InputTokens:  prediction.Usage.Prompt,
		OutputTokens: prediction.Usage.Completion,
		TotalTokens:  prediction.Usage.Prompt + prediction.Usage.Completion,
	}, true)

	// Emit response.completed
	bufferEvent(store, responseID, &schema.ORStreamEvent{
		Type:           "response.completed",
		SequenceNumber: sequenceNumber,
		Response:       response,
	})

	return response, nil
}

// handleBackgroundMCPResponse handles background MCP responses
func handleBackgroundMCPResponse(ctx context.Context, store *ResponseStore, responseID string, createdAt int64, input *schema.OpenResponsesRequest, cfg *config.ModelConfig, ml *model.ModelLoader, predInput string, openAIReq *schema.OpenAIRequest, appConfig *config.ApplicationConfig) (*schema.ORResponseResource, error) {
	// For now, MCP background is not fully implemented - return a simple response
	// Full MCP background support would require significant refactoring of the cogito integration
	xlog.Warn("Background MCP requests are not fully supported yet", "response_id", responseID)

	now := time.Now().Unix()
	response := buildORResponse(responseID, createdAt, &now, schema.ORStatusFailed, input, []schema.ORItemField{}, nil, true)
	response.Error = &schema.ORError{
		Type:    "server_error",
		Message: "Background MCP requests are not yet supported",
	}

	return response, fmt.Errorf("background MCP requests are not yet supported")
}

// bufferEvent stores an SSE event in the response store for streaming resume
func bufferEvent(store *ResponseStore, responseID string, event *schema.ORStreamEvent) {
	if err := store.AppendEvent(responseID, event); err != nil {
		xlog.Error("Failed to buffer event", "response_id", responseID, "error", err)
	}
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

	// Convert and serialize tools to OpenAI format for the backend
	toolsJSON := serializeToolsForBackend(input.Tools)
	toolChoiceJSON := ""
	if input.ToolChoice != nil {
		toolChoiceBytes, err := json.Marshal(input.ToolChoice)
		if err == nil {
			toolChoiceJSON = string(toolChoiceBytes)
		}
	}

	// Pass logprobs and logit_bias parameters if requested
	var logprobs *int
	if input.TopLogprobs != nil && *input.TopLogprobs > 0 {
		logprobs = input.TopLogprobs
	}

	predFunc, err := backend.ModelInference(
		input.Context, predInput, openAIReq.Messages, images, videos, audios, ml, cfg, cl, appConfig, nil, toolsJSON, toolChoiceJSON, logprobs, input.TopLogprobs, input.LogitBias)
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
	xlog.Debug("Open Responses - Raw model result", "result", result, "shouldUseFn", shouldUseFn)

	// Parse tool calls if using functions
	var outputItems []schema.ORItemField
	var toolCalls []schema.ToolCall

	if shouldUseFn {
		// Clean up the result first (handle reasoning tags, etc.)
		cleanedResult := functions.CleanupLLMResult(result, cfg.FunctionsConfig)
		xlog.Debug("Open Responses - Cleaned result", "cleanedResult", cleanedResult)

		funcCallResults := functions.ParseFunctionCall(cleanedResult, cfg.FunctionsConfig)
		textContent := functions.ParseTextContent(cleanedResult, cfg.FunctionsConfig)
		xlog.Debug("Open Responses - Parsed function calls", "count", len(funcCallResults), "textContent", textContent)

		// Check for noAction function (model chose to respond without tool)
		noActionName := "answer"
		if cfg.FunctionsConfig.NoActionFunctionName != "" {
			noActionName = cfg.FunctionsConfig.NoActionFunctionName
		}

		// Filter out noAction calls and extract the message
		for i, fc := range funcCallResults {
			if fc.Name == noActionName {
				// This is a text response, not a tool call
				// Try to extract the message from the arguments
				if fc.Arguments != "" {
					var args map[string]interface{}
					if err := json.Unmarshal([]byte(fc.Arguments), &args); err == nil {
						if msg, ok := args["message"].(string); ok && msg != "" {
							textContent = msg
						}
					}
				}
				continue
			}
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

		// Add message item with text content (include logprobs if available)
		if textContent != "" {
			outputItems = append(outputItems, schema.ORItemField{
				Type:    "message",
				ID:      fmt.Sprintf("msg_%s", uuid.New().String()),
				Status:  "completed",
				Role:    "assistant",
				Content: []schema.ORContentPart{makeOutputTextPartWithLogprobs(textContent, prediction.Logprobs)},
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

		// If we have no output items but the model did produce output, include the raw result as a message
		// This handles cases where the function call parsing failed but we still have model output
		if len(outputItems) == 0 && result != "" {
			xlog.Debug("Open Responses - No parsed output, falling back to raw result")
			outputItems = append(outputItems, schema.ORItemField{
				Type:    "message",
				ID:      fmt.Sprintf("msg_%s", uuid.New().String()),
				Status:  "completed",
				Role:    "assistant",
				Content: []schema.ORContentPart{makeOutputTextPartWithLogprobs(result, prediction.Logprobs)},
			})
		}
	} else {
		// Simple text response (include logprobs if available)
		outputItems = []schema.ORItemField{
			{
				Type:    "message",
				ID:      fmt.Sprintf("msg_%s", uuid.New().String()),
				Status:  "completed",
				Role:    "assistant",
				Content: []schema.ORContentPart{makeOutputTextPartWithLogprobs(result, prediction.Logprobs)},
			},
		}
	}

	// Build response with all required fields
	now := time.Now().Unix()
	response := buildORResponse(responseID, createdAt, &now, "completed", input, outputItems, &schema.ORUsage{
		InputTokens:  prediction.Usage.Prompt,
		OutputTokens: prediction.Usage.Completion,
		TotalTokens:  prediction.Usage.Prompt + prediction.Usage.Completion,
	}, shouldStore)

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

	// Emit response.created - use helper to create response with all required fields
	responseCreated := buildORResponse(responseID, createdAt, nil, "in_progress", input, []schema.ORItemField{}, nil, shouldStore)
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

	// Convert and serialize tools to OpenAI format for the backend
	toolsJSON := serializeToolsForBackend(input.Tools)
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
					textPart := makeOutputTextPart(functions.ParseTextContent(cleanedResult, cfg.FunctionsConfig))
					sendSSEEvent(c, &schema.ORStreamEvent{
						Type:           "response.content_part.done",
						SequenceNumber: sequenceNumber,
						ItemID:         currentMessageID,
						OutputIndex:    &outputIndex,
						ContentIndex:   &currentContentIndex,
						Part:           &textPart,
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
							Delta:          strPtr(tc.Arguments),
						})
						sequenceNumber++

						// Emit arguments done
						sendSSEEvent(c, &schema.ORStreamEvent{
							Type:           "response.function_call_arguments.done",
							SequenceNumber: sequenceNumber,
							ItemID:         toolCallID,
							OutputIndex:    &outputIndex,
							Arguments:      strPtr(tc.Arguments),
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
					emptyPart := makeOutputTextPart("")
					sendSSEEvent(c, &schema.ORStreamEvent{
						Type:           "response.content_part.added",
						SequenceNumber: sequenceNumber,
						ItemID:         currentMessageID,
						OutputIndex:    &outputIndex,
						ContentIndex:   &currentContentIndex,
						Part:           &emptyPart,
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
					Delta:          strPtr(token),
					Logprobs:       emptyLogprobs(),
				})
				sequenceNumber++
				c.Response().Flush()
			}
			return true
		}

		// Pass logprobs and logit_bias parameters if requested
		var streamLogprobs *int
		if input.TopLogprobs != nil && *input.TopLogprobs > 0 {
			streamLogprobs = input.TopLogprobs
		}

		predFunc, err := backend.ModelInference(
			input.Context, predInput, openAIReq.Messages, images, videos, audios, ml, cfg, cl, appConfig, tokenCallback, toolsJSON, toolChoiceJSON, streamLogprobs, input.TopLogprobs, input.LogitBias)
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
		xlog.Debug("Open Responses Stream - Cleaned result", "cleanedResult", cleanedResult)

		parsedToolCalls := functions.ParseFunctionCall(cleanedResult, cfg.FunctionsConfig)
		textContent := functions.ParseTextContent(cleanedResult, cfg.FunctionsConfig)

		// Handle noAction function (model chose to respond without tool)
		noActionName := "answer"
		if cfg.FunctionsConfig.NoActionFunctionName != "" {
			noActionName = cfg.FunctionsConfig.NoActionFunctionName
		}

		// Filter out noAction calls and extract the message
		var toolCalls []functions.FuncCallResults
		for _, fc := range parsedToolCalls {
			if fc.Name == noActionName {
				// This is a text response, not a tool call
				if fc.Arguments != "" {
					var args map[string]interface{}
					if err := json.Unmarshal([]byte(fc.Arguments), &args); err == nil {
						if msg, ok := args["message"].(string); ok && msg != "" {
							textContent = msg
						}
					}
				}
				continue
			}
			toolCalls = append(toolCalls, fc)
		}

		xlog.Debug("Open Responses Stream - Parsed", "toolCalls", len(toolCalls), "textContent", textContent)

		// Convert prediction logprobs for streaming events
		streamEventLogprobs := convertLogprobsForStreaming(prediction.Logprobs)

		// If we have no output but the model did produce something, use the raw result
		if textContent == "" && len(toolCalls) == 0 && result != "" {
			xlog.Debug("Open Responses Stream - No parsed output, using raw result")
			textContent = result
		}

		// Close message if we have text content
		if currentMessageID != "" && textContent != "" && !inToolCallMode {
			// Emit output_text.done
			sendSSEEvent(c, &schema.ORStreamEvent{
				Type:           "response.output_text.done",
				SequenceNumber: sequenceNumber,
				ItemID:         currentMessageID,
				OutputIndex:    &outputIndex,
				ContentIndex:   &currentContentIndex,
				Text:           strPtr(textContent),
				Logprobs:       logprobsPtr(streamEventLogprobs),
			})
			sequenceNumber++

			// Emit content_part.done (with actual logprobs)
			textPart := makeOutputTextPartWithLogprobs(textContent, prediction.Logprobs)
			sendSSEEvent(c, &schema.ORStreamEvent{
				Type:           "response.content_part.done",
				SequenceNumber: sequenceNumber,
				ItemID:         currentMessageID,
				OutputIndex:    &outputIndex,
				ContentIndex:   &currentContentIndex,
				Part:           &textPart,
			})
			sequenceNumber++

			// Emit output_item.done for message (with actual logprobs)
			messageItem := &schema.ORItemField{
				Type:    "message",
				ID:      currentMessageID,
				Status:  "completed",
				Role:    "assistant",
				Content: []schema.ORContentPart{makeOutputTextPartWithLogprobs(textContent, prediction.Logprobs)},
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

		// Build final response with all items (include logprobs)
		var allOutputItems []schema.ORItemField
		if currentMessageID != "" && textContent != "" {
			allOutputItems = append(allOutputItems, schema.ORItemField{
				Type:    "message",
				ID:      currentMessageID,
				Status:  "completed",
				Role:    "assistant",
				Content: []schema.ORContentPart{makeOutputTextPartWithLogprobs(textContent, prediction.Logprobs)},
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
		responseCompleted := buildORResponse(responseID, createdAt, &now, "completed", input, allOutputItems, &schema.ORUsage{
			InputTokens:  prediction.Usage.Prompt,
			OutputTokens: prediction.Usage.Completion,
			TotalTokens:  prediction.Usage.Prompt + prediction.Usage.Completion,
		}, shouldStore)

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
	emptyTextPart := makeOutputTextPart("")
	sendSSEEvent(c, &schema.ORStreamEvent{
		Type:           "response.content_part.added",
		SequenceNumber: sequenceNumber,
		ItemID:         currentMessageID,
		OutputIndex:    &outputIndex,
		ContentIndex:   &currentContentIndex,
		Part:           &emptyTextPart,
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
			Delta:          strPtr(token),
			Logprobs:       emptyLogprobs(),
		})
		sequenceNumber++
		c.Response().Flush()
		return true
	}

	// Pass logprobs and logit_bias parameters if requested
	var mcpLogprobs *int
	if input.TopLogprobs != nil && *input.TopLogprobs > 0 {
		mcpLogprobs = input.TopLogprobs
	}

	predFunc, err := backend.ModelInference(
		input.Context, predInput, openAIReq.Messages, images, videos, audios, ml, cfg, cl, appConfig, tokenCallback, toolsJSON, toolChoiceJSON, mcpLogprobs, input.TopLogprobs, input.LogitBias)
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

	// Convert prediction logprobs for streaming events
	mcpStreamLogprobs := convertLogprobsForStreaming(prediction.Logprobs)

	// Emit output_text.done
	sendSSEEvent(c, &schema.ORStreamEvent{
		Type:           "response.output_text.done",
		SequenceNumber: sequenceNumber,
		ItemID:         currentMessageID,
		OutputIndex:    &outputIndex,
		ContentIndex:   &currentContentIndex,
		Text:           strPtr(result),
		Logprobs:       logprobsPtr(mcpStreamLogprobs),
	})
	sequenceNumber++

	// Emit content_part.done (with actual logprobs)
	resultPart := makeOutputTextPartWithLogprobs(result, prediction.Logprobs)
	sendSSEEvent(c, &schema.ORStreamEvent{
		Type:           "response.content_part.done",
		SequenceNumber: sequenceNumber,
		ItemID:         currentMessageID,
		OutputIndex:    &outputIndex,
		ContentIndex:   &currentContentIndex,
		Part:           &resultPart,
	})
	sequenceNumber++

	// Emit output_item.done (with actual logprobs)
	messageItem.Status = "completed"
	messageItem.Content = []schema.ORContentPart{makeOutputTextPartWithLogprobs(result, prediction.Logprobs)}
	sendSSEEvent(c, &schema.ORStreamEvent{
		Type:           "response.output_item.done",
		SequenceNumber: sequenceNumber,
		OutputIndex:    &outputIndex,
		Item:           messageItem,
	})
	sequenceNumber++

	// Emit response.completed
	now := time.Now().Unix()

	// Collect final output items (use collected items if available, otherwise use messageItem)
	var finalOutputItems []schema.ORItemField
	if len(collectedOutputItems) > 0 {
		finalOutputItems = collectedOutputItems
	} else {
		finalOutputItems = []schema.ORItemField{*messageItem}
	}
	responseCompleted := buildORResponse(responseID, createdAt, &now, "completed", input, finalOutputItems, &schema.ORUsage{
		InputTokens:  prediction.Usage.Prompt,
		OutputTokens: prediction.Usage.Completion,
		TotalTokens:  prediction.Usage.Prompt + prediction.Usage.Completion,
	}, shouldStore)
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

	// Build response with all required fields
	now := time.Now().Unix()
	response := buildORResponse(responseID, createdAt, &now, "completed", input, outputItems, nil, shouldStore)

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

	// Emit response.created - use helper to create response with all required fields
	responseCreated := buildORResponse(responseID, createdAt, nil, "in_progress", input, []schema.ORItemField{}, nil, shouldStore)
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
			Type:    "message",
			ID:      messageID,
			Status:  "completed",
			Role:    "assistant",
			Content: []schema.ORContentPart{makeOutputTextPart(content)},
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
				responseCompleted := buildORResponse(responseID, createdAt, &now, "completed", input, collectedOutputItems, nil, shouldStore)
				sendSSEEvent(c, &schema.ORStreamEvent{
					Type:           "response.completed",
					SequenceNumber: sequenceNumber,
					Response:       responseCompleted,
				})
				sequenceNumber++

				// Store response (if enabled)
				if shouldStore {
					store := GetGlobalStore()
					store.Store(responseID, input, responseCompleted)
				}

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
			responseFailed := buildORResponse(responseID, createdAt, nil, "failed", input, collectedOutputItems, nil, shouldStore)
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
			Type:    "message",
			ID:      fmt.Sprintf("msg_%s", uuid.New().String()),
			Status:  "completed",
			Role:    "assistant",
			Content: []schema.ORContentPart{makeOutputTextPart(lastMsg.Content)},
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
				Delta:          strPtr(arguments),
			})
			*sequenceNumber++

			item.Status = "completed"
			item.Arguments = arguments
			sendSSEEvent(c, &schema.ORStreamEvent{
				Type:           "response.function_call_arguments.done",
				SequenceNumber: *sequenceNumber,
				ItemID:         itemID,
				OutputIndex:    &outputIndex,
				Arguments:      strPtr(arguments),
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
		emptyPart := makeOutputTextPart("")
		sendSSEEvent(c, &schema.ORStreamEvent{
			Type:           "response.content_part.added",
			SequenceNumber: *sequenceNumber,
			ItemID:         itemID,
			OutputIndex:    &outputIndex,
			ContentIndex:   func() *int { i := 0; return &i }(),
			Part:           &emptyPart,
		})
		*sequenceNumber++

		// Emit text done
		sendSSEEvent(c, &schema.ORStreamEvent{
			Type:           "response.output_text.done",
			SequenceNumber: *sequenceNumber,
			ItemID:         itemID,
			OutputIndex:    &outputIndex,
			ContentIndex:   func() *int { i := 0; return &i }(),
			Text:           strPtr(content),
			Logprobs:       emptyLogprobs(),
		})
		*sequenceNumber++

		// Emit content part done
		contentPart := makeOutputTextPart(content)
		sendSSEEvent(c, &schema.ORStreamEvent{
			Type:           "response.content_part.done",
			SequenceNumber: *sequenceNumber,
			ItemID:         itemID,
			OutputIndex:    &outputIndex,
			ContentIndex:   func() *int { i := 0; return &i }(),
			Part:           &contentPart,
		})
		*sequenceNumber++

		// Emit item done
		item.Status = "completed"
		item.Content = []schema.ORContentPart{makeOutputTextPart(content)}
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

// getTopLogprobs returns the top_logprobs value, defaulting to 0 if nil
func getTopLogprobs(topLogprobs *int) int {
	if topLogprobs != nil {
		return *topLogprobs
	}
	return 0
}

// Helper functions for pointer types in streaming events
func strPtr(s string) *string {
	return &s
}

func logprobsPtr(lp []schema.ORLogProb) *[]schema.ORLogProb {
	return &lp
}

func emptyLogprobs() *[]schema.ORLogProb {
	empty := []schema.ORLogProb{}
	return &empty
}

// makeOutputTextPart creates an output_text content part with all required fields per Open Responses spec
func makeOutputTextPart(text string) schema.ORContentPart {
	return schema.ORContentPartWithLogprobs(text, nil)
}

// makeOutputTextPartWithLogprobs creates an output_text content part with actual logprobs data
func makeOutputTextPartWithLogprobs(text string, logprobs *schema.Logprobs) schema.ORContentPart {
	return schema.ORContentPartWithLogprobs(text, logprobs)
}

// convertLogprobsForStreaming converts OpenAI-style logprobs to Open Responses format for streaming events
func convertLogprobsForStreaming(logprobs *schema.Logprobs) []schema.ORLogProb {
	if logprobs == nil || len(logprobs.Content) == 0 {
		return []schema.ORLogProb{}
	}

	result := make([]schema.ORLogProb, 0, len(logprobs.Content))
	for _, lp := range logprobs.Content {
		topLPs := make([]schema.ORTopLogProb, 0, len(lp.TopLogprobs))
		for _, tlp := range lp.TopLogprobs {
			topLPs = append(topLPs, schema.ORTopLogProb{
				Token:   tlp.Token,
				Logprob: tlp.Logprob,
				Bytes:   tlp.Bytes,
			})
		}
		result = append(result, schema.ORLogProb{
			Token:       lp.Token,
			Logprob:     lp.Logprob,
			Bytes:       lp.Bytes,
			TopLogprobs: topLPs,
		})
	}
	return result
}

// ensureUsageDetails ensures usage has all required detail fields
func ensureUsageDetails(usage *schema.ORUsage) *schema.ORUsage {
	if usage == nil {
		return nil
	}
	// Ensure details are always present (not nil)
	if usage.InputTokensDetails == nil {
		usage.InputTokensDetails = &schema.ORInputTokensDetails{CachedTokens: 0}
	}
	if usage.OutputTokensDetails == nil {
		usage.OutputTokensDetails = &schema.OROutputTokensDetails{ReasoningTokens: 0}
	}
	return usage
}

// buildORResponse creates a complete ORResponseResource with all required fields
func buildORResponse(responseID string, createdAt int64, completedAt *int64, status string, input *schema.OpenResponsesRequest, outputItems []schema.ORItemField, usage *schema.ORUsage, shouldStore bool) *schema.ORResponseResource {
	// Ensure output is never null - always an array
	if outputItems == nil {
		outputItems = []schema.ORItemField{}
	}

	// Ensure tools is never null - always an array
	tools := input.Tools
	if tools == nil {
		tools = []schema.ORFunctionTool{}
	}

	// Ensure metadata is never null - always a map
	metadata := input.Metadata
	if metadata == nil {
		metadata = map[string]string{}
	}

	// Set default values for sampling parameters
	temperature := 1.0
	if input.Temperature != nil {
		temperature = *input.Temperature
	}

	topP := 1.0
	if input.TopP != nil {
		topP = *input.TopP
	}

	presencePenalty := 0.0
	if input.PresencePenalty != nil {
		presencePenalty = *input.PresencePenalty
	}

	frequencyPenalty := 0.0
	if input.FrequencyPenalty != nil {
		frequencyPenalty = *input.FrequencyPenalty
	}

	// Default truncation to "auto"
	truncation := "auto"
	if input.Truncation != "" {
		truncation = input.Truncation
	}

	// Default service_tier to "default"
	serviceTier := "default"
	if input.ServiceTier != "" {
		serviceTier = input.ServiceTier
	}

	// Default parallel_tool_calls to true
	parallelToolCalls := true
	if input.ParallelToolCalls != nil {
		parallelToolCalls = *input.ParallelToolCalls
	}

	// Default tool_choice: "auto" if tools are present, "none" otherwise
	var toolChoice interface{}
	if input.ToolChoice != nil {
		toolChoice = input.ToolChoice
	} else if len(tools) > 0 {
		toolChoice = "auto"
	} else {
		toolChoice = "none"
	}

	// Background defaults to false
	background := false
	if input.Background != nil {
		background = *input.Background
	}

	// Convert nullable string fields
	var previousResponseID *string
	if input.PreviousResponseID != "" {
		previousResponseID = &input.PreviousResponseID
	}

	var instructions *string
	if input.Instructions != "" {
		instructions = &input.Instructions
	}

	// Convert reasoning
	var reasoning *schema.ORReasoning
	if input.Reasoning != nil {
		reasoning = &schema.ORReasoning{
			Effort:  input.Reasoning.Effort,
			Summary: input.Reasoning.Summary,
		}
	}

	// Build default text config
	textConfig := &schema.ORTextConfig{
		Format: &schema.ORTextFormat{
			Type: "text",
		},
	}

	return &schema.ORResponseResource{
		ID:                 responseID,
		Object:             "response",
		CreatedAt:          createdAt,
		CompletedAt:        completedAt,
		Status:             status,
		Model:              input.Model,
		Output:             outputItems,
		Error:              nil, // null when no error
		IncompleteDetails:  nil, // null when complete
		PreviousResponseID: previousResponseID,
		Instructions:       instructions,

		// Tool-related fields
		Tools:             tools,
		ToolChoice:        toolChoice,
		ParallelToolCalls: parallelToolCalls,
		MaxToolCalls:      input.MaxToolCalls,

		// Sampling parameters
		Temperature:      temperature,
		TopP:             topP,
		PresencePenalty:  presencePenalty,
		FrequencyPenalty: frequencyPenalty,
		TopLogprobs:      getTopLogprobs(input.TopLogprobs),
		MaxOutputTokens:  input.MaxOutputTokens,

		// Text format
		Text: textConfig,

		// Truncation and reasoning
		Truncation: truncation,
		Reasoning:  reasoning,

		// Usage
		Usage: ensureUsageDetails(usage),

		// Metadata and operational flags
		Metadata:    metadata,
		Store:       shouldStore,
		Background:  background,
		ServiceTier: serviceTier,

		// Safety and caching (nullable, not yet implemented)
		SafetyIdentifier: nil,
		PromptCacheKey:   nil,
	}
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

// convertORToolsToOpenAIFormat converts Open Responses tools to OpenAI format for the backend
// Open Responses format: { type, name, description, parameters }
// OpenAI format: { type, function: { name, description, parameters } }
func convertORToolsToOpenAIFormat(orTools []schema.ORFunctionTool) []functions.Tool {
	result := make([]functions.Tool, 0, len(orTools))
	for _, t := range orTools {
		result = append(result, functions.Tool{
			Type: "function",
			Function: functions.Function{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Parameters,
			},
		})
	}
	return result
}

// serializeToolsForBackend converts and serializes Open Responses tools to JSON for the backend
func serializeToolsForBackend(orTools []schema.ORFunctionTool) string {
	if len(orTools) == 0 {
		return ""
	}
	openAITools := convertORToolsToOpenAIFormat(orTools)
	toolsBytes, err := json.Marshal(openAITools)
	if err != nil {
		return ""
	}
	return string(toolsBytes)
}

// GetResponseEndpoint returns a handler for GET /responses/:id
// This endpoint is used for polling background responses or resuming streaming
// @Summary Get a response by ID
// @Description Retrieve a response by ID. Can be used for polling background responses or resuming streaming responses.
// @Param id path string true "Response ID"
// @Param stream query string false "Set to 'true' to resume streaming"
// @Param starting_after query int false "Sequence number to resume from (for streaming)"
// @Success 200 {object} schema.ORResponseResource "Response"
// @Failure 400 {object} map[string]interface{} "Bad Request"
// @Failure 404 {object} map[string]interface{} "Not Found"
// @Router /v1/responses/{id} [get]
func GetResponseEndpoint() func(c echo.Context) error {
	return func(c echo.Context) error {
		responseID := c.Param("id")
		if responseID == "" {
			return sendOpenResponsesError(c, 400, "invalid_request_error", "response ID is required", "id")
		}

		store := GetGlobalStore()
		stored, err := store.Get(responseID)
		if err != nil {
			return sendOpenResponsesError(c, 404, "not_found", fmt.Sprintf("response not found: %s", responseID), "id")
		}

		// Check if streaming resume is requested
		streamParam := c.QueryParam("stream")
		if streamParam == "true" {
			// Validate that the response was created with streaming enabled
			if !stored.StreamEnabled {
				return sendOpenResponsesError(c, 400, "invalid_request_error", "cannot stream a response that was not created with stream=true", "stream")
			}

			// Get starting_after parameter
			startingAfter := 0
			startingAfterParam := c.QueryParam("starting_after")
			if startingAfterParam != "" {
				if _, err := fmt.Sscanf(startingAfterParam, "%d", &startingAfter); err != nil {
					return sendOpenResponsesError(c, 400, "invalid_request_error", "starting_after must be an integer", "starting_after")
				}
			}

			return handleStreamResume(c, store, responseID, stored, startingAfter)
		}

		// Non-streaming: return the current response state
		stored.mu.RLock()
		response := stored.Response
		stored.mu.RUnlock()

		return c.JSON(200, response)
	}
}

// handleStreamResume handles resuming a streaming response from a specific sequence number
func handleStreamResume(c echo.Context, store *ResponseStore, responseID string, stored *StoredResponse, startingAfter int) error {
	c.Response().Header().Set("Content-Type", "text/event-stream")
	c.Response().Header().Set("Cache-Control", "no-cache")
	c.Response().Header().Set("Connection", "keep-alive")

	// Get buffered events after the starting point
	events, err := store.GetEventsAfter(responseID, startingAfter)
	if err != nil {
		return sendOpenResponsesError(c, 500, "server_error", fmt.Sprintf("failed to get events: %v", err), "")
	}

	// Send all buffered events
	for _, event := range events {
		fmt.Fprintf(c.Response().Writer, "event: %s\ndata: %s\n\n", event.EventType, string(event.Data))
		c.Response().Flush()
	}

	// Get the current status
	stored.mu.RLock()
	status := stored.Response.Status
	stored.mu.RUnlock()

	// If response is still in progress, subscribe to new events
	if status == schema.ORStatusQueued || status == schema.ORStatusInProgress {
		eventsChan, err := store.GetEventsChan(responseID)
		if err != nil {
			// Response might have completed, just finish
			fmt.Fprintf(c.Response().Writer, "data: [DONE]\n\n")
			c.Response().Flush()
			return nil
		}

		// Track last sent sequence number
		lastSeq := startingAfter
		if len(events) > 0 {
			lastSeq = events[len(events)-1].SequenceNumber
		}

		// Wait for new events or completion
		for {
			select {
			case <-c.Request().Context().Done():
				// Client disconnected
				return nil
			case <-eventsChan:
				// New events available
				newEvents, err := store.GetEventsAfter(responseID, lastSeq)
				if err != nil {
					break
				}
				for _, event := range newEvents {
					fmt.Fprintf(c.Response().Writer, "event: %s\ndata: %s\n\n", event.EventType, string(event.Data))
					c.Response().Flush()
					lastSeq = event.SequenceNumber
				}

				// Check if response is now complete
				stored.mu.RLock()
				status = stored.Response.Status
				stored.mu.RUnlock()

				if status != schema.ORStatusQueued && status != schema.ORStatusInProgress {
					fmt.Fprintf(c.Response().Writer, "data: [DONE]\n\n")
					c.Response().Flush()
					return nil
				}
			case <-time.After(30 * time.Second):
				// Timeout - send keepalive or check status
				stored.mu.RLock()
				status = stored.Response.Status
				stored.mu.RUnlock()

				if status != schema.ORStatusQueued && status != schema.ORStatusInProgress {
					fmt.Fprintf(c.Response().Writer, "data: [DONE]\n\n")
					c.Response().Flush()
					return nil
				}
			}
		}
	}

	// Response already complete
	fmt.Fprintf(c.Response().Writer, "data: [DONE]\n\n")
	c.Response().Flush()
	return nil
}

// CancelResponseEndpoint returns a handler for POST /responses/:id/cancel
// This endpoint cancels a background response if it's still in progress
// @Summary Cancel a response
// @Description Cancel a background response if it's still in progress
// @Param id path string true "Response ID"
// @Success 200 {object} schema.ORResponseResource "Response"
// @Failure 400 {object} map[string]interface{} "Bad Request"
// @Failure 404 {object} map[string]interface{} "Not Found"
// @Router /v1/responses/{id}/cancel [post]
func CancelResponseEndpoint() func(c echo.Context) error {
	return func(c echo.Context) error {
		responseID := c.Param("id")
		if responseID == "" {
			return sendOpenResponsesError(c, 400, "invalid_request_error", "response ID is required", "id")
		}

		store := GetGlobalStore()
		response, err := store.Cancel(responseID)
		if err != nil {
			return sendOpenResponsesError(c, 404, "not_found", fmt.Sprintf("response not found: %s", responseID), "id")
		}

		// Return the final response object
		return c.JSON(200, response)
	}
}
