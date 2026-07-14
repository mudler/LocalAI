package billing

import (
	"context"
	"time"

	"github.com/mudler/LocalAI/core/http/auth"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("MemoryBackend", func() {
	It("records and aggregates", func() {
		ctx := context.Background()
		b := NewMemoryBackend(0)
		defer func() { _ = b.Close() }()

		now := time.Now()
		for i := 0; i < 5; i++ {
			err := b.Record(ctx, &auth.UsageRecord{
				UserID:           "u-1",
				UserName:         "alice",
				Model:            "qwen-7b",
				Endpoint:         "/v1/chat/completions",
				PromptTokens:     10,
				CompletionTokens: 20,
				TotalTokens:      30,
				CreatedAt:        now,
			})
			Expect(err).NotTo(HaveOccurred(), "record")
		}
		for i := 0; i < 3; i++ {
			err := b.Record(ctx, &auth.UsageRecord{
				UserID:           "u-2",
				UserName:         "bob",
				Model:            "qwen-7b",
				Endpoint:         "/v1/chat/completions",
				PromptTokens:     7,
				CompletionTokens: 13,
				TotalTokens:      20,
				CreatedAt:        now,
			})
			Expect(err).NotTo(HaveOccurred(), "record")
		}

		buckets, err := b.Aggregate(ctx, AggregateQuery{UserID: "u-1", Period: "month"})
		Expect(err).NotTo(HaveOccurred(), "aggregate")
		var promptTotal, reqTotal int64
		for _, bk := range buckets {
			Expect(bk.UserID).To(Equal("u-1"), "expected only u-1 buckets")
			promptTotal += bk.PromptTokens
			reqTotal += bk.RequestCount
		}
		Expect(promptTotal).To(Equal(int64(50)))
		Expect(reqTotal).To(Equal(int64(5)))

		all, err := b.Aggregate(ctx, AggregateQuery{Period: "month"})
		Expect(err).NotTo(HaveOccurred(), "aggregate all")
		var allPrompt, allReqs int64
		for _, bk := range all {
			allPrompt += bk.PromptTokens
			allReqs += bk.RequestCount
		}
		Expect(allPrompt).To(Equal(int64(50 + 21)))
		Expect(allReqs).To(Equal(int64(8)))
	})

	It("filters by period", func() {
		ctx := context.Background()
		b := NewMemoryBackend(0)
		defer func() { _ = b.Close() }()

		old := time.Now().Add(-48 * time.Hour)
		recent := time.Now()

		err := b.Record(ctx, &auth.UsageRecord{
			UserID: "u", UserName: "u", Model: "m",
			PromptTokens: 100, TotalTokens: 100, CreatedAt: old,
		})
		Expect(err).NotTo(HaveOccurred())
		err = b.Record(ctx, &auth.UsageRecord{
			UserID: "u", UserName: "u", Model: "m",
			PromptTokens: 50, TotalTokens: 50, CreatedAt: recent,
		})
		Expect(err).NotTo(HaveOccurred())

		dayBuckets, err := b.Aggregate(ctx, AggregateQuery{UserID: "u", Period: "day"})
		Expect(err).NotTo(HaveOccurred())
		var dayTotal int64
		for _, bk := range dayBuckets {
			dayTotal += bk.PromptTokens
		}
		Expect(dayTotal).To(Equal(int64(50)), "day window should only include the recent record")

		monthBuckets, err := b.Aggregate(ctx, AggregateQuery{UserID: "u", Period: "month"})
		Expect(err).NotTo(HaveOccurred())
		var monthTotal int64
		for _, bk := range monthBuckets {
			monthTotal += bk.PromptTokens
		}
		Expect(monthTotal).To(Equal(int64(150)), "month window should include both records")
	})

	It("ring wraps", func() {
		ctx := context.Background()
		b := NewMemoryBackend(4) // tiny ring so we can observe wrap

		for i := 0; i < 10; i++ {
			err := b.Record(ctx, &auth.UsageRecord{
				UserID:       "u",
				UserName:     "u",
				Model:        "m",
				PromptTokens: 1,
				TotalTokens:  1,
				CreatedAt:    time.Now(),
			})
			Expect(err).NotTo(HaveOccurred())
		}

		buckets, err := b.Aggregate(ctx, AggregateQuery{UserID: "u", Period: "month"})
		Expect(err).NotTo(HaveOccurred())
		var total int64
		for _, bk := range buckets {
			total += bk.PromptTokens
		}
		Expect(total).To(Equal(int64(4)), "ring should keep last 4 records")
	})
})

var _ = Describe("DisabledBackend", func() {
	It("is a no-op", func() {
		ctx := context.Background()
		b := NewDisabledBackend()
		Expect(b.Record(ctx, &auth.UsageRecord{UserID: "u"})).To(Succeed(), "disabled record should not error")
		out, err := b.Aggregate(ctx, AggregateQuery{Period: "month"})
		Expect(err).NotTo(HaveOccurred(), "disabled aggregate should not error")
		Expect(out).To(BeNil(), "disabled aggregate should return nil")
	})
})
