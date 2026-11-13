package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
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
func installTestTracer(t *testing.T) *tracetest.InMemoryExporter {
	t.Helper()
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	previous := otel.GetTracerProvider()
	previousPropagator := otel.GetTextMapPropagator()
	otel.SetTracerProvider(provider)
	// Setup installs the W3C propagator in production; install it here so the
	// continue-an-inbound-trace test exercises the real extraction path.
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() {
		otel.SetTextMapPropagator(previousPropagator)
		otel.SetTracerProvider(previous)
		_ = provider.Shutdown(t.Context())
	})
	return exporter
}

func attrValue(span tracetest.SpanStub, key string) (string, bool) {
	for _, kv := range span.Attributes {
		if string(kv.Key) == key {
			return kv.Value.Emit(), true
		}
	}
	return "", false
}

func TestOTelTracingServerSpan(t *testing.T) {
	exporter := installTestTracer(t)

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
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	span := spans[0]

	if want := "POST /v1/chat/completions"; span.Name != want {
		t.Fatalf("span name = %q, want %q", span.Name, want)
	}
	if span.SpanKind != trace.SpanKindServer {
		t.Fatalf("span kind = %v, want server", span.SpanKind)
	}

	for key, want := range map[string]string{
		"http.request.method":       "POST",
		"http.route":                "/v1/chat/completions",
		"http.response.status_code": "200",
		"user_agent.original":       "trace-test",
		telemetry.ExchangeIDKey:     "42",
	} {
		got, ok := attrValue(span, key)
		if !ok {
			t.Fatalf("missing attribute %q", key)
		}
		if got != want {
			t.Fatalf("attribute %q = %q, want %q", key, got, want)
		}
	}

	// The no-body policy is the one that matters for a shared collector:
	// nothing from the prompt may appear on the span.
	for _, kv := range span.Attributes {
		if strings.Contains(kv.Value.Emit(), "PROMPT-THAT-MUST-NOT-LEAK") {
			t.Fatalf("request body leaked into attribute %q", kv.Key)
		}
	}
	for _, event := range span.Events {
		if strings.Contains(event.Name, "PROMPT-THAT-MUST-NOT-LEAK") {
			t.Fatal("request body leaked into a span event")
		}
	}
}

func TestOTelTracingContinuesInboundTrace(t *testing.T) {
	exporter := installTestTracer(t)

	const (
		traceID  = "4bf92f3577b34da6a3ce929d0e0e4736"
		parentID = "00f067aa0ba902b7"
	)

	e := echo.New()
	e.Use(OTelTracing())
	e.GET("/v1/models", func(c echo.Context) error { return c.NoContent(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("traceparent", "00-"+traceID+"-"+parentID+"-01")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	if got := spans[0].SpanContext.TraceID().String(); got != traceID {
		t.Fatalf("trace id = %q, want the inbound trace id %q", got, traceID)
	}
	if got := spans[0].Parent.SpanID().String(); got != parentID {
		t.Fatalf("parent span id = %q, want %q", got, parentID)
	}
}

func TestOTelTracingErrorStatus(t *testing.T) {
	exporter := installTestTracer(t)

	e := echo.New()
	e.Use(OTelTracing())
	e.POST("/v1/chat/completions", func(c echo.Context) error {
		return echo.NewHTTPError(http.StatusBadRequest, "model is required")
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("{}"))
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	if got := spans[0].Status.Code; got != codes.Error {
		t.Fatalf("span status = %v, want Error", got)
	}
	if got, _ := attrValue(spans[0], "http.response.status_code"); got != "400" {
		t.Fatalf("status attribute = %q, want 400 (the status the client receives)", got)
	}
	if got, _ := attrValue(spans[0], "error.type"); got != "400" {
		t.Fatalf("error.type = %q, want the status code 400", got)
	}
}

func TestOTelTracingSkipsHealthAndAdminTraces(t *testing.T) {
	exporter := installTestTracer(t)

	e := echo.New()
	e.Use(OTelTracing())
	for _, path := range []string{"/healthz", "/readyz", "/metrics", "/api/traces"} {
		path := path
		e.GET(path, func(c echo.Context) error { return c.NoContent(http.StatusOK) })
	}

	for _, path := range []string{"/healthz", "/readyz", "/metrics", "/api/traces"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		e.ServeHTTP(httptest.NewRecorder(), req)
	}

	if spans := exporter.GetSpans(); len(spans) != 0 {
		t.Fatalf("expected no spans for health/metrics/admin paths, got %d", len(spans))
	}
}

func TestOTelTracingUnmatchedRouteOmitsRoute(t *testing.T) {
	exporter := installTestTracer(t)

	e := echo.New()
	e.Use(OTelTracing())

	req := httptest.NewRequest(http.MethodGet, "/not-a-route", nil)
	e.ServeHTTP(httptest.NewRecorder(), req)

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span for an unmatched path, got %d", len(spans))
	}
	// Without a matched route there is no low-cardinality route to name the
	// span after, and exporting the raw path as http.route would make the
	// attribute as unique as the scanner traffic that caused it.
	if spans[0].Name != http.MethodGet {
		t.Fatalf("span name = %q, want %q", spans[0].Name, http.MethodGet)
	}
	if _, ok := attrValue(spans[0], "http.route"); ok {
		t.Fatal("unmatched request exported http.route")
	}
	if got, _ := attrValue(spans[0], "url.path"); got != "/not-a-route" {
		t.Fatalf("url.path = %q, want /not-a-route", got)
	}
}

func TestOTelTracingDisabledIsPassThrough(t *testing.T) {
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

	if gotTraceParent == "" {
		t.Fatal("middleware should leave the request untouched when export is off")
	}
}
