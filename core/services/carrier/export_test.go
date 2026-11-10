package carrier

// HookedCount is how many sets the holder keeps a reconnect hook for.
func (b *Broadcaster) HookedCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.hooked)
}
