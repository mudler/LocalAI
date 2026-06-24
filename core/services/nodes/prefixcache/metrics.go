package prefixcache

import (
	"context"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

var (
	pressureMetricsOnce sync.Once
	pressureCounter     metric.Int64Counter
)

func recordPressureMetric(model string) {
	pressureMetricsOnce.Do(func() {
		counter, err := otel.Meter("github.com/mudler/LocalAI").Int64Counter(
			"localai_prefix_cache_forced_disturb_total",
			metric.WithDescription("Prefix-cache forced-disturb events originated by this frontend"),
		)
		if err == nil {
			pressureCounter = counter
		}
	})
	if pressureCounter != nil {
		pressureCounter.Add(context.Background(), 1, metric.WithAttributes(attribute.String("model", model)))
	}
}
