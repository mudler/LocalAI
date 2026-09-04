package prefixcache

import (
	"fmt"
	"time"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/xlog"
)

// Sync wraps an Index, broadcasting new/extended observations to peers and
// applying peers' broadcasts. It is the cross-frontend coherence layer.
//
// It holds ONE carrier, and both directions use it: what this replica observes
// goes out on it, and what peers observe comes back in on it through
// SubscribeBroadcasts. Splitting those across two carriers would leave each
// frontend routing on nothing but its own history while every publish and every
// subscribe succeeded, so there is no way to spell that here.
type Sync struct {
	idx Provider
	// bus is messaging.Broadcaster, which is Publish plus Subscribe and nothing
	// more. This package is reached from the routing hot path and a spec drives
	// it with a two-method double; a wider parameter would hand the hot path
	// request/reply it must never make.
	bus messaging.Broadcaster
}

// NewSync wraps idx and puts its cross-frontend traffic on bus. A nil bus keeps
// the Sync local: it records and answers, and broadcasts nothing.
func NewSync(idx Provider, bus messaging.Broadcaster) *Sync { return &Sync{idx: idx, bus: bus} }

// SubscribeBroadcasts applies peers' observations and invalidations into the
// wrapped index. ApplyObserve and ApplyInvalidate update only the local index
// and never re-publish, so there is no broadcast loop.
//
// It takes no carrier argument on purpose. The carrier is the one this Sync
// publishes on, read from the same field, which is what makes "this replica
// hears what it would have said" a property of the type rather than of whoever
// wired it.
//
// Returns every subscription it opened; on a partial failure it releases what
// it already opened and returns nothing, so a caller cannot be left holding a
// half-subscribed Sync it believes is whole.
func (s *Sync) SubscribeBroadcasts() ([]messaging.Subscription, error) {
	if s.bus == nil {
		return nil, nil
	}
	var subs []messaging.Subscription
	release := func() {
		for _, sub := range subs {
			if err := sub.Unsubscribe(); err != nil {
				xlog.Warn("prefixcache: releasing a partial subscription", "error", err)
			}
		}
	}

	observeSub, err := messaging.SubscribeJSON(s.bus, messaging.SubjectPrefixCacheObserve, func(ev messaging.PrefixCacheObserveEvent) {
		s.ApplyObserve(ev, time.Now())
	})
	if err != nil {
		return nil, fmt.Errorf("prefixcache: subscribing to %s: %w", messaging.SubjectPrefixCacheObserve, err)
	}
	subs = append(subs, observeSub)

	invalidateSub, err := messaging.SubscribeJSON(s.bus, messaging.SubjectPrefixCacheInvalidate, func(ev messaging.PrefixCacheInvalidateEvent) {
		s.ApplyInvalidate(ev)
	})
	if err != nil {
		release()
		return nil, fmt.Errorf("prefixcache: subscribing to %s: %w", messaging.SubjectPrefixCacheInvalidate, err)
	}
	subs = append(subs, invalidateSub)
	return subs, nil
}

// Observe records locally and, if new/extended, broadcasts to peers. It returns
// whether the local index treated the assignment as new or extended, so Sync
// satisfies prefixcache.Provider.
//
// The LOCAL record happens first and is never conditional on the broadcast. A
// hint this replica could not share is still a fact this replica learned, and
// collapsing the two would cost the observing replica its own affinity for the
// very prompts it is already serving.
//
// The broadcast is an ordinary Publish, like every other family on the carrier.
// An observation is bounded by construction: ExtractChain caps a chain at
// Config.MaxDepth blocks, so the event a frontend publishes has a known worst
// case, and core/application refuses to start a deployment whose configured
// depth would push that worst case past what a notification can carry. Nothing
// here drops a message to stay under the cap: a drop would cost peers their
// affinity silently, and the startup refusal says so instead.
func (s *Sync) Observe(model string, chain []uint64, key ReplicaKey, now time.Time) bool {
	changed := s.idx.Observe(model, chain, key, now)
	if changed && s.bus != nil {
		ev := messaging.PrefixCacheObserveEvent{Model: model, Chain: chain, NodeID: key.NodeID, Replica: key.Replica}
		if err := s.bus.Publish(messaging.SubjectPrefixCacheObserve, ev); err != nil {
			xlog.Debug("prefixcache: observe publish failed", "error", err)
		}
	}
	return changed
}

// Invalidate drops the local entry for one replica and broadcasts to peers. The
// local drop is a no-op for models that were never cached (Index.Invalidate does
// not intern a tree). The broadcast is UNCONDITIONAL (when a carrier is
// configured): the registry chokepoint fires for every replica removal, and a
// peer frontend may hold a stale entry for the model even when THIS frontend
// never cached it, so gating the broadcast on local-tree existence would drop
// cross-frontend invalidations and leave peers routing to a removed replica
// until their TTL.
func (s *Sync) Invalidate(model string, key ReplicaKey) {
	s.idx.Invalidate(model, key)
	if s.bus != nil {
		ev := messaging.PrefixCacheInvalidateEvent{Model: model, NodeID: key.NodeID, Replica: key.Replica}
		if err := s.bus.Publish(messaging.SubjectPrefixCacheInvalidate, ev); err != nil {
			xlog.Debug("prefixcache: invalidate publish failed", "error", err)
		}
	}
}

// InvalidateNode drops the local entries for ALL replicas of node and broadcasts
// to peers. Like Invalidate the broadcast is unconditional for cross-frontend
// coherence. A negative Replica on the wire means "all replicas of the node".
func (s *Sync) InvalidateNode(model, node string) {
	s.idx.InvalidateNode(model, node)
	if s.bus != nil {
		ev := messaging.PrefixCacheInvalidateEvent{Model: model, NodeID: node, Replica: -1}
		if err := s.bus.Publish(messaging.SubjectPrefixCacheInvalidate, ev); err != nil {
			xlog.Debug("prefixcache: invalidate-node publish failed", "error", err)
		}
	}
}

// ApplyObserve applies a peer observe event locally (no re-broadcast).
func (s *Sync) ApplyObserve(ev messaging.PrefixCacheObserveEvent, now time.Time) {
	s.idx.Observe(ev.Model, ev.Chain, ReplicaKey{NodeID: ev.NodeID, Replica: ev.Replica}, now)
}

// ApplyInvalidate applies a peer invalidate event locally (no re-broadcast). A
// negative Replica targets all replicas of the node.
func (s *Sync) ApplyInvalidate(ev messaging.PrefixCacheInvalidateEvent) {
	if ev.Replica < 0 {
		s.idx.InvalidateNode(ev.Model, ev.NodeID)
		return
	}
	s.idx.Invalidate(ev.Model, ReplicaKey{NodeID: ev.NodeID, Replica: ev.Replica})
}

// Decide delegates to the wrapped index.
func (s *Sync) Decide(model string, chain []uint64, candidates []ReplicaKey, now time.Time) PrefixDecision {
	return s.idx.Decide(model, chain, candidates, now)
}

// Evict delegates eviction of expired entries to the wrapped index. It does not
// broadcast: each frontend evicts its own copy on its own TTL clock.
func (s *Sync) Evict(now time.Time) { s.idx.Evict(now) }
