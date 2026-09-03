package testutil_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/testutil"
)

// FakeBus stands in for the carrier in every cross-replica-sync spec in the
// tree. If it routed by its own rules, those specs would be proving a delivery
// behaviour production does not have, so these pin that it routes by the
// shared matcher and refuses the filters the carrier refuses.
var _ = Describe("FakeBus subject routing", func() {
	It("delivers to a wildcard subscriber, not only to an exact one", func() {
		bus := testutil.NewFakeBus()

		delivered := make(chan []byte, 4)
		_, err := bus.Subscribe(messaging.SubjectStagingProgressWildcard, func(payload []byte) {
			delivered <- payload
		})
		Expect(err).ToNot(HaveOccurred())

		Expect(bus.Publish(messaging.SubjectStagingProgress("a.b.c"), map[string]string{"id": "staging-payload"})).To(Succeed())

		var payload []byte
		Eventually(delivered).Should(Receive(&payload))
		Expect(string(payload)).To(ContainSubstring("staging-payload"))
	})

	It("does not deliver a subject the wildcard cannot match", func() {
		bus := testutil.NewFakeBus()

		delivered := make(chan []byte, 4)
		_, err := bus.Subscribe("jobs.*.progress", func(payload []byte) {
			delivered <- payload
		})
		Expect(err).ToNot(HaveOccurred())

		Expect(bus.Publish("jobs.abc.def.progress", map[string]string{"id": "abc"})).To(Succeed())
		Consistently(delivered).ShouldNot(Receive())
	})

	It("refuses a filter the carrier cannot honour instead of never firing", func() {
		bus := testutil.NewFakeBus()

		sub, err := bus.Subscribe("jobs.>", func([]byte) {})
		Expect(err).To(MatchError(messaging.ErrUnsupportedFilter))
		Expect(sub).To(BeNil())

		sub, err = bus.Subscribe("", func([]byte) {})
		Expect(err).To(MatchError(messaging.ErrUnsupportedFilter))
		Expect(sub).To(BeNil())
	})
})

var _ = Describe("FakeBus subscription identity", func() {
	It("unsubscribing one of two subscribers on the same filter leaves the other receiving", func() {
		// Two SyncedMaps in one process legitimately share a filter (the
		// cluster-wide view and any other unscoped map on the same name). If
		// Unsubscribe removed the first entry matching the FILTER, closing one
		// map would silently deafen the other, and the resulting spec failure
		// would read as a broken carrier rather than as a broken double.
		bus := testutil.NewFakeBus()

		firstSeen := 0
		first, err := bus.Subscribe("state.jobs.delta", func([]byte) { firstSeen++ })
		Expect(err).ToNot(HaveOccurred())

		secondSeen := 0
		_, err = bus.Subscribe("state.jobs.delta", func([]byte) { secondSeen++ })
		Expect(err).ToNot(HaveOccurred())

		Expect(first.Unsubscribe()).To(Succeed())
		Expect(bus.Publish("state.jobs.delta", map[string]string{"op": "set"})).To(Succeed())

		Expect(firstSeen).To(Equal(0), "the unsubscribed handler must not fire")
		Expect(secondSeen).To(Equal(1), "the surviving subscriber must still receive")
	})
})
