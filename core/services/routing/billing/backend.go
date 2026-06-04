// Package billing provides the StatsBackend abstraction that decouples
// per-request token tracking from the auth database. This lets a
// single-user no-auth deployment still see usage and costs, which the
// pre-routing-module middleware did not allow.
package billing

import (
	"context"

	"github.com/mudler/LocalAI/core/http/auth"
)

// StatsBackend is the persistence target for usage records. Three
// implementations exist:
//
//   - GORM (auth-DB-backed) — used when --auth is on; records share the
//     auth database and existing aggregation queries continue to work.
//   - Memory (ring buffer) — used when --auth is off and no other DB is
//     configured. Records are lost on restart by design; the same
//     process can still answer aggregation queries for live dashboards.
//   - Disabled — explicit no-op when --disable-stats is set, useful in
//     ephemeral CI runs.
//
// All implementations are safe for concurrent use. Record() must not
// block the caller for more than the time it takes to enqueue — durable
// flushing happens on a background goroutine inside the implementation.
type StatsBackend interface {
	// Record enqueues a single usage record. The record is asynchronously
	// persisted; callers should not assume durability on return. The ctx
	// is currently unused but reserved for future cancellation.
	Record(ctx context.Context, r *auth.UsageRecord) error

	// Aggregate returns time-bucketed totals for the dashboard. The
	// AggregateQuery's UserID is required; pass the empty string only
	// from admin-scoped paths. Implementations that do not support
	// aggregation (e.g., ring buffer in saturation) may return an empty
	// result with no error.
	Aggregate(ctx context.Context, q AggregateQuery) ([]auth.UsageBucket, error)

	// Close releases resources (flushes pending records, stops
	// goroutines). Safe to call multiple times.
	Close() error
}

// AggregateQuery describes a usage aggregation request. Period is one of
// "day", "week", "month", "all" (matching the existing auth.UsageRecord
// vocabulary). UserID empty means cluster-wide; callers must enforce
// admin permission before passing the empty string.
type AggregateQuery struct {
	UserID string
	Period string
}
