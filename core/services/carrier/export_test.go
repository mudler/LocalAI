package carrier

import (
	"time"

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

// HoldPublish makes the set look as if a publish ran on it, until the
// returned function is called.
func (s *Set) HoldPublish() (done func()) {
	s.publishing.Add(1)
	return func() { s.publishing.Add(-1) }
}

// SetReleaseWait changes how long Release waits for the publishes of a set and
// returns a function that puts the old value back.
func SetReleaseWait(d time.Duration) (restore func()) {
	old := releaseWait
	releaseWait = d
	return func() { releaseWait = old }
}
