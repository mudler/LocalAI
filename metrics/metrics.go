package metrics

import (
	"context"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/adaptor"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/prometheus"
	api "go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/sdk/metric"
)

type Metrics struct {
	meter         api.Meter
	apiTimeMetric api.Float64Histogram
}

// setupOTelSDK bootstraps the OpenTelemetry pipeline.
// If it does not return an error, make sure to call shutdown for proper cleanup.
func SetupMetrics() (*Metrics, error) {
	exporter, err := prometheus.New()
	if err != nil {
		return nil, err
	}
	provider := metric.NewMeterProvider(metric.WithReader(exporter))
	meter := provider.Meter("github.com/go-skynet/LocalAI")

	apiTimeMetric, err := meter.Float64Histogram("api_call", api.WithDescription("api calls"))
	if err != nil {
		return nil, err
	}

	return &Metrics{
		meter:         meter,
		apiTimeMetric: apiTimeMetric,
	}, nil
}

func MetricsHandler() fiber.Handler {
	return adaptor.HTTPHandler(promhttp.Handler())
}

type apiMiddlewareConfig struct {
	Filter  func(c *fiber.Ctx) bool
	metrics *Metrics
}

func APIMiddleware(metrics *Metrics) fiber.Handler {
	cfg := apiMiddlewareConfig{
		metrics: metrics,
		Filter: func(c *fiber.Ctx) bool {
			if c.Path() == "/metrics" {
				return true
			}
			return false
		},
	}

	return func(c *fiber.Ctx) error {
		if cfg.Filter != nil && cfg.Filter(c) {
			return c.Next()
		}
		path := c.Path()
		method := c.Method()

		start := time.Now()
		err := c.Next()
		elapsed := float64(time.Since(start)) / float64(time.Second)
		cfg.metrics.ObserveAPICall(method, path, elapsed)
		return err
	}
}

func (m *Metrics) ObserveAPICall(method string, path string, duration float64) {
	opts := api.WithAttributes(
		attribute.String("method", method),
		attribute.String("path", path),
	)
	m.apiTimeMetric.Record(context.Background(), duration, opts)
}
