package agentpool

import (
	"context"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// Prometheus metrics for agent chat runs. Operators need a scrape-friendly
// signal for "are agent turns completing, erroring or getting cancelled,
// and how long do they take" — log-derived counters proved brittle
// (ANSI/timezone parsing, container-restart gaps). Chat() is the single
// choke point of the local execution path, so instrumenting the response
// handoff covers UI chats, API chats and connector-triggered asks alike.
//
// Lazily initialised on first record so the package works no matter when
// (or whether) the Prometheus-backed global MeterProvider is installed —
// same pattern as core/services/routing/pii.
var (
	agentMetricsOnce sync.Once
	runsCounter      metric.Int64Counter
	runSeconds       metric.Float64Histogram
)

func recordAgentRun(agent, outcome string, seconds float64) {
	agentMetricsOnce.Do(func() {
		meter := otel.Meter("github.com/mudler/LocalAI")
		if c, err := meter.Int64Counter(
			"localai_agent_runs_total",
			metric.WithDescription("Agent chat runs, labeled by agent and outcome (completed|error|cancelled)"),
		); err == nil {
			runsCounter = c
		}
		if h, err := meter.Float64Histogram(
			"localai_agent_run_seconds",
			metric.WithDescription("Wall-clock duration of agent chat runs in seconds"),
		); err == nil {
			runSeconds = h
		}
	})
	attrs := metric.WithAttributes(
		attribute.String("agent", agent),
		attribute.String("outcome", outcome),
	)
	if runsCounter != nil {
		runsCounter.Add(context.Background(), 1, attrs)
	}
	if runSeconds != nil {
		runSeconds.Record(context.Background(), seconds, attrs)
	}
}
