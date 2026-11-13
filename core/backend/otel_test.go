package backend

import (
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/telemetry"
)

// installTestTracer makes spans land in an in-memory exporter synchronously.
func installTestTracer(t *testing.T) *tracetest.InMemoryExporter {
	t.Helper()
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		_ = provider.Shutdown(t.Context())
	})
	return exporter
}

func spanAttribute(span tracetest.SpanStub, key string) (string, bool) {
	for _, kv := range span.Attributes {
		if string(kv.Key) == key {
			return kv.Value.Emit(), true
		}
	}
	return "", false
}

func TestInferenceSpanRecordsMetadataOnly(t *testing.T) {
	exporter := installTestTracer(t)

	maxTokens := 128
	temperature := 0.2
	topP := 0.9
	topK := 40
	cfg := &config.ModelConfig{Name: "test-model", Backend: "llama-cpp"}
	cfg.Maxtokens = &maxTokens
	cfg.Temperature = &temperature
	cfg.TopP = &topP
	cfg.TopK = &topK

	ctx := telemetry.WithExchangeID(t.Context(), "7")
	span := startInferenceSpan(ctx, cfg, true, "chat")
	if span == nil {
		t.Fatal("startInferenceSpan returned a nil span")
	}
	endInferenceSpan(span, TokenUsage{Prompt: 11, Completion: 3}, true, nil, 250*time.Millisecond, 2*time.Second)

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	got := spans[0]

	if want := "gen_ai.chat test-model"; got.Name != want {
		t.Fatalf("span name = %q, want %q", got.Name, want)
	}

	for key, want := range map[string]string{
		"gen_ai.operation.name":          "chat",
		"gen_ai.provider.name":           "llama.cpp",
		"gen_ai.request.model":           "test-model",
		"gen_ai.request.max_tokens":      "128",
		"gen_ai.usage.input_tokens":      "11",
		"gen_ai.usage.output_tokens":     "3",
		"localai.backend":                "llama-cpp",
		"localai.streaming":              "true",
		telemetry.ExchangeIDKey:          "7",
		"localai.time_to_first_token_ms": "250",
	} {
		value, ok := spanAttribute(got, key)
		if !ok {
			t.Fatalf("missing attribute %q", key)
		}
		if value != want {
			t.Fatalf("attribute %q = %q, want %q", key, value, want)
		}
	}

	// The no-body policy: nothing that could carry a prompt or a completion
	// may be attached to an exported span.
	for _, kv := range got.Attributes {
		lower := strings.ToLower(string(kv.Key))
		for _, banned := range []string{"prompt", "completion", "message", "content", "tool_arguments"} {
			if strings.Contains(lower, banned) {
				t.Fatalf("span attribute %q looks like payload, not metadata", kv.Key)
			}
		}
	}
}

func TestInferenceSpanRecordsFailure(t *testing.T) {
	exporter := installTestTracer(t)

	cfg := &config.ModelConfig{Name: "test-model", Backend: "llama-cpp"}
	span := startInferenceSpan(t.Context(), cfg, false, "chat")
	endInferenceSpan(span, TokenUsage{}, false, errBackendUnavailable{}, 0, 10*time.Millisecond)

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	if got := spans[0].Status.Code.String(); got != "Error" {
		t.Fatalf("span status = %q, want Error", got)
	}
	if value, _ := spanAttribute(spans[0], "error.type"); value != "backend.errBackendUnavailable" {
		t.Fatalf("error.type = %q, want the error type", value)
	}
	// A failed call has no usage, so it must not report zero tokens.
	for _, key := range []string{"gen_ai.usage.input_tokens", "gen_ai.usage.output_tokens"} {
		if _, ok := spanAttribute(spans[0], key); ok {
			t.Fatalf("failed call reported %q", key)
		}
	}
}

type errBackendUnavailable struct{}

func (errBackendUnavailable) Error() string { return "backend is not running" }
