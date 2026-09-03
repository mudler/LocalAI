package testutil

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/messaging"
)

// FakeBus is an in-memory messaging.MessagingClient that delivers each published
// message synchronously to every registered subscriber whose subject filter
// matches. Matching is messaging.SubjectMatches, the same function the real
// carrier uses, so a filter that fires here fires in production too.
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
	// nextSubID names each subscription uniquely. Unsubscribe used to match on
	// the filter string, which silently removed a DIFFERENT subscriber's entry
	// whenever two subscribers shared one filter - and two subscribers sharing
	// one filter is exactly the topology every cross-replica spec builds.
	nextSubID int64
	// publishCounts records how many messages were published per subject, so a
	// spec can assert the echo-loop guard (an applied delta must not re-publish).
	publishCounts map[string]int

	// reconnectCbs back the optional OnReconnect/TriggerReconnect pair, letting a
	// spec exercise the component's reconnect re-hydrate path without a real
	// NATS server.
	reconnectCbs []func()
}

type fakeBusSub struct {
	id      int64
	subject string
	handler func([]byte)
}

// The two interfaces this double stands in for. Broadcaster is asserted here
// and not left to the first adopter: every cross-replica map now takes a
// messaging.Broadcaster, and a fake that drifted out of that interface would
// break each adopter's suite in turn rather than the package that owns it.
var (
	_ messaging.MessagingClient = (*FakeBus)(nil)
	_ messaging.Broadcaster     = (*FakeBus)(nil)
)

// NewFakeBus returns a ready-to-use in-memory bus.
func NewFakeBus() *FakeBus {
	return &FakeBus{publishCounts: map[string]int{}}
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
		if messaging.SubjectMatches(s.subject, subject) {
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

// Subscribers reports how many live subscriptions this bus is carrying.
//
// It exists so a spec can assert the NEGATIVE: a component configured
// standalone must register nothing at all, and "nothing was delivered" cannot
// tell that apart from "a subscription exists and nobody published".
func (b *FakeBus) Subscribers() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subs)
}

type fakeBusSubscription struct {
	bus    *FakeBus
	subRef fakeBusSub
}

// Unsubscribe removes THIS subscription and no other. Identity is the id
// minted at Subscribe time, not the filter: a map that subscribes to the same
// filter as a peer must not be able to deafen the peer by closing itself.
func (s *fakeBusSubscription) Unsubscribe() error {
	s.bus.mu.Lock()
	defer s.bus.mu.Unlock()
	for i, candidate := range s.bus.subs {
		if candidate.id == s.subRef.id {
			s.bus.subs = append(s.bus.subs[:i], s.bus.subs[i+1:]...)
			return nil
		}
	}
	return nil
}

// Subscribe refuses the same filters the real carrier refuses, so a spec cannot
// register a filter that would silently never fire in production.
func (b *FakeBus) Subscribe(subject string, handler func([]byte)) (messaging.Subscription, error) {
	if err := messaging.ValidFilter(subject); err != nil {
		return nil, err
	}
	b.mu.Lock()
	b.nextSubID++
	sub := fakeBusSub{id: b.nextSubID, subject: subject, handler: handler}
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
