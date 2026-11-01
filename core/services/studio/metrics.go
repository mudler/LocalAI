package studio

import (
	"context"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// Turn outcomes, the values of the outcome label.
const (
	outcomeCompleted = "completed"
	outcomeError     = "error"
	outcomeCancelled = "cancelled"
)

// The Prometheus metrics of the chat turns keep the names the old agent pool
// had (localai_agent_runs_total and localai_agent_run_seconds), so a dashboard
// built on them keeps working. Every turn of the loop counts: a chat in the web
// page, a saved agent, a trigger run, a connector message and an agent job.
// Labels: agent (the saved agent's name, empty for a plain chat), origin (chat,
// trigger, connector or job) and outcome (completed, error or cancelled).
//
// The instruments are created on first use, so the metrics work whenever the
// Prometheus-backed global meter provider is installed; before that the calls
// go to a no-op meter.
var (
	turnMetricsOnce sync.Once
	turnsCounter    metric.Int64Counter
	turnSeconds     metric.Float64Histogram
)

func recordTurn(agent, origin, outcome string, seconds float64) {
	turnMetricsOnce.Do(func() {
		meter := otel.Meter("github.com/mudler/LocalAI")
		if c, err := meter.Int64Counter("localai_agent_runs_total",
			metric.WithDescription("Chat turns, labeled by agent, origin and outcome (completed|error|cancelled)")); err == nil {
			turnsCounter = c
		}
		if h, err := meter.Float64Histogram("localai_agent_run_seconds",
			metric.WithDescription("Wall-clock duration of chat turns in seconds")); err == nil {
			turnSeconds = h
		}
	})
	if origin == "" {
		origin = "chat"
	}
	attrs := metric.WithAttributes(
		attribute.String("agent", agent),
		attribute.String("origin", origin),
		attribute.String("outcome", outcome),
	)
	if turnsCounter != nil {
		turnsCounter.Add(context.Background(), 1, attrs)
	}
	if turnSeconds != nil {
		turnSeconds.Record(context.Background(), seconds, attrs)
	}
}

// outcomeOf maps how a turn ended to the outcome label.
func outcomeOf(stopped, failed bool) string {
	switch {
	case stopped:
		return outcomeCancelled
	case failed:
		return outcomeError
	}
	return outcomeCompleted
}

// observeTurn records the end of a turn. It reads the chat for its agent and
// origin; a failed read still counts the turn, with empty labels.
func (s *Service) observeTurn(ctx context.Context, p, conv string, started time.Time, outcome string) {
	var agent, origin string
	if rec, err := s.d.Store.GetChat(ctx, p, conv); err == nil {
		origin = rec.Origin
		if rec.AgentID != "" && s.d.Agents != nil {
			if ag, err := s.d.Agents.Get(ctx, p, rec.AgentID); err == nil {
				agent = ag.Name
			}
		}
	}
	recordTurn(agent, origin, outcome, time.Since(started).Seconds())
}
