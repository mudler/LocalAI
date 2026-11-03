package session

// HarnessStateLen returns the size of the harness state that the chat holds in
// memory.
func (c *Chat) HarnessStateLen() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.rec.HarnessState)
}

// The limits of the queue of waiting messages, for the specs.
const (
	MaxPendingMessages = maxPendingMessages
	MaxPendingBytes    = maxPendingBytes
)
