package router

import (
	"sort"
	"strings"
	"sync"
)

// labelSetCache memoises classifier output (a sorted active-label set)
// keyed on the case-folded, whitespace-trimmed prompt. Both Score and
// Rerank classifiers embed one.
//
// Eviction is naive (drop one arbitrary entry on overflow); the cache
// is a hot-prompt amortiser, not a long-tail store, so LRU semantics
// aren't worth the extra bookkeeping. Cap=0 disables the cache.
type labelSetCache struct {
	mu    sync.RWMutex
	store map[string][]string
	cap   int
}

func newLabelSetCache(size int) *labelSetCache {
	if size < 0 {
		size = 0
	}
	return &labelSetCache{store: make(map[string][]string, size), cap: size}
}

// cacheKey normalises a prompt for cache equality. Callers can compute
// it once at the top of Classify and pass it to both get and put to
// save the second TrimSpace+ToLower allocation on a miss.
func cacheKey(prompt string) string {
	return strings.ToLower(strings.TrimSpace(prompt))
}

func (c *labelSetCache) get(key string) ([]string, bool) {
	if c.cap == 0 {
		return nil, false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	v, ok := c.store[key]
	return v, ok
}

func (c *labelSetCache) put(key string, labels []string) {
	if c.cap == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.store) >= c.cap {
		for k := range c.store {
			delete(c.store, k)
			break
		}
	}
	// Defensive copy + sort: cached label sets must be stable so
	// callers can't mutate via aliasing, and equality comparisons
	// in tests don't depend on insertion order.
	cp := make([]string, len(labels))
	copy(cp, labels)
	sort.Strings(cp)
	c.store[key] = cp
}

func (c *labelSetCache) len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.store)
}

// selectActive picks the labels whose corresponding score clears
// threshold, plus the index of the argmax. If no label clears the
// threshold the caller falls back to the argmax — both classifiers
// guarantee a non-empty active set so the surrounding middleware
// always has something to route on. Returns nil active when labels
// is empty.
func selectActive(scores []float64, labels []string, threshold float64) (active []string, bestIdx int) {
	if len(labels) == 0 {
		return nil, 0
	}
	active = make([]string, 0, 2)
	for i, s := range scores {
		if s > scores[bestIdx] {
			bestIdx = i
		}
		if s >= threshold {
			active = append(active, labels[i])
		}
	}
	if len(active) == 0 {
		active = []string{labels[bestIdx]}
	}
	return active, bestIdx
}
