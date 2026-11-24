package prefixcache

import (
	"sort"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/messaging"
)

// ReportedIndex is an exact-residency Provider populated only by backend
// events. Request routing observations intentionally do not mutate it.
type ReportedIndex struct {
	mu          sync.RWMutex
	residencies map[string]map[ReplicaKey][][]uint64
}

func NewReportedIndex() *ReportedIndex {
	return &ReportedIndex{residencies: map[string]map[ReplicaKey][][]uint64{}}
}

func (ix *ReportedIndex) Apply(event messaging.PrefixCacheResidencyEvent) {
	key := ReplicaKey{NodeID: event.NodeID, Replica: event.Replica}
	if event.Model == "" || key.NodeID == "" {
		return
	}

	ix.mu.Lock()
	defer ix.mu.Unlock()
	byReplica := ix.residencies[event.Model]
	switch event.Operation {
	case messaging.PrefixCacheStore:
		if len(event.Chain) == 0 {
			return
		}
		if byReplica == nil {
			byReplica = map[ReplicaKey][][]uint64{}
			ix.residencies[event.Model] = byReplica
		}
		for _, chain := range byReplica[key] {
			if equalChain(chain, event.Chain) {
				return
			}
		}
		byReplica[key] = append(byReplica[key], append([]uint64(nil), event.Chain...))
	case messaging.PrefixCacheRemove:
		if byReplica == nil || len(event.Chain) == 0 {
			return
		}
		chains := byReplica[key]
		for i, chain := range chains {
			if equalChain(chain, event.Chain) {
				chains = append(chains[:i], chains[i+1:]...)
				break
			}
		}
		if len(chains) == 0 {
			delete(byReplica, key)
		} else {
			byReplica[key] = chains
		}
	case messaging.PrefixCacheClear:
		delete(byReplica, key)
	}
}

func (ix *ReportedIndex) Decide(model string, chain []uint64, candidates []ReplicaKey, _ time.Time) PrefixDecision {
	order := append([]ReplicaKey(nil), candidates...)
	sort.Slice(order, func(i, j int) bool { return order[i].less(order[j]) })
	d := PrefixDecision{ColdOrder: order}
	if len(chain) == 0 {
		return d
	}

	ix.mu.RLock()
	defer ix.mu.RUnlock()
	byReplica := ix.residencies[model]
	bestDepth := 0
	for _, key := range order {
		for _, reported := range byReplica[key] {
			depth := commonDepth(chain, reported)
			if depth > bestDepth {
				bestDepth = depth
				d.Hot = key
				d.HasHot = true
			}
		}
	}
	if d.HasHot {
		d.MatchRatio = float64(bestDepth) / float64(len(chain))
	}
	return d
}

func (ix *ReportedIndex) Observe(string, []uint64, ReplicaKey, time.Time) bool { return false }

func (ix *ReportedIndex) Invalidate(model string, key ReplicaKey) {
	ix.Apply(messaging.PrefixCacheResidencyEvent{Operation: messaging.PrefixCacheClear, Model: model, NodeID: key.NodeID, Replica: key.Replica})
}

func (ix *ReportedIndex) InvalidateNode(model, nodeID string) {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	for key := range ix.residencies[model] {
		if key.NodeID == nodeID {
			delete(ix.residencies[model], key)
		}
	}
}

func (ix *ReportedIndex) Evict(time.Time) {}

func equalChain(a, b []uint64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func commonDepth(a, b []uint64) int {
	depth := min(len(a), len(b))
	for i := range depth {
		if a[i] != b[i] {
			return i
		}
	}
	return depth
}
