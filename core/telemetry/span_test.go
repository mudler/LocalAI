package telemetry

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/trace"
)

func TestInheritCarriesSpanAndExchangeID(t *testing.T) {
	traceID := trace.TraceID{0x4b, 0xf9, 0x2f, 0x35, 0x77, 0xb3, 0x4d, 0xa6, 0xa3, 0xce, 0x92, 0x9d, 0x0e, 0x0e, 0x47, 0x36}
	spanID := trace.SpanID{0x00, 0xf0, 0x67, 0xaa, 0x0b, 0xa9, 0x02, 0xb7}
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.FlagsSampled,
		Remote:     true,
	})

	src := trace.ContextWithSpanContext(t.Context(), sc)
	src = WithExchangeID(src, "42")

	// A fresh context, like the one request handling builds from the
	// application context, starts with neither.
	base := context.Background()
	if trace.SpanContextFromContext(base).IsValid() {
		t.Fatal("test precondition: base context already carries a span")
	}

	got := Inherit(base, src)
	if id := ExchangeID(got); id != "42" {
		t.Fatalf("exchange ID = %q, want 42", id)
	}
	gotSC := trace.SpanContextFromContext(got)
	if !gotSC.IsValid() {
		t.Fatal("inherited context lost the span context")
	}
	if gotSC.TraceID() != traceID || gotSC.SpanID() != spanID {
		t.Fatalf("span context = %s/%s, want %s/%s", gotSC.TraceID(), gotSC.SpanID(), traceID, spanID)
	}

	// Inheriting from a context with neither must leave the destination
	// untouched rather than stamping zero values on it.
	plain := Inherit(base, context.Background())
	if ExchangeID(plain) != "" || trace.SpanContextFromContext(plain).IsValid() {
		t.Fatal("Inherit added values from an empty source")
	}
}
