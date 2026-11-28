package telemetry

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.opentelemetry.io/otel/trace"
)

var _ = Describe("span context propagation", func() {
	It("carries the span and the exchange ID across a context rebuild", func() {
		traceID := trace.TraceID{0x4b, 0xf9, 0x2f, 0x35, 0x77, 0xb3, 0x4d, 0xa6, 0xa3, 0xce, 0x92, 0x9d, 0x0e, 0x0e, 0x47, 0x36}
		spanID := trace.SpanID{0x00, 0xf0, 0x67, 0xaa, 0x0b, 0xa9, 0x02, 0xb7}
		sc := trace.NewSpanContext(trace.SpanContextConfig{
			TraceID:    traceID,
			SpanID:     spanID,
			TraceFlags: trace.FlagsSampled,
			Remote:     true,
		})

		src := trace.ContextWithSpanContext(context.Background(), sc)
		src = WithExchangeID(src, "42")

		// A fresh context, like the one request handling builds from the
		// application context, starts with neither.
		base := context.Background()
		Expect(trace.SpanContextFromContext(base).IsValid()).To(BeFalse(),
			"test precondition: base context already carries a span")

		got := Inherit(base, src)
		Expect(ExchangeID(got)).To(Equal("42"))

		gotSC := trace.SpanContextFromContext(got)
		Expect(gotSC.IsValid()).To(BeTrue(), "inherited context lost the span context")
		Expect(gotSC.TraceID()).To(Equal(traceID))
		Expect(gotSC.SpanID()).To(Equal(spanID))
	})

	It("leaves the destination untouched when the source carries neither", func() {
		// Stamping zero values on the destination would be indistinguishable
		// from a real, empty trace context.
		plain := Inherit(context.Background(), context.Background())
		Expect(ExchangeID(plain)).To(BeEmpty())
		Expect(trace.SpanContextFromContext(plain).IsValid()).To(BeFalse())
	})
})
