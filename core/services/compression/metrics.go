package compression

import (
	"context"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

var (
	metricsOnce sync.Once
	events      metric.Int64Counter
	ratio       metric.Float64Histogram
	duration    metric.Float64Histogram
)

func record(model, result string, started time.Time, original, compressed int) {
	metricsOnce.Do(func() {
		meter := otel.Meter("github.com/mudler/LocalAI")
		events, _ = meter.Int64Counter("localai_compression_events_total", metric.WithDescription("Chat context compression attempts by model and result"))
		ratio, _ = meter.Float64Histogram("localai_compression_ratio", metric.WithDescription("Original-to-compressed chat token ratio by model"))
		duration, _ = meter.Float64Histogram("localai_compression_duration_seconds", metric.WithDescription("Chat context compression duration by model"))
	})
	attrs := metric.WithAttributes(attribute.String("model", model), attribute.String("result", result))
	if events != nil {
		events.Add(context.Background(), 1, attrs)
	}
	if started.IsZero() {
		return
	}
	modelAttr := metric.WithAttributes(attribute.String("model", model))
	if duration != nil {
		duration.Record(context.Background(), time.Since(started).Seconds(), modelAttr)
	}
	if ratio != nil && compressed > 0 {
		ratio.Record(context.Background(), float64(original)/float64(compressed), modelAttr)
	}
}
