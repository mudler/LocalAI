package prefixcache

import (
	"sort"
	"sync"
	"time"

	"github.com/mudler/LocalAI/pkg/radixtree"
)

// Index is the guessed (routing-history) Provider backed by per-model radix
// trees. Safe for concurrent use.
type Index struct {
	cfg   Config
	mu    sync.RWMutex
	trees map[string]*radixtree.Tree[string]
}

func NewIndex(cfg Config) *Index {
	return &Index{cfg: cfg, trees: map[string]*radixtree.Tree[string]{}}
}

func (ix *Index) tree(model string) *radixtree.Tree[string] {
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
	t = radixtree.New[string](radixtree.Options{TTL: ix.cfg.TTL, HalfLife: ix.cfg.HalfLife})
	ix.trees[model] = t
	return t
}

func (ix *Index) Decide(model string, chain []uint64, candidateNodeIDs []string, now time.Time) PrefixDecision {
	t := ix.tree(model)
	var d PrefixDecision
	// Build the candidate set once: used both to validate the hot match and to
	// weigh cold candidates.
	candidates := make(map[string]struct{}, len(candidateNodeIDs))
	for _, id := range candidateNodeIDs {
		candidates[id] = struct{}{}
	}
	if len(chain) > 0 {
		if node, depth, ok := t.LongestMatch(chain, now); ok {
			// LongestMatch searches the whole tree, so the deepest match can be
			// a node that is offline / unloaded / not in the candidate set.
			// Treating that as a hot match produces a false forced-disturb signal
			// upstream (the warm node was absent, not load-saturated). Only honor
			// the match when the matched node is an actual candidate; otherwise
			// fall back to cold placement. A future refinement could ask the tree
			// for the longest match restricted to the candidate nodes, yielding a
			// shallower-but-valid match instead of dropping it entirely.
			if _, ok := candidates[node]; ok {
				d.HotNodeID = node
				d.MatchRatio = float64(depth) / float64(len(chain))
			}
		}
	}
	// Cold order: candidates ascending by cacheWeight, tie-break by node id.
	// WeightsFor computes every candidate weight in a single tree walk, so the
	// sort comparator reads precomputed weights instead of triggering an O(tree
	// size) Weight call per comparison.
	weights := t.WeightsFor(candidateNodeIDs, now)
	order := make([]string, len(candidateNodeIDs))
	copy(order, candidateNodeIDs)
	sort.Slice(order, func(i, j int) bool {
		if weights[order[i]] != weights[order[j]] {
			return weights[order[i]] < weights[order[j]]
		}
		return order[i] < order[j]
	})
	d.ColdOrder = order
	return d
}

func (ix *Index) Observe(model string, chain []uint64, nodeID string, now time.Time) bool {
	if len(chain) == 0 || nodeID == "" {
		return false
	}
	t := ix.tree(model)
	// New/extended iff the current deepest match for this exact chain is not
	// already this node at full depth.
	node, depth, ok := t.LongestMatch(chain, now)
	t.Insert(chain, nodeID, now)
	return !ok || depth < len(chain) || node != nodeID
}

func (ix *Index) Invalidate(model, nodeID string) {
	ix.tree(model).Remove(nodeID)
}

func (ix *Index) Evict(now time.Time) {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	for _, t := range ix.trees {
		t.Evict(now)
	}
}
