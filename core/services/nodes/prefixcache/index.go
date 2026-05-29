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
	if len(chain) > 0 {
		if node, depth, ok := t.LongestMatch(chain, now); ok {
			d.HotNodeID = node
			d.MatchRatio = float64(depth) / float64(len(chain))
		}
	}
	// Cold order: candidates ascending by cacheWeight, tie-break by node id.
	cold := append([]string(nil), candidateNodeIDs...)
	sort.Slice(cold, func(i, j int) bool {
		wi, wj := t.Weight(cold[i], now), t.Weight(cold[j], now)
		if wi != wj {
			return wi < wj
		}
		return cold[i] < cold[j]
	})
	d.ColdOrder = cold
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
