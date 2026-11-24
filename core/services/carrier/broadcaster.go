package carrier

import (
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/services/messaging"
)

// BroadcasterOptions are the inputs of NewBroadcaster.
type BroadcasterOptions struct {
	// Floor is the bus that every frontend and chat worker has: LISTEN and
	// NOTIFY on the cluster database. It is required.
	Floor *Bus
	// Primary is the NATS bus, when a NATS server is configured for the
	// cluster. It may be nil.
	Primary *Bus
}

// Broadcaster is the fan-out holder of the frontends and chat workers.
//
// It listens on every bus it has: always on the floor (PostgreSQL), and on NATS
// when a NATS server is configured. It publishes each broadcast on exactly one
// of them: NATS when it is configured and this process is connected to it, the
// floor otherwise. Every process listens on the floor, so a process that lost
// NATS still reaches the others. No message is published on two buses, so
// nothing is delivered twice and a message needs no id.
//
// A process that has NATS configured and cannot reach it does not hear what the
// others publish on NATS. Unready reports it, and the process fails its
// readiness probe until NATS is back.
//
// The holder keeps a registry of every subscription, because a subscription
// lives on one bus and has to be made on each of them.
type Broadcaster struct {
	floor   *Bus
	primary *Bus
	// last is the bus of the latest publish. A change of the bus runs the
	// reconnect hooks, because messages across the change are not ordered.
	last atomic.Pointer[Bus]

	mu        sync.Mutex
	listening []*Bus                // buses the subscriptions are attached to
	hooked    map[*Bus]*atomic.Bool // live flag of the reconnect hook of each bus
	subs      map[*subscription]struct{}
	reconnect []func()
}

var _ messaging.Broadcaster = (*Broadcaster)(nil)

// NewBroadcaster returns a holder that listens on the buses of o.
func NewBroadcaster(o BroadcasterOptions) (*Broadcaster, error) {
	if err := o.Floor.Validate(); err != nil {
		return nil, fmt.Errorf("the floor of the broadcaster: %w", err)
	}
	if o.Primary != nil {
		if err := o.Primary.Validate(); err != nil {
			return nil, fmt.Errorf("the primary bus of the broadcaster: %w", err)
		}
	}
	b := &Broadcaster{
		floor:   o.Floor,
		primary: o.Primary,
		hooked:  map[*Bus]*atomic.Bool{},
		subs:    map[*subscription]struct{}{},
	}
	for _, m := range []*Bus{o.Floor, o.Primary} {
		if m != nil {
			// Nothing is registered yet, so this attaches no subscription.
			if err := b.listen(m); err != nil {
				return nil, err
			}
		}
	}
	return b, nil
}

// publishBus is the bus that a publish goes to now.
func (b *Broadcaster) publishBus() *Bus {
	if b.primary != nil && b.primary.connected() {
		return b.primary
	}
	return b.floor
}

// Publish sends on one bus: the primary when this process reaches it, the floor
// otherwise. It takes no lock and allocates nothing.
func (b *Broadcaster) Publish(subject string, data any) error {
	bus := b.publishBus()
	if last := b.last.Load(); last != bus {
		b.moved(last, bus)
	}
	return bus.Broadcaster.Publish(subject, data)
}

// moved records that publishes go to another bus now. Messages that this
// process published on the old bus and on the new one are not ordered for a
// subscriber, so the consumers read their state again.
func (b *Broadcaster) moved(from, to *Bus) {
	if !b.last.CompareAndSwap(from, to) || from == nil {
		return
	}
	if to == b.floor {
		xlog.Warn("Broadcasts go over PostgreSQL: this process does not reach the NATS server of the cluster", "from", from.Name, "to", to.Name)
	} else {
		xlog.Info("Broadcasts go over NATS again", "from", from.Name, "to", to.Name)
	}
	go b.NotifyReconnect()
}

// PublishBus names the bus that a publish goes to now.
func (b *Broadcaster) PublishBus() string { return b.publishBus().Name }

// Unready reports why this process is not ready to serve: a bus that is
// configured and that it does not reach. It wraps ErrBusUnreachable. It is nil
// when every bus is reachable.
func (b *Broadcaster) Unready() error {
	if b.primary != nil && !b.primary.connected() {
		return fmt.Errorf("%w: the %s bus is configured for the cluster and this process does not reach it, so it does not hear the broadcasts of the others; it listens and publishes on %s until the bus is back",
			ErrBusUnreachable, b.primary.Name, b.floor.Name)
	}
	return nil
}

// Subscribe attaches handler to every bus the holder listens on. It fails, and
// leaves nothing attached, when any of them refuses.
//
// The holder carries fan-out and nothing else. It refuses, with
// messaging.ErrUnservedSubject, a subject outside the broadcast roots: the
// control roots (request and reply to one node or one agent worker) have their
// own clients, and a bus with no request and reply, such as pgbus, cannot serve
// them. So a subscription that is accepted is servable by every bus.
func (b *Broadcaster) Subscribe(subject string, handler func([]byte)) (messaging.Subscription, error) {
	if err := messaging.ValidateBroadcastSubject(subject); err != nil {
		return nil, err
	}
	return b.register(subject, func(m *Bus) (messaging.Subscription, error) {
		return m.Broadcaster.Subscribe(subject, handler)
	})
}

// register keeps open as a subscription of the holder and runs it on every bus
// that the holder listens on.
func (b *Broadcaster) register(subject string, open func(*Bus) (messaging.Subscription, error)) (messaging.Subscription, error) {
	sub := &subscription{owner: b, subject: subject, open: open, inner: map[*Bus]messaging.Subscription{}}

	// Read the buses and register in one step, so a concurrent Listen either
	// sees this subscription or is seen by it, never neither.
	b.mu.Lock()
	members := slices.Clone(b.listening)
	b.subs[sub] = struct{}{}
	b.mu.Unlock()

	for _, m := range members {
		if err := sub.attach(m); err != nil {
			_ = sub.Unsubscribe()
			return nil, err
		}
	}
	return sub, nil
}

// listen attaches every registered subscription to m. It does nothing if m is
// already attached. It does not undo a partial attach.
func (b *Broadcaster) listen(m *Bus) error {
	b.mu.Lock()
	if slices.Contains(b.listening, m) {
		b.mu.Unlock()
		return nil
	}
	b.listening = append(b.listening, m)
	b.hook(m)
	subs := b.snapshotLocked()
	b.mu.Unlock()

	for _, sub := range subs {
		if err := sub.attach(m); err != nil {
			return err
		}
	}
	return nil
}

// release detaches every subscription from m.
func (b *Broadcaster) release(m *Bus) error {
	b.mu.Lock()
	i := slices.Index(b.listening, m)
	if i < 0 {
		b.mu.Unlock()
		return nil
	}
	b.listening = slices.Delete(b.listening, i, i+1)
	// A member has no way to drop a callback, so the hook that it holds is
	// switched off instead: a later Listen registers a new one, and only one of
	// them is live.
	if live, ok := b.hooked[m]; ok {
		live.Store(false)
		delete(b.hooked, m)
	}
	subs := b.snapshotLocked()
	b.mu.Unlock()

	var errs error
	for _, sub := range subs {
		errs = errors.Join(errs, sub.detach(m))
	}
	return errs
}

// Close detaches every subscription from the buses and closes them. The holder
// must not be used after it.
func (b *Broadcaster) Close() {
	for _, m := range []*Bus{b.primary, b.floor} {
		if m == nil {
			continue
		}
		if err := b.release(m); err != nil {
			xlog.Debug("Could not detach the subscriptions from a bus", "bus", m.Name, "error", err)
		}
		m.close()
	}
}

// OnReconnect registers cb to run after any bus the holder uses recovers a lost
// connection, and after publishes moved to another bus. Consumers re-read their
// state from tables on it, because a reconnecting subscriber misses messages and
// messages across two buses are not ordered. A nil cb is ignored.
func (b *Broadcaster) OnReconnect(cb func()) {
	if cb == nil {
		return
	}
	b.mu.Lock()
	b.reconnect = append(b.reconnect, cb)
	b.mu.Unlock()
}

// NotifyReconnect runs every registered hook once.
func (b *Broadcaster) NotifyReconnect() {
	b.mu.Lock()
	cbs := slices.Clone(b.reconnect)
	b.mu.Unlock()
	for _, cb := range cbs {
		cb()
	}
}

// hook asks a member to tell the holder about its reconnects. The caller holds
// b.mu or owns b exclusively. A member has one live hook while it is attached.
func (b *Broadcaster) hook(m *Bus) {
	if _, done := b.hooked[m]; done || m.OnReconnect == nil {
		return
	}
	live := &atomic.Bool{}
	live.Store(true)
	b.hooked[m] = live
	m.OnReconnect(func() {
		if live.Load() {
			b.NotifyReconnect()
		}
	})
}

func (b *Broadcaster) snapshotLocked() []*subscription {
	subs := make([]*subscription, 0, len(b.subs))
	for s := range b.subs {
		subs = append(subs, s)
	}
	return subs
}

func (b *Broadcaster) attached(m *Bus) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Contains(b.listening, m)
}

// subscription is one registered subscription and its attachment to each
// bus.
type subscription struct {
	owner   *Broadcaster
	subject string
	open    func(*Bus) (messaging.Subscription, error)

	mu     sync.Mutex
	closed bool
	inner  map[*Bus]messaging.Subscription
}

// attach subscribes on m. It drops the new subscription again when this one
// was closed, or m was released, while the bus was busy.
func (sub *subscription) attach(m *Bus) error {
	in, err := sub.open(m)
	if err != nil || in == nil {
		return err
	}
	sub.mu.Lock()
	defer sub.mu.Unlock()
	if sub.closed || !sub.owner.attached(m) {
		return in.Unsubscribe()
	}
	if _, dup := sub.inner[m]; dup {
		return in.Unsubscribe()
	}
	sub.inner[m] = in
	return nil
}

func (sub *subscription) detach(m *Bus) error {
	sub.mu.Lock()
	in, ok := sub.inner[m]
	delete(sub.inner, m)
	sub.mu.Unlock()
	if !ok {
		return nil
	}
	return in.Unsubscribe()
}

// Unsubscribe removes the subscription from every bus. It is safe to call
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
