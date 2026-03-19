//go:build auth

package auth_test

import (
	"time"

	"github.com/mudler/LocalAI/core/http/auth"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
)

var _ = Describe("Sessions", func() {
	var (
		db   *gorm.DB
		user *auth.User
	)

	BeforeEach(func() {
		db = testDB()
		user = createTestUser(db, "session@example.com", auth.RoleUser, auth.ProviderGitHub)
	})

	Describe("CreateSession", func() {
		It("creates a session with 64-char hex ID", func() {
			sessionID, err := auth.CreateSession(db, user.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(sessionID).To(HaveLen(64))
		})

		It("sets expiry to approximately 30 days from now", func() {
			sessionID, err := auth.CreateSession(db, user.ID)
			Expect(err).ToNot(HaveOccurred())

			var session auth.Session
			db.First(&session, "id = ?", sessionID)

			expectedExpiry := time.Now().Add(30 * 24 * time.Hour)
			Expect(session.ExpiresAt).To(BeTemporally("~", expectedExpiry, time.Minute))
		})

		It("associates session with correct user", func() {
			sessionID, err := auth.CreateSession(db, user.ID)
			Expect(err).ToNot(HaveOccurred())

			var session auth.Session
			db.First(&session, "id = ?", sessionID)
			Expect(session.UserID).To(Equal(user.ID))
		})
	})

	Describe("ValidateSession", func() {
		It("returns user for valid session", func() {
			sessionID := createTestSession(db, user.ID)

			found := auth.ValidateSession(db, sessionID)
			Expect(found).ToNot(BeNil())
			Expect(found.ID).To(Equal(user.ID))
		})

		It("returns nil for non-existent session", func() {
			found := auth.ValidateSession(db, "nonexistent-session-id")
			Expect(found).To(BeNil())
		})

		It("returns nil for expired session", func() {
			sessionID := createTestSession(db, user.ID)

			// Manually expire the session
			db.Model(&auth.Session{}).Where("id = ?", sessionID).
				Update("expires_at", time.Now().Add(-1*time.Hour))

			found := auth.ValidateSession(db, sessionID)
			Expect(found).To(BeNil())
		})
	})

	Describe("DeleteSession", func() {
		It("removes the session from DB", func() {
			sessionID := createTestSession(db, user.ID)

			err := auth.DeleteSession(db, sessionID)
			Expect(err).ToNot(HaveOccurred())

			found := auth.ValidateSession(db, sessionID)
			Expect(found).To(BeNil())
		})

		It("does not error on non-existent session", func() {
			err := auth.DeleteSession(db, "nonexistent")
			Expect(err).ToNot(HaveOccurred())
		})
	})

	Describe("CleanExpiredSessions", func() {
		It("removes expired sessions", func() {
			sessionID := createTestSession(db, user.ID)

			// Manually expire the session
			db.Model(&auth.Session{}).Where("id = ?", sessionID).
				Update("expires_at", time.Now().Add(-1*time.Hour))

			err := auth.CleanExpiredSessions(db)
			Expect(err).ToNot(HaveOccurred())

			var count int64
			db.Model(&auth.Session{}).Where("id = ?", sessionID).Count(&count)
			Expect(count).To(Equal(int64(0)))
		})

		It("keeps active sessions", func() {
			sessionID := createTestSession(db, user.ID)

			err := auth.CleanExpiredSessions(db)
			Expect(err).ToNot(HaveOccurred())

			var count int64
			db.Model(&auth.Session{}).Where("id = ?", sessionID).Count(&count)
			Expect(count).To(Equal(int64(1)))
		})
	})
})
