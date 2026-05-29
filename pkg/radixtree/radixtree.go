// Package radixtree implements a generic prefix tree over sequences of uint64
// key-elements, mapping the longest stored prefix of a query sequence to a
// value. Entries carry a TTL and the tree tracks a recency-weighted score per
// value. The clock is injected (callers pass `now`) so behavior is fully
// deterministic and testable. It has no external dependencies.
package radixtree

import (
	"sync"
	"time"
)

// Options configures a Tree.
type Options struct {
	// TTL is the idle lifetime of an entry. An entry whose lastSeen is older
	// than TTL (relative to the `now` passed in) is treated as absent and is
	// swept by Evict. Refreshed on every Insert that traverses it.
	TTL time.Duration
	// HalfLife controls recency weighting in Weight(). An entry contributes
	// 0.5^(age/HalfLife). Zero means "no decay" (every live entry counts 1).
	HalfLife time.Duration
	// MaxEntries bounds the number of value-bearing nodes. Zero means
	// unbounded. When exceeded, Insert evicts the least-recently-seen entry.
	MaxEntries int
}

// Tree is a prefix tree. V is the stored value type (for prefix-cache routing,
// a node identifier). Safe for concurrent use.
type Tree[V comparable] struct {
	mu   sync.Mutex
	opts Options
	root *node[V]
	size int
}

type node[V comparable] struct {
	children map[uint64]*node[V]
	value    V
	hasValue bool
	lastSeen time.Time
}

// New creates an empty Tree.
func New[V comparable](opts Options) *Tree[V] {
	return &Tree[V]{opts: opts, root: &node[V]{children: map[uint64]*node[V]{}}}
}

// LongestMatch returns the value at the deepest stored, non-expired prefix of
// key, the matched depth (number of key elements consumed), and ok.
func (t *Tree[V]) LongestMatch(key []uint64, now time.Time) (V, int, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	var best V
	bestDepth, found := 0, false
	cur := t.root
	for i, k := range key {
		next, ok := cur.children[k]
		if !ok {
			break
		}
		cur = next
		if cur.hasValue && !t.expired(cur, now) {
			best, bestDepth, found = cur.value, i+1, true
		}
	}
	return best, bestDepth, found
}

func (t *Tree[V]) expired(n *node[V], now time.Time) bool {
	return t.opts.TTL > 0 && now.Sub(n.lastSeen) > t.opts.TTL
}

// Insert records value at the node for key, refreshing lastSeen along the
// traversed path so active prefixes stay live. Re-inserting an existing key
// overwrites the value (last writer wins) and refreshes recency.
func (t *Tree[V]) Insert(key []uint64, value V, now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	cur := t.root
	for _, k := range key {
		next, ok := cur.children[k]
		if !ok {
			next = &node[V]{children: map[uint64]*node[V]{}}
			cur.children[k] = next
		}
		cur = next
		cur.lastSeen = now
	}
	if !cur.hasValue {
		t.size++
	}
	cur.value, cur.hasValue, cur.lastSeen = value, true, now
	if t.opts.MaxEntries > 0 && t.size > t.opts.MaxEntries {
		t.evictOldestLocked(now)
	}
}

// evictOldestLocked drops the single least-recently-seen value-bearing node.
// Called with t.mu held.
func (t *Tree[V]) evictOldestLocked(now time.Time) {
	var victim *node[V]
	var walk func(n *node[V])
	walk = func(n *node[V]) {
		if n.hasValue && (victim == nil || n.lastSeen.Before(victim.lastSeen)) {
			victim = n
		}
		for _, c := range n.children {
			walk(c)
		}
	}
	walk(t.root)
	if victim != nil {
		victim.hasValue = false
		var zero V
		victim.value = zero
		t.size--
	}
}
