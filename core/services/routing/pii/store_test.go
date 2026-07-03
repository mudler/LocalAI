package pii

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("EventStore Origin filter", func() {
	var store EventStore
	ctx := context.Background()

	BeforeEach(func() {
		store = NewMemoryEventStore(0)
		// Three redaction events from three different surfaces.
		for _, o := range []Origin{OriginMiddleware, OriginRedactAPI, OriginAnalyzeAPI} {
			Expect(store.Record(ctx, PIIEvent{
				ID:        NewEventID(),
				Kind:      KindPII,
				Origin:    o,
				PatternID: "ner:EMAIL",
			})).To(Succeed())
		}
		// An older row with no Origin (pre-field) must not match any origin filter.
		Expect(store.Record(ctx, PIIEvent{ID: NewEventID(), Kind: KindPII, PatternID: "ner:EMAIL"})).To(Succeed())
	})

	It("returns only events from the requested origin", func() {
		got, err := store.List(ctx, ListQuery{Origin: OriginRedactAPI})
		Expect(err).ToNot(HaveOccurred())
		Expect(got).To(HaveLen(1))
		Expect(got[0].Origin).To(Equal(OriginRedactAPI))
	})

	It("an empty origin matches every event (including pre-field rows)", func() {
		got, err := store.List(ctx, ListQuery{})
		Expect(err).ToNot(HaveOccurred())
		Expect(got).To(HaveLen(4))
	})

	It("does not match a pre-field (empty-origin) row against a concrete origin", func() {
		got, err := store.List(ctx, ListQuery{Origin: OriginMiddleware})
		Expect(err).ToNot(HaveOccurred())
		Expect(got).To(HaveLen(1))
		Expect(got[0].Origin).To(Equal(OriginMiddleware))
	})
})
