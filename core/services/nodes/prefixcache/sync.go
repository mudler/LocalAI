package prefixcache

import (
	"time"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/xlog"
)

// publisher is the minimal slice of messaging.Client that Sync needs.
type publisher interface {
	Publish(subject string, v any) error
}

// Sync wraps an Index, broadcasting new/extended observations to peers and
// applying peers' broadcasts. It is the cross-frontend coherence layer.
type Sync struct {
	idx Provider
	pub publisher
}

func NewSync(idx Provider, pub publisher) *Sync { return &Sync{idx: idx, pub: pub} }

// Observe records locally and, if new/extended, broadcasts to peers. It returns
// whether the local index treated the assignment as new or extended, so Sync
// satisfies prefixcache.Provider.
func (s *Sync) Observe(model string, chain []uint64, nodeID string, now time.Time) bool {
	changed := s.idx.Observe(model, chain, nodeID, now)
	if changed && s.pub != nil {
		ev := messaging.PrefixCacheObserveEvent{Model: model, Chain: chain, NodeID: nodeID}
		if err := s.pub.Publish(messaging.SubjectPrefixCacheObserve, ev); err != nil {
			xlog.Debug("prefixcache: observe publish failed", "error", err)
		}
	}
	return changed
}

// invalidator is the optional capability, satisfied by *Index, that reports
// whether an invalidation could have dropped anything (i.e. a tree for the
// model existed). Sync uses it to avoid broadcasting empty invalidations: the
// registry chokepoint fires Invalidate for every replica removal of every
// model, so most calls target models that never used the prefix cache.
type invalidator interface {
	invalidateExisting(model, nodeID string) bool
}

// Invalidate drops locally and broadcasts only when there was something to
// drop. When the wrapped Provider exposes the invalidator capability we gate
// the NATS broadcast on its result; otherwise we fall back to the prior
// always-invalidate, always-broadcast behavior.
func (s *Sync) Invalidate(model, nodeID string) {
	hadTree := true
	if inv, ok := s.idx.(invalidator); ok {
		hadTree = inv.invalidateExisting(model, nodeID)
	} else {
		s.idx.Invalidate(model, nodeID)
	}
	if hadTree && s.pub != nil {
		ev := messaging.PrefixCacheInvalidateEvent{Model: model, NodeID: nodeID}
		if err := s.pub.Publish(messaging.SubjectPrefixCacheInvalidate, ev); err != nil {
			xlog.Debug("prefixcache: invalidate publish failed", "error", err)
		}
	}
}

// ApplyObserve applies a peer observe event locally (no re-broadcast).
func (s *Sync) ApplyObserve(ev messaging.PrefixCacheObserveEvent, now time.Time) {
	s.idx.Observe(ev.Model, ev.Chain, ev.NodeID, now)
}

// ApplyInvalidate applies a peer invalidate event locally (no re-broadcast).
func (s *Sync) ApplyInvalidate(ev messaging.PrefixCacheInvalidateEvent) {
	s.idx.Invalidate(ev.Model, ev.NodeID)
}

// Decide delegates to the wrapped index.
func (s *Sync) Decide(model string, chain []uint64, candidateNodeIDs []string, now time.Time) PrefixDecision {
	return s.idx.Decide(model, chain, candidateNodeIDs, now)
}

// Evict delegates eviction of expired entries to the wrapped index. It does not
// broadcast: each frontend evicts its own copy on its own TTL clock.
func (s *Sync) Evict(now time.Time) { s.idx.Evict(now) }
