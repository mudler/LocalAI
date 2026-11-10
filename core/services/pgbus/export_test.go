package pgbus

import (
	"context"
	"time"
)

// These expose internals to the specs. The production code has no caller for
// them.

const (
	MaxNotifyPayloadBytes = maxNotifyPayloadBytes
	SpillSweepSQL         = spillSweepSQL
)

func (b *Bus) Subscribers() int              { return b.subscribers() }
func (b *Bus) ApplicationName() string       { return b.appName }
func (b *Bus) QueueDepth() int               { return cap(b.inbound) }
func (b *Bus) SpillRetention() time.Duration { return b.retention }
func (b *Bus) DSN() string                   { return b.cfg.DSN }
func (b *Bus) PurgeBefore(ctx context.Context, olderThan time.Duration) (int64, error) {
	return b.purgeBefore(ctx, olderThan)
}
