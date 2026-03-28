package openresponses

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

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
	reason "github.com/mudler/LocalAI/pkg/reasoning"
	"github.com/mudler/LocalAI/pkg/utils"
	"github.com/mudler/xlog"
)

// ResponsesEndpoint is the Open Responses API endpoint
// https://www.openresponses.org/specification
// @Summary Create a response using the Open Responses API
// @Param request body schema.OpenResponsesRequest true "Request body"
// @Success 200 {object} schema.ORResponseResource "Response"
// @Router /v1/responses [post]
func ResponsesEndpoint(cl *config.ModelConfigLoader, ml *model.ModelLoader, evaluator *templates.Evaluator, appConfig *config.ApplicationConfig, natsClient mcpTools.MCPNATSClient) echo.HandlerFunc {
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
		var mcpExecutor mcpTools.ToolExecutor

		if len(input.Tools) > 0 {
			funcs, shouldUseFn = convertORToolsToFunctions(input, cfg)
		}

		// MCP injection: prompts, resources, and tools
		mcpServers := mcpTools.MCPServersFromMetadata(input.Metadata)
		mcpPromptName, mcpPromptArgs := mcpTools.MCPPromptFromMetadata(input.Metadata)
		mcpResourceURIs := mcpTools.MCPResourcesFromMetadata(input.Metadata)

		hasMCPRequest := len(mcpServers) > 0 || mcpPromptName != "" || len(mcpResourceURIs) > 0
		hasMCPConfig := cfg.MCP.Servers != "" || cfg.MCP.Stdio != ""

		if (hasMCPRequest && hasMCPConfig) || (len(input.Tools) == 0 && hasMCPConfig) {
			remote, stdio, mcpErr := cfg.MCP.MCPConfigFromYAML()
			if mcpErr == nil {
				enabledServers := mcpServers
				if !hasMCPRequest {
					enabledServers = nil // backward compat: auto-activate all servers
				}
				mcpExecutor = mcpTools.NewToolExecutor(c.Request().Context(), natsClient, cfg.Name, remote, stdio, enabledServers)

				// Prompt and resource injection (local mode only)
				if natsClient == nil && hasMCPRequest {
					namedSessions, sessErr := mcpTools.NamedSessionsFromMCPConfig(cfg.Name, remote, stdio, mcpServers)
					if sessErr == nil && len(namedSessions) > 0 {
						mcpCtx, _ := mcpTools.InjectMCPContext(c.Request().Context(), namedSessions, mcpPromptName, mcpPromptArgs, mcpResourceURIs)
						if mcpCtx != nil {
							messages = append(mcpCtx.PromptMessages, messages...)
							mcpTools.AppendResourceSuffix(messages, mcpCtx.ResourceSuffix)
						}
					}
				}

				// Tool injection via executor
				if mcpExecutor.HasTools() {
					mcpFuncs, discErr := mcpExecutor.DiscoverTools(c.Request().Context())
					if discErr == nil {
						for _, fn := range mcpFuncs {
							funcs = append(funcs, fn)
							input.Tools = append(input.Tools, schema.ORFunctionTool{
								Type:        "function",
								Name:        fn.Name,
								Description: fn.Description,
								Parameters:  fn.Parameters,
							})
						}
						shouldUseFn = len(funcs) > 0 && cfg.ShouldUseFunctions()
						xlog.Debug("Open Responses MCP tools injected", "count", len(mcpFuncs), "total_funcs", len(funcs))
					}
				}
			} else {
				xlog.Error("Failed to parse MCP config", "error", mcpErr)
			}
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
				Parameters: map[string]any{
					"properties": map[string]any{
						"message": map[string]any{
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

				if input.Stream {
					// Background streaming processing (buffer events)
					finalResponse, bgErr = handleBackgroundStream(bgCtx, store, responseID, createdAt, input, cfg, ml, cl, appConfig, predInput, openAIReq, funcs, shouldUseFn, mcpExecutor, evaluator)
				} else {
					// Background non-streaming processing
					finalResponse, bgErr = handleBackgroundNonStream(bgCtx, store, responseID, createdAt, input, cfg, ml, cl, appConfig, predInput, openAIReq, funcs, shouldUseFn, mcpExecutor, evaluator)
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

		if input.Stream {
			return handleOpenResponsesStream(c, responseID, createdAt, input, cfg, ml, cl, appConfig, predInput, openAIReq, funcs, shouldUseFn, shouldStore, mcpExecutor, evaluator)
		}

		return handleOpenResponsesNonStream(c, responseID, createdAt, input, cfg, ml, cl, appConfig, predInput, openAIReq, funcs, shouldUseFn, shouldStore, mcpExecutor, evaluator, 0)
	}
}

// convertORInputToMessages converts Open Responses input to internal Messages
func convertORInputToMessages(input any, cfg *config.ModelConfig) ([]schema.Message, error) {
	var messages []schema.Message

	switch v := input.(type) {
	case string:
		// Simple string = user message
		return []schema.Message{{Role: "user", StringContent: v}}, nil
	case []any:
		// Array of items
		for _, itemRaw := range v {
			itemMap, ok := itemRaw.(map[string]any)
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
			case "reasoning":
				msg, err := convertORReasoningItemToMessage(itemMap)
				if err != nil {
					return nil, err
				}
				messages = append(messages, msg)
			case "function_call":
				msg, err := convertORFunctionCallItemToMessage(itemMap)
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
		return mergeContiguousAssistantMessages(messages), nil
	default:
		return nil, fmt.Errorf("unsupported input type: %T", input)
	}
}

// convertORReasoningItemToMessage converts an Open Responses reasoning item to an assistant Message fragment (for merging).
func convertORReasoningItemToMessage(itemMap map[string]any) (schema.Message, error) {
	var reasoning string
	if content := itemMap["content"]; content != nil {
		if s, ok := content.(string); ok {
			reasoning = s
		} else if parts, ok := content.([]any); ok {
			for _, p := range parts {
				if partMap, ok := p.(map[string]any); ok {
					if t, _ := partMap["type"].(string); (t == "output_text" || t == "input_text") && partMap["text"] != nil {
						if tStr, ok := partMap["text"].(string); ok {
							reasoning += tStr
						}
					}
				}
			}
		}
	}
	return schema.Message{Role: "assistant", Reasoning: stringPtr(reasoning)}, nil
}

// convertORFunctionCallItemToMessage converts an Open Responses function_call item to an assistant Message fragment (for merging).
func convertORFunctionCallItemToMessage(itemMap map[string]any) (schema.Message, error) {
	callID, _ := itemMap["call_id"].(string)
	name, _ := itemMap["name"].(string)
	arguments, _ := itemMap["arguments"].(string)
	if callID == "" {
		callID = fmt.Sprintf("call_%s", name)
	}
	return schema.Message{
		Role: "assistant",
		ToolCalls: []schema.ToolCall{{
			Index:        0,
			ID:           callID,
			Type:         "function",
			FunctionCall: schema.FunctionCall{Name: name, Arguments: arguments},
		}},
	}, nil
}

func stringPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
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
	case "reasoning":
		reasoning := extractReasoningContentFromORItem(item)
		return schema.Message{Role: "assistant", Reasoning: stringPtr(reasoning)}, nil
	case "function_call":
		callID := item.CallID
		if callID == "" {
			callID = fmt.Sprintf("call_%s", item.Name)
		}
		return schema.Message{
			Role: "assistant",
			ToolCalls: []schema.ToolCall{{
				Index:        0,
				ID:           callID,
				Type:         "function",
				FunctionCall: schema.FunctionCall{Name: item.Name, Arguments: item.Arguments},
			}},
		}, nil
	default:
		return schema.Message{}, fmt.Errorf("unsupported item type for conversion: %s (from response %s)", item.Type, responseID)
	}
}

func extractReasoningContentFromORItem(item *schema.ORItemField) string {
	if contentParts, ok := item.Content.([]schema.ORContentPart); ok {
		var s string
		for _, part := range contentParts {
			if part.Type == "output_text" || part.Type == "input_text" {
				s += part.Text
			}
		}
		return s
	}
	if s, ok := item.Content.(string); ok {
		return s
	}
	return ""
}

// convertOROutputItemsToMessages converts Open Responses output items to internal Messages.
// Contiguous assistant items (message, reasoning, function_call) are merged into a single message.
func convertOROutputItemsToMessages(outputItems []schema.ORItemField) ([]schema.Message, error) {
	var messages []schema.Message

	for _, item := range outputItems {
		switch item.Type {
		case "message":
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
		case "reasoning":
			reasoning := extractReasoningContentFromORItem(&item)
			messages = append(messages, schema.Message{Role: "assistant", Reasoning: stringPtr(reasoning)})
		case "function_call":
			msg := schema.Message{
				Role: "assistant",
				ToolCalls: []schema.ToolCall{{
					Index:        0,
					ID:           item.CallID,
					Type:         "function",
					FunctionCall: schema.FunctionCall{Name: item.Name, Arguments: item.Arguments},
				}},
			}
			if msg.ToolCalls[0].ID == "" {
				msg.ToolCalls[0].ID = fmt.Sprintf("call_%s", item.Name)
			}
			messages = append(messages, msg)
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

	return mergeContiguousAssistantMessages(messages), nil
}

// mergeContiguousAssistantMessages merges contiguous assistant messages into one.
// Many chat templates expect content, reasoning, and tool calls in a single assistant message
// (see e.g. llama.cpp PR 19773). This avoids creating separate messages per input item.
func mergeContiguousAssistantMessages(messages []schema.Message) []schema.Message {
	if len(messages) == 0 {
		return messages
	}
	var out []schema.Message
	var acc *schema.Message
	for i := range messages {
		m := &messages[i]
		if m.Role != "assistant" {
			flushAssistantAccumulator(&out, &acc)
			out = append(out, *m)
			continue
		}
		if acc == nil {
			acc = &schema.Message{Role: "assistant"}
		}
		if m.StringContent != "" {
			if acc.StringContent != "" {
				acc.StringContent += "\n" + m.StringContent
			} else {
				acc.StringContent = m.StringContent
			}
			if acc.Content == nil {
				acc.Content = m.Content
			} else if _, ok := m.Content.(string); ok {
				acc.Content = acc.StringContent
			}
		}
		if m.Reasoning != nil && *m.Reasoning != "" {
			if acc.Reasoning == nil {
				acc.Reasoning = m.Reasoning
			} else {
				combined := *acc.Reasoning + "\n" + *m.Reasoning
				acc.Reasoning = &combined
			}
		}
		if len(m.ToolCalls) > 0 {
			acc.ToolCalls = append(acc.ToolCalls, m.ToolCalls...)
		}
	}
	flushAssistantAccumulator(&out, &acc)
	return out
}

func flushAssistantAccumulator(out *[]schema.Message, acc **schema.Message) {
	if acc == nil || *acc == nil {
		return
	}
	m := *acc
	if m.StringContent == "" && (m.Reasoning == nil || *m.Reasoning == "") && len(m.ToolCalls) == 0 {
		*acc = nil
		return
	}
	if m.Content == nil {
		m.Content = m.StringContent
	}
	// Re-index tool calls after merge (each may have been 0)
	for i := range m.ToolCalls {
		m.ToolCalls[i].Index = i
	}
	*out = append(*out, *m)
	*acc = nil
}

// convertORMessageItem converts an Open Responses message item to internal Message
func convertORMessageItem(itemMap map[string]any, cfg *config.ModelConfig) (schema.Message, error) {
	role, _ := itemMap["role"].(string)
	msg := schema.Message{Role: role}

	content := itemMap["content"]
	switch contentVal := content.(type) {
	case string:
		msg.StringContent = contentVal
		msg.Content = contentVal
	case []any:
		// Array of content parts
		var textContent string
		var stringImages []string
		var stringVideos []string
		var stringAudios []string

		for _, partRaw := range contentVal {
			partMap, ok := partRaw.(map[string]any)
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
			switch tc {
			case "required":
				cfg.SetFunctionCallString("required")
			case "none":
				return nil, false
			case "auto":
				// "auto" is the default - let model decide whether to use tools
				// Tools are available but not forced
			}
		case map[string]any:
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
func convertTextFormatToResponseFormat(textFormat any) any {
	switch tf := textFormat.(type) {
	case map[string]any:
		if tfType, ok := tf["type"].(string); ok {
			if tfType == "json_schema" {
				return map[string]any{
					"type":        "json_schema",
					"json_schema": tf,
				}
			}
			return map[string]any{"type": tfType}
		}
	case string:
		return map[string]any{"type": tf}
	}
	return nil
}

// handleBackgroundNonStream handles background non-streaming responses
func handleBackgroundNonStream(ctx context.Context, store *ResponseStore, responseID string, createdAt int64, input *schema.OpenResponsesRequest, cfg *config.ModelConfig, ml *model.ModelLoader, cl *config.ModelConfigLoader, appConfig *config.ApplicationConfig, predInput string, openAIReq *schema.OpenAIRequest, funcs functions.Functions, shouldUseFn bool, mcpExecutor mcpTools.ToolExecutor, evaluator *templates.Evaluator) (*schema.ORResponseResource, error) {
	mcpMaxIterations := 10
	if cfg.Agent.MaxIterations > 0 {
		mcpMaxIterations = cfg.Agent.MaxIterations
	}
	hasMCPTools := mcpExecutor != nil && mcpExecutor.HasTools()
	var allOutputItems []schema.ORItemField

	for mcpIteration := 0; mcpIteration <= mcpMaxIterations; mcpIteration++ {
		if mcpIteration > 0 {
			predInput = evaluator.TemplateMessages(*openAIReq, openAIReq.Messages, cfg, funcs, shouldUseFn)
			xlog.Debug("Background MCP re-templating", "iteration", mcpIteration)
		}

		// Populate openAIReq fields for ComputeChoices
		openAIReq.Tools = convertORToolsToOpenAIFormat(input.Tools)
		openAIReq.ToolsChoice = input.ToolChoice
		if input.TopLogprobs != nil && *input.TopLogprobs > 0 {
			openAIReq.TopLogprobs = input.TopLogprobs
			openAIReq.Logprobs = schema.LogprobsValue{Enabled: true}
		}
		openAIReq.LogitBias = input.LogitBias

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		var result string
		cb := func(s string, c *[]schema.Choice) {
			result = s
		}
		choices, tokenUsage, chatDeltas, err := openaiEndpoint.ComputeChoices(openAIReq, predInput, cfg, cl, appConfig, ml, cb, nil)
		if err != nil {
			return nil, fmt.Errorf("model inference failed: %w", err)
		}

		// Extract logprobs from choices if available
		var resultLogprobs *schema.Logprobs
		if len(choices) > 0 {
			resultLogprobs = choices[0].Logprobs
		}

		// Parse tool calls
		var funcCallResults []functions.FuncCallResults
		var textContent string

		if shouldUseFn {
			if deltaToolCalls := functions.ToolCallsFromChatDeltas(chatDeltas); len(deltaToolCalls) > 0 {
				funcCallResults = deltaToolCalls
				textContent = functions.ContentFromChatDeltas(chatDeltas)
			} else {
				cleanedResult := functions.CleanupLLMResult(result, cfg.FunctionsConfig)
				funcCallResults = functions.ParseFunctionCall(cleanedResult, cfg.FunctionsConfig)
				textContent = functions.ParseTextContent(cleanedResult, cfg.FunctionsConfig)
			}

			noActionName := "answer"
			if cfg.FunctionsConfig.NoActionFunctionName != "" {
				noActionName = cfg.FunctionsConfig.NoActionFunctionName
			}

			var toolCalls []schema.ToolCall
			for i, fc := range funcCallResults {
				if fc.Name == noActionName {
					if fc.Arguments != "" {
						var args map[string]any
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

			// MCP tool execution
			if hasMCPTools && len(toolCalls) > 0 {
				var hasMCPCalls bool
				for _, tc := range toolCalls {
					if mcpExecutor != nil && mcpExecutor.IsTool(tc.FunctionCall.Name) {
						hasMCPCalls = true
						break
					}
				}
				if hasMCPCalls {
					assistantMsg := schema.Message{Role: "assistant", Content: result, ToolCalls: toolCalls}
					openAIReq.Messages = append(openAIReq.Messages, assistantMsg)

					for _, tc := range toolCalls {
						// Emit function_call + function_call_output items
						allOutputItems = append(allOutputItems, schema.ORItemField{
							Type: "function_call", ID: fmt.Sprintf("fc_%s", uuid.New().String()),
							Status: "completed", CallID: tc.ID, Name: tc.FunctionCall.Name, Arguments: tc.FunctionCall.Arguments,
						})

						if mcpExecutor == nil || !mcpExecutor.IsTool(tc.FunctionCall.Name) {
							continue
						}
						toolResult, toolErr := mcpExecutor.ExecuteTool(ctx, tc.FunctionCall.Name, tc.FunctionCall.Arguments)
						if toolErr != nil {
							toolResult = fmt.Sprintf("Error: %v", toolErr)
						}
						openAIReq.Messages = append(openAIReq.Messages, schema.Message{
							Role: "tool", Content: toolResult, StringContent: toolResult, ToolCallID: tc.ID, Name: tc.FunctionCall.Name,
						})
						allOutputItems = append(allOutputItems, schema.ORItemField{
							Type: "function_call_output", ID: fmt.Sprintf("fco_%s", uuid.New().String()),
							Status: "completed", CallID: tc.ID, Output: toolResult,
						})
					}
					continue // next MCP iteration
				}
			}

			// No MCP calls, build output items
			if textContent != "" {
				allOutputItems = append(allOutputItems, schema.ORItemField{
					Type: "message", ID: fmt.Sprintf("msg_%s", uuid.New().String()),
					Status: "completed", Role: "assistant",
					Content: []schema.ORContentPart{makeOutputTextPartWithLogprobs(textContent, resultLogprobs)},
				})
			}
			for _, tc := range toolCalls {
				allOutputItems = append(allOutputItems, schema.ORItemField{
					Type: "function_call", ID: fmt.Sprintf("fc_%s", uuid.New().String()),
					Status: "completed", CallID: tc.ID, Name: tc.FunctionCall.Name, Arguments: tc.FunctionCall.Arguments,
				})
			}
			if len(allOutputItems) == 0 && result != "" {
				allOutputItems = append(allOutputItems, schema.ORItemField{
					Type: "message", ID: fmt.Sprintf("msg_%s", uuid.New().String()),
					Status: "completed", Role: "assistant",
					Content: []schema.ORContentPart{makeOutputTextPartWithLogprobs(result, resultLogprobs)},
				})
			}
		} else if !shouldUseFn && cfg.FunctionsConfig.AutomaticToolParsingFallback && result != "" {
			// Automatic tool parsing fallback: no tools in request but model emitted tool call markup
			parsed := functions.ParseFunctionCall(result, cfg.FunctionsConfig)
			if len(parsed) > 0 {
				stripped := functions.StripToolCallMarkup(result)
				if stripped != "" {
					allOutputItems = append(allOutputItems, schema.ORItemField{
						Type: "message", ID: fmt.Sprintf("msg_%s", uuid.New().String()),
						Status: "completed", Role: "assistant",
						Content: []schema.ORContentPart{makeOutputTextPartWithLogprobs(stripped, resultLogprobs)},
					})
				}
				for _, fc := range parsed {
					toolCallID := fc.ID
					if toolCallID == "" {
						toolCallID = fmt.Sprintf("fc_%s", uuid.New().String())
					}
					allOutputItems = append(allOutputItems, schema.ORItemField{
						Type: "function_call", ID: fmt.Sprintf("fc_%s", uuid.New().String()),
						Status: "completed", CallID: toolCallID, Name: fc.Name, Arguments: fc.Arguments,
					})
				}
			} else {
				allOutputItems = append(allOutputItems, schema.ORItemField{
					Type: "message", ID: fmt.Sprintf("msg_%s", uuid.New().String()),
					Status: "completed", Role: "assistant",
					Content: []schema.ORContentPart{makeOutputTextPartWithLogprobs(result, resultLogprobs)},
				})
			}
		} else {
			allOutputItems = append(allOutputItems, schema.ORItemField{
				Type: "message", ID: fmt.Sprintf("msg_%s", uuid.New().String()),
				Status: "completed", Role: "assistant",
				Content: []schema.ORContentPart{makeOutputTextPartWithLogprobs(result, resultLogprobs)},
			})
		}

		now := time.Now().Unix()
		return buildORResponse(responseID, createdAt, &now, schema.ORStatusCompleted, input, allOutputItems, &schema.ORUsage{
			InputTokens:  tokenUsage.Prompt,
			OutputTokens: tokenUsage.Completion,
			TotalTokens:  tokenUsage.Prompt + tokenUsage.Completion,
		}, true), nil
	} // end MCP iteration loop

	return nil, fmt.Errorf("MCP iteration limit reached")
}

// handleBackgroundStream handles background streaming responses with event buffering
func handleBackgroundStream(ctx context.Context, store *ResponseStore, responseID string, createdAt int64, input *schema.OpenResponsesRequest, cfg *config.ModelConfig, ml *model.ModelLoader, cl *config.ModelConfigLoader, appConfig *config.ApplicationConfig, predInput string, openAIReq *schema.OpenAIRequest, funcs functions.Functions, shouldUseFn bool, mcpExecutor mcpTools.ToolExecutor, evaluator *templates.Evaluator) (*schema.ORResponseResource, error) {
	// Populate openAIReq fields for ComputeChoices
	openAIReq.Tools = convertORToolsToOpenAIFormat(input.Tools)
	openAIReq.ToolsChoice = input.ToolChoice
	if input.TopLogprobs != nil && *input.TopLogprobs > 0 {
		openAIReq.TopLogprobs = input.TopLogprobs
		openAIReq.Logprobs = schema.LogprobsValue{Enabled: true}
	}
	openAIReq.LogitBias = input.LogitBias

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

	mcpBgStreamMaxIterations := 10
	if cfg.Agent.MaxIterations > 0 {
		mcpBgStreamMaxIterations = cfg.Agent.MaxIterations
	}
	hasMCPTools := mcpExecutor != nil && mcpExecutor.HasTools()

	var lastTokenUsage backend.TokenUsage
	var lastLogprobs *schema.Logprobs

	for mcpIter := 0; mcpIter <= mcpBgStreamMaxIterations; mcpIter++ {
		if mcpIter > 0 {
			predInput = evaluator.TemplateMessages(*openAIReq, openAIReq.Messages, cfg, funcs, shouldUseFn)
			xlog.Debug("Background stream MCP re-templating", "iteration", mcpIter)
		}

		accumulatedText = ""
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

		var result string
		cb := func(s string, c *[]schema.Choice) {
			result = s
		}
		choices, tokenUsage, chatDeltas, err := openaiEndpoint.ComputeChoices(openAIReq, predInput, cfg, cl, appConfig, ml, cb, tokenCallback)
		if err != nil {
			return nil, fmt.Errorf("model inference failed: %w", err)
		}
		lastTokenUsage = tokenUsage
		if len(choices) > 0 {
			lastLogprobs = choices[0].Logprobs
		}

		// Check for MCP tool calls in the streamed result
		if shouldUseFn && hasMCPTools {
			var funcCallResults []functions.FuncCallResults
			if deltaToolCalls := functions.ToolCallsFromChatDeltas(chatDeltas); len(deltaToolCalls) > 0 {
				funcCallResults = deltaToolCalls
			} else {
				cleanedResult := functions.CleanupLLMResult(result, cfg.FunctionsConfig)
				funcCallResults = functions.ParseFunctionCall(cleanedResult, cfg.FunctionsConfig)
			}

			noActionName := "answer"
			if cfg.FunctionsConfig.NoActionFunctionName != "" {
				noActionName = cfg.FunctionsConfig.NoActionFunctionName
			}

			var toolCalls []schema.ToolCall
			for i, fc := range funcCallResults {
				if fc.Name == noActionName {
					continue
				}
				toolCalls = append(toolCalls, schema.ToolCall{
					Index: i, ID: fmt.Sprintf("fc_%s", uuid.New().String()),
					Type: "function",
					FunctionCall: schema.FunctionCall{Name: fc.Name, Arguments: fc.Arguments},
				})
			}

			var hasMCPCalls bool
			for _, tc := range toolCalls {
				if mcpExecutor != nil && mcpExecutor.IsTool(tc.FunctionCall.Name) {
					hasMCPCalls = true
					break
				}
			}

			if hasMCPCalls {
				// Close the current message
				bufferEvent(store, responseID, &schema.ORStreamEvent{
					Type: "response.output_text.done", SequenceNumber: sequenceNumber,
					ItemID: currentMessageID, OutputIndex: &outputIndex,
					ContentIndex: &currentContentIndex, Text: strPtr(accumulatedText),
					Logprobs: emptyLogprobs(),
				})
				sequenceNumber++
				textPart := makeOutputTextPart(accumulatedText)
				bufferEvent(store, responseID, &schema.ORStreamEvent{
					Type: "response.content_part.done", SequenceNumber: sequenceNumber,
					ItemID: currentMessageID, OutputIndex: &outputIndex,
					ContentIndex: &currentContentIndex, Part: &textPart,
				})
				sequenceNumber++
				completedMsg := &schema.ORItemField{
					Type: "message", ID: currentMessageID, Status: "completed",
					Role: "assistant", Content: []schema.ORContentPart{textPart},
				}
				bufferEvent(store, responseID, &schema.ORStreamEvent{
					Type: "response.output_item.done", SequenceNumber: sequenceNumber,
					OutputIndex: &outputIndex, Item: completedMsg,
				})
				sequenceNumber++
				collectedOutputItems = append(collectedOutputItems, *completedMsg)

				// Append assistant message with tool calls
				assistantMsg := schema.Message{Role: "assistant", Content: result, ToolCalls: toolCalls}
				openAIReq.Messages = append(openAIReq.Messages, assistantMsg)

				// Execute MCP tools and emit events
				for _, tc := range toolCalls {
					outputIndex++
					functionCallItem := &schema.ORItemField{
						Type: "function_call", ID: tc.ID, Status: "completed",
						CallID: tc.ID, Name: tc.FunctionCall.Name, Arguments: tc.FunctionCall.Arguments,
					}
					bufferEvent(store, responseID, &schema.ORStreamEvent{
						Type: "response.output_item.added", SequenceNumber: sequenceNumber,
						OutputIndex: &outputIndex, Item: functionCallItem,
					})
					sequenceNumber++
					bufferEvent(store, responseID, &schema.ORStreamEvent{
						Type: "response.output_item.done", SequenceNumber: sequenceNumber,
						OutputIndex: &outputIndex, Item: functionCallItem,
					})
					sequenceNumber++
					collectedOutputItems = append(collectedOutputItems, *functionCallItem)

					if mcpExecutor == nil || !mcpExecutor.IsTool(tc.FunctionCall.Name) {
						continue
					}

					xlog.Debug("Executing MCP tool (background stream)", "tool", tc.FunctionCall.Name, "iteration", mcpIter)
					toolResult, toolErr := mcpExecutor.ExecuteTool(ctx, tc.FunctionCall.Name, tc.FunctionCall.Arguments)
					if toolErr != nil {
						toolResult = fmt.Sprintf("Error: %v", toolErr)
					}
					openAIReq.Messages = append(openAIReq.Messages, schema.Message{
						Role: "tool", Content: toolResult, StringContent: toolResult, ToolCallID: tc.ID, Name: tc.FunctionCall.Name,
					})

					outputIndex++
					outputItem := &schema.ORItemField{
						Type: "function_call_output", ID: fmt.Sprintf("fco_%s", uuid.New().String()),
						Status: "completed", CallID: tc.ID, Output: toolResult,
					}
					bufferEvent(store, responseID, &schema.ORStreamEvent{
						Type: "response.output_item.added", SequenceNumber: sequenceNumber,
						OutputIndex: &outputIndex, Item: outputItem,
					})
					sequenceNumber++
					bufferEvent(store, responseID, &schema.ORStreamEvent{
						Type: "response.output_item.done", SequenceNumber: sequenceNumber,
						OutputIndex: &outputIndex, Item: outputItem,
					})
					sequenceNumber++
					collectedOutputItems = append(collectedOutputItems, *outputItem)
				}
				continue // next MCP iteration
			}
		}

		// No MCP tools — close the message and break
		streamEventLogprobs := convertLogprobsForStreaming(lastLogprobs)
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

		textPart := makeOutputTextPartWithLogprobs(accumulatedText, lastLogprobs)
		bufferEvent(store, responseID, &schema.ORStreamEvent{
			Type:           "response.content_part.done",
			SequenceNumber: sequenceNumber,
			ItemID:         currentMessageID,
			OutputIndex:    &outputIndex,
			ContentIndex:   &currentContentIndex,
			Part:           &textPart,
		})
		sequenceNumber++

		completedMessageItem := &schema.ORItemField{
			Type:    "message",
			ID:      currentMessageID,
			Status:  "completed",
			Role:    "assistant",
			Content: []schema.ORContentPart{makeOutputTextPartWithLogprobs(accumulatedText, lastLogprobs)},
		}
		bufferEvent(store, responseID, &schema.ORStreamEvent{
			Type:           "response.output_item.done",
			SequenceNumber: sequenceNumber,
			OutputIndex:    &outputIndex,
			Item:           completedMessageItem,
		})
		sequenceNumber++
		collectedOutputItems = append(collectedOutputItems, *completedMessageItem)

		break
	} // end MCP background stream iteration loop

	// Build final response
	now := time.Now().Unix()
	response := buildORResponse(responseID, createdAt, &now, schema.ORStatusCompleted, input, collectedOutputItems, &schema.ORUsage{
		InputTokens:  lastTokenUsage.Prompt,
		OutputTokens: lastTokenUsage.Completion,
		TotalTokens:  lastTokenUsage.Prompt + lastTokenUsage.Completion,
	}, true)

	// Emit response.completed
	bufferEvent(store, responseID, &schema.ORStreamEvent{
		Type:           "response.completed",
		SequenceNumber: sequenceNumber,
		Response:       response,
	})

	return response, nil
}

// bufferEvent stores an SSE event in the response store for streaming resume
func bufferEvent(store *ResponseStore, responseID string, event *schema.ORStreamEvent) {
	normalizeORStreamEvent(event)
	if err := store.AppendEvent(responseID, event); err != nil {
		xlog.Error("Failed to buffer event", "response_id", responseID, "error", err)
	}
}

// handleOpenResponsesNonStream handles non-streaming responses
func handleOpenResponsesNonStream(c echo.Context, responseID string, createdAt int64, input *schema.OpenResponsesRequest, cfg *config.ModelConfig, ml *model.ModelLoader, cl *config.ModelConfigLoader, appConfig *config.ApplicationConfig, predInput string, openAIReq *schema.OpenAIRequest, funcs functions.Functions, shouldUseFn bool, shouldStore bool, mcpExecutor mcpTools.ToolExecutor, evaluator *templates.Evaluator, mcpIteration int) error {
	mcpMaxIterations := 10
	if cfg.Agent.MaxIterations > 0 {
		mcpMaxIterations = cfg.Agent.MaxIterations
	}
	if mcpIteration > mcpMaxIterations {
		return sendOpenResponsesError(c, 500, "server_error", "MCP iteration limit reached", "")
	}
	// Populate openAIReq fields for ComputeChoices
	openAIReq.Tools = convertORToolsToOpenAIFormat(input.Tools)
	openAIReq.ToolsChoice = input.ToolChoice
	if input.TopLogprobs != nil && *input.TopLogprobs > 0 {
		openAIReq.TopLogprobs = input.TopLogprobs
		openAIReq.Logprobs = schema.LogprobsValue{Enabled: true}
	}
	openAIReq.LogitBias = input.LogitBias

	var result string
	cb := func(s string, c *[]schema.Choice) {
		result = s
	}
	choices, tokenUsage, chatDeltas, err := openaiEndpoint.ComputeChoices(openAIReq, predInput, cfg, cl, appConfig, ml, cb, nil)
	if err != nil {
		xlog.Error("Open Responses model inference failed", "error", err)
		return sendOpenResponsesError(c, 500, "model_error", fmt.Sprintf("model inference failed: %v", err), "")
	}
	var resultLogprobs *schema.Logprobs
	if len(choices) > 0 {
		resultLogprobs = choices[0].Logprobs
	}
	xlog.Debug("Open Responses - Raw model result", "result", result, "shouldUseFn", shouldUseFn)

	// Detect if thinking token is already in prompt or template
	var template string
	if cfg.TemplateConfig.UseTokenizerTemplate {
		template = cfg.GetModelTemplate()
	} else {
		template = predInput
	}
	thinkingStartToken := reason.DetectThinkingStartToken(template, &cfg.ReasoningConfig)

	// Extract reasoning from result before cleaning
	reasoningContent, cleanedResult := reason.ExtractReasoningWithConfig(result, thinkingStartToken, cfg.ReasoningConfig)

	// Parse tool calls if using functions
	var outputItems []schema.ORItemField
	var toolCalls []schema.ToolCall

	// Add reasoning item if reasoning was found (reasoning comes first per spec)
	if reasoningContent != "" {
		reasoningItem := schema.ORItemField{
			Type:    "reasoning",
			ID:      fmt.Sprintf("reasoning_%s", uuid.New().String()),
			Status:  "completed",
			Content: []schema.ORContentPart{makeOutputTextPart(reasoningContent)},
		}
		outputItems = append(outputItems, reasoningItem)
		xlog.Debug("Open Responses - Extracted reasoning", "reasoning_length", len(reasoningContent))
	}

	if shouldUseFn {
		var funcCallResults []functions.FuncCallResults
		var textContent string

		// Try pre-parsed tool calls from C++ autoparser first
		if deltaToolCalls := functions.ToolCallsFromChatDeltas(chatDeltas); len(deltaToolCalls) > 0 {
			xlog.Debug("[ChatDeltas] OpenResponses: using pre-parsed tool calls", "count", len(deltaToolCalls))
			funcCallResults = deltaToolCalls
			textContent = functions.ContentFromChatDeltas(chatDeltas)
		} else {
			xlog.Debug("[ChatDeltas] OpenResponses: no pre-parsed tool calls, falling back to Go-side text parsing")
			// Clean up the result (already extracted reasoning above)
			cleanedResult = functions.CleanupLLMResult(cleanedResult, cfg.FunctionsConfig)
			funcCallResults = functions.ParseFunctionCall(cleanedResult, cfg.FunctionsConfig)
			textContent = functions.ParseTextContent(cleanedResult, cfg.FunctionsConfig)
		}
		xlog.Debug("[ChatDeltas] OpenResponses: final tool call decision", "count", len(funcCallResults), "textContent", textContent)

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
					var args map[string]any
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

		// MCP server-side tool execution: if any tool calls are MCP tools, execute and re-run
		if mcpExecutor != nil && mcpExecutor.HasTools() && len(toolCalls) > 0 {
			var hasMCPCalls bool
			for _, tc := range toolCalls {
				if mcpExecutor != nil && mcpExecutor.IsTool(tc.FunctionCall.Name) {
					hasMCPCalls = true
					break
				}
			}
			if hasMCPCalls {
				// Append assistant message with tool_calls to conversation
				assistantMsg := schema.Message{Role: "assistant", Content: result, ToolCalls: toolCalls}
				openAIReq.Messages = append(openAIReq.Messages, assistantMsg)

				// Execute each MCP tool call and append results
				for _, tc := range toolCalls {
					if mcpExecutor == nil || !mcpExecutor.IsTool(tc.FunctionCall.Name) {
						continue
					}
					xlog.Debug("Executing MCP tool (Open Responses)", "tool", tc.FunctionCall.Name)
					toolResult, toolErr := mcpExecutor.ExecuteTool(
						c.Request().Context(), tc.FunctionCall.Name, tc.FunctionCall.Arguments,
					)
					if toolErr != nil {
						xlog.Error("MCP tool execution failed", "tool", tc.FunctionCall.Name, "error", toolErr)
						toolResult = fmt.Sprintf("Error: %v", toolErr)
					}
					openAIReq.Messages = append(openAIReq.Messages, schema.Message{
						Role: "tool", Content: toolResult, StringContent: toolResult, ToolCallID: tc.ID, Name: tc.FunctionCall.Name,
					})

					// Collect function_call + function_call_output items for the response
					outputItems = append(outputItems, schema.ORItemField{
						Type: "function_call", ID: fmt.Sprintf("fc_%s", uuid.New().String()),
						Status: "completed", CallID: tc.ID, Name: tc.FunctionCall.Name, Arguments: tc.FunctionCall.Arguments,
					})
					outputItems = append(outputItems, schema.ORItemField{
						Type: "function_call_output", ID: fmt.Sprintf("fco_%s", uuid.New().String()),
						Status: "completed", CallID: tc.ID, Output: toolResult,
					})
				}

				// Re-template and re-run inference
				predInput = evaluator.TemplateMessages(*openAIReq, openAIReq.Messages, cfg, funcs, shouldUseFn)
				return handleOpenResponsesNonStream(c, responseID, createdAt, input, cfg, ml, cl, appConfig, predInput, openAIReq, funcs, shouldUseFn, shouldStore, mcpExecutor, evaluator, mcpIteration+1)
			}
		}

		// Add message item with text content (include logprobs if available)
		if textContent != "" {
			outputItems = append(outputItems, schema.ORItemField{
				Type:    "message",
				ID:      fmt.Sprintf("msg_%s", uuid.New().String()),
				Status:  "completed",
				Role:    "assistant",
				Content: []schema.ORContentPart{makeOutputTextPartWithLogprobs(textContent, resultLogprobs)},
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

		// If we have no output items but the model did produce output, include the cleaned result as a message
		hasMessageItem := false
		for _, item := range outputItems {
			if item.Type == "message" {
				hasMessageItem = true
				break
			}
		}
		if !hasMessageItem && cleanedResult != "" {
			xlog.Debug("Open Responses - No parsed output, falling back to cleaned result")
			outputItems = append(outputItems, schema.ORItemField{
				Type:    "message",
				ID:      fmt.Sprintf("msg_%s", uuid.New().String()),
				Status:  "completed",
				Role:    "assistant",
				Content: []schema.ORContentPart{makeOutputTextPartWithLogprobs(cleanedResult, resultLogprobs)},
			})
		}
	} else if !shouldUseFn && cfg.FunctionsConfig.AutomaticToolParsingFallback && cleanedResult != "" {
		// Automatic tool parsing fallback: no tools in request but model emitted tool call markup
		parsed := functions.ParseFunctionCall(cleanedResult, cfg.FunctionsConfig)
		if len(parsed) > 0 {
			stripped := functions.StripToolCallMarkup(cleanedResult)
			if stripped != "" {
				outputItems = append(outputItems, schema.ORItemField{
					Type:    "message",
					ID:      fmt.Sprintf("msg_%s", uuid.New().String()),
					Status:  "completed",
					Role:    "assistant",
					Content: []schema.ORContentPart{makeOutputTextPartWithLogprobs(stripped, resultLogprobs)},
				})
			}
			for _, fc := range parsed {
				toolCallID := fc.ID
				if toolCallID == "" {
					toolCallID = fmt.Sprintf("fc_%s", uuid.New().String())
				}
				outputItems = append(outputItems, schema.ORItemField{
					Type:      "function_call",
					ID:        fmt.Sprintf("fc_%s", uuid.New().String()),
					Status:    "completed",
					CallID:    toolCallID,
					Name:      fc.Name,
					Arguments: fc.Arguments,
				})
			}
		} else {
			outputItems = append(outputItems, schema.ORItemField{
				Type:    "message",
				ID:      fmt.Sprintf("msg_%s", uuid.New().String()),
				Status:  "completed",
				Role:    "assistant",
				Content: []schema.ORContentPart{makeOutputTextPartWithLogprobs(cleanedResult, resultLogprobs)},
			})
		}
	} else {
		// Simple text response (include logprobs if available)
		messageItem := schema.ORItemField{
			Type:    "message",
			ID:      fmt.Sprintf("msg_%s", uuid.New().String()),
			Status:  "completed",
			Role:    "assistant",
			Content: []schema.ORContentPart{makeOutputTextPartWithLogprobs(cleanedResult, resultLogprobs)},
		}
		outputItems = append(outputItems, messageItem)
	}

	// Calculate reasoning tokens (approximate: character count / 4)
	reasoningTokens := 0
	if reasoningContent != "" {
		// Simple estimation: ~4 characters per token
		reasoningTokens = len(reasoningContent) / 4
		if reasoningTokens == 0 && len(reasoningContent) > 0 {
			reasoningTokens = 1
		}
	}

	// Build response with all required fields
	now := time.Now().Unix()
	response := buildORResponse(responseID, createdAt, &now, "completed", input, outputItems, &schema.ORUsage{
		InputTokens:  tokenUsage.Prompt,
		OutputTokens: tokenUsage.Completion,
		TotalTokens:  tokenUsage.Prompt + tokenUsage.Completion,
		OutputTokensDetails: &schema.OROutputTokensDetails{
			ReasoningTokens: reasoningTokens,
		},
	}, shouldStore)

	// Store response for future reference (if enabled)
	if shouldStore {
		store := GetGlobalStore()
		store.Store(responseID, input, response)
	}

	return c.JSON(200, response)
}

// handleOpenResponsesStream handles streaming responses
func handleOpenResponsesStream(c echo.Context, responseID string, createdAt int64, input *schema.OpenResponsesRequest, cfg *config.ModelConfig, ml *model.ModelLoader, cl *config.ModelConfigLoader, appConfig *config.ApplicationConfig, predInput string, openAIReq *schema.OpenAIRequest, funcs functions.Functions, shouldUseFn bool, shouldStore bool, mcpExecutor mcpTools.ToolExecutor, evaluator *templates.Evaluator) error {
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

	// Populate openAIReq fields for ComputeChoices
	openAIReq.Tools = convertORToolsToOpenAIFormat(input.Tools)
	openAIReq.ToolsChoice = input.ToolChoice
	if input.TopLogprobs != nil && *input.TopLogprobs > 0 {
		openAIReq.TopLogprobs = input.TopLogprobs
		openAIReq.Logprobs = schema.LogprobsValue{Enabled: true}
	}
	openAIReq.LogitBias = input.LogitBias

	// Detect if thinking token is already in prompt or template
	var template string
	if cfg.TemplateConfig.UseTokenizerTemplate {
		template = cfg.GetModelTemplate()
	} else {
		template = predInput
	}
	thinkingStartToken := reason.DetectThinkingStartToken(template, &cfg.ReasoningConfig)

	// Track state for streaming
	var currentMessageID string
	var currentContentIndex int
	var accumulatedText string
	var lastEmittedToolCallCount int
	outputIndex := 0
	inToolCallMode := false

	// Track reasoning state for streaming
	var currentReasoningID string
	var currentReasoningContentIndex int
	var reasoningTokens int
	extractor := reason.NewReasoningExtractor(thinkingStartToken, cfg.ReasoningConfig)

	// Collect all output items for storage
	var collectedOutputItems []schema.ORItemField

	if shouldUseFn {
		mcpStreamMaxIterations := 10
		if cfg.Agent.MaxIterations > 0 {
			mcpStreamMaxIterations = cfg.Agent.MaxIterations
		}
		hasMCPToolsStream := mcpExecutor != nil && mcpExecutor.HasTools()

		var result, finalReasoning, finalCleanedResult string
		var textContent string
		var parsedToolCalls []functions.FuncCallResults
		var toolCalls []functions.FuncCallResults
		var lastStreamTokenUsage backend.TokenUsage
		var lastStreamLogprobs *schema.Logprobs

		for mcpStreamIter := 0; mcpStreamIter <= mcpStreamMaxIterations; mcpStreamIter++ {
		if mcpStreamIter > 0 {
			// Reset reasoning and tool-call state for re-inference so reasoning
			// extraction runs again on subsequent iterations
			inToolCallMode = false
			extractor.Reset()
			currentMessageID = ""
			lastEmittedToolCallCount = 0
			currentReasoningID = ""

			predInput = evaluator.TemplateMessages(*openAIReq, openAIReq.Messages, cfg, funcs, shouldUseFn)
			xlog.Debug("Open Responses stream MCP re-templating", "iteration", mcpStreamIter)
		}

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

			// If no tool calls detected yet, handle reasoning and text
			if !inToolCallMode {
				reasoningDelta, contentDelta := extractor.ProcessToken(token)

				// Handle reasoning item
				if extractor.Reasoning() != "" {
					// Check if we need to create reasoning item
					if currentReasoningID == "" {
						outputIndex++
						currentReasoningID = fmt.Sprintf("reasoning_%s", uuid.New().String())
						reasoningItem := &schema.ORItemField{
							Type:   "reasoning",
							ID:     currentReasoningID,
							Status: "in_progress",
						}
						sendSSEEvent(c, &schema.ORStreamEvent{
							Type:           "response.output_item.added",
							SequenceNumber: sequenceNumber,
							OutputIndex:    &outputIndex,
							Item:           reasoningItem,
						})
						sequenceNumber++

						// Emit content_part.added for reasoning
						currentReasoningContentIndex = 0
						emptyPart := makeOutputTextPart("")
						sendSSEEvent(c, &schema.ORStreamEvent{
							Type:           "response.content_part.added",
							SequenceNumber: sequenceNumber,
							ItemID:         currentReasoningID,
							OutputIndex:    &outputIndex,
							ContentIndex:   &currentReasoningContentIndex,
							Part:           &emptyPart,
						})
						sequenceNumber++
					}

					// Emit reasoning delta if there's new content
					if reasoningDelta != "" {
						sendSSEEvent(c, &schema.ORStreamEvent{
							Type:           "response.output_text.delta",
							SequenceNumber: sequenceNumber,
							ItemID:         currentReasoningID,
							OutputIndex:    &outputIndex,
							ContentIndex:   &currentReasoningContentIndex,
							Delta:          strPtr(reasoningDelta),
							Logprobs:       emptyLogprobs(),
						})
						sequenceNumber++
						c.Response().Flush()
					}
				}

				// Only emit message content if there's actual content (not just reasoning)
				if contentDelta != "" {
					if currentMessageID == "" {
						// Emit output_item.added for message
						outputIndex++
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
						Delta:          strPtr(contentDelta),
						Logprobs:       emptyLogprobs(),
					})
					sequenceNumber++
					c.Response().Flush()
				}
			}
			return true
		}

		var ccResult string
		ccCb := func(s string, c *[]schema.Choice) {
			ccResult = s
		}
		choices, ccTokenUsage, chatDeltas, err := openaiEndpoint.ComputeChoices(openAIReq, predInput, cfg, cl, appConfig, ml, ccCb, tokenCallback)
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
		result = ccResult
		lastStreamTokenUsage = ccTokenUsage
		if len(choices) > 0 {
			lastStreamLogprobs = choices[0].Logprobs
		}

		// Source reasoning from: (1) ChatDeltas from C++ autoparser, (2) extractor's
		// streaming state, (3) final extraction from the finetuned result.
		if chatDeltaReasoning := functions.ReasoningFromChatDeltas(chatDeltas); chatDeltaReasoning != "" {
			finalReasoning = chatDeltaReasoning
			finalCleanedResult = functions.ContentFromChatDeltas(chatDeltas)
			if finalCleanedResult == "" {
				finalCleanedResult = extractor.CleanedContent()
			}
		} else {
			finalReasoning = extractor.Reasoning()
			finalCleanedResult = extractor.CleanedContent()
		}
		if finalReasoning == "" && finalCleanedResult == "" {
			finalReasoning, finalCleanedResult = reason.ExtractReasoningWithConfig(result, thinkingStartToken, cfg.ReasoningConfig)
		}

		// Close reasoning item if it exists and wasn't closed yet
		if currentReasoningID != "" && finalReasoning != "" {
			// Emit output_text.done for reasoning
			sendSSEEvent(c, &schema.ORStreamEvent{
				Type:           "response.output_text.done",
				SequenceNumber: sequenceNumber,
				ItemID:         currentReasoningID,
				OutputIndex:    &outputIndex,
				ContentIndex:   &currentReasoningContentIndex,
				Text:           strPtr(finalReasoning),
				Logprobs:       emptyLogprobs(),
			})
			sequenceNumber++

			// Emit content_part.done for reasoning
			reasoningPart := makeOutputTextPart(finalReasoning)
			sendSSEEvent(c, &schema.ORStreamEvent{
				Type:           "response.content_part.done",
				SequenceNumber: sequenceNumber,
				ItemID:         currentReasoningID,
				OutputIndex:    &outputIndex,
				ContentIndex:   &currentReasoningContentIndex,
				Part:           &reasoningPart,
			})
			sequenceNumber++

			// Emit output_item.done for reasoning
			reasoningItem := &schema.ORItemField{
				Type:    "reasoning",
				ID:      currentReasoningID,
				Status:  "completed",
				Content: []schema.ORContentPart{reasoningPart},
			}
			sendSSEEvent(c, &schema.ORStreamEvent{
				Type:           "response.output_item.done",
				SequenceNumber: sequenceNumber,
				OutputIndex:    &outputIndex,
				Item:           reasoningItem,
			})
			sequenceNumber++

			// Collect reasoning item for storage
			collectedOutputItems = append(collectedOutputItems, *reasoningItem)

			// Calculate reasoning tokens
			reasoningTokens = len(finalReasoning) / 4
			if reasoningTokens == 0 && len(finalReasoning) > 0 {
				reasoningTokens = 1
			}
		}

		parsedToolCalls = nil
		textContent = ""

		// Try pre-parsed tool calls from C++ autoparser first
		if deltaToolCalls := functions.ToolCallsFromChatDeltas(chatDeltas); len(deltaToolCalls) > 0 {
			xlog.Debug("[ChatDeltas] OpenResponses Stream: using pre-parsed tool calls", "count", len(deltaToolCalls))
			parsedToolCalls = deltaToolCalls
			textContent = functions.ContentFromChatDeltas(chatDeltas)
		} else {
			xlog.Debug("[ChatDeltas] OpenResponses Stream: no pre-parsed tool calls, falling back to Go-side text parsing")
			cleanedResult := functions.CleanupLLMResult(finalCleanedResult, cfg.FunctionsConfig)
			parsedToolCalls = functions.ParseFunctionCall(cleanedResult, cfg.FunctionsConfig)
			textContent = functions.ParseTextContent(cleanedResult, cfg.FunctionsConfig)
		}

		// Handle noAction function (model chose to respond without tool)
		noActionName := "answer"
		if cfg.FunctionsConfig.NoActionFunctionName != "" {
			noActionName = cfg.FunctionsConfig.NoActionFunctionName
		}

		// Filter out noAction calls and extract the message
		toolCalls = nil
		for _, fc := range parsedToolCalls {
			if fc.Name == noActionName {
				// This is a text response, not a tool call
				if fc.Arguments != "" {
					var args map[string]any
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

		// MCP streaming tool execution: check if any tool calls are MCP tools
		if hasMCPToolsStream && len(toolCalls) > 0 {
			var hasMCPCalls bool
			for _, tc := range toolCalls {
				if mcpExecutor != nil && mcpExecutor.IsTool(tc.Name) {
					hasMCPCalls = true
					break
				}
			}
			if hasMCPCalls {
				// Build schema.ToolCall list for the assistant message
				var schemaToolCalls []schema.ToolCall
				for i, tc := range toolCalls {
					schemaToolCalls = append(schemaToolCalls, schema.ToolCall{
						Index: i, ID: fmt.Sprintf("fc_%s", uuid.New().String()),
						Type: "function",
						FunctionCall: schema.FunctionCall{Name: tc.Name, Arguments: tc.Arguments},
					})
				}
				assistantMsg := schema.Message{Role: "assistant", Content: result, ToolCalls: schemaToolCalls}
				openAIReq.Messages = append(openAIReq.Messages, assistantMsg)

				for idx, tc := range toolCalls {
					tcID := schemaToolCalls[idx].ID

					// Emit function_call item
					outputIndex++
					functionCallItem := &schema.ORItemField{
						Type: "function_call", ID: tcID, Status: "completed",
						CallID: tcID, Name: tc.Name, Arguments: tc.Arguments,
					}
					sendSSEEvent(c, &schema.ORStreamEvent{
						Type: "response.output_item.added", SequenceNumber: sequenceNumber,
						OutputIndex: &outputIndex, Item: functionCallItem,
					})
					sequenceNumber++
					sendSSEEvent(c, &schema.ORStreamEvent{
						Type: "response.output_item.done", SequenceNumber: sequenceNumber,
						OutputIndex: &outputIndex, Item: functionCallItem,
					})
					sequenceNumber++
					collectedOutputItems = append(collectedOutputItems, *functionCallItem)

					if mcpExecutor == nil || !mcpExecutor.IsTool(tc.Name) {
						continue
					}

					// Execute MCP tool
					xlog.Debug("Executing MCP tool (Open Responses stream)", "tool", tc.Name, "iteration", mcpStreamIter)
					toolResult, toolErr := mcpExecutor.ExecuteTool(
						input.Context, tc.Name, tc.Arguments,
					)
					if toolErr != nil {
						xlog.Error("MCP tool execution failed", "tool", tc.Name, "error", toolErr)
						toolResult = fmt.Sprintf("Error: %v", toolErr)
					}
					openAIReq.Messages = append(openAIReq.Messages, schema.Message{
						Role: "tool", Content: toolResult, StringContent: toolResult, ToolCallID: tcID, Name: tc.Name,
					})

					// Emit function_call_output item
					outputIndex++
					outputItem := &schema.ORItemField{
						Type: "function_call_output", ID: fmt.Sprintf("fco_%s", uuid.New().String()),
						Status: "completed", CallID: tcID, Output: toolResult,
					}
					sendSSEEvent(c, &schema.ORStreamEvent{
						Type: "response.output_item.added", SequenceNumber: sequenceNumber,
						OutputIndex: &outputIndex, Item: outputItem,
					})
					sequenceNumber++
					sendSSEEvent(c, &schema.ORStreamEvent{
						Type: "response.output_item.done", SequenceNumber: sequenceNumber,
						OutputIndex: &outputIndex, Item: outputItem,
					})
					sequenceNumber++
					collectedOutputItems = append(collectedOutputItems, *outputItem)
				}
				c.Response().Flush()
				xlog.Debug("MCP streaming tools executed, re-running inference", "iteration", mcpStreamIter)
				continue // next MCP stream iteration
			}
		}


		// Convert logprobs for streaming events
		streamEventLogprobs := convertLogprobsForStreaming(lastStreamLogprobs)

		// If we have no output but the model did produce something, use the cleaned result (without reasoning tags)
		if textContent == "" && len(toolCalls) == 0 && finalCleanedResult != "" {
			xlog.Debug("Open Responses Stream - No parsed output, using cleaned result")
			textContent = finalCleanedResult
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
			textPart := makeOutputTextPartWithLogprobs(textContent, lastStreamLogprobs)
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
				Content: []schema.ORContentPart{makeOutputTextPartWithLogprobs(textContent, lastStreamLogprobs)},
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

		break // no MCP tools to execute, exit loop
		} // end MCP stream iteration loop

		// Build final response with all items (include reasoning first, then messages, then tool calls)
		var allOutputItems []schema.ORItemField
		// Add reasoning item if it exists
		if currentReasoningID != "" && finalReasoning != "" {
			allOutputItems = append(allOutputItems, schema.ORItemField{
				Type:    "reasoning",
				ID:      currentReasoningID,
				Status:  "completed",
				Content: []schema.ORContentPart{makeOutputTextPart(finalReasoning)},
			})
		}
		// Add message item
		if currentMessageID != "" && textContent != "" {
			allOutputItems = append(allOutputItems, schema.ORItemField{
				Type:    "message",
				ID:      currentMessageID,
				Status:  "completed",
				Role:    "assistant",
				Content: []schema.ORContentPart{makeOutputTextPartWithLogprobs(textContent, lastStreamLogprobs)},
			})
		}
		// Add tool call items
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
			InputTokens:  lastStreamTokenUsage.Prompt,
			OutputTokens: lastStreamTokenUsage.Completion,
			TotalTokens:  lastStreamTokenUsage.Prompt + lastStreamTokenUsage.Completion,
			OutputTokensDetails: &schema.OROutputTokensDetails{
				ReasoningTokens: reasoningTokens,
			},
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

	// Stream text deltas with reasoning extraction
	tokenCallback := func(token string, tokenUsage backend.TokenUsage) bool {
		accumulatedText += token
		reasoningDelta, contentDelta := extractor.ProcessToken(token)

		// Handle reasoning item
		if extractor.Reasoning() != "" {
			// Check if we need to create reasoning item
			if currentReasoningID == "" {
				outputIndex++
				currentReasoningID = fmt.Sprintf("reasoning_%s", uuid.New().String())
				reasoningItem := &schema.ORItemField{
					Type:   "reasoning",
					ID:     currentReasoningID,
					Status: "in_progress",
				}
				sendSSEEvent(c, &schema.ORStreamEvent{
					Type:           "response.output_item.added",
					SequenceNumber: sequenceNumber,
					OutputIndex:    &outputIndex,
					Item:           reasoningItem,
				})
				sequenceNumber++

				// Emit content_part.added for reasoning
				currentReasoningContentIndex = 0
				emptyPart := makeOutputTextPart("")
				sendSSEEvent(c, &schema.ORStreamEvent{
					Type:           "response.content_part.added",
					SequenceNumber: sequenceNumber,
					ItemID:         currentReasoningID,
					OutputIndex:    &outputIndex,
					ContentIndex:   &currentReasoningContentIndex,
					Part:           &emptyPart,
				})
				sequenceNumber++
			}

			// Emit reasoning delta if there's new content
			if reasoningDelta != "" {
				sendSSEEvent(c, &schema.ORStreamEvent{
					Type:           "response.output_text.delta",
					SequenceNumber: sequenceNumber,
					ItemID:         currentReasoningID,
					OutputIndex:    &outputIndex,
					ContentIndex:   &currentReasoningContentIndex,
					Delta:          strPtr(reasoningDelta),
					Logprobs:       emptyLogprobs(),
				})
				sequenceNumber++
				c.Response().Flush()
			}
		}

		// Only emit message content if there's actual content (not just reasoning)
		if contentDelta != "" {
			// Emit text delta
			sendSSEEvent(c, &schema.ORStreamEvent{
				Type:           "response.output_text.delta",
				SequenceNumber: sequenceNumber,
				ItemID:         currentMessageID,
				OutputIndex:    &outputIndex,
				ContentIndex:   &currentContentIndex,
				Delta:          strPtr(contentDelta),
				Logprobs:       emptyLogprobs(),
			})
			sequenceNumber++
			c.Response().Flush()
		}
		return true
	}

	var noToolResult string
	noToolCb := func(s string, c *[]schema.Choice) {
		noToolResult = s
	}
	noToolChoices, noToolTokenUsage, noToolChatDeltas, err := openaiEndpoint.ComputeChoices(openAIReq, predInput, cfg, cl, appConfig, ml, noToolCb, tokenCallback)
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
	result := noToolResult
	var noToolLogprobs *schema.Logprobs
	if len(noToolChoices) > 0 {
		noToolLogprobs = noToolChoices[0].Logprobs
	}

	// Source reasoning from: (1) ChatDeltas from C++ autoparser, (2) extractor's
	// streaming state, (3) final extraction from the finetuned result.
	var finalReasoning, finalCleanedResult string
	if chatDeltaReasoning := functions.ReasoningFromChatDeltas(noToolChatDeltas); chatDeltaReasoning != "" {
		finalReasoning = chatDeltaReasoning
		finalCleanedResult = functions.ContentFromChatDeltas(noToolChatDeltas)
		if finalCleanedResult == "" {
			finalCleanedResult = extractor.CleanedContent()
		}
	} else {
		finalReasoning = extractor.Reasoning()
		finalCleanedResult = extractor.CleanedContent()
	}
	if finalReasoning == "" && finalCleanedResult == "" {
		finalReasoning, finalCleanedResult = reason.ExtractReasoningWithConfig(result, thinkingStartToken, cfg.ReasoningConfig)
	}

	// Close reasoning item if it exists and wasn't closed yet
	if currentReasoningID != "" && finalReasoning != "" {
		// Emit output_text.done for reasoning
		sendSSEEvent(c, &schema.ORStreamEvent{
			Type:           "response.output_text.done",
			SequenceNumber: sequenceNumber,
			ItemID:         currentReasoningID,
			OutputIndex:    &outputIndex,
			ContentIndex:   &currentReasoningContentIndex,
			Text:           strPtr(finalReasoning),
			Logprobs:       emptyLogprobs(),
		})
		sequenceNumber++

		// Emit content_part.done for reasoning
		reasoningPart := makeOutputTextPart(finalReasoning)
		sendSSEEvent(c, &schema.ORStreamEvent{
			Type:           "response.content_part.done",
			SequenceNumber: sequenceNumber,
			ItemID:         currentReasoningID,
			OutputIndex:    &outputIndex,
			ContentIndex:   &currentReasoningContentIndex,
			Part:           &reasoningPart,
		})
		sequenceNumber++

		// Emit output_item.done for reasoning
		reasoningItem := &schema.ORItemField{
			Type:    "reasoning",
			ID:      currentReasoningID,
			Status:  "completed",
			Content: []schema.ORContentPart{reasoningPart},
		}
		sendSSEEvent(c, &schema.ORStreamEvent{
			Type:           "response.output_item.done",
			SequenceNumber: sequenceNumber,
			OutputIndex:    &outputIndex,
			Item:           reasoningItem,
		})
		sequenceNumber++

		// Collect reasoning item for storage
		collectedOutputItems = append(collectedOutputItems, *reasoningItem)

		// Calculate reasoning tokens
		reasoningTokens = len(finalReasoning) / 4
		if reasoningTokens == 0 && len(finalReasoning) > 0 {
			reasoningTokens = 1
		}
	}

	result = finalCleanedResult

	// Automatic tool parsing fallback for streaming: parse tool calls from accumulated text
	var streamFallbackToolCalls []functions.FuncCallResults
	if cfg.FunctionsConfig.AutomaticToolParsingFallback && result != "" {
		streamFallbackToolCalls = functions.ParseFunctionCall(result, cfg.FunctionsConfig)
		if len(streamFallbackToolCalls) > 0 {
			result = functions.StripToolCallMarkup(result)
		}
	}

	// Convert logprobs for streaming events
	mcpStreamLogprobs := convertLogprobsForStreaming(noToolLogprobs)

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
	resultPart := makeOutputTextPartWithLogprobs(result, noToolLogprobs)
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
	messageItem.Content = []schema.ORContentPart{makeOutputTextPartWithLogprobs(result, noToolLogprobs)}
	sendSSEEvent(c, &schema.ORStreamEvent{
		Type:           "response.output_item.done",
		SequenceNumber: sequenceNumber,
		OutputIndex:    &outputIndex,
		Item:           messageItem,
	})
	sequenceNumber++

	// Emit function_call items from automatic tool parsing fallback
	for _, fc := range streamFallbackToolCalls {
		toolCallID := fc.ID
		if toolCallID == "" {
			toolCallID = fmt.Sprintf("fc_%s", uuid.New().String())
		}
		outputIndex++
		functionCallItem := &schema.ORItemField{
			Type:      "function_call",
			ID:        toolCallID,
			Status:    "completed",
			CallID:    toolCallID,
			Name:      fc.Name,
			Arguments: fc.Arguments,
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
		collectedOutputItems = append(collectedOutputItems, *functionCallItem)
	}

	// Emit response.completed
	now := time.Now().Unix()

	// Collect final output items (reasoning first, then messages, then tool calls)
	var finalOutputItems []schema.ORItemField
	// Add reasoning item if it exists
	if currentReasoningID != "" && finalReasoning != "" {
		finalOutputItems = append(finalOutputItems, schema.ORItemField{
			Type:    "reasoning",
			ID:      currentReasoningID,
			Status:  "completed",
			Content: []schema.ORContentPart{makeOutputTextPart(finalReasoning)},
		})
	}
	// Add message item
	if len(collectedOutputItems) > 0 {
		// Use collected items (may include reasoning already)
		for _, item := range collectedOutputItems {
			if item.Type == "message" {
				finalOutputItems = append(finalOutputItems, item)
			}
		}
	} else {
		finalOutputItems = append(finalOutputItems, *messageItem)
	}
	// Add function_call items from fallback
	for _, item := range collectedOutputItems {
		if item.Type == "function_call" {
			finalOutputItems = append(finalOutputItems, item)
		}
	}
	responseCompleted := buildORResponse(responseID, createdAt, &now, "completed", input, finalOutputItems, &schema.ORUsage{
		InputTokens:  noToolTokenUsage.Prompt,
		OutputTokens: noToolTokenUsage.Completion,
		TotalTokens:  noToolTokenUsage.Prompt + noToolTokenUsage.Completion,
		OutputTokensDetails: &schema.OROutputTokensDetails{
			ReasoningTokens: reasoningTokens,
		},
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

// sendSSEEvent sends a Server-Sent Event
func sendSSEEvent(c echo.Context, event *schema.ORStreamEvent) {
	normalizeORStreamEvent(event)
	data, err := json.Marshal(event)
	if err != nil {
		xlog.Error("Failed to marshal SSE event", "error", err)
		return
	}
	fmt.Fprintf(c.Response().Writer, "event: %s\ndata: %s\n\n", event.Type, string(data))
}

// normalizeORStreamEvent ensures required fields like Summary are never null.
func normalizeORStreamEvent(event *schema.ORStreamEvent) {
	if event.Item != nil && event.Item.Summary == nil {
		event.Item.Summary = []schema.ORContentPart{}
	}
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

	// Ensure Summary is never null on any output item
	for i := range outputItems {
		if outputItems[i].Summary == nil {
			outputItems[i].Summary = []schema.ORContentPart{}
		}
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
	var toolChoice any
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
	errorResp := map[string]any{
		"error": map[string]any{
			"type":    errorType,
			"message": message,
		},
	}
	if param != "" {
		errorResp["error"].(map[string]any)["param"] = param
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

// GetResponseEndpoint returns a handler for GET /responses/:id
// This endpoint is used for polling background responses or resuming streaming
// @Summary Get a response by ID
// @Description Retrieve a response by ID. Can be used for polling background responses or resuming streaming responses.
// @Param id path string true "Response ID"
// @Param stream query string false "Set to 'true' to resume streaming"
// @Param starting_after query int false "Sequence number to resume from (for streaming)"
// @Success 200 {object} schema.ORResponseResource "Response"
// @Failure 400 {object} map[string]any "Bad Request"
// @Failure 404 {object} map[string]any "Not Found"
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
// @Failure 400 {object} map[string]any "Bad Request"
// @Failure 404 {object} map[string]any "Not Found"
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
