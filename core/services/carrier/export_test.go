package carrier

// HookedCount is how many members the holder keeps a reconnect hook for.
func (b *Broadcaster) HookedCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.hooked)
}

// ListenBus attaches every subscription to one more bus, as the holder does
// for the buses that it is built with.
func (b *Broadcaster) ListenBus(m *Bus) error { return b.listen(m) }

// ReleaseBus detaches every subscription from a bus.
func (b *Broadcaster) ReleaseBus(m *Bus) error { return b.release(m) }

// DropCache forgets the routes read for every node, as the time-to-live of the
// cache does.
func (t *RouteTable) DropCache() {
	t.mu.Lock()
	clear(t.cache)
	t.mu.Unlock()
}
