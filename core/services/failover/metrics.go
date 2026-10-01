package failover

import (
	"context"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

var (
	metricsOnce sync.Once
	switches    metric.Int64Counter
)

func initMetrics() {
	metricsOnce.Do(func() {
		meter := otel.Meter("github.com/mudler/LocalAI")
		switches, _ = meter.Int64Counter("localai_failover_switches_total",
			metric.WithDescription("Failover chain switches between targets"))
	})
}

func recordSwitch(ev Event) {
	initMetrics()
	if switches == nil {
		return
	}
	switches.Add(context.Background(), 1, metric.WithAttributes(
		attribute.String("chain", ev.Chain),
		attribute.String("from", ev.From),
		attribute.String("to", ev.To),
		attribute.String("reason", string(ev.Reason)),
	))
}

// RegisterMetrics exports target health as a gauge. The application calls it
// once for its manager; tests create many managers and skip it.
func RegisterMetrics(m *Manager) {
	meter := otel.Meter("github.com/mudler/LocalAI")
	_, _ = meter.Int64ObservableGauge("localai_failover_target_up",
		metric.WithDescription("1 when a failover target is healthy, 0 otherwise"),
		metric.WithInt64Callback(func(_ context.Context, o metric.Int64Observer) error {
			for name, state := range m.targetStates() {
				v := int64(0)
				if state == StateHealthy {
					v = 1
				}
				o.Observe(v, metric.WithAttributes(attribute.String("target", name)))
			}
			return nil
		}))
}
