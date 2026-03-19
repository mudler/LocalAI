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

	// Use empty HMAC secret for basic tests
	hmacSecret := ""

	BeforeEach(func() {
		db = testDB()
		user = createTestUser(db, "session@example.com", auth.RoleUser, auth.ProviderGitHub)
	})

	Describe("CreateSession", func() {
		It("creates a session and returns 64-char hex plaintext token", func() {
			token, err := auth.CreateSession(db, user.ID, hmacSecret)
			Expect(err).ToNot(HaveOccurred())
			Expect(token).To(HaveLen(64))
		})

		It("stores the hash (not plaintext) in the DB", func() {
			token, err := auth.CreateSession(db, user.ID, hmacSecret)
			Expect(err).ToNot(HaveOccurred())

			hash := auth.HashAPIKey(token, hmacSecret)
			var session auth.Session
			err = db.First(&session, "id = ?", hash).Error
			Expect(err).ToNot(HaveOccurred())
			Expect(session.UserID).To(Equal(user.ID))
			// The plaintext token should NOT be stored as the ID
			Expect(session.ID).ToNot(Equal(token))
			Expect(session.ID).To(Equal(hash))
		})

		It("sets expiry to approximately 30 days from now", func() {
			token, err := auth.CreateSession(db, user.ID, hmacSecret)
			Expect(err).ToNot(HaveOccurred())

			hash := auth.HashAPIKey(token, hmacSecret)
			var session auth.Session
			db.First(&session, "id = ?", hash)

			expectedExpiry := time.Now().Add(30 * 24 * time.Hour)
			Expect(session.ExpiresAt).To(BeTemporally("~", expectedExpiry, time.Minute))
		})

		It("sets RotatedAt on creation", func() {
			token, err := auth.CreateSession(db, user.ID, hmacSecret)
			Expect(err).ToNot(HaveOccurred())

			hash := auth.HashAPIKey(token, hmacSecret)
			var session auth.Session
			db.First(&session, "id = ?", hash)

			Expect(session.RotatedAt).To(BeTemporally("~", time.Now(), time.Minute))
		})

		It("associates session with correct user", func() {
			token, err := auth.CreateSession(db, user.ID, hmacSecret)
			Expect(err).ToNot(HaveOccurred())

			hash := auth.HashAPIKey(token, hmacSecret)
			var session auth.Session
			db.First(&session, "id = ?", hash)
			Expect(session.UserID).To(Equal(user.ID))
		})
	})

	Describe("ValidateSession", func() {
		It("returns user for valid session", func() {
			token := createTestSession(db, user.ID)

			found, session := auth.ValidateSession(db, token, hmacSecret)
			Expect(found).ToNot(BeNil())
			Expect(found.ID).To(Equal(user.ID))
			Expect(session).ToNot(BeNil())
		})

		It("returns nil for non-existent session", func() {
			found, session := auth.ValidateSession(db, "nonexistent-session-id", hmacSecret)
			Expect(found).To(BeNil())
			Expect(session).To(BeNil())
		})

		It("returns nil for expired session", func() {
			token := createTestSession(db, user.ID)
			hash := auth.HashAPIKey(token, hmacSecret)

			// Manually expire the session
			db.Model(&auth.Session{}).Where("id = ?", hash).
				Update("expires_at", time.Now().Add(-1*time.Hour))

			found, _ := auth.ValidateSession(db, token, hmacSecret)
			Expect(found).To(BeNil())
		})
	})

	Describe("DeleteSession", func() {
		It("removes the session from DB", func() {
			token := createTestSession(db, user.ID)

			err := auth.DeleteSession(db, token, hmacSecret)
			Expect(err).ToNot(HaveOccurred())

			found, _ := auth.ValidateSession(db, token, hmacSecret)
			Expect(found).To(BeNil())
		})

		It("does not error on non-existent session", func() {
			err := auth.DeleteSession(db, "nonexistent", hmacSecret)
			Expect(err).ToNot(HaveOccurred())
		})
	})

	Describe("CleanExpiredSessions", func() {
		It("removes expired sessions", func() {
			token := createTestSession(db, user.ID)
			hash := auth.HashAPIKey(token, hmacSecret)

			// Manually expire the session
			db.Model(&auth.Session{}).Where("id = ?", hash).
				Update("expires_at", time.Now().Add(-1*time.Hour))

			err := auth.CleanExpiredSessions(db)
			Expect(err).ToNot(HaveOccurred())

			var count int64
			db.Model(&auth.Session{}).Where("id = ?", hash).Count(&count)
			Expect(count).To(Equal(int64(0)))
		})

		It("keeps active sessions", func() {
			token := createTestSession(db, user.ID)
			hash := auth.HashAPIKey(token, hmacSecret)

			err := auth.CleanExpiredSessions(db)
			Expect(err).ToNot(HaveOccurred())

			var count int64
			db.Model(&auth.Session{}).Where("id = ?", hash).Count(&count)
			Expect(count).To(Equal(int64(1)))
		})
	})

	Describe("RotateSession", func() {
		It("creates a new session and deletes the old one", func() {
			token := createTestSession(db, user.ID)
			hash := auth.HashAPIKey(token, hmacSecret)

			// Get the old session
			var oldSession auth.Session
			db.First(&oldSession, "id = ?", hash)

			newToken, err := auth.RotateSession(db, &oldSession, hmacSecret)
			Expect(err).ToNot(HaveOccurred())
			Expect(newToken).To(HaveLen(64))
			Expect(newToken).ToNot(Equal(token))

			// Old session should be gone
			var count int64
			db.Model(&auth.Session{}).Where("id = ?", hash).Count(&count)
			Expect(count).To(Equal(int64(0)))

			// New session should exist and validate
			found, _ := auth.ValidateSession(db, newToken, hmacSecret)
			Expect(found).ToNot(BeNil())
			Expect(found.ID).To(Equal(user.ID))
		})

		It("preserves user ID and expiry", func() {
			token := createTestSession(db, user.ID)
			hash := auth.HashAPIKey(token, hmacSecret)

			var oldSession auth.Session
			db.First(&oldSession, "id = ?", hash)

			newToken, err := auth.RotateSession(db, &oldSession, hmacSecret)
			Expect(err).ToNot(HaveOccurred())

			newHash := auth.HashAPIKey(newToken, hmacSecret)
			var newSession auth.Session
			db.First(&newSession, "id = ?", newHash)

			Expect(newSession.UserID).To(Equal(oldSession.UserID))
			Expect(newSession.ExpiresAt).To(BeTemporally("~", oldSession.ExpiresAt, time.Second))
		})
	})

	Context("with HMAC secret", func() {
		hmacSecret := "test-hmac-secret-123"

		It("creates and validates sessions with HMAC secret", func() {
			token, err := auth.CreateSession(db, user.ID, hmacSecret)
			Expect(err).ToNot(HaveOccurred())

			found, session := auth.ValidateSession(db, token, hmacSecret)
			Expect(found).ToNot(BeNil())
			Expect(found.ID).To(Equal(user.ID))
			Expect(session).ToNot(BeNil())
		})

		It("does not validate with wrong HMAC secret", func() {
			token, err := auth.CreateSession(db, user.ID, hmacSecret)
			Expect(err).ToNot(HaveOccurred())

			found, _ := auth.ValidateSession(db, token, "wrong-secret")
			Expect(found).To(BeNil())
		})

		It("does not validate with empty HMAC secret", func() {
			token, err := auth.CreateSession(db, user.ID, hmacSecret)
			Expect(err).ToNot(HaveOccurred())

			found, _ := auth.ValidateSession(db, token, "")
			Expect(found).To(BeNil())
		})

		It("session created with empty secret does not validate with non-empty secret", func() {
			token, err := auth.CreateSession(db, user.ID, "")
			Expect(err).ToNot(HaveOccurred())

			found, _ := auth.ValidateSession(db, token, hmacSecret)
			Expect(found).To(BeNil())
		})

		It("deletes session with correct HMAC secret", func() {
			token, err := auth.CreateSession(db, user.ID, hmacSecret)
			Expect(err).ToNot(HaveOccurred())

			err = auth.DeleteSession(db, token, hmacSecret)
			Expect(err).ToNot(HaveOccurred())

			found, _ := auth.ValidateSession(db, token, hmacSecret)
			Expect(found).To(BeNil())
		})

		It("rotates session with HMAC secret", func() {
			token, err := auth.CreateSession(db, user.ID, hmacSecret)
			Expect(err).ToNot(HaveOccurred())

			hash := auth.HashAPIKey(token, hmacSecret)
			var oldSession auth.Session
			db.First(&oldSession, "id = ?", hash)

			newToken, err := auth.RotateSession(db, &oldSession, hmacSecret)
			Expect(err).ToNot(HaveOccurred())

			// Old token should not validate
			found, _ := auth.ValidateSession(db, token, hmacSecret)
			Expect(found).To(BeNil())

			// New token should validate
			found, _ = auth.ValidateSession(db, newToken, hmacSecret)
			Expect(found).ToNot(BeNil())
			Expect(found.ID).To(Equal(user.ID))
		})
	})
})
