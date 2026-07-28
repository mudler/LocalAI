// Package syncstate provides SyncedMap, a reusable cross-replica in-memory map.
//
// LocalAI in distributed mode runs multiple frontend replicas behind a
// round-robin load balancer. Several features keep process-local in-memory state
// that is surfaced to the HTTP/UI API; without cross-replica sync a poll that
// lands on a replica which did not originate a change sees stale or missing data.
// SyncedMap collapses the three legs each feature otherwise hand-wires - an
// in-memory map, a NATS broadcast/apply path, and optional durable read-through -
// into one well-tested component so cross-replica consistency is a configuration
// choice rather than a bespoke re-implementation.
package syncstate

import (
	"context"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/xlog"
)

// Op values carried on the wire and passed to OnApply.
const (
	opSet    = "set"
	opDelete = "delete"
)

// Store is optional durable backing for a SyncedMap. In distributed mode it is a
// single shared DB, so the apply path (a delta received from a peer) updates
// memory only and never re-writes the Store.
type Store[K comparable, V any] interface {
	List(ctx context.Context) ([]V, error)
	Upsert(ctx context.Context, v V) error
	Delete(ctx context.Context, k K) error
}

// Config configures a SyncedMap.
type Config[K comparable, V any] struct {
	Name      string                                 // subject namespace, e.g. "finetune.jobs"
	Key       func(V) K                              // extract the key from a value
	Nats      messaging.MessagingClient              // nil => standalone: in-memory only, no broadcast/subscribe
	Store     Store[K, V]                            // optional read-through persistence
	Loader    func(ctx context.Context) ([]V, error) // source when there is no Store (e.g. disk reload)
	OnApply   func(op string, k K, v V)              // optional hook after an applied change (e.g. ShutdownModel)
	Reconcile time.Duration                          // optional periodic re-hydrate; 0 = off
}

// delta is the JSON wire envelope broadcast on every local mutation. Value is
// omitempty so a delete carries only op+key.
type delta[K comparable, V any] struct {
	Op    string `json:"op"`
	Key   K      `json:"key"`
	Value V      `json:"value,omitempty"`
}

// SyncedMap is a cross-replica in-memory map. A local write (Set/Delete) updates
// memory, the optional durable Store, then broadcasts a delta to peers. A peer's
// delta updates memory only and fires OnApply - it never re-broadcasts and never
// writes the Store. That structural split is the echo-loop guard (same pattern as
// galleryop.mergeStatus / OpCache.applyStart): receiving your own broadcast just
// re-applies an idempotent value to memory, so there is no storm and no
// double-write.
type SyncedMap[K comparable, V any] struct {
	cfg Config[K, V]

	mu   sync.RWMutex
	data map[K]V

	sub Subscription

	// lifeCtx outlives Start's argument: a reconnect callback or reconcile tick
	// can fire long after Start returns, so they must not be tied to a ctx the
	// caller may cancel. Close cancels it.
	lifeCtx context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

// Subscription is the subset of messaging.Subscription the component holds onto.
type Subscription = messaging.Subscription

// New constructs a SyncedMap. Call Start to hydrate and begin syncing.
func New[K comparable, V any](cfg Config[K, V]) *SyncedMap[K, V] {
	return &SyncedMap[K, V]{cfg: cfg, data: make(map[K]V)}
}

func (m *SyncedMap[K, V]) subject() string {
	return messaging.SubjectSyncStateDelta(m.cfg.Name)
}

// Start hydrates from the source, subscribes for peer deltas, registers a
// reconnect re-hydrate (when the client supports it), and starts the optional
// reconcile ticker.
func (m *SyncedMap[K, V]) Start(ctx context.Context) error {
	if err := m.hydrate(ctx); err != nil {
		return err
	}

	// The cancel func is stored on the struct and invoked in Close (covered by
	// tests); lifeCtx must outlive Start to drive the reconnect/reconcile
	// goroutines, so it cannot be cancelled or deferred within this scope.
	m.lifeCtx, m.cancel = context.WithCancel(context.Background()) // #nosec G118 -- cancel is invoked in Close()

	if m.cfg.Nats != nil {
		sub, err := messaging.SubscribeJSON(m.cfg.Nats, m.subject(), m.apply)
		if err != nil {
			return err
		}
		m.sub = sub

		// nats.go transparently resubscribes on reconnect, but it cannot know we
		// kept derived in-memory state that may have drifted while the link was
		// down, so re-hydrate from the durable source. Detected via an optional
		// interface so MessagingClient itself stays minimal; standalone/test
		// clients without the method simply fall back to the reconcile ticker.
		if r, ok := m.cfg.Nats.(interface{ OnReconnect(func()) }); ok {
			r.OnReconnect(func() {
				if err := m.hydrate(m.lifeCtx); err != nil {
					xlog.Warn("syncstate: reconnect re-hydrate failed", "name", m.cfg.Name, "error", err)
				}
			})
		}
	}

	if m.cfg.Reconcile > 0 {
		m.wg.Add(1)
		go m.reconcileLoop()
	}
	return nil
}

// Close unsubscribes and stops the reconcile ticker.
func (m *SyncedMap[K, V]) Close() error {
	if m.cancel != nil {
		m.cancel()
	}
	m.wg.Wait()
	if m.sub != nil {
		return m.sub.Unsubscribe()
	}
	return nil
}

// Set updates the value locally, writes through the Store, then broadcasts.
// Per the data-flow contract the Store write happens under the lock so memory and
// durable state move together; the broadcast is best-effort after unlocking.
func (m *SyncedMap[K, V]) Set(ctx context.Context, v V) error {
	k := m.cfg.Key(v)
	m.mu.Lock()
	m.data[k] = v
	if m.cfg.Store != nil {
		if err := m.cfg.Store.Upsert(ctx, v); err != nil {
			m.mu.Unlock()
			return err
		}
	}
	m.mu.Unlock()
	m.publish(opSet, k, v)
	return nil
}

// Delete removes the key locally, deletes it from the Store, then broadcasts.
func (m *SyncedMap[K, V]) Delete(ctx context.Context, k K) error {
	m.mu.Lock()
	delete(m.data, k)
	if m.cfg.Store != nil {
		if err := m.cfg.Store.Delete(ctx, k); err != nil {
			m.mu.Unlock()
			return err
		}
	}
	m.mu.Unlock()
	var zero V
	m.publish(opDelete, k, zero)
	return nil
}

// Get returns the value for k and whether it was present.
func (m *SyncedMap[K, V]) Get(k K) (V, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	v, ok := m.data[k]
	return v, ok
}

// List returns a snapshot slice of all values.
func (m *SyncedMap[K, V]) List() []V {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]V, 0, len(m.data))
	for _, v := range m.data {
		out = append(out, v)
	}
	return out
}

// Snapshot returns a copy of the underlying map.
func (m *SyncedMap[K, V]) Snapshot() map[K]V {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[K]V, len(m.data))
	for k, v := range m.data {
		out[k] = v
	}
	return out
}

// publish broadcasts a delta. Standalone (nil Nats) is a strict no-op.
func (m *SyncedMap[K, V]) publish(op string, k K, v V) {
	if m.cfg.Nats == nil {
		return
	}
	if err := m.cfg.Nats.Publish(m.subject(), delta[K, V]{Op: op, Key: k, Value: v}); err != nil {
		xlog.Warn("syncstate: failed to broadcast delta", "name", m.cfg.Name, "op", op, "error", err)
	}
}

// apply handles a peer's delta: memory-only update plus OnApply. It deliberately
// never writes the Store nor re-publishes - that is the echo-loop guard.
func (m *SyncedMap[K, V]) apply(d delta[K, V]) {
	switch d.Op {
	case opSet:
		m.mu.Lock()
		m.data[d.Key] = d.Value
		m.mu.Unlock()
	case opDelete:
		m.mu.Lock()
		delete(m.data, d.Key)
		m.mu.Unlock()
	default:
		xlog.Warn("syncstate: ignoring delta with unknown op", "name", m.cfg.Name, "op", d.Op)
		return
	}
	if m.cfg.OnApply != nil {
		m.cfg.OnApply(d.Op, d.Key, d.Value)
	}
}

// hydrate replaces the whole map from the durable source: Store if present, else
// Loader. With neither, a late joiner starts empty and catches up via deltas
// (acceptable only for ephemeral state).
func (m *SyncedMap[K, V]) hydrate(ctx context.Context) error {
	var (
		vals []V
		err  error
	)
	switch {
	case m.cfg.Store != nil:
		vals, err = m.cfg.Store.List(ctx)
	case m.cfg.Loader != nil:
		vals, err = m.cfg.Loader(ctx)
	default:
		return nil
	}
	if err != nil {
		return err
	}
	m.replaceAll(vals)
	return nil
}

// replaceAll atomically swaps the map contents for the given values, keyed via
// cfg.Key.
func (m *SyncedMap[K, V]) replaceAll(vals []V) {
	next := make(map[K]V, len(vals))
	for _, v := range vals {
		next[m.cfg.Key(v)] = v
	}
	m.mu.Lock()
	m.data = next
	m.mu.Unlock()
}

// reconcileLoop periodically re-hydrates to repair silent drift (missed deltas).
func (m *SyncedMap[K, V]) reconcileLoop() {
	defer m.wg.Done()
	t := time.NewTicker(m.cfg.Reconcile)
	defer t.Stop()
	for {
		select {
		case <-m.lifeCtx.Done():
			return
		case <-t.C:
			if err := m.hydrate(m.lifeCtx); err != nil {
				xlog.Warn("syncstate: reconcile re-hydrate failed", "name", m.cfg.Name, "error", err)
			}
		}
	}
}
