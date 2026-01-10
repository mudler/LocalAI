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

		// Template the prompt
		predInput := evaluator.TemplateMessages(*openAIReq, openAIReq.Messages, cfg, nil, false)
		xlog.Debug("Anthropic Messages - Prompt (after templating)", "prompt", predInput)

		if input.Stream {
			return handleAnthropicStream(c, id, input, cfg, ml, predInput)
		}

		return handleAnthropicNonStream(c, id, input, cfg, ml, predInput, openAIReq)
	}
}

func handleAnthropicNonStream(c echo.Context, id string, input *schema.AnthropicRequest, cfg *config.ModelConfig, ml *model.ModelLoader, predInput string, openAIReq *schema.OpenAIRequest) error {
	images := []string{}
	for _, m := range openAIReq.Messages {
		images = append(images, m.StringImages...)
	}

	predFunc, err := backend.ModelInference(
		input.Context, predInput, openAIReq.Messages, images, nil, nil, ml, cfg, nil, nil, nil, "", "", nil, nil, nil)
	if err != nil {
		xlog.Error("Anthropic model inference failed", "error", err)
		return sendAnthropicError(c, 500, "api_error", fmt.Sprintf("model inference failed: %v", err))
	}

	prediction, err := predFunc()
	if err != nil {
		xlog.Error("Anthropic prediction failed", "error", err)
		return sendAnthropicError(c, 500, "api_error", fmt.Sprintf("prediction failed: %v", err))
	}

	result := backend.Finetune(*cfg, predInput, prediction.Response)
	stopReason := "end_turn"

	resp := &schema.AnthropicResponse{
		ID:         fmt.Sprintf("msg_%s", id),
		Type:       "message",
		Role:       "assistant",
		Model:      input.Model,
		StopReason: &stopReason,
		Content: []schema.AnthropicContentBlock{
			{Type: "text", Text: result},
		},
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

func handleAnthropicStream(c echo.Context, id string, input *schema.AnthropicRequest, cfg *config.ModelConfig, ml *model.ModelLoader, predInput string) error {
	c.Response().Header().Set("Content-Type", "text/event-stream")
	c.Response().Header().Set("Cache-Control", "no-cache")
	c.Response().Header().Set("Connection", "keep-alive")

	// Create OpenAI messages for inference
	openAIMessages := convertAnthropicToOpenAIMessages(input)

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

	// Send content_block_start event
	contentBlockStart := schema.AnthropicStreamEvent{
		Type:         "content_block_start",
		Index:        0,
		ContentBlock: &schema.AnthropicContentBlock{Type: "text", Text: ""},
	}
	sendAnthropicSSE(c, contentBlockStart)

	// Stream content deltas
	tokenCallback := func(token string, usage backend.TokenUsage) bool {
		delta := schema.AnthropicStreamEvent{
			Type:  "content_block_delta",
			Index: 0,
			Delta: &schema.AnthropicStreamDelta{
				Type: "text_delta",
				Text: token,
			},
		}
		sendAnthropicSSE(c, delta)
		return true
	}

	predFunc, err := backend.ModelInference(
		input.Context, predInput, openAIMessages, images, nil, nil, ml, cfg, nil, nil, tokenCallback, "", "", nil, nil, nil)
	if err != nil {
		xlog.Error("Anthropic stream model inference failed", "error", err)
		return sendAnthropicError(c, 500, "api_error", fmt.Sprintf("model inference failed: %v", err))
	}

	prediction, err := predFunc()
	if err != nil {
		xlog.Error("Anthropic stream prediction failed", "error", err)
		return sendAnthropicError(c, 500, "api_error", fmt.Sprintf("prediction failed: %v", err))
	}

	// Send content_block_stop event
	contentBlockStop := schema.AnthropicStreamEvent{
		Type:  "content_block_stop",
		Index: 0,
	}
	sendAnthropicSSE(c, contentBlockStop)

	// Send message_delta event with stop_reason
	stopReason := "end_turn"
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
		messages = append(messages, schema.Message{
			Role:          "system",
			StringContent: input.System,
			Content:       input.System,
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
					}
				}
			}
			openAIMsg.StringContent = textContent
			openAIMsg.Content = textContent
			openAIMsg.StringImages = stringImages
		}

		messages = append(messages, openAIMsg)
	}

	return messages
}
