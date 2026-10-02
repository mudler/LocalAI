// SPDX-License-Identifier: MIT

package nodes

import (
	"sync"

	"github.com/mudler/xlog"
)

// DepartedNode identifies the node a departure is about.
//
// It carries the NAME as well as the ID because the per-node caches this
// notification exists to drop are not keyed alike: the prefix index and the
// probe cache key on the node's ID, and the staging tracker keys on the node
// NAME a transfer was attributed to. A payload carrying only one of them would
// leave the other subscriber to look the node up in the database from inside an
// eviction hook, which is a query on a path whose whole point is that it is
// cheap and edge triggered.
type DepartedNode struct {
	ID   string
	Name string
	Type string
}

// departureSubscriber is one registered eviction, with the name it is reported
// under. The name is not decoration: it is what a wiring spec asserts on and
// what names the offender when one of them panics, and a bare count could say
// only that some cache was forgotten, never which.
type departureSubscriber struct {
	name string
	fn   func(DepartedNode)
}

// DepartureNotifier is the ONE place a node's departure evicts per-node state.
//
// A departure is a routing fact: no live replica holds this node's tunnel and
// the departure has outlived the reconnect grace. It is NOT an absent
// connection (a tunnel lost inside the grace), NOT an unreachable peer and NOT
// the node's own answer, and this type is reachable from exactly one caller for
// that reason. A cache that wants to be dropped when a node goes registers
// here; it does not get to decide for itself what "gone" means, because
// deciding that in four places is how the four conditions collapse into two.
//
// EDGE TRIGGERED. The health monitor runs on a ticker and a departed node stays
// departed, so a level-triggered notifier would re-evict every cache every tick
// for as long as the node is away. Subscribers are invoked on the transition
// into departure and again only after the node has been seen present.
//
// Safe for concurrent use, and safe on a nil receiver: a monitor built with no
// notifier evicts nothing rather than panicking on the first departed node.
type DepartureNotifier struct {
	mu   sync.Mutex
	subs []departureSubscriber
	// departed is the edge. A node in it has already had its departure
	// announced; Present is what takes it back out. It is bounded by the number
	// of distinct nodes that have departed and not returned, which is the same
	// order as the fleet.
	departed map[string]struct{}
}

// NewDepartureNotifier returns a notifier with no subscribers.
func NewDepartureNotifier() *DepartureNotifier {
	return &DepartureNotifier{departed: map[string]struct{}{}}
}

// OnDeparture registers a subscriber under name. Every registered subscriber is
// invoked; registering one never displaces another, which is the bug
// NodeRegistry.AddReplicaRemovedHook's own comment records from its single-slot
// ancestor.
//
// Register at wiring time, before the health monitor starts.
func (n *DepartureNotifier) OnDeparture(name string, fn func(DepartedNode)) {
	if n == nil || fn == nil {
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	n.subs = append(n.subs, departureSubscriber{name: name, fn: fn})
}

// SubscriberNames returns the names of the registered subscribers, in
// registration order.
//
// Names and not a count, because what the wiring spec has to catch is a
// FORGOTTEN cache and a count can only say that one of four is missing. This is
// the whole symptom: a deployment whose departed nodes keep their per-node
// state does not fail, log, or slow down, it just answers with entries for a
// node that left.
func (n *DepartureNotifier) SubscriberNames() []string {
	if n == nil {
		return nil
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	names := make([]string, 0, len(n.subs))
	for _, s := range n.subs {
		names = append(names, s.name)
	}
	return names
}

// Departed records that node has departed and invokes every subscriber, once
// per departure. A second call for a node that has not been seen present since
// invokes nothing.
//
// Each subscriber is invoked with the lock released and its panic contained: an
// eviction is best effort, and a cache that panics must not strand the caches
// registered after it.
func (n *DepartureNotifier) Departed(node DepartedNode) {
	if n == nil || node.ID == "" {
		return
	}
	n.mu.Lock()
	if _, already := n.departed[node.ID]; already {
		n.mu.Unlock()
		return
	}
	n.departed[node.ID] = struct{}{}
	subs := make([]departureSubscriber, len(n.subs))
	copy(subs, n.subs)
	n.mu.Unlock()

	for _, s := range subs {
		invokeDepartureSubscriber(s, node)
	}
}

// invokeDepartureSubscriber runs one subscriber and contains its panic, naming
// the subscriber that failed. Extracted so the deferred recover is scoped to a
// single subscriber rather than to the whole loop, which is what makes the
// remaining subscribers run.
func invokeDepartureSubscriber(s departureSubscriber, node DepartedNode) {
	defer func() {
		if r := recover(); r != nil {
			xlog.Error("A departure subscriber panicked; the node's state it owns was not dropped",
				"subscriber", s.name, "node", node.Name, "nodeID", node.ID, "panic", r)
		}
	}()
	s.fn(node)
}

// Present records that nodeID has been seen present, arming the next departure.
// Called on every tick for a node that is not departed, so it is cheap and
// invokes nothing.
func (n *DepartureNotifier) Present(nodeID string) {
	if n == nil || nodeID == "" {
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	delete(n.departed, nodeID)
}
