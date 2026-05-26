package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/routing/billing"
	"github.com/mudler/xlog"
)

// usageResponseBody is the minimal structure we need from an OpenAI-shaped
// JSON response. Anthropic responses are decoded separately because their
// usage block uses different field names (input_tokens / output_tokens).
type usageResponseBody struct {
	Model string `json:"model"`
	Usage *struct {
		PromptTokens     int64 `json:"prompt_tokens"`
		CompletionTokens int64 `json:"completion_tokens"`
		TotalTokens      int64 `json:"total_tokens"`
	} `json:"usage"`
}

// anthropicResponseBody covers /v1/messages JSON responses.
type anthropicResponseBody struct {
	Model string `json:"model"`
	Usage *struct {
		InputTokens  int64 `json:"input_tokens"`
		OutputTokens int64 `json:"output_tokens"`
	} `json:"usage"`
}

// UsageMiddleware records token usage for inference requests via the
// billing.Recorder. Two paths produce a record:
//
//  1. Handler-stamped (preferred): the request handler called
//     middleware.StampUsage with the canonical token counts before
//     returning. This is the only reliable path for streaming responses
//     — clients rarely set OpenAI's stream_options.include_usage, and
//     Anthropic's usage lives in a separate message_delta event.
//  2. Body-parsed (fallback): the response is parsed for an OpenAI- or
//     Anthropic-shaped usage block. Used by passthrough proxies and
//     foreign endpoints.
//
// Recorder being nil (e.g., --disable-stats) makes the middleware a
// transparent pass-through. fallbackUser is used when auth.GetUser(c)
// returns nil; without it, an unauthenticated request would be dropped.
//
// Every request that fails to produce a record ticks
// localai_usage_unrecorded_total so silent billing misses are observable.
func UsageMiddleware(recorder *billing.Recorder, fallbackUser *auth.User) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if recorder == nil {
				return next(c)
			}

			startTime := time.Now()

			// Wrap response writer to capture body for the fallback parser.
			// When the handler stamps the context we never read this buffer,
			// so the cost is the per-chunk Write going through one extra
			// indirection — accepted overhead in exchange for one billing
			// path that works for both stamping and body-parse callers.
			resBody := new(bytes.Buffer)
			origWriter := c.Response().Writer
			mw := &bodyWriter{
				ResponseWriter: origWriter,
				body:           resBody,
			}
			c.Response().Writer = mw

			handlerErr := next(c)

			c.Response().Writer = origWriter

			endpoint := c.Request().URL.Path

			if c.Response().Status < 200 || c.Response().Status >= 300 {
				return handlerErr
			}

			user := auth.GetUser(c)
			if user == nil {
				user = fallbackUser
			}
			if user == nil || user.ID == "" {
				billing.CountUnrecorded(context.Background(), endpoint, "no_user")
				return handlerErr
			}

			model, prompt, completion, total, ok := tokensFromContext(c)
			if !ok {
				model, prompt, completion, total, ok = tokensFromBody(resBody.Bytes(), c.Response().Header().Get("Content-Type"))
			}
			if !ok {
				billing.CountUnrecorded(context.Background(), endpoint, "no_usage")
				return handlerErr
			}

			requested, served := modelsFromContext(c, model)
			pre, post := promptTokensFromContext(c, prompt)

			source := auth.GetSource(c)
			if source == "" {
				// Auth disabled or unrecognised path: classify as web so the row is still
				// bucketable rather than silently dropped from per-source aggregates.
				source = auth.UsageSourceWeb
			}

			record := &auth.UsageRecord{
				UserID:                 user.ID,
				UserName:               user.Name,
				Source:                 source,
				Model:                  model,
				Endpoint:               endpoint,
				PromptTokens:           prompt,
				CompletionTokens:       completion,
				TotalTokens:            total,
				Duration:               time.Since(startTime).Milliseconds(),
				CreatedAt:              startTime,
				RequestedModel:         requested,
				ServedModel:            served,
				PreFilterPromptTokens:  pre,
				PostFilterPromptTokens: post,
				CorrelationID:          correlationIDFromContext(c),
			}

			if key := auth.GetAPIKey(c); key != nil {
				id := key.ID
				record.APIKeyID = &id
				record.APIKeyName = key.Name
			}

			if err := recorder.Record(context.Background(), record); err != nil {
				xlog.Error("usage middleware: recorder.Record failed", "error", err, "user", user.ID, "model", model)
				billing.CountUnrecorded(context.Background(), endpoint, "record_failed")
			}

			return handlerErr
		}
	}
}

// tokensFromContext returns canonical token counts stamped by a handler
// via middleware.StampUsage. Returns ok=false when no stamp is present
// — the caller then tries the body-parse fallback.
//
// A model name without token counts is not considered "stamped" because a
// record with zero tokens looks the same as a never-recorded request to
// later analytics; the second condition is what gates ok.
func tokensFromContext(c echo.Context) (model string, prompt, completion, total int64, ok bool) {
	if v, found := c.Get(ContextKeyResponseModel).(string); found {
		model = v
	}
	pPresent := false
	cPresent := false
	if v, found := c.Get(ContextKeyPromptTokens).(int64); found {
		prompt = v
		pPresent = true
	}
	if v, found := c.Get(ContextKeyCompletionTokens).(int64); found {
		completion = v
		cPresent = true
	}
	if v, found := c.Get(ContextKeyTotalTokens).(int64); found {
		total = v
	} else {
		total = prompt + completion
	}
	ok = pPresent || cPresent
	return
}

// tokensFromBody covers the passthrough-proxy / foreign-endpoint case
// where no handler stamps the context. Returns ok=false on any parse
// failure or missing-usage; the caller increments the unrecorded counter.
func tokensFromBody(responseBytes []byte, contentType string) (model string, prompt, completion, total int64, ok bool) {
	if len(responseBytes) == 0 {
		return
	}
	isJSON := contentType == "" || contentType == "application/json" || bytes.HasPrefix([]byte(contentType), []byte("application/json"))
	isSSE := bytes.HasPrefix([]byte(contentType), []byte("text/event-stream"))
	if !isJSON && !isSSE {
		return
	}

	payload := responseBytes
	if isSSE {
		// For SSE, the canonical usage chunk is the *last* non-[DONE] data
		// line. OpenAI clients only emit one if stream_options.include_usage
		// is set; Anthropic emits a final message_delta with usage. Both
		// fit the "last data: line" rule.
		last, lastOk := lastSSEData(responseBytes)
		if !lastOk {
			return
		}
		payload = last
	}

	// Try OpenAI shape first (handles /v1/chat/completions, /v1/completions,
	// /v1/embeddings, /v1/edits, and any proxy that translates to OpenAI).
	// A usage block whose token fields all decoded to zero is ambiguous —
	// it could be an Anthropic body that happens to have a `usage` key —
	// so fall through to the Anthropic parser instead of recording zeros.
	var openAI usageResponseBody
	if err := json.Unmarshal(payload, &openAI); err == nil && openAI.Usage != nil {
		if openAI.Usage.PromptTokens != 0 || openAI.Usage.CompletionTokens != 0 || openAI.Usage.TotalTokens != 0 {
			model = openAI.Model
			prompt = openAI.Usage.PromptTokens
			completion = openAI.Usage.CompletionTokens
			total = openAI.Usage.TotalTokens
			if total == 0 {
				total = prompt + completion
			}
			ok = true
			return
		}
	}

	// Fall through to Anthropic shape (proxy passthrough territory).
	var ant anthropicResponseBody
	if err := json.Unmarshal(payload, &ant); err == nil && ant.Usage != nil {
		if ant.Usage.InputTokens != 0 || ant.Usage.OutputTokens != 0 {
			model = ant.Model
			prompt = ant.Usage.InputTokens
			completion = ant.Usage.OutputTokens
			total = prompt + completion
			ok = true
			return
		}
	}

	return
}

// modelsFromContext returns (requested, served) using context-set values
// when present, falling back to the response-reported model for both.
// The router middleware (subsystem 2 of the routing plan) populates
// these; until it lands they are equal.
func modelsFromContext(c echo.Context, fallback string) (string, string) {
	requested := fallback
	served := fallback
	if v, ok := c.Get(ContextKeyRequestedModel).(string); ok && v != "" {
		requested = v
	}
	if v, ok := c.Get(ContextKeyServedModel).(string); ok && v != "" {
		served = v
	}
	return requested, served
}

func promptTokensFromContext(c echo.Context, fallback int64) (int64, int64) {
	pre := fallback
	post := fallback
	if v, ok := c.Get(ContextKeyPreFilterPromptTokens).(int64); ok && v > 0 {
		pre = v
	}
	if v, ok := c.Get(ContextKeyPostFilterPromptTokens).(int64); ok && v > 0 {
		post = v
	}
	return pre, post
}

func correlationIDFromContext(c echo.Context) string {
	if v, ok := c.Get(ContextKeyCorrelationID).(string); ok {
		return v
	}
	// X-Correlation-ID header is set by trace.go middleware; read it as a
	// fallback if the echo-context binding hasn't been populated yet.
	return c.Response().Header().Get("X-Correlation-ID")
}

// lastSSEData returns the payload of the last "data: " line whose content is not "[DONE]".
func lastSSEData(b []byte) ([]byte, bool) {
	prefix := []byte("data: ")
	var last []byte
	for _, line := range bytes.Split(b, []byte("\n")) {
		line = bytes.TrimRight(line, "\r")
		if bytes.HasPrefix(line, prefix) {
			payload := line[len(prefix):]
			if !bytes.Equal(payload, []byte("[DONE]")) {
				last = payload
			}
		}
	}
	return last, last != nil
}
