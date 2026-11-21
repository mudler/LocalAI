package carrier

import (
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/tunnel"
)

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

// NewTunnelReplies builds the request and reply of a tunnel set without the
// rest of the set.
func NewTunnelReplies(self string, peers PeerRequester, local *tunnel.Replies, queues map[string]QueueMembers) messaging.RequestReply {
	return &tunnelReplies{self: self, peers: peers, local: local, queues: queues}
}
