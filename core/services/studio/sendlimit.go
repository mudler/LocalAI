package studio

import (
	"sync"
	"time"
)

// The model of a chat in Studio may write only so many messages to a messaging
// connection: a model that loops, or one that a page talked into it, must not
// fill a channel. The limit is for the chat, and a loop run is part of its
// chat. The outbox adds the limit of the connection itself.
const (
	studioSendMax    = 5
	studioSendWindow = 10 * time.Minute
)

// sendLimiter counts, for each chat, the messages sent in the last window.
//
// State mode: per-instance. The count is in memory, like the queues of the
// outbox it sits in front of; a restart or another frontend gives a chat a
// fresh budget. The limit is a brake for a model that runs away, not a quota.
type sendLimiter struct {
	mu sync.Mutex
	by map[string][]time.Time
}

// take records a message of the chat at now and reports whether it is within
// the limit. A refused message is not recorded.
func (l *sendLimiter) take(chat string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.by == nil {
		l.by = map[string][]time.Time{}
	}
	kept := l.by[chat][:0]
	for _, t := range l.by[chat] {
		if now.Sub(t) < studioSendWindow {
			kept = append(kept, t)
		}
	}
	if len(kept) >= studioSendMax {
		l.by[chat] = kept
		return false
	}
	l.by[chat] = append(kept, now)
	return true
}
