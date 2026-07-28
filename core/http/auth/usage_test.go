//go:build auth

package auth_test

import (
	"fmt"
	"time"

	"github.com/mudler/LocalAI/core/http/auth"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
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

	Describe("Usage source backfill", func() {
		It("backfills 'web' for pre-feature rows", func() {
			db := testDB()

			rawDB, err := db.DB()
			Expect(err).ToNot(HaveOccurred())
			_, err = rawDB.Exec(
				`INSERT INTO usage_records (user_id, source, model, created_at, total_tokens, prompt_tokens, completion_tokens, duration) VALUES (?, '', ?, ?, 0, 0, 0, 0)`,
				"user-x", "gpt-4", time.Now())
			Expect(err).ToNot(HaveOccurred())

			Expect(auth.BackfillUsageSource(db)).To(Succeed())

			var loaded auth.UsageRecord
			Expect(db.Where("user_id = ?", "user-x").First(&loaded).Error).To(Succeed())
			Expect(loaded.Source).To(Equal(auth.UsageSourceWeb))
		})

		It("backfills 'legacy' for pre-feature rows with legacy-api-key user_id", func() {
			db := testDB()

			rawDB, err := db.DB()
			Expect(err).ToNot(HaveOccurred())
			_, err = rawDB.Exec(
				`INSERT INTO usage_records (user_id, source, model, created_at, total_tokens, prompt_tokens, completion_tokens, duration) VALUES (?, '', ?, ?, 0, 0, 0, 0)`,
				"legacy-api-key", "gpt-4", time.Now())
			Expect(err).ToNot(HaveOccurred())

			Expect(auth.BackfillUsageSource(db)).To(Succeed())

			var loaded auth.UsageRecord
			Expect(db.Where("user_id = ?", "legacy-api-key").First(&loaded).Error).To(Succeed())
			Expect(loaded.Source).To(Equal(auth.UsageSourceLegacy))
		})

		It("is idempotent on re-run", func() {
			db := testDB()
			Expect(auth.BackfillUsageSource(db)).To(Succeed())
			Expect(auth.BackfillUsageSource(db)).To(Succeed())
		})
	})

	Describe("UsageRecord with source fields", func() {
		It("persists Source, APIKeyID, APIKeyName", func() {
			db := testDB()
			keyID := "key-uuid-1"
			record := &auth.UsageRecord{
				UserID:      "user-1",
				UserName:    "Test User",
				Source:      auth.UsageSourceAPIKey,
				APIKeyID:    &keyID,
				APIKeyName:  "ci-runner",
				Model:       "gpt-4",
				Endpoint:    "/v1/chat/completions",
				TotalTokens: 150,
				CreatedAt:   time.Now(),
			}
			Expect(auth.RecordUsage(db, record)).To(Succeed())

			var loaded auth.UsageRecord
			Expect(db.First(&loaded, record.ID).Error).To(Succeed())
			Expect(loaded.Source).To(Equal(auth.UsageSourceAPIKey))
			Expect(loaded.APIKeyID).ToNot(BeNil())
			Expect(*loaded.APIKeyID).To(Equal("key-uuid-1"))
			Expect(loaded.APIKeyName).To(Equal("ci-runner"))
		})

		It("allows nil APIKeyID for web/legacy sources", func() {
			db := testDB()
			record := &auth.UsageRecord{
				UserID:    "user-1",
				Source:    auth.UsageSourceWeb,
				Model:     "gpt-4",
				CreatedAt: time.Now(),
			}
			Expect(auth.RecordUsage(db, record)).To(Succeed())

			var loaded auth.UsageRecord
			Expect(db.First(&loaded, record.ID).Error).To(Succeed())
			Expect(loaded.Source).To(Equal(auth.UsageSourceWeb))
			Expect(loaded.APIKeyID).To(BeNil())
			Expect(loaded.APIKeyName).To(BeEmpty())
		})
	})

	Describe("GetUserUsageBySource", func() {
		insert := func(db *gorm.DB, userID, source, keyID, keyName string, tokens int64, when time.Time) {
			rec := &auth.UsageRecord{
				UserID:      userID,
				Source:      source,
				Model:       "gpt-4",
				TotalTokens: tokens,
				CreatedAt:   when,
			}
			if keyID != "" {
				rec.APIKeyID = &keyID
				rec.APIKeyName = keyName
			}
			Expect(auth.RecordUsage(db, rec)).To(Succeed())
		}

		It("returns only the caller's rows, never legacy", func() {
			db := testDB()
			now := time.Now()
			insert(db, "alice", auth.UsageSourceAPIKey, "k1", "ci", 100, now)
			insert(db, "alice", auth.UsageSourceWeb, "", "", 50, now)
			insert(db, "alice", auth.UsageSourceLegacy, "", "", 30, now)
			insert(db, "bob", auth.UsageSourceAPIKey, "k2", "bobk", 90, now)

			buckets, totals, err := auth.GetUserUsageBySource(db, "alice", "month")
			Expect(err).ToNot(HaveOccurred())

			for _, b := range buckets {
				Expect(b.UserID).To(Or(BeEmpty(), Equal("alice")))
				Expect(b.Source).ToNot(Equal(auth.UsageSourceLegacy))
			}

			Expect(totals.GrandTotal.Tokens).To(Equal(int64(150)))
			Expect(totals.BySource[auth.UsageSourceAPIKey].Tokens).To(Equal(int64(100)))
			Expect(totals.BySource[auth.UsageSourceWeb].Tokens).To(Equal(int64(50)))
			_, hasLegacy := totals.BySource[auth.UsageSourceLegacy]
			Expect(hasLegacy).To(BeFalse())
		})

		It("snapshots survive key deletion", func() {
			db := testDB()
			now := time.Now()
			insert(db, "alice", auth.UsageSourceAPIKey, "deleted-key", "old-name", 42, now)
			_, totals, err := auth.GetUserUsageBySource(db, "alice", "month")
			Expect(err).ToNot(HaveOccurred())
			Expect(totals.ByKey).To(HaveLen(1))
			Expect(totals.ByKey[0].APIKeyName).To(Equal("old-name"))
			Expect(totals.ByKey[0].APIKeyID).To(Equal("deleted-key"))
			Expect(totals.ByKey[0].LastUsed).ToNot(BeZero())
			Expect(totals.ByKey[0].LastUsed).To(BeTemporally("~", now, 2*time.Second))
		})
	})

	Describe("GetAllUsageBySource", func() {
		insert := func(db *gorm.DB, userID, source, keyID string, tokens int64) {
			rec := &auth.UsageRecord{
				UserID:      userID,
				Source:      source,
				Model:       "gpt-4",
				TotalTokens: tokens,
				CreatedAt:   time.Now(),
			}
			if keyID != "" {
				rec.APIKeyID = &keyID
				rec.APIKeyName = "name-" + keyID
			}
			Expect(auth.RecordUsage(db, rec)).To(Succeed())
		}

		It("includes legacy for admins", func() {
			db := testDB()
			insert(db, "alice", auth.UsageSourceAPIKey, "k1", 10)
			insert(db, "legacy-api-key", auth.UsageSourceLegacy, "", 5)

			_, totals, _, err := auth.GetAllUsageBySource(db, "month", "", "")
			Expect(err).ToNot(HaveOccurred())
			Expect(totals.BySource).To(HaveKey(auth.UsageSourceLegacy))
			Expect(totals.BySource[auth.UsageSourceLegacy].Tokens).To(Equal(int64(5)))
		})

		It("filters by user_id AND api_key_id", func() {
			db := testDB()
			insert(db, "alice", auth.UsageSourceAPIKey, "k1", 10)
			insert(db, "alice", auth.UsageSourceAPIKey, "k2", 20)
			insert(db, "bob", auth.UsageSourceAPIKey, "k3", 30)

			_, totals, _, err := auth.GetAllUsageBySource(db, "month", "alice", "k2")
			Expect(err).ToNot(HaveOccurred())
			Expect(totals.GrandTotal.Tokens).To(Equal(int64(20)))
		})

		It("sets truncated=true when by_key exceeds the cap", func() {
			db := testDB()
			for i := 0; i < 210; i++ {
				insert(db, "alice", auth.UsageSourceAPIKey, fmt.Sprintf("key-%03d", i), int64(210-i))
			}

			_, totals, truncated, err := auth.GetAllUsageBySource(db, "month", "", "")
			Expect(err).ToNot(HaveOccurred())
			Expect(truncated).To(BeTrue())
			Expect(totals.ByKey).To(HaveLen(200))
			Expect(totals.ByKey[0].Tokens > totals.ByKey[199].Tokens).To(BeTrue())
		})

		// insertNamed records a row with explicit user_id, user_name, source,
		// and optional api key snapshot. Used by the user-attribution tests
		// below which the older insert helper can't express.
		insertNamed := func(db *gorm.DB, userID, userName, source, keyID, keyName string, tokens int64) {
			rec := &auth.UsageRecord{
				UserID:      userID,
				UserName:    userName,
				Source:      source,
				Model:       "gpt-4",
				TotalTokens: tokens,
				CreatedAt:   time.Now(),
			}
			if keyID != "" {
				rec.APIKeyID = &keyID
				rec.APIKeyName = keyName
			}
			Expect(auth.RecordUsage(db, rec)).To(Succeed())
		}

		It("attributes each KeyTotal to its owner user", func() {
			db := testDB()
			insertNamed(db, "alice", "Alice", auth.UsageSourceAPIKey, "k1", "ci-runner", 100)
			insertNamed(db, "bob", "Bob", auth.UsageSourceAPIKey, "k2", "lap", 50)

			_, totals, _, err := auth.GetAllUsageBySource(db, "month", "", "")
			Expect(err).ToNot(HaveOccurred())
			Expect(totals.ByKey).To(HaveLen(2))

			byID := map[string]auth.KeyTotal{}
			for _, k := range totals.ByKey {
				byID[k.APIKeyID] = k
			}
			Expect(byID["k1"].UserID).To(Equal("alice"))
			Expect(byID["k1"].UserName).To(Equal("Alice"))
			Expect(byID["k2"].UserID).To(Equal("bob"))
			Expect(byID["k2"].UserName).To(Equal("Bob"))
		})

		It("breaks Web UI and legacy traffic out per user in by_user_source for admin", func() {
			db := testDB()
			// Alice and Bob both have Web UI traffic; a synthetic legacy user
			// also contributes. ByUserSource should expose one row per
			// (source, user) pair, never for source=apikey.
			insertNamed(db, "alice", "Alice", auth.UsageSourceWeb, "", "", 30)
			insertNamed(db, "bob", "Bob", auth.UsageSourceWeb, "", "", 70)
			insertNamed(db, "legacy-api-key", "API Key User", auth.UsageSourceLegacy, "", "", 10)
			insertNamed(db, "alice", "Alice", auth.UsageSourceAPIKey, "k1", "ci-runner", 5)

			_, totals, _, err := auth.GetAllUsageBySource(db, "month", "", "")
			Expect(err).ToNot(HaveOccurred())
			Expect(totals.ByUserSource).ToNot(BeEmpty())

			for _, r := range totals.ByUserSource {
				Expect(r.Source).ToNot(Equal(auth.UsageSourceAPIKey))
			}

			webByUser := map[string]int64{}
			legacyByUser := map[string]int64{}
			for _, r := range totals.ByUserSource {
				switch r.Source {
				case auth.UsageSourceWeb:
					webByUser[r.UserID] = r.Tokens
				case auth.UsageSourceLegacy:
					legacyByUser[r.UserID] = r.Tokens
				}
			}
			Expect(webByUser["alice"]).To(Equal(int64(30)))
			Expect(webByUser["bob"]).To(Equal(int64(70)))
			Expect(legacyByUser["legacy-api-key"]).To(Equal(int64(10)))
		})

		It("does NOT populate by_user_source in the non-admin path", func() {
			db := testDB()
			insertNamed(db, "alice", "Alice", auth.UsageSourceWeb, "", "", 30)

			_, totals, err := auth.GetUserUsageBySource(db, "alice", "month")
			Expect(err).ToNot(HaveOccurred())
			// Non-admin path uses includeLegacy=false, so by_user_source stays nil.
			Expect(totals.ByUserSource).To(BeNil())
		})
	})
})
