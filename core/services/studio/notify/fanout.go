package notify

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/studio/fanout"
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
func NewLocalFanout() Fanout {
	return localFanout{hub: fanout.NewHub[string, Notification](subscriberBuffer)}
}

type localFanout struct {
	hub *fanout.Hub[string, Notification]
}

func (f localFanout) Publish(principal string, n Notification) { f.hub.Publish(principal, n) }

func (f localFanout) Subscribe(principal string) (<-chan Notification, func()) {
	return f.hub.Subscribe(principal)
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
	pipe := fanout.NewPipe[Notification](subscriberBuffer)
	sub, err := f.b.Subscribe(subject(principal), func(data []byte) {
		var n Notification
		if json.Unmarshal(data, &n) != nil {
			return
		}
		pipe.Send(n)
	})
	if err != nil {
		f.log.Warn("notify: could not subscribe", "error", err)
		pipe.Close()
		return pipe.C(), func() {}
	}
	return pipe.C(), func() { _ = sub.Unsubscribe(); pipe.Close() }
}
