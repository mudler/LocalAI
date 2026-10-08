package carrier

import "context"

// HandoffForTest exposes the Handoff of the tunnel set to the specs.
func (c *ClaimWork) HandoffForTest() func(context.Context, *Set) error { return c.handoff() }
