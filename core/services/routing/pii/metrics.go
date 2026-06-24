package pii

import (
	"context"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// Prometheus counter for PII events. The EventStore ring buffer is
// capacity-bound and meant for recent-audit browsing; operators also want
// a monotonic, scrape-friendly signal ("how many detections/blocks per
// hour, did the filter stop firing after a deploy"). Record() is the
// single choke point every producer already goes through (request
// middleware, response scrubbing, MITM proxy connects/intercepts), so one
// counter here covers all paths without touching the producers.
//
// Initialised lazily on first Record so the package works no matter when
// (or whether) the Prometheus-backed global MeterProvider is installed —
// same pattern as core/services/routing/billing.
var (
	metricsOnce   sync.Once
	eventsCounter metric.Int64Counter
)

func recordEventMetric(e PIIEvent) {
	metricsOnce.Do(func() {
		meter := otel.Meter("github.com/mudler/LocalAI")
		c, err := meter.Int64Counter(
			"localai_pii_events_total",
			metric.WithDescription("PII/audit events recorded, labeled by kind, origin, action and direction"),
		)
		if err == nil {
			eventsCounter = c
		}
	})
	if eventsCounter == nil {
		return
	}
	eventsCounter.Add(context.Background(), 1, metric.WithAttributes(
		attribute.String("kind", string(e.Kind)),
		attribute.String("origin", string(e.Origin)),
		attribute.String("action", string(e.Action)),
		attribute.String("direction", string(e.Direction)),
	))
}
