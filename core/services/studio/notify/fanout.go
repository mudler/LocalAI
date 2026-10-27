package notify

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"sync"

	"github.com/mudler/LocalAI/core/services/messaging"
)

// Fanout delivers each new notification to the live clients of its owner on
// every frontend. Delivery is live only and at most once: a client that missed
// one reads the list. The store is the record.
type Fanout interface {
	Publish(principal string, n Notification)
	// Subscribe returns the notifications of a principal from now on. Call
	// cancel when done; the channel closes then, or when the subscriber falls
	// too far behind.
	Subscribe(principal string) (<-chan Notification, func())
}

const subscriberBuffer = 32

// NewLocalFanout returns a Fanout for one process.
func NewLocalFanout() Fanout { return &localFanout{subs: map[string]map[int]chan Notification{}} }

type localFanout struct {
	mu   sync.Mutex
	next int
	subs map[string]map[int]chan Notification
}

func (f *localFanout) Publish(principal string, n Notification) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for id, ch := range f.subs[principal] {
		select {
		case ch <- n:
		default: // too far behind: drop it, it reconnects and reads the list
			close(ch)
			delete(f.subs[principal], id)
		}
	}
}

func (f *localFanout) Subscribe(principal string) (<-chan Notification, func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.next++
	id, ch := f.next, make(chan Notification, subscriberBuffer)
	if f.subs[principal] == nil {
		f.subs[principal] = map[int]chan Notification{}
	}
	f.subs[principal][id] = ch
	return ch, func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		if c, ok := f.subs[principal][id]; ok {
			close(c)
			delete(f.subs[principal], id)
		}
	}
}

// NewBroadcastFanout returns a Fanout on the cluster's message bus. A frontend
// hears its own notifications through the bus like the others do.
func NewBroadcastFanout(b messaging.Broadcaster, log *slog.Logger) Fanout {
	if log == nil {
		log = slog.Default()
	}
	return &broadcastFanout{b: b, log: log}
}

type broadcastFanout struct {
	b   messaging.Broadcaster
	log *slog.Logger
}

// subject turns a principal into one subject token: a user id may hold
// characters a subject cannot.
func subject(principal string) string {
	sum := sha256.Sum256([]byte(principal))
	return "chats.notify." + hex.EncodeToString(sum[:])
}

func (f *broadcastFanout) Publish(principal string, n Notification) {
	if err := f.b.Publish(subject(principal), n); err != nil {
		f.log.Warn("notify: could not publish a notification", "error", err)
	}
}

func (f *broadcastFanout) Subscribe(principal string) (<-chan Notification, func()) {
	ch := make(chan Notification, subscriberBuffer)
	var (
		mu     sync.Mutex
		closed bool
	)
	closeCh := func() {
		mu.Lock()
		defer mu.Unlock()
		if !closed {
			closed = true
			close(ch)
		}
	}
	sub, err := f.b.Subscribe(subject(principal), func(data []byte) {
		var n Notification
		if json.Unmarshal(data, &n) != nil {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if closed {
			return
		}
		select {
		case ch <- n:
		default:
			closed = true
			close(ch)
		}
	})
	if err != nil {
		f.log.Warn("notify: could not subscribe", "error", err)
		closeCh()
		return ch, func() {}
	}
	return ch, func() { _ = sub.Unsubscribe(); closeCh() }
}
