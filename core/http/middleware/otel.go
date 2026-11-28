package middleware

import (
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.37.0"

	"github.com/mudler/LocalAI/core/telemetry"
)

// otelSkippedPrefixes are the paths that never produce a span. Health probes
// and the metrics scrape fire on a fixed schedule regardless of traffic, and
// the admin traces UI polls /api/traces every few seconds: exporting spans for
// them would drown the spans an operator actually wants to see. Static assets
// are skipped for the same reason.
var otelSkippedPrefixes = []string{
	"/healthz",
	"/readyz",
	"/metrics",
	"/api/traces",
	"/favicon.svg",
	"/apple-touch-icon.png",
	"/static/",
	"/assets/",
}

// OTelTracing traces every API request as a server span. It continues an
// inbound W3C trace (a gateway or client that already sends traceparent) and
// makes the span the parent of the backend calls the handler makes.
//
// The middleware is a no-op unless OTLP export was configured, so it is safe
// to register unconditionally. Register it outside the panic-recovery
// middleware: then a panicking handler still leaves a span with an error
// status instead of a span that never ends.
func OTelTracing() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			req := c.Request()
			if !telemetry.Enabled() || skipOTelPath(req.URL.Path) {
				return next(c)
			}

			ctx := otel.GetTextMapPropagator().Extract(req.Context(), propagation.HeaderCarrier(req.Header))
			route := c.Path()
			// semconv: name the span "METHOD route", and when no route matched
			// (a 404 from an unknown path, typically a scanner) leave
			// http.route unset rather than exporting the arbitrary path as a
			// route and blowing up its cardinality.
			name := req.Method
			attrs := []attribute.KeyValue{
				semconv.HTTPRequestMethodKey.String(req.Method),
				semconv.URLPathKey.String(req.URL.Path),
				semconv.ClientAddressKey.String(c.RealIP()),
			}
			if route != "" {
				name = req.Method + " " + route
				attrs = append(attrs, semconv.HTTPRouteKey.String(route))
			}
			if ua := req.UserAgent(); ua != "" {
				attrs = append(attrs, semconv.UserAgentOriginalKey.String(ua))
			}
			if host, _, err := net.SplitHostPort(req.Host); err == nil {
				attrs = append(attrs, semconv.ServerAddressKey.String(host))
			}

			ctx, span := telemetry.StartServerSpan(ctx, name, attrs...)
			defer span.End()
			c.SetRequest(req.WithContext(ctx))

			handlerErr := next(c)

			status := c.Response().Status
			if handlerErr != nil && !c.Response().Committed {
				status = http.StatusInternalServerError
				var httpErr *echo.HTTPError
				if errors.As(handlerErr, &httpErr) {
					status = httpErr.Code
				}
			}
			span.SetAttributes(semconv.HTTPResponseStatusCode(status))

			// Join the span to the captured request/response payload the
			// in-process tracer stores, when that tracer is enabled.
			if id, ok := c.Get(telemetry.ExchangeIDKey).(string); ok && id != "" {
				span.SetAttributes(attribute.String(telemetry.ExchangeIDKey, id))
			}

			// The handler error's message is not recorded: it can echo request
			// content (a bind error names the offending value). The status
			// code carries the outcome.
			switch {
			case handlerErr != nil, status >= http.StatusInternalServerError:
				span.SetStatus(codes.Error, http.StatusText(status))
				// semconv: for an HTTP span, error.type is the status code
				// that was returned, which is low cardinality and directly
				// actionable.
				span.SetAttributes(attribute.String("error.type", strconv.Itoa(status)))
			}
			return handlerErr
		}
	}
}

func skipOTelPath(path string) bool {
	for _, prefix := range otelSkippedPrefixes {
		if path == prefix || strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}
