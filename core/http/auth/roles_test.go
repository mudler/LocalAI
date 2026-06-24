//go:build auth

package auth_test

import (
	"github.com/mudler/LocalAI/core/http/auth"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
)

var _ = Describe("Roles", func() {
	var db *gorm.DB

	BeforeEach(func() {
		db = testDB()
	})

	Describe("AssignRole", func() {
		It("returns admin for the first user (empty DB)", func() {
			role := auth.AssignRole(db, "first@example.com", "")
			Expect(role).To(Equal(auth.RoleAdmin))
		})

		It("returns user for the second user", func() {
			createTestUser(db, "first@example.com", auth.RoleAdmin, auth.ProviderGitHub)

			role := auth.AssignRole(db, "second@example.com", "")
			Expect(role).To(Equal(auth.RoleUser))
		})

		It("returns admin when email matches adminEmail", func() {
			createTestUser(db, "first@example.com", auth.RoleAdmin, auth.ProviderGitHub)

			role := auth.AssignRole(db, "admin@example.com", "admin@example.com")
			Expect(role).To(Equal(auth.RoleAdmin))
		})

		It("is case-insensitive for admin email match", func() {
			createTestUser(db, "first@example.com", auth.RoleAdmin, auth.ProviderGitHub)

			role := auth.AssignRole(db, "Admin@Example.COM", "admin@example.com")
			Expect(role).To(Equal(auth.RoleAdmin))
		})

		It("returns user when email does not match adminEmail", func() {
			createTestUser(db, "first@example.com", auth.RoleAdmin, auth.ProviderGitHub)

			role := auth.AssignRole(db, "other@example.com", "admin@example.com")
			Expect(role).To(Equal(auth.RoleUser))
		})
	})

	Describe("MaybePromote", func() {
		It("promotes user to admin when email matches", func() {
			user := createTestUser(db, "promoted@example.com", auth.RoleUser, auth.ProviderGitHub)

			promoted := auth.MaybePromote(db, user, "promoted@example.com")
			Expect(promoted).To(BeTrue())
			Expect(user.Role).To(Equal(auth.RoleAdmin))

			// Verify in DB
			var dbUser auth.User
			db.First(&dbUser, "id = ?", user.ID)
			Expect(dbUser.Role).To(Equal(auth.RoleAdmin))
		})

		It("does not promote when email does not match", func() {
			user := createTestUser(db, "user@example.com", auth.RoleUser, auth.ProviderGitHub)

			promoted := auth.MaybePromote(db, user, "admin@example.com")
			Expect(promoted).To(BeFalse())
			Expect(user.Role).To(Equal(auth.RoleUser))
		})

		It("does not demote an existing admin", func() {
			user := createTestUser(db, "admin@example.com", auth.RoleAdmin, auth.ProviderGitHub)

			promoted := auth.MaybePromote(db, user, "other@example.com")
			Expect(promoted).To(BeFalse())
			Expect(user.Role).To(Equal(auth.RoleAdmin))
		})
	})
})
