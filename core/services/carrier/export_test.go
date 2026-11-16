package carrier

import (
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/tunnel"
)

// HookedCount is how many sets the holder keeps a reconnect hook for.
func (b *Broadcaster) HookedCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.hooked)
}

// NewTunnelReplies builds the request and reply of a tunnel set without the
// rest of the set.
func NewTunnelReplies(self string, peers PeerRequester, local *tunnel.Replies, queues map[string]QueueMembers) messaging.RequestReply {
	return &tunnelReplies{self: self, peers: peers, local: local, queues: queues}
}
