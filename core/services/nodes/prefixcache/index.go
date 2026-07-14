package prefixcache

import (
	"sort"
	"sync"
	"time"

	"github.com/mudler/LocalAI/pkg/radixtree"
)

// Index is the guessed (routing-history) Provider backed by per-model radix
// trees keyed by ReplicaKey. Affinity is per replica, so the same prefix served
// by two replicas of one node resolves back to the exact replica that served it.
// Safe for concurrent use.
type Index struct {
	cfg   Config
	mu    sync.RWMutex
	trees map[string]*radixtree.Tree[ReplicaKey]
}

func NewIndex(cfg Config) *Index {
	return &Index{cfg: cfg, trees: map[string]*radixtree.Tree[ReplicaKey]{}}
}

// existingTree returns the tree for model without creating one. The bool
// reports whether a tree already existed.
func (ix *Index) existingTree(model string) (*radixtree.Tree[ReplicaKey], bool) {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	t, ok := ix.trees[model]
	return t, ok
}

func (ix *Index) tree(model string) *radixtree.Tree[ReplicaKey] {
	ix.mu.RLock()
	t, ok := ix.trees[model]
	ix.mu.RUnlock()
	if ok {
		return t
	}
	ix.mu.Lock()
	defer ix.mu.Unlock()
	if t, ok = ix.trees[model]; ok {
		return t
	}
	t = radixtree.New[ReplicaKey](radixtree.Options{TTL: ix.cfg.TTL, HalfLife: ix.cfg.HalfLife})
	ix.trees[model] = t
	return t
}

func (ix *Index) Decide(model string, chain []uint64, candidates []ReplicaKey, now time.Time) PrefixDecision {
	t := ix.tree(model)
	var d PrefixDecision
	// WeightsFor computes every candidate weight in a single tree walk and
	// returns a map pre-populated with an entry (weight 0 by default) for every
	// requested candidate. Candidacy is therefore exactly "is a key in weights",
	// so we derive the hot-match membership check from it rather than building a
	// second set.
	weights := t.WeightsFor(candidates, now)
	if len(chain) > 0 {
		if key, depth, ok := t.LongestMatch(chain, now); ok {
			// LongestMatch searches the whole tree, so the deepest match can be
			// a replica that is offline / unloaded / not in the candidate set.
			// Treating that as a hot match produces a false forced-disturb signal
			// upstream (the warm replica was absent, not load-saturated). Only honor
			// the match when the matched replica is an actual candidate; otherwise
			// fall back to cold placement.
			if _, ok := weights[key]; ok {
				d.Hot = key
				d.HasHot = true
				d.MatchRatio = float64(depth) / float64(len(chain))
			}
		}
	}
	// Cold order: candidates ascending by cacheWeight, tie-break by NodeID then
	// Replica. The sort comparator reads precomputed weights instead of triggering
	// an O(tree size) Weight call per comparison. With at most one candidate the
	// input order is already the cold order, so skip the sort.
	order := make([]ReplicaKey, len(candidates))
	copy(order, candidates)
	if len(order) > 1 {
		sort.Slice(order, func(i, j int) bool {
			if weights[order[i]] != weights[order[j]] {
				return weights[order[i]] < weights[order[j]]
			}
			return order[i].less(order[j])
		})
	}
	d.ColdOrder = order
	return d
}

func (ix *Index) Observe(model string, chain []uint64, key ReplicaKey, now time.Time) bool {
	if len(chain) == 0 || key.NodeID == "" {
		return false
	}
	t := ix.tree(model)
	// New/extended iff the current deepest match for this exact chain is not
	// already this replica at full depth.
	cur, depth, ok := t.LongestMatch(chain, now)
	t.Insert(chain, key, now)
	return !ok || depth < len(chain) || cur != key
}

// Invalidate drops all entries for ONE replica. It never interns an empty tree
// (a registry chokepoint fires Invalidate for every replica removal of every
// model, including round-robin models that never used the prefix cache, so
// lazily creating a tree here would grow the trees map unboundedly).
func (ix *Index) Invalidate(model string, key ReplicaKey) {
	if t, ok := ix.existingTree(model); ok {
		t.RemoveFunc(func(k ReplicaKey) bool { return k == key })
	}
}

// InvalidateNode drops entries for ALL replicas of nodeID. Like Invalidate it
// does not intern an empty tree.
func (ix *Index) InvalidateNode(model, nodeID string) {
	if t, ok := ix.existingTree(model); ok {
		t.RemoveFunc(func(k ReplicaKey) bool { return k.NodeID == nodeID })
	}
}

func (ix *Index) Evict(now time.Time) {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	for _, t := range ix.trees {
		t.Evict(now)
	}
}
