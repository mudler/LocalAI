// Package contract provides runtime invariant assertions for the routing
// module. Each Invariant call logs at error level via xlog, increments a
// Prometheus counter, and (under build tag routing_strict) panics so test
// runs surface violations as test failures.
//
// The routing subsystems (billing, router, pii, proxy, admission) all
// publish invariants through this single package so that observability —
// dashboards, alerts, post-mortem analysis — joins on a single counter
// name regardless of which subsystem fired.
package contract

import (
	"context"

	"github.com/mudler/xlog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

var violationCounter metric.Int64Counter

func init() {
	meter := otel.Meter("github.com/mudler/LocalAI/core/services/routing")
	c, err := meter.Int64Counter(
		"localai_invariant_violation_total",
		metric.WithDescription("Routing-module runtime invariant violations, labeled by name"),
	)
	if err != nil {
		// OTel API never returns an error in practice for a simple counter;
		// log and fall back to a nil counter (Add becomes a no-op).
		xlog.Error("failed to create invariant violation counter", "error", err)
		return
	}
	violationCounter = c
}

// Invariant asserts that cond is true. If false, it logs the violation
// and increments localai_invariant_violation_total{name=name}. Use
// fields for structured context (e.g., "model", "qwen-7b", "user", uid).
//
// In a build with -tags=routing_strict, a violation panics — meant for
// test suites and nightly E2E runs to surface drift. Production builds
// degrade silently into a metric so a single bad request does not crash
// the server.
func Invariant(name string, cond bool, fields ...any) {
	if cond {
		return
	}
	xlog.Error("routing invariant violated", append([]any{"name", name}, fields...)...)
	if violationCounter != nil {
		violationCounter.Add(context.Background(), 1, metric.WithAttributes(attribute.String("name", name)))
	}
	panicIfStrict(name, fields...)
}
