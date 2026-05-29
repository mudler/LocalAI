package prefixcache_test

import (
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

var _ = Describe("Sync", func() {
	It("publishes an observe event when Observe is new", func() {
		idx := prefixcache.NewIndex(prefixcache.DefaultConfig())
		pub := &fakePub{}
		s := prefixcache.NewSync(idx, pub)
		s.Observe("m", []uint64{1, 2}, "A", t0) // first time -> publish
		Expect(pub.published).To(HaveLen(1))
		s.Observe("m", []uint64{1, 2}, "A", t0) // same -> no publish
		Expect(pub.published).To(HaveLen(1))
	})

	It("applies a peer observe event into the local index", func() {
		idx := prefixcache.NewIndex(prefixcache.DefaultConfig())
		s := prefixcache.NewSync(idx, &fakePub{})
		s.ApplyObserve(messaging.PrefixCacheObserveEvent{Model: "m", Chain: []uint64{1, 2}, NodeID: "A"}, t0)
		d := idx.Decide("m", []uint64{1, 2}, []string{"A"}, t0)
		Expect(d.HotNodeID).To(Equal("A"))
	})
})
