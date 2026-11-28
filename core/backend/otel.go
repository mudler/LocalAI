package backend

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	semconv "go.opentelemetry.io/otel/semconv/v1.37.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/telemetry"
)

// gen_ai.* attribute names that the semantic conventions define for requests
// but do not (yet) expose as typed keys.
const (
	attrGenAIRequestTopP     = attribute.Key("gen_ai.request.top_p")
	attrGenAIRequestTopK     = attribute.Key("gen_ai.request.top_k")
	attrLocalAIStreaming     = attribute.Key("localai.streaming")
	attrLocalAIBackend       = attribute.Key("localai.backend")
	attrLocalAIExchangeID    = attribute.Key(telemetry.ExchangeIDKey)
	attrLocalAITimeToFirstMs = attribute.Key("localai.time_to_first_token_ms")
	attrLocalAIDurationMs    = attribute.Key("localai.duration_ms")
)

// instrumentInferenceAttempts wraps the returned inference function so each
// call of it gets its own span. firstToken is the per-request mark that the
// token callback sets on the first streamed token. It is shared by every
// attempt, so it is cleared at the start of each one; otherwise a later attempt
// would inherit the earlier attempt's timestamp and report no time to first
// token.
func instrumentInferenceAttempts(ctx context.Context, c *config.ModelConfig, streaming bool, operation string, firstToken *atomic.Int64, inner func() (LLMResponse, error)) func() (LLMResponse, error) {
	return func() (LLMResponse, error) {
		firstToken.Store(0)
		startedAt := time.Now()
		span := startInferenceSpan(ctx, c, streaming, operation)
		resp, err := inner()
		var ttft time.Duration
		if first := firstToken.Load(); first > 0 {
			ttft = time.Duration(first - startedAt.UnixNano())
		}
		endInferenceSpan(span, resp.Usage, streaming, err, ttft, time.Since(startedAt))
		return resp, err
	}
}

// startInferenceSpan opens the client span for one backend/model call.
//
// Only metadata is recorded. Prompts, completions, tool arguments and
// reasoning tokens are deliberately absent: they are available to an
// administrator through the in-process tracer, capped and on disk, but they
// must never leave the process through an OTLP collector.
func startInferenceSpan(ctx context.Context, c *config.ModelConfig, streaming bool, operation string) trace.Span {
	attrs := []attribute.KeyValue{
		semconv.GenAIOperationNameKey.String(operation),
		semconv.GenAIProviderNameKey.String(genAIProvider(c.Backend)),
		semconv.GenAIRequestModelKey.String(c.Name),
		attrLocalAIBackend.String(c.Backend),
		attrLocalAIStreaming.Bool(streaming),
	}
	if c.Maxtokens != nil {
		attrs = append(attrs, semconv.GenAIRequestMaxTokensKey.Int(*c.Maxtokens))
	}
	if c.Temperature != nil {
		attrs = append(attrs, semconv.GenAIRequestTemperatureKey.Float64(*c.Temperature))
	}
	if c.TopP != nil {
		attrs = append(attrs, attrGenAIRequestTopP.Float64(*c.TopP))
	}
	if c.TopK != nil {
		attrs = append(attrs, attrGenAIRequestTopK.Int(*c.TopK))
	}
	if id := telemetry.ExchangeID(ctx); id != "" {
		attrs = append(attrs, attrLocalAIExchangeID.String(id))
	}
	_, span := telemetry.StartClientSpan(ctx, "gen_ai."+operation+" "+c.Name, attrs...)
	return span
}

// endInferenceSpan records the outcome. Usage is the only response body
// information that is exported; everything else is timing and status.
func endInferenceSpan(span trace.Span, usage TokenUsage, streaming bool, err error, ttft, duration time.Duration) {
	attrs := []attribute.KeyValue{
		attrLocalAIDurationMs.Float64(float64(duration.Microseconds()) / 1000),
	}
	if err == nil {
		// A failed call has no usage; reporting zeros would read as "the
		// model ran and produced nothing".
		attrs = append(attrs,
			semconv.GenAIUsageInputTokensKey.Int(usage.Prompt),
			semconv.GenAIUsageOutputTokensKey.Int(usage.Completion),
		)
	}
	span.SetAttributes(attrs...)
	if streaming && ttft > 0 {
		span.SetAttributes(attrLocalAITimeToFirstMs.Float64(float64(ttft.Microseconds()) / 1000))
	}
	if err != nil {
		// The error message is deliberately not recorded. A backend error can
		// echo part of the prompt or a tool argument, and this span must not
		// carry payload. The Go type identifies the failure; the detail stays
		// in the process logs.
		span.SetStatus(codes.Error, "inference failed")
		span.SetAttributes(attribute.String("error.type", fmt.Sprintf("%T", err)))
	}
	span.End()
}

// genAIProvider maps a LocalAI backend name onto gen_ai.provider.name. The
// conventions define values for hosted providers; for the in-process and
// OpenAI-compatible backends LocalAI ships, the backend name is the honest
// answer, and it is what makes "which backend served this" answerable in a
// trace without another attribute.
func genAIProvider(backend string) string {
	switch backend {
	case "":
		return "unknown"
	case "llama-cpp", "llama.cpp":
		return "llama.cpp"
	default:
		return backend
	}
}
