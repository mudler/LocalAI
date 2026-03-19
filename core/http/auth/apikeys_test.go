//go:build auth

package auth_test

import (
	"strings"

	"github.com/mudler/LocalAI/core/http/auth"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
)

var _ = Describe("API Keys", func() {
	var (
		db   *gorm.DB
		user *auth.User
	)

	// Use empty HMAC secret for tests (falls back to plain SHA-256)
	hmacSecret := ""

	BeforeEach(func() {
		db = testDB()
		user = createTestUser(db, "apikey@example.com", auth.RoleUser, auth.ProviderGitHub)
	})

	Describe("GenerateAPIKey", func() {
		It("returns key with 'lai-' prefix", func() {
			plaintext, _, _, err := auth.GenerateAPIKey(hmacSecret)
			Expect(err).ToNot(HaveOccurred())
			Expect(plaintext).To(HavePrefix("lai-"))
		})

		It("returns consistent hash for same plaintext", func() {
			plaintext, hash, _, err := auth.GenerateAPIKey(hmacSecret)
			Expect(err).ToNot(HaveOccurred())
			Expect(auth.HashAPIKey(plaintext, hmacSecret)).To(Equal(hash))
		})

		It("returns prefix for display", func() {
			_, _, prefix, err := auth.GenerateAPIKey(hmacSecret)
			Expect(err).ToNot(HaveOccurred())
			Expect(prefix).To(HavePrefix("lai-"))
			Expect(len(prefix)).To(Equal(12)) // "lai-" + 8 chars
		})

		It("generates unique keys", func() {
			key1, _, _, _ := auth.GenerateAPIKey(hmacSecret)
			key2, _, _, _ := auth.GenerateAPIKey(hmacSecret)
			Expect(key1).ToNot(Equal(key2))
		})
	})

	Describe("CreateAPIKey", func() {
		It("stores hashed key in DB", func() {
			plaintext, record, err := auth.CreateAPIKey(db, user.ID, "test key", auth.RoleUser, hmacSecret, nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(plaintext).To(HavePrefix("lai-"))
			Expect(record.KeyHash).To(Equal(auth.HashAPIKey(plaintext, hmacSecret)))
		})

		It("does not store plaintext in DB", func() {
			plaintext, _, err := auth.CreateAPIKey(db, user.ID, "test key", auth.RoleUser, hmacSecret, nil)
			Expect(err).ToNot(HaveOccurred())

			var keys []auth.UserAPIKey
			db.Find(&keys)
			for _, k := range keys {
				Expect(k.KeyHash).ToNot(Equal(plaintext))
				Expect(strings.Contains(k.KeyHash, "lai-")).To(BeFalse())
			}
		})

		It("inherits role from parameter", func() {
			_, record, err := auth.CreateAPIKey(db, user.ID, "admin key", auth.RoleAdmin, hmacSecret, nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(record.Role).To(Equal(auth.RoleAdmin))
		})
	})

	Describe("ValidateAPIKey", func() {
		It("returns UserAPIKey for valid key", func() {
			plaintext, _, err := auth.CreateAPIKey(db, user.ID, "valid key", auth.RoleUser, hmacSecret, nil)
			Expect(err).ToNot(HaveOccurred())

			found, err := auth.ValidateAPIKey(db, plaintext, hmacSecret)
			Expect(err).ToNot(HaveOccurred())
			Expect(found).ToNot(BeNil())
			Expect(found.UserID).To(Equal(user.ID))
		})

		It("returns error for invalid key", func() {
			_, err := auth.ValidateAPIKey(db, "lai-invalidkey12345678901234567890", hmacSecret)
			Expect(err).To(HaveOccurred())
		})

		It("updates LastUsed timestamp", func() {
			plaintext, record, err := auth.CreateAPIKey(db, user.ID, "used key", auth.RoleUser, hmacSecret, nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(record.LastUsed).To(BeNil())

			_, err = auth.ValidateAPIKey(db, plaintext, hmacSecret)
			Expect(err).ToNot(HaveOccurred())

			var updated auth.UserAPIKey
			db.First(&updated, "id = ?", record.ID)
			Expect(updated.LastUsed).ToNot(BeNil())
		})

		It("loads associated user", func() {
			plaintext, _, err := auth.CreateAPIKey(db, user.ID, "with user", auth.RoleUser, hmacSecret, nil)
			Expect(err).ToNot(HaveOccurred())

			found, err := auth.ValidateAPIKey(db, plaintext, hmacSecret)
			Expect(err).ToNot(HaveOccurred())
			Expect(found.User.ID).To(Equal(user.ID))
			Expect(found.User.Email).To(Equal("apikey@example.com"))
		})
	})

	Describe("ListAPIKeys", func() {
		It("returns all keys for the user", func() {
			auth.CreateAPIKey(db, user.ID, "key1", auth.RoleUser, hmacSecret, nil)
			auth.CreateAPIKey(db, user.ID, "key2", auth.RoleUser, hmacSecret, nil)

			keys, err := auth.ListAPIKeys(db, user.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(keys).To(HaveLen(2))
		})

		It("does not return other users' keys", func() {
			other := createTestUser(db, "other@example.com", auth.RoleUser, auth.ProviderGitHub)
			auth.CreateAPIKey(db, user.ID, "my key", auth.RoleUser, hmacSecret, nil)
			auth.CreateAPIKey(db, other.ID, "other key", auth.RoleUser, hmacSecret, nil)

			keys, err := auth.ListAPIKeys(db, user.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(keys).To(HaveLen(1))
			Expect(keys[0].Name).To(Equal("my key"))
		})
	})

	Context("with HMAC secret", func() {
		hmacSecretVal := "test-hmac-secret-456"

		It("generates different hash than empty secret", func() {
			plaintext, _, _, err := auth.GenerateAPIKey("")
			Expect(err).ToNot(HaveOccurred())

			hashEmpty := auth.HashAPIKey(plaintext, "")
			hashHMAC := auth.HashAPIKey(plaintext, hmacSecretVal)
			Expect(hashEmpty).ToNot(Equal(hashHMAC))
		})

		It("round-trips CreateAPIKey and ValidateAPIKey with HMAC secret", func() {
			plaintext, _, err := auth.CreateAPIKey(db, user.ID, "hmac key", auth.RoleUser, hmacSecretVal, nil)
			Expect(err).ToNot(HaveOccurred())

			found, err := auth.ValidateAPIKey(db, plaintext, hmacSecretVal)
			Expect(err).ToNot(HaveOccurred())
			Expect(found).ToNot(BeNil())
			Expect(found.UserID).To(Equal(user.ID))
		})

		It("does not validate with wrong HMAC secret", func() {
			plaintext, _, err := auth.CreateAPIKey(db, user.ID, "hmac key2", auth.RoleUser, hmacSecretVal, nil)
			Expect(err).ToNot(HaveOccurred())

			_, err = auth.ValidateAPIKey(db, plaintext, "wrong-secret")
			Expect(err).To(HaveOccurred())
		})

		It("does not validate key created with empty secret using non-empty secret", func() {
			plaintext, _, err := auth.CreateAPIKey(db, user.ID, "empty-secret key", auth.RoleUser, "", nil)
			Expect(err).ToNot(HaveOccurred())

			_, err = auth.ValidateAPIKey(db, plaintext, hmacSecretVal)
			Expect(err).To(HaveOccurred())
		})

		It("does not validate key created with non-empty secret using empty secret", func() {
			plaintext, _, err := auth.CreateAPIKey(db, user.ID, "nonempty-secret key", auth.RoleUser, hmacSecretVal, nil)
			Expect(err).ToNot(HaveOccurred())

			_, err = auth.ValidateAPIKey(db, plaintext, "")
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("RevokeAPIKey", func() {
		It("deletes the key record", func() {
			plaintext, record, err := auth.CreateAPIKey(db, user.ID, "to revoke", auth.RoleUser, hmacSecret, nil)
			Expect(err).ToNot(HaveOccurred())

			err = auth.RevokeAPIKey(db, record.ID, user.ID)
			Expect(err).ToNot(HaveOccurred())

			_, err = auth.ValidateAPIKey(db, plaintext, hmacSecret)
			Expect(err).To(HaveOccurred())
		})

		It("only allows owner to revoke their own key", func() {
			_, record, err := auth.CreateAPIKey(db, user.ID, "mine", auth.RoleUser, hmacSecret, nil)
			Expect(err).ToNot(HaveOccurred())

			other := createTestUser(db, "attacker@example.com", auth.RoleUser, auth.ProviderGitHub)
			err = auth.RevokeAPIKey(db, record.ID, other.ID)
			Expect(err).To(HaveOccurred())
		})
	})
})
