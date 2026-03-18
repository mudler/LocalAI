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

	BeforeEach(func() {
		db = testDB()
		user = createTestUser(db, "apikey@example.com", auth.RoleUser, "github")
	})

	Describe("GenerateAPIKey", func() {
		It("returns key with 'lai-' prefix", func() {
			plaintext, _, _, err := auth.GenerateAPIKey()
			Expect(err).ToNot(HaveOccurred())
			Expect(plaintext).To(HavePrefix("lai-"))
		})

		It("returns consistent hash for same plaintext", func() {
			plaintext, hash, _, err := auth.GenerateAPIKey()
			Expect(err).ToNot(HaveOccurred())
			Expect(auth.HashAPIKey(plaintext)).To(Equal(hash))
		})

		It("returns prefix for display", func() {
			_, _, prefix, err := auth.GenerateAPIKey()
			Expect(err).ToNot(HaveOccurred())
			Expect(prefix).To(HavePrefix("lai-"))
			Expect(len(prefix)).To(Equal(12)) // "lai-" + 8 chars
		})

		It("generates unique keys", func() {
			key1, _, _, _ := auth.GenerateAPIKey()
			key2, _, _, _ := auth.GenerateAPIKey()
			Expect(key1).ToNot(Equal(key2))
		})
	})

	Describe("CreateAPIKey", func() {
		It("stores hashed key in DB", func() {
			plaintext, record, err := auth.CreateAPIKey(db, user.ID, "test key", auth.RoleUser)
			Expect(err).ToNot(HaveOccurred())
			Expect(plaintext).To(HavePrefix("lai-"))
			Expect(record.KeyHash).To(Equal(auth.HashAPIKey(plaintext)))
		})

		It("does not store plaintext in DB", func() {
			plaintext, _, err := auth.CreateAPIKey(db, user.ID, "test key", auth.RoleUser)
			Expect(err).ToNot(HaveOccurred())

			var keys []auth.UserAPIKey
			db.Find(&keys)
			for _, k := range keys {
				Expect(k.KeyHash).ToNot(Equal(plaintext))
				Expect(strings.Contains(k.KeyHash, "lai-")).To(BeFalse())
			}
		})

		It("inherits role from parameter", func() {
			_, record, err := auth.CreateAPIKey(db, user.ID, "admin key", auth.RoleAdmin)
			Expect(err).ToNot(HaveOccurred())
			Expect(record.Role).To(Equal(auth.RoleAdmin))
		})
	})

	Describe("ValidateAPIKey", func() {
		It("returns UserAPIKey for valid key", func() {
			plaintext, _, err := auth.CreateAPIKey(db, user.ID, "valid key", auth.RoleUser)
			Expect(err).ToNot(HaveOccurred())

			found, err := auth.ValidateAPIKey(db, plaintext)
			Expect(err).ToNot(HaveOccurred())
			Expect(found).ToNot(BeNil())
			Expect(found.UserID).To(Equal(user.ID))
		})

		It("returns error for invalid key", func() {
			_, err := auth.ValidateAPIKey(db, "lai-invalidkey12345678901234567890")
			Expect(err).To(HaveOccurred())
		})

		It("updates LastUsed timestamp", func() {
			plaintext, record, err := auth.CreateAPIKey(db, user.ID, "used key", auth.RoleUser)
			Expect(err).ToNot(HaveOccurred())
			Expect(record.LastUsed).To(BeNil())

			_, err = auth.ValidateAPIKey(db, plaintext)
			Expect(err).ToNot(HaveOccurred())

			var updated auth.UserAPIKey
			db.First(&updated, "id = ?", record.ID)
			Expect(updated.LastUsed).ToNot(BeNil())
		})

		It("loads associated user", func() {
			plaintext, _, err := auth.CreateAPIKey(db, user.ID, "with user", auth.RoleUser)
			Expect(err).ToNot(HaveOccurred())

			found, err := auth.ValidateAPIKey(db, plaintext)
			Expect(err).ToNot(HaveOccurred())
			Expect(found.User.ID).To(Equal(user.ID))
			Expect(found.User.Email).To(Equal("apikey@example.com"))
		})
	})

	Describe("ListAPIKeys", func() {
		It("returns all keys for the user", func() {
			auth.CreateAPIKey(db, user.ID, "key1", auth.RoleUser)
			auth.CreateAPIKey(db, user.ID, "key2", auth.RoleUser)

			keys, err := auth.ListAPIKeys(db, user.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(keys).To(HaveLen(2))
		})

		It("does not return other users' keys", func() {
			other := createTestUser(db, "other@example.com", auth.RoleUser, "github")
			auth.CreateAPIKey(db, user.ID, "my key", auth.RoleUser)
			auth.CreateAPIKey(db, other.ID, "other key", auth.RoleUser)

			keys, err := auth.ListAPIKeys(db, user.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(keys).To(HaveLen(1))
			Expect(keys[0].Name).To(Equal("my key"))
		})
	})

	Describe("RevokeAPIKey", func() {
		It("deletes the key record", func() {
			plaintext, record, err := auth.CreateAPIKey(db, user.ID, "to revoke", auth.RoleUser)
			Expect(err).ToNot(HaveOccurred())

			err = auth.RevokeAPIKey(db, record.ID, user.ID)
			Expect(err).ToNot(HaveOccurred())

			_, err = auth.ValidateAPIKey(db, plaintext)
			Expect(err).To(HaveOccurred())
		})

		It("only allows owner to revoke their own key", func() {
			_, record, err := auth.CreateAPIKey(db, user.ID, "mine", auth.RoleUser)
			Expect(err).ToNot(HaveOccurred())

			other := createTestUser(db, "attacker@example.com", auth.RoleUser, "github")
			err = auth.RevokeAPIKey(db, record.ID, other.ID)
			Expect(err).To(HaveOccurred())
		})
	})
})
