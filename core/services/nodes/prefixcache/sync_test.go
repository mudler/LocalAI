package prefixcache_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/nodes/prefixcache"
)

type fakePub struct{ published []any }

func (f *fakePub) Publish(subject string, v any) error {
	f.published = append(f.published, v)
	return nil
}

// Sync must satisfy the Provider seam so SmartRouter can hold a single
// prefixcache.Provider that broadcasts via NATS.
var _ prefixcache.Provider = (*prefixcache.Sync)(nil)

var _ = Describe("Sync", func() {
	It("delegates Evict to the wrapped index", func() {
		cfg := prefixcache.DefaultConfig()
		cfg.TTL = time.Minute
		idx := prefixcache.NewIndex(cfg)
		s := prefixcache.NewSync(idx, &fakePub{})
		s.Observe("m", []uint64{1, 2}, "A", t0)
		// Before TTL: still hot.
		Expect(idx.Decide("m", []uint64{1, 2}, []string{"A"}, t0).HotNodeID).To(Equal("A"))
		// After TTL via Sync.Evict: entry is swept.
		s.Evict(t0.Add(2 * time.Minute))
		Expect(idx.Decide("m", []uint64{1, 2}, []string{"A"}, t0.Add(2*time.Minute)).HotNodeID).To(BeEmpty())
	})

	It("publishes an observe event when Observe is new", func() {
		idx := prefixcache.NewIndex(prefixcache.DefaultConfig())
		pub := &fakePub{}
		s := prefixcache.NewSync(idx, pub)
		s.Observe("m", []uint64{1, 2}, "A", t0) // first time -> publish
		Expect(pub.published).To(HaveLen(1))
		s.Observe("m", []uint64{1, 2}, "A", t0) // same -> no publish
		Expect(pub.published).To(HaveLen(1))
	})

	It("does not publish an invalidate for a model with no prefix-cache tree", func() {
		idx := prefixcache.NewIndex(prefixcache.DefaultConfig())
		pub := &fakePub{}
		s := prefixcache.NewSync(idx, pub)
		// Round-robin model that never used the cache: removing a replica must
		// neither intern a tree nor broadcast an invalidate.
		s.Invalidate("never-cached", "A")
		Expect(pub.published).To(BeEmpty())
		Expect(idx.TreeCountForTest()).To(Equal(0))
	})

	It("publishes an invalidate only after an Observe created the tree", func() {
		idx := prefixcache.NewIndex(prefixcache.DefaultConfig())
		pub := &fakePub{}
		s := prefixcache.NewSync(idx, pub)
		s.Observe("m", []uint64{1, 2}, "A", t0) // creates the tree (also publishes observe)
		pub.published = nil
		s.Invalidate("m", "A") // tree exists -> publish
		Expect(pub.published).To(HaveLen(1))
		Expect(pub.published[0]).To(BeAssignableToTypeOf(messaging.PrefixCacheInvalidateEvent{}))
	})

	It("applies a peer observe event into the local index", func() {
		idx := prefixcache.NewIndex(prefixcache.DefaultConfig())
		s := prefixcache.NewSync(idx, &fakePub{})
		s.ApplyObserve(messaging.PrefixCacheObserveEvent{Model: "m", Chain: []uint64{1, 2}, NodeID: "A"}, t0)
		d := idx.Decide("m", []uint64{1, 2}, []string{"A"}, t0)
		Expect(d.HotNodeID).To(Equal("A"))
	})
})
