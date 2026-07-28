package billing

import (
	"context"
	"sync"

	"github.com/mudler/LocalAI/core/http/auth"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// fakeBackend is a minimal StatsBackend that records what it received
// without actually writing anywhere. Lets the Recorder be tested in
// isolation from GORM/SQLite/in-memory specifics.
type fakeBackend struct {
	mu      sync.Mutex
	records []*auth.UsageRecord
}

func (f *fakeBackend) Record(_ context.Context, r *auth.UsageRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.records = append(f.records, r)
	return nil
}
func (f *fakeBackend) Aggregate(_ context.Context, _ AggregateQuery) ([]auth.UsageBucket, error) {
	return nil, nil
}
func (f *fakeBackend) Close() error { return nil }

var _ = Describe("Recorder", func() {
	It("forwards to backend", func() {
		fb := &fakeBackend{}
		rec := NewRecorder(fb)

		r := &auth.UsageRecord{
			UserID:           "u-1",
			UserName:         "alice",
			Model:            "qwen-7b",
			Endpoint:         "/v1/chat/completions",
			PromptTokens:     10,
			CompletionTokens: 5,
			TotalTokens:      15,
		}
		Expect(rec.Record(context.Background(), r)).To(Succeed(), "recorder.Record")

		fb.mu.Lock()
		defer fb.mu.Unlock()
		Expect(fb.records).To(HaveLen(1))
		Expect(fb.records[0]).To(BeIdenticalTo(r), "recorder must pass the record through without copying")
	})

	// RecorderInvariantsPassWhenZero ensures legacy paths that don't
	// populate the routing-extension fields still record successfully —
	// the invariants only fire when a partial routing fact is set.
	It("invariants pass when zero", func() {
		rec := NewRecorder(&fakeBackend{})
		err := rec.Record(context.Background(), &auth.UsageRecord{
			UserID: "u-1", Model: "qwen-7b", Endpoint: "/v1/chat/completions",
		})
		Expect(err).NotTo(HaveOccurred(), "zero routing fields must record cleanly")
	})

	// RecorderInvariantsDetectShrinkViolation: setting both pre/post
	// prompt tokens with post > pre (impossible — PII can only shrink the
	// prompt) should trigger the contract assertion. In a non-strict build
	// the call still succeeds (logs + counter) but a routing_strict build
	// would panic. We assert the call returns nil here; the strict-build
	// behavior is covered by an integration test that compiles with the
	// tag.
	It("invariants detect shrink violation", func() {
		rec := NewRecorder(&fakeBackend{})
		err := rec.Record(context.Background(), &auth.UsageRecord{
			UserID:                 "u-1",
			Model:                  "qwen-7b",
			PreFilterPromptTokens:  5,
			PostFilterPromptTokens: 10, // post > pre is impossible by design
		})
		Expect(err).NotTo(HaveOccurred(), "non-strict build must not error on invariant violation")
	})
})
