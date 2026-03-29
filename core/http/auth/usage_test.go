//go:build auth

package auth_test

import (
	"time"

	"github.com/mudler/LocalAI/core/http/auth"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Usage", func() {
	Describe("RecordUsage", func() {
		It("inserts a usage record", func() {
			db := testDB()
			record := &auth.UsageRecord{
				UserID:           "user-1",
				UserName:         "Test User",
				Model:            "gpt-4",
				Endpoint:         "/v1/chat/completions",
				PromptTokens:     100,
				CompletionTokens: 50,
				TotalTokens:      150,
				Duration:         1200,
				CreatedAt:        time.Now(),
			}
			err := auth.RecordUsage(db, record)
			Expect(err).ToNot(HaveOccurred())
			Expect(record.ID).ToNot(BeZero())
		})
	})

	Describe("GetUserUsage", func() {
		It("returns aggregated usage for a specific user", func() {
			db := testDB()

			// Insert records for two users
			for range 3 {
				err := auth.RecordUsage(db, &auth.UsageRecord{
					UserID:       "user-a",
					UserName:     "Alice",
					Model:        "gpt-4",
					Endpoint:     "/v1/chat/completions",
					PromptTokens: 100,
					TotalTokens:  150,
					CreatedAt:    time.Now(),
				})
				Expect(err).ToNot(HaveOccurred())
			}
			err := auth.RecordUsage(db, &auth.UsageRecord{
				UserID:       "user-b",
				UserName:     "Bob",
				Model:        "gpt-4",
				PromptTokens: 200,
				TotalTokens:  300,
				CreatedAt:    time.Now(),
			})
			Expect(err).ToNot(HaveOccurred())

			buckets, err := auth.GetUserUsage(db, "user-a", "month")
			Expect(err).ToNot(HaveOccurred())
			Expect(buckets).ToNot(BeEmpty())

			// All returned buckets should be for user-a's model
			totalPrompt := int64(0)
			for _, b := range buckets {
				totalPrompt += b.PromptTokens
			}
			Expect(totalPrompt).To(Equal(int64(300)))
		})

		It("filters by period", func() {
			db := testDB()

			// Record in the past (beyond day window)
			err := auth.RecordUsage(db, &auth.UsageRecord{
				UserID:       "user-c",
				UserName:     "Carol",
				Model:        "gpt-4",
				PromptTokens: 100,
				TotalTokens:  100,
				CreatedAt:    time.Now().Add(-48 * time.Hour),
			})
			Expect(err).ToNot(HaveOccurred())

			// Record now
			err = auth.RecordUsage(db, &auth.UsageRecord{
				UserID:       "user-c",
				UserName:     "Carol",
				Model:        "gpt-4",
				PromptTokens: 200,
				TotalTokens:  200,
				CreatedAt:    time.Now(),
			})
			Expect(err).ToNot(HaveOccurred())

			// Day period should only include recent record
			buckets, err := auth.GetUserUsage(db, "user-c", "day")
			Expect(err).ToNot(HaveOccurred())
			totalPrompt := int64(0)
			for _, b := range buckets {
				totalPrompt += b.PromptTokens
			}
			Expect(totalPrompt).To(Equal(int64(200)))

			// Month period should include both
			buckets, err = auth.GetUserUsage(db, "user-c", "month")
			Expect(err).ToNot(HaveOccurred())
			totalPrompt = 0
			for _, b := range buckets {
				totalPrompt += b.PromptTokens
			}
			Expect(totalPrompt).To(Equal(int64(300)))
		})
	})

	Describe("GetAllUsage", func() {
		It("returns usage for all users", func() {
			db := testDB()

			for _, uid := range []string{"user-x", "user-y"} {
				err := auth.RecordUsage(db, &auth.UsageRecord{
					UserID:       uid,
					UserName:     uid,
					Model:        "gpt-4",
					PromptTokens: 100,
					TotalTokens:  150,
					CreatedAt:    time.Now(),
				})
				Expect(err).ToNot(HaveOccurred())
			}

			buckets, err := auth.GetAllUsage(db, "month", "")
			Expect(err).ToNot(HaveOccurred())
			Expect(len(buckets)).To(BeNumerically(">=", 2))
		})

		It("filters by user ID when specified", func() {
			db := testDB()

			err := auth.RecordUsage(db, &auth.UsageRecord{
				UserID: "user-p", UserName: "Pat", Model: "gpt-4",
				PromptTokens: 100, TotalTokens: 100, CreatedAt: time.Now(),
			})
			Expect(err).ToNot(HaveOccurred())

			err = auth.RecordUsage(db, &auth.UsageRecord{
				UserID: "user-q", UserName: "Quinn", Model: "gpt-4",
				PromptTokens: 200, TotalTokens: 200, CreatedAt: time.Now(),
			})
			Expect(err).ToNot(HaveOccurred())

			buckets, err := auth.GetAllUsage(db, "month", "user-p")
			Expect(err).ToNot(HaveOccurred())
			for _, b := range buckets {
				Expect(b.UserID).To(Equal("user-p"))
			}
		})
	})
})
