//go:build auth

package auth_test

import (
	"time"

	"github.com/google/uuid"
	"github.com/mudler/LocalAI/core/http/auth"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
)

// Regression coverage for the production bug where deleting a user from the
// distributed-mode (PostgreSQL) admin UI returned "user not found" because the
// old delete path ignored result.Error and left several tables uncleaned.
var _ = Describe("DeleteUserCascade", Label("auth"), func() {
	var db *gorm.DB

	BeforeEach(func() {
		db = testDB()
	})

	It("returns ErrRecordNotFound when the user does not exist", func() {
		err := auth.DeleteUserCascade(db, uuid.New().String())
		Expect(err).To(Equal(gorm.ErrRecordNotFound))
	})

	It("removes invite codes the user authored", func() {
		target := createTestUser(db, "author@test.com", auth.RoleAdmin, auth.ProviderLocal)
		Expect(db.Create(&auth.InviteCode{
			ID: uuid.New().String(), Code: "code-author-1", CodePrefix: "code-aut",
			CreatedBy: target.ID, ExpiresAt: time.Now().Add(time.Hour),
		}).Error).ToNot(HaveOccurred())

		Expect(auth.DeleteUserCascade(db, target.ID)).ToNot(HaveOccurred())

		var count int64
		db.Model(&auth.InviteCode{}).Where("created_by = ?", target.ID).Count(&count)
		Expect(count).To(Equal(int64(0)))
	})

	It("nulls used_by on invite codes the user consumed but keeps the audit row", func() {
		admin := createTestUser(db, "admin-keep@test.com", auth.RoleAdmin, auth.ProviderLocal)
		target := createTestUser(db, "consumer@test.com", auth.RoleUser, auth.ProviderLocal)

		usedBy := target.ID
		now := time.Now()
		invite := &auth.InviteCode{
			ID: uuid.New().String(), Code: "code-used-1", CodePrefix: "code-use",
			CreatedBy: admin.ID, UsedBy: &usedBy, UsedAt: &now,
			ExpiresAt: now.Add(time.Hour),
		}
		Expect(db.Create(invite).Error).ToNot(HaveOccurred())

		Expect(auth.DeleteUserCascade(db, target.ID)).ToNot(HaveOccurred())

		var refreshed auth.InviteCode
		Expect(db.First(&refreshed, "id = ?", invite.ID).Error).ToNot(HaveOccurred())
		Expect(refreshed.UsedBy).To(BeNil(), "used_by should be cleared so the FK no longer points to the deleted user")
	})

	It("wipes sessions, api keys, permissions, quotas, and usage metrics", func() {
		target := createTestUser(db, "owns-data@test.com", auth.RoleUser, auth.ProviderLocal)

		_ = createTestSession(db, target.ID)
		_, _, err := auth.CreateAPIKey(db, target.ID, "k1", auth.RoleUser, "", nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(auth.UpdateUserPermissions(db, target.ID, auth.PermissionMap{auth.FeatureChat: true})).ToNot(HaveOccurred())
		max := int64(100)
		_, err = auth.CreateOrUpdateQuotaRule(db, target.ID, "", &max, nil, 3600)
		Expect(err).ToNot(HaveOccurred())
		Expect(auth.RecordUsage(db, &auth.UsageRecord{
			UserID: target.ID, UserName: target.Name, Model: "test-model",
			Endpoint: "/v1/chat/completions", PromptTokens: 5, CompletionTokens: 10, TotalTokens: 15,
		})).ToNot(HaveOccurred())

		Expect(auth.DeleteUserCascade(db, target.ID)).ToNot(HaveOccurred())

		var sessions, keys, perms, quotas, usage int64
		db.Model(&auth.Session{}).Where("user_id = ?", target.ID).Count(&sessions)
		db.Model(&auth.UserAPIKey{}).Where("user_id = ?", target.ID).Count(&keys)
		db.Model(&auth.UserPermission{}).Where("user_id = ?", target.ID).Count(&perms)
		db.Model(&auth.QuotaRule{}).Where("user_id = ?", target.ID).Count(&quotas)
		db.Model(&auth.UsageRecord{}).Where("user_id = ?", target.ID).Count(&usage)

		Expect(sessions).To(Equal(int64(0)))
		Expect(keys).To(Equal(int64(0)))
		Expect(perms).To(Equal(int64(0)))
		Expect(quotas).To(Equal(int64(0)))
		Expect(usage).To(Equal(int64(0)), "usage metrics must be removed alongside the user")
	})

	It("succeeds with foreign keys enforced — the production failure mode", func() {
		// Mirror PostgreSQL's strict FK behavior on the SQLite test DB. Without
		// the cleanup of invite_codes.created_by, the engine would reject the
		// user delete with a constraint violation, which the old handler then
		// surfaced as a misleading 404.
		Expect(db.Exec("PRAGMA foreign_keys = ON").Error).ToNot(HaveOccurred())

		target := createTestUser(db, "fk-author@test.com", auth.RoleAdmin, auth.ProviderLocal)
		Expect(db.Create(&auth.InviteCode{
			ID: uuid.New().String(), Code: "code-fk-1", CodePrefix: "code-fk1",
			CreatedBy: target.ID, ExpiresAt: time.Now().Add(time.Hour),
		}).Error).ToNot(HaveOccurred())

		Expect(auth.DeleteUserCascade(db, target.ID)).ToNot(HaveOccurred())

		var users int64
		db.Model(&auth.User{}).Where("id = ?", target.ID).Count(&users)
		Expect(users).To(Equal(int64(0)))
	})
})
