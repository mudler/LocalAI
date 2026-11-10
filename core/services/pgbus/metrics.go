package pgbus

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// Where a broadcast was lost. The value is the "stage" attribute of the
// dropped counter.
const (
	// stageListener: the notification queue between the LISTEN connection and
	// the resolver was full.
	stageListener = "listener"
	// stageSubscription: the queue of one handler was full.
	stageSubscription = "subscription"
	// stageResolve: the notification could not be decoded, or the row that it
	// names could not be read.
	stageResolve = "resolve"
)

// Which way a broadcast travelled. The value is the "path" attribute of the
// published counter.
const (
	pathInline = "inline"
	pathSpill  = "spill"
)

// metrics are the counters of one Bus.
//
// A broadcast is at-most-once, so the counters are the only place where a loss
// is visible to an operator. Without them a loss shows up as a log line on the
// replica that took it, and as a missing event on the others.
type metrics struct {
	published     metric.Int64Counter
	dropped       metric.Int64Counter
	spillPurged   metric.Int64Counter
	reconnections metric.Int64Counter
}

// newMetrics creates the instruments on meter. An instrument that cannot be
// created is left nil and its updates are skipped, because a metric must not
// stop the carrier.
func newMetrics(meter metric.Meter) *metrics {
	if meter == nil {
		meter = otel.Meter("github.com/mudler/LocalAI")
	}
	m := &metrics{}
	m.published, _ = meter.Int64Counter("localai_pgbus_published_total",
		metric.WithDescription("Broadcasts published through the database, by path: inline in the notification, or spilled to a row"))
	m.dropped, _ = meter.Int64Counter("localai_pgbus_dropped_total",
		metric.WithDescription("Broadcasts that this replica received and lost, by stage: listener queue, subscription queue or resolve"))
	m.spillPurged, _ = meter.Int64Counter("localai_pgbus_spill_purged_total",
		metric.WithDescription("Spilled broadcast rows deleted after the retention"))
	m.reconnections, _ = meter.Int64Counter("localai_pgbus_reconnections_total",
		metric.WithDescription("Times the LISTEN connection was lost and opened again"))
	return m
}

func (m *metrics) publish(path string) {
	if m.published != nil {
		m.published.Add(context.Background(), 1, metric.WithAttributes(attribute.String("path", path)))
	}
}

func (m *metrics) drop(stage string) {
	if m.dropped != nil {
		m.dropped.Add(context.Background(), 1, metric.WithAttributes(attribute.String("stage", stage)))
	}
}

func (m *metrics) purge(rows int64) {
	if m.spillPurged != nil && rows > 0 {
		m.spillPurged.Add(context.Background(), rows)
	}
}

func (m *metrics) reconnect() {
	if m.reconnections != nil {
		m.reconnections.Add(context.Background(), 1)
	}
}
