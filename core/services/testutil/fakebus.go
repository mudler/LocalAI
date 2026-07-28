package testutil

import (
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/messaging"
)

// FakeBus is an in-memory messaging.MessagingClient that delivers each published
// message synchronously to every registered subscriber whose subject filter
// matches, including NATS-style wildcard subjects (`*` matches exactly one
// token).
//
// Synchronous delivery keeps specs deterministic: the moment Publish returns,
// every matching subscriber's handler has already run, so the spec body can read
// the resulting state without polling. It is the shared test double for every
// cross-replica-sync adopter (gallery, syncstate, ...) so they exercise the same
// delivery semantics. It deliberately depends only on the standard library and
// the messaging package — no test framework — so it is importable anywhere.
type FakeBus struct {
	mu   sync.Mutex
	subs []fakeBusSub
	// publishCounts records how many messages were published per subject, so a
	// spec can assert the echo-loop guard (an applied delta must not re-publish).
	publishCounts map[string]int

	// reconnectCbs back the optional OnReconnect/TriggerReconnect pair, letting a
	// spec exercise the component's reconnect re-hydrate path without a real
	// NATS server.
	reconnectCbs []func()
}

type fakeBusSub struct {
	subject string
	handler func([]byte)
}

// NewFakeBus returns a ready-to-use in-memory bus.
func NewFakeBus() *FakeBus {
	return &FakeBus{publishCounts: map[string]int{}}
}

// subjectMatches reports whether a subscription filter matches a concrete
// subject, honoring the single-token `*` wildcard used by NATS.
func subjectMatches(filter, subject string) bool {
	if filter == subject {
		return true
	}
	fp := strings.Split(filter, ".")
	sp := strings.Split(subject, ".")
	if len(fp) != len(sp) {
		return false
	}
	for i := range fp {
		if fp[i] == "*" {
			continue
		}
		if fp[i] != sp[i] {
			return false
		}
	}
	return true
}

// Publish marshals data as JSON and delivers it synchronously to every matching
// subscriber.
func (b *FakeBus) Publish(subject string, data any) error {
	payload, err := json.Marshal(data)
	if err != nil {
		return err
	}
	b.mu.Lock()
	b.publishCounts[subject]++
	subs := append([]fakeBusSub(nil), b.subs...)
	b.mu.Unlock()
	for _, s := range subs {
		if subjectMatches(s.subject, subject) {
			s.handler(payload)
		}
	}
	return nil
}

// PublishCount returns how many messages were published on the exact subject.
func (b *FakeBus) PublishCount(subject string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.publishCounts[subject]
}

type fakeBusSubscription struct {
	bus    *FakeBus
	subRef fakeBusSub
}

func (s *fakeBusSubscription) Unsubscribe() error {
	s.bus.mu.Lock()
	defer s.bus.mu.Unlock()
	for i, candidate := range s.bus.subs {
		if candidate.subject == s.subRef.subject {
			s.bus.subs = append(s.bus.subs[:i], s.bus.subs[i+1:]...)
			return nil
		}
	}
	return nil
}

func (b *FakeBus) Subscribe(subject string, handler func([]byte)) (messaging.Subscription, error) {
	sub := fakeBusSub{subject: subject, handler: handler}
	b.mu.Lock()
	b.subs = append(b.subs, sub)
	b.mu.Unlock()
	return &fakeBusSubscription{bus: b, subRef: sub}, nil
}

func (b *FakeBus) QueueSubscribe(subject, _ string, handler func([]byte)) (messaging.Subscription, error) {
	return b.Subscribe(subject, handler)
}

func (b *FakeBus) QueueSubscribeReply(string, string, func([]byte, func([]byte))) (messaging.Subscription, error) {
	return &fakeBusSubscription{bus: b}, nil
}

func (b *FakeBus) SubscribeReply(string, func([]byte, func([]byte))) (messaging.Subscription, error) {
	return &fakeBusSubscription{bus: b}, nil
}

func (b *FakeBus) Request(string, []byte, time.Duration) ([]byte, error) {
	return nil, nil
}

func (b *FakeBus) IsConnected() bool { return true }
func (b *FakeBus) Close()            {}

// OnReconnect mirrors *messaging.Client.OnReconnect so a spec can drive the
// component's reconnect re-hydrate path. The component detects this method via an
// optional interface assertion; implementing it here keeps the fake a faithful
// stand-in for the concrete client.
func (b *FakeBus) OnReconnect(cb func()) {
	if cb == nil {
		return
	}
	b.mu.Lock()
	b.reconnectCbs = append(b.reconnectCbs, cb)
	b.mu.Unlock()
}

// TriggerReconnect runs every registered reconnect callback, simulating a NATS
// reconnect event.
func (b *FakeBus) TriggerReconnect() {
	b.mu.Lock()
	cbs := append([]func(){}, b.reconnectCbs...)
	b.mu.Unlock()
	for _, cb := range cbs {
		cb()
	}
}
