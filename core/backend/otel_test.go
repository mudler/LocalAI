package backend

import (
	"context"
	"strings"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/telemetry"
)

// installTestTracer makes spans land in an in-memory exporter synchronously.
func installTestTracer() *tracetest.InMemoryExporter {
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	DeferCleanup(func() {
		otel.SetTracerProvider(previous)
		_ = provider.Shutdown(context.Background())
	})
	return exporter
}

func spanAttribute(span tracetest.SpanStub, key string) (string, bool) {
	for _, kv := range span.Attributes {
		if string(kv.Key) == key {
			return kv.Value.String(), true
		}
	}
	return "", false
}

var _ = Describe("inference spans", func() {
	It("records metadata only, with no payload-carrying attribute", func() {
		exporter := installTestTracer()

		maxTokens := 128
		temperature := 0.2
		topP := 0.9
		topK := 40
		cfg := &config.ModelConfig{Name: "bankrut-test-model", Backend: "llama-cpp"}
		cfg.Maxtokens = &maxTokens
		cfg.Temperature = &temperature
		cfg.TopP = &topP
		cfg.TopK = &topK

		ctx := telemetry.WithExchangeID(context.Background(), "7")
		span := startInferenceSpan(ctx, cfg, true, "chat")
		Expect(span).NotTo(BeNil())
		endInferenceSpan(span, TokenUsage{Prompt: 11, Completion: 3}, true, nil, 250*time.Millisecond, 2*time.Second)

		spans := exporter.GetSpans()
		Expect(spans).To(HaveLen(1))
		got := spans[0]
		Expect(got.Name).To(Equal("gen_ai.chat bankrut-test-model"))

		for key, want := range map[string]string{
			"gen_ai.operation.name":          "chat",
			"gen_ai.provider.name":           "llama.cpp",
			"gen_ai.request.model":           "bankrut-test-model",
			"gen_ai.request.max_tokens":      "128",
			"gen_ai.usage.input_tokens":      "11",
			"gen_ai.usage.output_tokens":     "3",
			"localai.backend":                "llama-cpp",
			"localai.streaming":              "true",
			telemetry.ExchangeIDKey:          "7",
			"localai.time_to_first_token_ms": "250",
		} {
			value, ok := spanAttribute(got, key)
			Expect(ok).To(BeTrue(), "missing attribute %q", key)
			Expect(value).To(Equal(want), "attribute %q", key)
		}

		// The no-body policy: nothing that could carry a prompt or a completion
		// may be attached to an exported span.
		for _, kv := range got.Attributes {
			lower := strings.ToLower(string(kv.Key))
			for _, banned := range []string{"prompt", "completion", "message", "content", "tool_arguments"} {
				Expect(lower).NotTo(ContainSubstring(banned), "span attribute %q looks like payload, not metadata", kv.Key)
			}
		}
	})

	It("records a failure as an error status and type, without the error text", func() {
		exporter := installTestTracer()

		cfg := &config.ModelConfig{Name: "bankrut-test-model", Backend: "llama-cpp"}
		span := startInferenceSpan(context.Background(), cfg, false, "chat")
		endInferenceSpan(span, TokenUsage{}, false, errBackendUnavailable{}, 0, 10*time.Millisecond)

		spans := exporter.GetSpans()
		Expect(spans).To(HaveLen(1))
		Expect(spans[0].Status.Code.String()).To(Equal("Error"))

		value, _ := spanAttribute(spans[0], "error.type")
		Expect(value).To(Equal("backend.errBackendUnavailable"))

		// A failed call has no usage, so it must not report zero tokens.
		for _, key := range []string{"gen_ai.usage.input_tokens", "gen_ai.usage.output_tokens"} {
			_, ok := spanAttribute(spans[0], key)
			Expect(ok).To(BeFalse(), "failed call reported %q", key)
		}

		// The error text can echo prompt content, so it must not reach the span
		// in any form: no exception event, and no message in the status.
		Expect(spans[0].Events).To(BeEmpty(), "error text must not be exported as an event")
		Expect(spans[0].Status.Description).NotTo(ContainSubstring("backend is not running"))
	})

	// Each attempt of the returned inference function must report its own time
	// to first token. The first-token mark is shared across attempts, so a
	// second attempt that streams a token must still get a positive value.
	It("reports time to first token for each attempt, not only the first", func() {
		exporter := installTestTracer()

		var firstToken atomic.Int64
		attempt := func() (LLMResponse, error) {
			// Mimics the token callback: the first token arrives 1ms after the
			// call started, so the reported value is always positive.
			firstToken.CompareAndSwap(0, time.Now().Add(time.Millisecond).UnixNano())
			return LLMResponse{}, nil
		}
		cfg := &config.ModelConfig{Name: "bankrut-test-model", Backend: "llama-cpp"}
		call := instrumentInferenceAttempts(context.Background(), cfg, true, "chat", &firstToken, attempt)

		for range 2 {
			_, err := call()
			Expect(err).NotTo(HaveOccurred())
		}

		spans := exporter.GetSpans()
		Expect(spans).To(HaveLen(2))
		for i, span := range spans {
			value, ok := spanAttribute(span, "localai.time_to_first_token_ms")
			Expect(ok).To(BeTrue(), "attempt %d: no time to first token recorded", i+1)
			Expect(value).NotTo(HavePrefix("-"), "attempt %d: time to first token = %q, want a positive value", i+1, value)
			Expect(value).NotTo(BeEmpty())
		}
	})
})

type errBackendUnavailable struct{}

func (errBackendUnavailable) Error() string { return "backend is not running" }
