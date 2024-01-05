package services

import (
	"github.com/go-skynet/LocalAI/pkg/schema"
	"go.opentelemetry.io/otel/exporters/prometheus"
	api "go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/sdk/metric"
)

// setupOTelSDK bootstraps the OpenTelemetry pipeline.
// If it does not return an error, make sure to call shutdown for proper cleanup.
func SetupMetrics() (*schema.LocalAIMetrics, error) {
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

	return &schema.LocalAIMetrics{
		Meter:         meter,
		ApiTimeMetric: apiTimeMetric,
	}, nil
}
