package carrier

import "errors"

// IsReleased reports whether cause is the one the work of a released set is
// cancelled with.
func IsReleased(cause error) bool { return errors.Is(cause, errCarrierReleased) }

// HookedCount is how many sets the holder keeps a reconnect hook for.
func (b *Broadcaster) HookedCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.hooked)
}
