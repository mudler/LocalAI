package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/labstack/echo/v4"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/mudler/LocalAI/core/telemetry"
)

// installTestTracer swaps in a synchronous in-memory exporter. Traces are
// collected as soon as a span ends, so a test can assert on them directly.
func installTestTracer() *tracetest.InMemoryExporter {
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	previous := otel.GetTracerProvider()
	previousPropagator := otel.GetTextMapPropagator()
	otel.SetTracerProvider(provider)
	// Setup installs the W3C propagator in production; install it here so the
	// continue-an-inbound-trace test exercises the real extraction path.
	otel.SetTextMapPropagator(propagation.TraceContext{})
	DeferCleanup(func() {
		otel.SetTextMapPropagator(previousPropagator)
		otel.SetTracerProvider(previous)
		_ = provider.Shutdown(context.Background())
	})
	return exporter
}

func attrValue(span tracetest.SpanStub, key string) (string, bool) {
	for _, kv := range span.Attributes {
		if string(kv.Key) == key {
			return kv.Value.String(), true
		}
	}
	return "", false
}

var _ = Describe("OTelTracing middleware", func() {
	It("records a server span with the route, method, status and exchange ID, and no body", func() {
		exporter := installTestTracer()

		e := echo.New()
		e.Use(OTelTracing())
		e.POST("/v1/chat/completions", func(c echo.Context) error {
			c.Set(telemetry.ExchangeIDKey, "42")
			return c.JSON(http.StatusOK, map[string]string{"ok": "1"})
		})

		body := `{"messages":[{"role":"user","content":"PROMPT-THAT-MUST-NOT-LEAK"}]}`
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "trace-test")
		e.ServeHTTP(httptest.NewRecorder(), req)

		spans := exporter.GetSpans()
		Expect(spans).To(HaveLen(1))
		span := spans[0]
		Expect(span.Name).To(Equal("POST /v1/chat/completions"))
		Expect(span.SpanKind).To(Equal(trace.SpanKindServer))

		for key, want := range map[string]string{
			"http.request.method":       "POST",
			"http.route":                "/v1/chat/completions",
			"http.response.status_code": "200",
			"user_agent.original":       "trace-test",
			telemetry.ExchangeIDKey:     "42",
		} {
			got, ok := attrValue(span, key)
			Expect(ok).To(BeTrue(), "missing attribute %q", key)
			Expect(got).To(Equal(want), "attribute %q", key)
		}

		// The no-body policy is the one that matters for a shared collector:
		// nothing from the prompt may appear on the span.
		for _, kv := range span.Attributes {
			Expect(kv.Value.String()).NotTo(ContainSubstring("PROMPT-THAT-MUST-NOT-LEAK"),
				"request body leaked into attribute %q", kv.Key)
		}
		for _, event := range span.Events {
			Expect(event.Name).NotTo(ContainSubstring("PROMPT-THAT-MUST-NOT-LEAK"))
		}
	})

	It("continues an inbound W3C trace", func() {
		exporter := installTestTracer()

		const (
			traceID  = "4bf92f3577b34da6a3ce929d0e0e4736"
			parentID = "00f067aa0ba902b7"
		)

		e := echo.New()
		e.Use(OTelTracing())
		e.GET("/v1/models", func(c echo.Context) error { return c.NoContent(http.StatusOK) })

		req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
		req.Header.Set("traceparent", "00-"+traceID+"-"+parentID+"-01")
		e.ServeHTTP(httptest.NewRecorder(), req)

		spans := exporter.GetSpans()
		Expect(spans).To(HaveLen(1))
		Expect(spans[0].SpanContext.TraceID().String()).To(Equal(traceID))
		Expect(spans[0].Parent.SpanID().String()).To(Equal(parentID))
	})

	It("marks a client error as an error status with the status code, without the message", func() {
		exporter := installTestTracer()

		e := echo.New()
		e.Use(OTelTracing())
		e.POST("/v1/chat/completions", func(c echo.Context) error {
			return echo.NewHTTPError(http.StatusBadRequest, "model is required")
		})

		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("{}"))
		e.ServeHTTP(httptest.NewRecorder(), req)

		spans := exporter.GetSpans()
		Expect(spans).To(HaveLen(1))
		Expect(spans[0].Status.Code).To(Equal(codes.Error))

		status, _ := attrValue(spans[0], "http.response.status_code")
		Expect(status).To(Equal("400"), "the status the client receives")
		errType, _ := attrValue(spans[0], "error.type")
		Expect(errType).To(Equal("400"))

		// The handler's message can echo request content, so it must not be
		// recorded on the span.
		Expect(spans[0].Events).To(BeEmpty(), "the handler message must not be exported as an event")
		Expect(spans[0].Status.Description).NotTo(ContainSubstring("model is required"))
	})

	It("does not trace health, metrics and admin paths", func() {
		exporter := installTestTracer()

		e := echo.New()
		e.Use(OTelTracing())
		paths := []string{"/healthz", "/readyz", "/metrics", "/api/traces"}
		for _, path := range paths {
			e.GET(path, func(c echo.Context) error { return c.NoContent(http.StatusOK) })
		}
		for _, path := range paths {
			e.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
		}

		Expect(exporter.GetSpans()).To(BeEmpty(), "no spans for health/metrics/admin paths")
	})

	It("omits http.route for an unmatched path so scanner traffic cannot inflate cardinality", func() {
		exporter := installTestTracer()

		e := echo.New()
		e.Use(OTelTracing())
		e.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/not-a-route", nil))

		spans := exporter.GetSpans()
		Expect(spans).To(HaveLen(1))
		Expect(spans[0].Name).To(Equal(http.MethodGet))
		_, hasRoute := attrValue(spans[0], "http.route")
		Expect(hasRoute).To(BeFalse(), "unmatched request exported http.route")
		path, _ := attrValue(spans[0], "url.path")
		Expect(path).To(Equal("/not-a-route"))
	})

	It("leaves the request untouched when export is off", func() {
		// No provider installed: telemetry.Enabled() is false and the middleware
		// must not alter the request context.
		e := echo.New()
		e.Use(OTelTracing())

		var gotTraceParent string
		e.GET("/v1/models", func(c echo.Context) error {
			gotTraceParent = c.Request().Header.Get("traceparent")
			return c.NoContent(http.StatusOK)
		})

		req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
		req.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
		e.ServeHTTP(httptest.NewRecorder(), req)

		Expect(gotTraceParent).NotTo(BeEmpty(), "middleware should leave the request untouched when export is off")
	})
})
