package telemetry

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// ExchangeIDKey is the echo context key that carries the in-process trace ID.
// The OTel middleware reads it after the handler returns to stamp
// localai.exchange_id on the server span, which is how a span in SigNoz is
// joined to the full request/response payload in /api/traces.
const ExchangeIDKey = "localai.exchange_id"

type exchangeIDKey struct{}

// WithExchangeID returns a context carrying the in-process trace ID.
func WithExchangeID(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, exchangeIDKey{}, id)
}

// ExchangeID returns the in-process trace ID carried by ctx, if any.
func ExchangeID(ctx context.Context) string {
	id, _ := ctx.Value(exchangeIDKey{}).(string)
	return id
}

// StartServerSpan starts a server span for an inbound request.
func StartServerSpan(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	return Tracer().Start(ctx, name,
		trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(attrs...),
	)
}

// StartClientSpan starts a client span for a call LocalAI makes (a model
// backend, an upstream API, a tool server).
func StartClientSpan(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	return Tracer().Start(ctx, name,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(attrs...),
	)
}

// Inherit carries the OTel span and the in-process exchange ID from src into
// dst, leaving dst's lifetime and its other values alone.
//
// Request handling rebuilds the inference context from the long-lived
// application context so the model load can outlive the HTTP transport. That
// rebuild drops the span the server middleware started, which would make each
// backend span its own trace instead of a child of the request. This is the
// seam that puts the parent back.
func Inherit(dst, src context.Context) context.Context {
	if id := ExchangeID(src); id != "" {
		dst = WithExchangeID(dst, id)
	}
	if sc := trace.SpanContextFromContext(src); sc.IsValid() {
		dst = trace.ContextWithSpanContext(dst, sc)
	}
	return dst
}
