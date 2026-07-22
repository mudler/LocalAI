package ollama

import (
	"encoding/json"
	"fmt"
	"math"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/xlog"
)

// writeNDJSON writes a JSON object followed by a newline to the response (NDJSON format)
func writeNDJSON(c echo.Context, v any) bool {
	data, err := json.Marshal(v)
	if err != nil {
		xlog.Error("Failed to marshal NDJSON", "error", err)
		return false
	}
	_, err = fmt.Fprintf(c.Response().Writer, "%s\n", data)
	if err != nil {
		return false
	}
	c.Response().Flush()
	return true
}

// ollamaError sends an Ollama-compatible JSON error response
func ollamaError(c echo.Context, statusCode int, message string) error {
	return c.JSON(statusCode, map[string]string{"error": message})
}

// applyOllamaOptions applies Ollama options to the model configuration
func applyOllamaOptions(opts *schema.OllamaOptions, cfg *config.ModelConfig) {
	if opts == nil {
		return
	}
	if opts.Temperature != nil {
		cfg.Temperature = opts.Temperature
	}
	if opts.TopP != nil {
		cfg.TopP = opts.TopP
	}
	if opts.TopK != nil {
		cfg.TopK = opts.TopK
	}
	if opts.NumPredict != nil {
		cfg.Maxtokens = opts.NumPredict
	}
	if opts.RepeatPenalty != 0 {
		cfg.RepeatPenalty = opts.RepeatPenalty
	}
	if opts.RepeatLastN != 0 {
		cfg.RepeatLastN = opts.RepeatLastN
	}
	if len(opts.Stop) > 0 {
		cfg.StopWords = append(cfg.StopWords, opts.Stop...)
	}
	if opts.NumCtx > 0 {
		numCtx := opts.NumCtx
		// The model configuration / hardware-aware defaults have already
		// populated cfg.ContextSize by the time we get here. Treat any
		// existing positive value as the server-side ceiling: an
		// unauthenticated client must never be able to *raise* the context
		// window, since that drives KV-cache allocation and an oversized
		// value (e.g. 2,000,000,000) can trigger a catastrophic OOM. A
		// smaller num_ctx is still honored. See issue #11022.
		if cfg.ContextSize != nil && *cfg.ContextSize > 0 && numCtx > *cfg.ContextSize {
			numCtx = *cfg.ContextSize
		}
		// Regardless of the ceiling, keep the value int32-safe: ContextSize is
		// cast to int32 before it reaches the backend (core/backend/options.go),
		// so a value above math.MaxInt32 would silently wrap into a negative
		// context size when no smaller ceiling exists.
		if numCtx > math.MaxInt32 {
			numCtx = math.MaxInt32
		}
		cfg.ContextSize = &numCtx
	}
}

// ollamaMessagesToOpenAI converts Ollama messages to OpenAI-compatible messages
func ollamaMessagesToOpenAI(messages []schema.OllamaMessage) []schema.Message {
	var result []schema.Message
	for _, msg := range messages {
		openAIMsg := schema.Message{
			Role:          msg.Role,
			StringContent: msg.Content,
			Content:       msg.Content,
		}

		// Convert base64 images to data URIs
		for _, img := range msg.Images {
			dataURI := fmt.Sprintf("data:image/png;base64,%s", img)
			openAIMsg.StringImages = append(openAIMsg.StringImages, dataURI)
		}

		result = append(result, openAIMsg)
	}
	return result
}