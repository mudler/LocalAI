package services

import (
	"context"

	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/metric"
	metricApi "go.opentelemetry.io/otel/sdk/metric"
)

type LocalAIMetricsService struct {
	Meter         metric.Meter
	ApiTimeMetric metric.Float64Histogram
}

func (m *LocalAIMetricsService) ObserveAPICall(method string, path string, duration float64) {
	opts := metric.WithAttributes(
		attribute.String("method", method),
		attribute.String("path", path),
	)
	m.ApiTimeMetric.Record(context.Background(), duration, opts)
}

// setupOTelSDK bootstraps the OpenTelemetry pipeline.
// If it does not return an error, make sure to call shutdown for proper cleanup.
func NewLocalAIMetricsService() (*LocalAIMetricsService, error) {
	exporter, err := prometheus.New()
	if err != nil {
		return nil, err
	}
	provider := metricApi.NewMeterProvider(metricApi.WithReader(exporter))
	meter := provider.Meter("github.com/mudler/LocalAI")

	apiTimeMetric, err := meter.Float64Histogram("api_call", metric.WithDescription("api calls"))
	if err != nil {
		return nil, err
	}

	return &LocalAIMetricsService{
		Meter:         meter,
		ApiTimeMetric: apiTimeMetric,
	}, nil
}

func (lams LocalAIMetricsService) Shutdown() error {
	// TODO: Not sure how to actually do this:
	//// setupOTelSDK bootstraps the OpenTelemetry pipeline.
	//// If it does not return an error, make sure to call shutdown for proper cleanup.

	log.Warn().Msgf("LocalAIMetricsService Shutdown called, but OTelSDK proper shutdown not yet implemented?")
	return nil
}
