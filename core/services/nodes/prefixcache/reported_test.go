package prefixcache_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/nodes/prefixcache"
)

var _ prefixcache.Provider = (*prefixcache.ReportedIndex)(nil)

var _ = Describe("ReportedIndex", func() {
	var idx *prefixcache.ReportedIndex

	BeforeEach(func() { idx = prefixcache.NewReportedIndex() })

	It("routes to the replica with the longest reported prefix", func() {
		idx.Apply(messaging.PrefixCacheResidencyEvent{Operation: messaging.PrefixCacheStore, Model: "m", NodeID: "A", Replica: 0, Chain: []uint64{1, 2}})
		idx.Apply(messaging.PrefixCacheResidencyEvent{Operation: messaging.PrefixCacheStore, Model: "m", NodeID: "B", Replica: 0, Chain: []uint64{1, 2, 3, 4}})
		d := idx.Decide("m", []uint64{1, 2, 3, 9}, []prefixcache.ReplicaKey{rk("A", 0), rk("B", 0)}, t0)
		Expect(d.HasHot).To(BeTrue())
		Expect(d.Hot).To(Equal(rk("B", 0)))
		Expect(d.MatchRatio).To(Equal(0.75))
	})

	It("removes only the announced residency", func() {
		for _, chain := range [][]uint64{{1, 2}, {7, 8}} {
			idx.Apply(messaging.PrefixCacheResidencyEvent{Operation: messaging.PrefixCacheStore, Model: "m", NodeID: "A", Replica: 0, Chain: chain})
		}
		idx.Apply(messaging.PrefixCacheResidencyEvent{Operation: messaging.PrefixCacheRemove, Model: "m", NodeID: "A", Replica: 0, Chain: []uint64{1, 2}})
		Expect(idx.Decide("m", []uint64{1, 2}, []prefixcache.ReplicaKey{rk("A", 0)}, t0).HasHot).To(BeFalse())
		Expect(idx.Decide("m", []uint64{7, 8}, []prefixcache.ReplicaKey{rk("A", 0)}, t0).HasHot).To(BeTrue())
	})

	It("clears every residency for one replica only", func() {
		idx.Apply(messaging.PrefixCacheResidencyEvent{Operation: messaging.PrefixCacheStore, Model: "m", NodeID: "A", Replica: 0, Chain: []uint64{1, 2}})
		idx.Apply(messaging.PrefixCacheResidencyEvent{Operation: messaging.PrefixCacheStore, Model: "m", NodeID: "B", Replica: 0, Chain: []uint64{3, 4}})
		idx.Apply(messaging.PrefixCacheResidencyEvent{Operation: messaging.PrefixCacheClear, Model: "m", NodeID: "A", Replica: 0})
		candidates := []prefixcache.ReplicaKey{rk("A", 0), rk("B", 0)}
		Expect(idx.Decide("m", []uint64{1, 2}, candidates, t0).HasHot).To(BeFalse())
		Expect(idx.Decide("m", []uint64{3, 4}, candidates, t0).Hot).To(Equal(rk("B", 0)))
	})

	It("ignores guessed request observations and keeps cold ordering deterministic", func() {
		Expect(idx.Observe("m", []uint64{1, 2}, rk("A", 0), t0)).To(BeFalse())
		d := idx.Decide("m", []uint64{1, 2}, []prefixcache.ReplicaKey{rk("B", 1), rk("A", 1), rk("A", 0)}, time.Now())
		Expect(d.HasHot).To(BeFalse())
		Expect(d.ColdOrder).To(Equal([]prefixcache.ReplicaKey{rk("A", 0), rk("A", 1), rk("B", 1)}))
	})
})
