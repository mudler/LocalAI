package carrier

import (
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/mudler/LocalAI/core/services/messaging"
)

// ErrActiveSet is returned when a caller asks to release the active set.
var ErrActiveSet = errors.New("the active carrier set cannot be released")

// Broadcaster is the fan-out holder. Publish goes to the current set. It also
// keeps a registry of every subscription, because a subscription lives on one
// carrier and has to be made again on the next one.
//
// A swap runs in three calls:
//
//   - Listen(next) attaches every registered subscription to next as well, so
//     each handler listens on both carriers. Publish still uses the old one.
//   - The caller stores next in the pointer. Publish now uses next, and a
//     message that a slower peer still publishes on the old carrier is heard.
//   - Release(old) drops the subscriptions on the old carrier.
//
// No message is published on two carriers, so nothing is delivered twice.
type Broadcaster struct {
	cur *atomic.Pointer[Set]

	mu        sync.Mutex
	listening []*Set // sets the subscriptions are attached to
	hooked    map[*Set]struct{}
	subs      map[*subscription]struct{}
	reconnect []func()
}

var _ messaging.Broadcaster = (*Broadcaster)(nil)

// NewBroadcaster returns a holder over cur, which must name a set. Existing
// and later subscriptions attach to that set.
func NewBroadcaster(cur *atomic.Pointer[Set]) *Broadcaster {
	b := &Broadcaster{
		cur:    cur,
		hooked: map[*Set]struct{}{},
		subs:   map[*subscription]struct{}{},
	}
	first := cur.Load()
	b.listening = []*Set{first}
	b.hook(first)
	return b
}

// Publish sends on the current set. It takes no lock.
func (b *Broadcaster) Publish(subject string, data any) error {
	return b.cur.Load().Broadcaster.Publish(subject, data)
}

// Subscribe attaches handler to every set the holder listens on. It fails, and
// leaves nothing attached, when any of them refuses.
//
// The holder carries fan-out and nothing else. It refuses, with
// messaging.ErrUnservedSubject, a subject outside the broadcast roots: the
// control roots (request and reply to one node or one agent worker) have their
// own clients, and a carrier with no request and reply, such as pgbus, cannot
// serve them. A subscription that the holder accepted here and the target set
// refused later would fail the Listen of a carrier switch, long after the call
// that caused it. So a subscription that is accepted is servable by every
// carrier, and Listen does not fail for the subject.
func (b *Broadcaster) Subscribe(subject string, handler func([]byte)) (messaging.Subscription, error) {
	if err := messaging.ValidateBroadcastSubject(subject); err != nil {
		return nil, err
	}
	sub := &subscription{owner: b, subject: subject, handler: handler, inner: map[*Set]messaging.Subscription{}}

	// Read the sets and register in one step, so a concurrent Listen either
	// sees this subscription or is seen by it, never neither.
	b.mu.Lock()
	sets := slices.Clone(b.listening)
	b.subs[sub] = struct{}{}
	b.mu.Unlock()

	for _, s := range sets {
		if err := sub.attach(s); err != nil {
			_ = sub.Unsubscribe()
			return nil, err
		}
	}
	return sub, nil
}

// Listen attaches every registered subscription to next. It does nothing if
// next is already attached. On failure it detaches next again, so the caller
// can report that the carrier is not ready.
func (b *Broadcaster) Listen(next *Set) error {
	b.mu.Lock()
	if slices.Contains(b.listening, next) {
		b.mu.Unlock()
		return nil
	}
	b.listening = append(b.listening, next)
	b.hook(next)
	subs := b.snapshotLocked()
	b.mu.Unlock()

	for _, sub := range subs {
		if err := sub.attach(next); err != nil {
			_ = b.Release(next)
			return fmt.Errorf("attaching subscriptions to carrier %q: %w", next.Name, err)
		}
	}
	return nil
}

// Release detaches every subscription from old. It refuses the active set.
// Releasing a set that is not attached does nothing.
func (b *Broadcaster) Release(old *Set) error {
	if b.cur.Load() == old {
		return ErrActiveSet
	}
	b.mu.Lock()
	i := slices.Index(b.listening, old)
	if i < 0 {
		b.mu.Unlock()
		return nil
	}
	b.listening = slices.Delete(b.listening, i, i+1)
	subs := b.snapshotLocked()
	b.mu.Unlock()

	var errs error
	for _, sub := range subs {
		errs = errors.Join(errs, sub.detach(old))
	}
	return errs
}

// OnReconnect registers cb to run after any carrier the holder uses recovers a
// lost connection. Consumers re-read their state from tables on it, because a
// reconnecting subscriber misses messages. A nil cb is ignored.
func (b *Broadcaster) OnReconnect(cb func()) {
	if cb == nil {
		return
	}
	b.mu.Lock()
	b.reconnect = append(b.reconnect, cb)
	b.mu.Unlock()
}

// NotifyReconnect runs every registered hook once. The swap code calls it when
// a swap is complete: messages sent across the flip are not ordered, so the
// consumers read their state again.
func (b *Broadcaster) NotifyReconnect() {
	b.mu.Lock()
	cbs := slices.Clone(b.reconnect)
	b.mu.Unlock()
	for _, cb := range cbs {
		cb()
	}
}

// hook asks a set to tell the holder about its reconnects. The caller holds
// b.mu or owns b exclusively. A set is hooked once for the life of the holder.
func (b *Broadcaster) hook(s *Set) {
	if _, done := b.hooked[s]; done || s.OnReconnect == nil {
		return
	}
	b.hooked[s] = struct{}{}
	s.OnReconnect(b.NotifyReconnect)
}

func (b *Broadcaster) snapshotLocked() []*subscription {
	subs := make([]*subscription, 0, len(b.subs))
	for s := range b.subs {
		subs = append(subs, s)
	}
	return subs
}

func (b *Broadcaster) attached(s *Set) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Contains(b.listening, s)
}

// subscription is one registered subscription and its attachment to each set.
type subscription struct {
	owner   *Broadcaster
	subject string
	handler func([]byte)

	mu     sync.Mutex
	closed bool
	inner  map[*Set]messaging.Subscription
}

// attach subscribes on s. It drops the new subscription again when this one
// was closed, or s was released, while the carrier was busy.
func (sub *subscription) attach(s *Set) error {
	in, err := s.Broadcaster.Subscribe(sub.subject, sub.handler)
	if err != nil {
		return err
	}
	sub.mu.Lock()
	defer sub.mu.Unlock()
	if sub.closed || !sub.owner.attached(s) {
		return in.Unsubscribe()
	}
	if _, dup := sub.inner[s]; dup {
		return in.Unsubscribe()
	}
	sub.inner[s] = in
	return nil
}

func (sub *subscription) detach(s *Set) error {
	sub.mu.Lock()
	in, ok := sub.inner[s]
	delete(sub.inner, s)
	sub.mu.Unlock()
	if !ok {
		return nil
	}
	return in.Unsubscribe()
}

// Unsubscribe removes the subscription from every set. It is safe to call
// twice.
func (sub *subscription) Unsubscribe() error {
	sub.mu.Lock()
	if sub.closed {
		sub.mu.Unlock()
		return nil
	}
	sub.closed = true
	inner := sub.inner
	sub.inner = nil
	sub.mu.Unlock()

	sub.owner.mu.Lock()
	delete(sub.owner.subs, sub)
	sub.owner.mu.Unlock()

	var errs error
	for _, in := range inner {
		errs = errors.Join(errs, in.Unsubscribe())
	}
	return errs
}
