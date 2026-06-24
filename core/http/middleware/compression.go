package middleware

import (
	"context"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	compressionservice "github.com/mudler/LocalAI/core/services/compression"
	"github.com/mudler/LocalAI/pkg/tokens"
)

const contextKeyCompressionMetadata = "COMPRESSION_METADATA"

type ChatCompressor interface {
	Transform(context.Context, config.CompressionConfig, int, string, []schema.Message) ([]schema.Message, *compressionservice.Metadata, error)
}

func ContextCompression(compressor ChatCompressor) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if err := CompressChatRequest(c, compressor); err != nil {
				return err
			}
			return next(c)
		}
	}
}

func CompressChatRequest(c echo.Context, compressor ChatCompressor) error {
	cfg, ok := c.Get(CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig)
	if !ok || cfg == nil || !cfg.Compression.Enabled {
		return nil
	}
	if cfg.IsCloudProxyBackendPassthrough() {
		return echo.NewHTTPError(http.StatusBadRequest, "context compression is not supported by cloud-proxy passthrough models; configure translate mode or a local compressor model")
	}
	input, ok := c.Get(CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.OpenAIRequest)
	if !ok {
		return echo.NewHTTPError(http.StatusBadRequest, "context compression requires a chat request")
	}
	contextSize := config.DefaultContextSize
	if cfg.ContextSize != nil && *cfg.ContextSize > 0 {
		contextSize = *cfg.ContextSize
	}
	extraPayload := make(map[string]any)
	if len(input.Functions) > 0 {
		extraPayload["functions"] = input.Functions
	}
	if len(input.Tools) > 0 {
		extraPayload["tools"] = input.Tools
	}
	if input.FunctionCall != nil {
		extraPayload["function_call"] = input.FunctionCall
	}
	if input.ToolsChoice != nil {
		extraPayload["tool_choice"] = input.ToolsChoice
	}
	if input.ResponseFormat != nil {
		extraPayload["response_format"] = input.ResponseFormat
	}
	requestOverhead, err := tokens.CountPayload(extraPayload)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	if cfg.Maxtokens != nil && *cfg.Maxtokens > 0 {
		requestOverhead += *cfg.Maxtokens
	}
	messages, meta, err := compressor.Transform(c.Request().Context(), cfg.Compression, contextSize-requestOverhead, cfg.ModelID(), input.Messages)
	if err != nil {
		if compressionservice.IsOverflow(err) {
			return echo.NewHTTPError(http.StatusRequestEntityTooLarge, err.Error())
		}
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	input.Messages = messages
	if meta != nil {
		meta.OriginalTokens += requestOverhead
		meta.CompressedTokens += requestOverhead
		if previous, ok := c.Get(contextKeyCompressionMetadata).(*compressionservice.Metadata); ok && previous != nil {
			meta.OriginalTokens = previous.OriginalTokens
			meta.DroppedTurns += previous.DroppedTurns
			meta.SummaryTokens += previous.SummaryTokens
			meta.OverflowRecoveries += previous.OverflowRecoveries
		}
		c.Set(contextKeyCompressionMetadata, meta)
	}
	return nil
}

func CompressionMetadata(c echo.Context) *schema.CompressionMetadata {
	meta, ok := c.Get(contextKeyCompressionMetadata).(*compressionservice.Metadata)
	if !ok || meta == nil {
		return nil
	}
	return &schema.CompressionMetadata{
		OriginalTokens: meta.OriginalTokens, CompressedTokens: meta.CompressedTokens,
		DroppedTurns: meta.DroppedTurns, Compressor: meta.Compressor,
		SummaryTokens: meta.SummaryTokens, OverflowRecoveries: meta.OverflowRecoveries,
	}
}
