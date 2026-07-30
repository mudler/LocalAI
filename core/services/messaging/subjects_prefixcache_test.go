package messaging_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/messaging"
)

var _ = Describe("PrefixCache subjects", func() {
	It("exposes stable subject constants", func() {
		Expect(messaging.SubjectPrefixCacheObserve).To(Equal("prefixcache.observe"))
		Expect(messaging.SubjectPrefixCacheInvalidate).To(Equal("prefixcache.invalidate"))
		Expect(messaging.SubjectPrefixCachePressure).To(Equal("prefixcache.pressure"))
	})

	It("carries a replica index on the observe event", func() {
		ev := messaging.PrefixCacheObserveEvent{Model: "m", Chain: []uint64{1, 2}, NodeID: "A", Replica: 3}
		Expect(ev.Replica).To(Equal(3))
	})

	It("uses a negative replica on the invalidate event to mean all replicas of a node", func() {
		all := messaging.PrefixCacheInvalidateEvent{Model: "m", NodeID: "A", Replica: -1}
		Expect(all.Replica).To(BeNumerically("<", 0))
		one := messaging.PrefixCacheInvalidateEvent{Model: "m", NodeID: "A", Replica: 0}
		Expect(one.Replica).To(Equal(0))
	})

	It("carries a unique pressure event identity", func() {
		ev := messaging.PrefixCachePressureEvent{ID: "frontend-a:1", Model: "m"}
		Expect(ev.ID).To(Equal("frontend-a:1"))
		Expect(ev.Model).To(Equal("m"))
	})
})
