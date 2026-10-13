package cluster

import (
	"context"
	"time"
)

// These expose internals to the specs. The production code has no caller for
// them.

// SetInterval changes how often the loop ticks, and how long one database call
// of a tick may take. Call it before Start.
func (m *Membership) SetInterval(d time.Duration) { m.interval = d }

// Tick runs one pass of the loop on the calling goroutine.
func (m *Membership) Tick(ctx context.Context) { m.tick(ctx) }
