package studio

import (
	"sync"

	"github.com/mudler/LocalAI/core/services/studio/session"
)

// hub fans a conversation's events out to its subscribers. A subscriber that
// falls behind is dropped, not waited for: it reconnects and reads the
// transcript, so one slow browser never stalls the loop.
type hub struct {
	mu   sync.Mutex
	subs map[string]map[*subscriber]struct{}
}

type subscriber struct {
	ch   chan session.Event
	once sync.Once
}

const subscriberBuffer = 512

func newHub() *hub { return &hub{subs: map[string]map[*subscriber]struct{}{}} }

func (h *hub) subscribe(key string) (<-chan session.Event, func()) {
	sub := &subscriber{ch: make(chan session.Event, subscriberBuffer)}
	h.mu.Lock()
	if h.subs[key] == nil {
		h.subs[key] = map[*subscriber]struct{}{}
	}
	h.subs[key][sub] = struct{}{}
	h.mu.Unlock()
	return sub.ch, func() { h.drop(key, sub) }
}

func (h *hub) drop(key string, sub *subscriber) {
	h.mu.Lock()
	delete(h.subs[key], sub)
	if len(h.subs[key]) == 0 {
		delete(h.subs, key)
	}
	h.mu.Unlock()
	sub.once.Do(func() { close(sub.ch) })
}

func (h *hub) publish(key string, ev session.Event) {
	h.mu.Lock()
	var slow []*subscriber
	for sub := range h.subs[key] {
		select {
		case sub.ch <- ev:
		default:
			slow = append(slow, sub)
		}
	}
	h.mu.Unlock()
	for _, sub := range slow {
		h.drop(key, sub)
	}
}
