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
