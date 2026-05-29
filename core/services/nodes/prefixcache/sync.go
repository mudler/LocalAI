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

// Invalidate drops the local entry and broadcasts to peers. The local drop is a
// no-op for models that were never cached (Index.Invalidate does not intern a
// tree). The broadcast is UNCONDITIONAL (when a publisher is configured): the
// registry chokepoint fires for every replica removal, and a peer frontend may
// hold a stale entry for the model even when THIS frontend never cached it, so
// gating the broadcast on local-tree existence would drop cross-frontend
// invalidations and leave peers routing to a removed replica until their TTL.
func (s *Sync) Invalidate(model, nodeID string) {
	s.idx.Invalidate(model, nodeID)
	if s.pub != nil {
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
