package studio

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"sync"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/studio/session"
)

// ControlMsg is a request to the frontend that runs a chat's turn.
type ControlMsg struct {
	Stop bool `json:"stop,omitempty"`
}

// Bus carries a chat's live events, and control messages to the frontend that
// runs its turn, between everything that serves the chat. In one process it is a
// local fan-out; in a cluster it rides the message bus, so a client connected to
// one frontend sees the turn that another frontend runs. Delivery is live only
// and at most once: a client that missed events reads the transcript.
type Bus interface {
	Publish(key string, ev session.Event)
	// Subscribe returns the events of a chat from now on. Call cancel when done;
	// the channel closes then, or when the subscriber falls too far behind.
	Subscribe(key string) (events <-chan session.Event, cancel func())
	// Control sends a message to whoever handles the chat with OnControl.
	Control(key string, msg ControlMsg)
	// OnControl registers a handler for the chat's control messages.
	OnControl(key string, h func(ControlMsg)) (cancel func())
}

// NewLocalBus returns a Bus for one process.
func NewLocalBus() Bus { return &localBus{hub: newHub(), ctl: map[string]map[int]func(ControlMsg){}} }

type localBus struct {
	hub *hub

	mu   sync.Mutex
	next int
	ctl  map[string]map[int]func(ControlMsg)
}

func (b *localBus) Publish(key string, ev session.Event) { b.hub.publish(key, ev) }

func (b *localBus) Subscribe(key string) (<-chan session.Event, func()) { return b.hub.subscribe(key) }

func (b *localBus) Control(key string, msg ControlMsg) {
	b.mu.Lock()
	var hs []func(ControlMsg)
	for _, h := range b.ctl[key] {
		hs = append(hs, h)
	}
	b.mu.Unlock()
	for _, h := range hs {
		go h(msg)
	}
}

func (b *localBus) OnControl(key string, h func(ControlMsg)) func() {
	b.mu.Lock()
	b.next++
	id := b.next
	if b.ctl[key] == nil {
		b.ctl[key] = map[int]func(ControlMsg){}
	}
	b.ctl[key][id] = h
	b.mu.Unlock()
	return func() {
		b.mu.Lock()
		delete(b.ctl[key], id)
		if len(b.ctl[key]) == 0 {
			delete(b.ctl, key)
		}
		b.mu.Unlock()
	}
}

// maxEventBytes keeps a broadcast under the size the message bus guarantees
// (7999 bytes), with room for the envelope.
const maxEventBytes = 7000

// NewBroadcastBus returns a Bus on the cluster's message bus. Its subjects are
// under the chats root.
func NewBroadcastBus(b messaging.Broadcaster, log *slog.Logger) Bus {
	if log == nil {
		log = slog.Default()
	}
	return &broadcastBus{b: b, log: log}
}

type broadcastBus struct {
	b   messaging.Broadcaster
	log *slog.Logger
}

// token turns a conversation key into one subject token. The key holds a user
// id, which may contain characters a subject cannot.
func token(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

func eventSubject(key string) string   { return "chats.events." + token(key) }
func controlSubject(key string) string { return "chats.control." + token(key) }

func (b *broadcastBus) Publish(key string, ev session.Event) {
	if err := b.b.Publish(eventSubject(key), fit(ev)); err != nil {
		b.log.Warn("studio: could not publish an event", "error", err)
	}
}

// fit trims an event to maxEventBytes. Events are for display, and the
// transcript has the full text, so a long one is cut and never dropped.
func fit(ev session.Event) session.Event {
	if size(ev) <= maxEventBytes {
		return ev
	}
	ev.Text = cut(ev.Text, 1500)
	if ev.Tool != nil {
		t := *ev.Tool
		t.Output, t.Summary = cut(t.Output, 1500), cut(t.Summary, 200)
		ev.Tool = &t
	}
	if size(ev) > maxEventBytes {
		ev.Change = nil
		ev.IDs = nil
		ev.Text = cut(ev.Text, 200)
	}
	return ev
}

func size(ev session.Event) int {
	raw, err := json.Marshal(ev)
	if err != nil {
		return 0
	}
	return len(raw)
}

func cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func (b *broadcastBus) Subscribe(key string) (<-chan session.Event, func()) {
	sub := &subscriber{ch: make(chan session.Event, subscriberBuffer)}
	var mu sync.Mutex
	closed := false
	closeSub := func() {
		mu.Lock()
		defer mu.Unlock()
		if !closed {
			closed = true
			close(sub.ch)
		}
	}
	nsub, err := b.b.Subscribe(eventSubject(key), func(data []byte) {
		var ev session.Event
		if err := json.Unmarshal(data, &ev); err != nil {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if closed {
			return
		}
		select {
		case sub.ch <- ev:
		default: // a subscriber that falls behind is dropped; it reconnects
			closed = true
			close(sub.ch)
		}
	})
	if err != nil {
		b.log.Warn("studio: could not subscribe to a chat's events", "error", err)
		closeSub()
		return sub.ch, func() {}
	}
	return sub.ch, func() {
		_ = nsub.Unsubscribe()
		closeSub()
	}
}

func (b *broadcastBus) Control(key string, msg ControlMsg) {
	if err := b.b.Publish(controlSubject(key), msg); err != nil {
		b.log.Warn("studio: could not send a control message", "error", err)
	}
}

func (b *broadcastBus) OnControl(key string, h func(ControlMsg)) func() {
	nsub, err := b.b.Subscribe(controlSubject(key), func(data []byte) {
		var msg ControlMsg
		if err := json.Unmarshal(data, &msg); err != nil {
			return
		}
		go h(msg)
	})
	if err != nil {
		b.log.Warn("studio: could not listen for control messages", "error", err)
		return func() {}
	}
	return func() { _ = nsub.Unsubscribe() }
}
