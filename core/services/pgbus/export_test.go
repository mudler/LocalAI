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

func (b *Bus) ApplicationName() string       { return b.appName }
func (b *Bus) QueueDepth() int               { return cap(b.inbound) }
func (b *Bus) SpillRetention() time.Duration { return b.retention }
func (b *Bus) DSN() string                   { return b.cfg.DSN }
func (b *Bus) PurgeBefore(ctx context.Context, olderThan time.Duration) (int64, error) {
	return b.purgeBefore(ctx, olderThan)
}

// Subscribers reports how many handlers are registered, in every channel. A
// handler that is counted is live: its channel was listened before it was
// counted.
//
// The count exists to be asserted, because the leak that it shows has no other
// symptom. Two subscriptions in a deployment are opened and closed for every
// HTTP request (the progress stream of a job and the event stream of an agent),
// and only the first subscriber of a channel issues a LISTEN. The others only
// register a filter in the process. An Unsubscribe that did not remove its filter
// would leave a replica that served ten thousand requests running ten thousand
// closures for every notification, and nothing would fail. It would be slower.
func (b *Bus) Subscribers() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := 0
	for _, channel := range b.subs {
		n += len(channel)
	}
	return n
}
