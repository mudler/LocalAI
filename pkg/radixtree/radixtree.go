// Package radixtree implements a generic prefix tree over sequences of uint64
// key-elements, mapping the longest stored prefix of a query sequence to a
// value. Entries carry a TTL and the tree tracks a recency-weighted score per
// value. The clock is injected (callers pass `now`) so behavior is fully
// deterministic and testable. It has no external dependencies.
package radixtree

import (
	"math"
	"sync"
	"time"
)

// Options configures a Tree.
type Options struct {
	// TTL is the idle lifetime of an entry. An entry whose lastSeen is older
	// than TTL (relative to the `now` passed in) is treated as absent and is
	// swept by Evict. Refreshed on every Insert that traverses it. The boundary
	// is strict greater-than: an entry whose age is exactly equal to TTL is
	// still live; it expires only once age exceeds TTL.
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
	mu   sync.RWMutex
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
	t.mu.RLock()
	defer t.mu.RUnlock()
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

// expired reports whether n's lastSeen is older than the configured TTL. The
// comparison is strict greater-than: an entry whose age equals TTL exactly is
// still considered live. With TTL == 0 (unbounded) nothing ever expires.
func (t *Tree[V]) expired(n *node[V], now time.Time) bool {
	return t.opts.TTL > 0 && now.Sub(n.lastSeen) > t.opts.TTL
}

// Insert records value at EVERY node along the key chain, not just the leaf,
// so each prefix-block node remembers the value (node id) that served that
// prefix. This is what makes LongestMatch find a shared prefix even when the
// query tail diverges (SGLang/vLLM-style prefix matching). Re-inserting a
// different value over a shared prefix node overwrites it: the last writer
// owns the shared prefix node (a recency heuristic, and the correct one - the
// most recent chain that traversed that block is the one most likely warm).
// lastSeen is refreshed on every traversed node so active prefixes stay live.
// Inserting an empty key is a no-op: the root never holds a value.
func (t *Tree[V]) Insert(key []uint64, value V, now time.Time) {
	if len(key) == 0 {
		return
	}
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
		if !cur.hasValue {
			t.size++
		}
		cur.value, cur.hasValue, cur.lastSeen = value, true, now
	}
	if t.opts.MaxEntries > 0 && t.size > t.opts.MaxEntries {
		t.evictOldestLocked(now)
	}
}

// evictOldestLocked drops the single least-recently-seen value-bearing node and
// prunes any empty branches the removal leaves behind. Called with t.mu held.
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
		// Clear the victim's value and reclaim it plus any ancestors that are
		// now both value-less and childless.
		t.pruneWalk(t.root, func(n *node[V]) bool { return n == victim })
	}
}

// pruneWalk clears the value of every node for which shouldClear returns true,
// then removes the now empty (value-less and childless) branches that result.
// It keeps t.size accurate by decrementing once per cleared node. Returns true
// if n itself should be removed from its parent. Called with t.mu held.
func (t *Tree[V]) pruneWalk(n *node[V], shouldClear func(*node[V]) bool) bool {
	for k, c := range n.children {
		if t.pruneWalk(c, shouldClear) {
			delete(n.children, k)
		}
	}
	if n.hasValue && shouldClear(n) {
		n.hasValue = false
		var zero V
		n.value = zero
		t.size--
	}
	return n != t.root && !n.hasValue && len(n.children) == 0
}

// Len returns the number of live (value-bearing) entries, including not-yet-
// swept expired ones. Use after Evict for the post-sweep count.
func (t *Tree[V]) Len() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.size
}

// Evict removes expired value-bearing nodes and prunes resulting empty
// branches. O(n) in tree size; call periodically from a background sweeper.
func (t *Tree[V]) Evict(now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pruneWalk(t.root, func(n *node[V]) bool { return t.expired(n, now) })
}

// contribution returns the recency-weighted score a single live, non-expired
// node adds to its value's weight: 1.0 when HalfLife<=0 (a plain count), else
// 0.5^(age/HalfLife). It does not check hasValue or expiry; callers must filter
// those first. Shared by Weight and WeightsFor so the metric stays identical.
func (t *Tree[V]) contribution(n *node[V], now time.Time) float64 {
	if t.opts.HalfLife <= 0 {
		return 1
	}
	age := now.Sub(n.lastSeen).Seconds()
	return math.Pow(0.5, age/t.opts.HalfLife.Seconds())
}

// Weight returns the recency-weighted count of live entries anchored to value:
// sum over non-expired entries of 0.5^(age/HalfLife). With HalfLife==0 every
// live entry contributes 1.0 (a plain count). This is the "valuable warm cache"
// proxy used for cold placement and autoscale.
func (t *Tree[V]) Weight(value V, now time.Time) float64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	var sum float64
	var walk func(n *node[V])
	walk = func(n *node[V]) {
		if n.hasValue && n.value == value && !t.expired(n, now) {
			sum += t.contribution(n, now)
		}
		for _, c := range n.children {
			walk(c)
		}
	}
	walk(t.root)
	return sum
}

// WeightsFor returns the recency-weighted weight (same metric as Weight) for
// each value in values, computed in a single tree traversal. Values not present
// in the tree map to 0. This is O(N + len(values)) versus calling Weight once
// per value (O(len(values) * N)). Concurrency-safe (read lock).
func (t *Tree[V]) WeightsFor(values []V, now time.Time) map[V]float64 {
	want := make(map[V]struct{}, len(values))
	result := make(map[V]float64, len(values))
	for _, v := range values {
		want[v] = struct{}{}
		result[v] = 0
	}
	if len(want) == 0 {
		return result
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	var walk func(n *node[V])
	walk = func(n *node[V]) {
		if n.hasValue && !t.expired(n, now) {
			if _, ok := want[n.value]; ok {
				result[n.value] += t.contribution(n, now)
			}
		}
		for _, c := range n.children {
			walk(c)
		}
	}
	walk(t.root)
	return result
}

// Remove drops every entry whose value equals value, then prunes empty
// branches. Used when a replica is unloaded or its node goes offline so the
// tree never points at a node that no longer holds the model. It is the
// equality special case of RemoveFunc.
func (t *Tree[V]) Remove(value V) {
	t.RemoveFunc(func(v V) bool { return v == value })
}

// RemoveFunc drops every entry whose value satisfies pred, then prunes empty
// branches. Generalizes Remove (Remove(v) == RemoveFunc(func(x V) bool { return
// x == v })). Used to drop, in one walk, every entry that belongs to a class of
// values (for example all replicas of a single node).
func (t *Tree[V]) RemoveFunc(pred func(V) bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pruneWalk(t.root, func(n *node[V]) bool { return pred(n.value) })
}
