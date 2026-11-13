package backend

import (
	"context"
	"fmt"
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
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		// The Go type is the closest thing to exception.type for an internal
		// call; the message itself stays in the exception event.
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
