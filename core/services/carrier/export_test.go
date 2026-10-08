package carrier

import "context"

// HandoffForTest exposes the Handoff of the tunnel set to the specs.
func (c *ClaimWork) HandoffForTest() func(context.Context, *Set) error { return c.handoff() }

// HookedCount is how many sets the holder keeps a reconnect hook for.
func (b *Broadcaster) HookedCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.hooked)
}
