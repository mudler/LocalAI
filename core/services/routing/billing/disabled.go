package billing

import (
	"context"

	"github.com/mudler/LocalAI/core/http/auth"
)

// disabledBackend drops every record. Used when --disable-stats is set,
// e.g., for ephemeral CI runs where token tracking is just noise.
type disabledBackend struct{}

// NewDisabledBackend returns a no-op StatsBackend.
func NewDisabledBackend() StatsBackend { return disabledBackend{} }

func (disabledBackend) Record(_ context.Context, _ *auth.UsageRecord) error { return nil }
func (disabledBackend) Aggregate(_ context.Context, _ AggregateQuery) ([]auth.UsageBucket, error) {
	return nil, nil
}
func (disabledBackend) Close() error { return nil }
