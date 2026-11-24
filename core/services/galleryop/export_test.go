package galleryop

// Test-only seams onto unexported behaviour. Compiled into the test binary
// only, so nothing here reaches the shipped surface.

// ApplyEndForTest drives the NATS end path without standing up a broker.
// applyEnd is unexported and only ever reached from a subscription callback,
// which an external test package cannot trigger.
func (m *OpCache) ApplyEndForTest(jobID string) {
	m.applyEnd(OpCacheEvent{JobID: jobID})
}

// MergeStatusForTest applies a peer's status the way the wildcard NATS
// subscriber does.
func (g *GalleryService) MergeStatusForTest(opID string, op *OpStatus) {
	g.mergeStatus(opID, op)
}

// AnnounceOpForTest reports the end of an operation the way the worker does.
func (g *GalleryService) AnnounceOpForTest(id, name string, backend, deletion, upgrade bool, err error) {
	g.announceOp(id, name, backend, deletion, upgrade, false, err)
}

// AnnounceRunOpForTest is AnnounceOpForTest for an operation of an upgrade run.
func (g *GalleryService) AnnounceRunOpForTest(id, name string, backend, upgrade bool, err error) {
	g.announceOp(id, name, backend, false, upgrade, true, err)
}
