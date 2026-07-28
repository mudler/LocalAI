package galleryop

// Test-only seams onto unexported behaviour. Compiled into the test binary
// only, so nothing here reaches the shipped surface.

// ApplyEndForTest drives the NATS end path without standing up a broker.
// applyEnd is unexported and only ever reached from a subscription callback,
// which an external test package cannot trigger.
func (m *OpCache) ApplyEndForTest(jobID string) {
	m.applyEnd(OpCacheEvent{JobID: jobID})
}
