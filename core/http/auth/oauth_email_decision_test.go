//go:build auth

package auth

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
)

var _ = Describe("emailForRoleDecision", func() {
	It("returns the email when verified", func() {
		Expect(emailForRoleDecision("admin@example.com", true)).
			To(Equal("admin@example.com"))
	})

	It("returns empty when not verified", func() {
		Expect(emailForRoleDecision("admin@example.com", false)).
			To(Equal(""))
	})

	It("returns empty when email is empty regardless of flag", func() {
		Expect(emailForRoleDecision("", true)).To(Equal(""))
		Expect(emailForRoleDecision("", false)).To(Equal(""))
	})

	Context("integration with AssignRole", func() {
		var db *gorm.DB

		BeforeEach(func() {
			db, _ = InitDB(":memory:")
			// Seed at least one user so the "first user becomes admin"
			// branch doesn't hide the gate we're testing.
			seed := &User{
				ID:       "seed-user",
				Email:    "seed@example.com",
				Provider: ProviderGitHub,
				Subject:  "seed",
				Role:     RoleAdmin,
				Status:   StatusActive,
			}
			Expect(db.Create(seed).Error).To(Succeed())
		})

		It("does NOT promote on unverified email matching admin email", func() {
			role := AssignRole(db, emailForRoleDecision("admin@example.com", false), "admin@example.com")
			Expect(role).To(Equal(RoleUser),
				"unverified IdP claim of admin email must not yield admin role")
		})

		It("DOES promote on verified email matching admin email", func() {
			role := AssignRole(db, emailForRoleDecision("admin@example.com", true), "admin@example.com")
			Expect(role).To(Equal(RoleAdmin))
		})

		It("ignores email when admin email is unset, regardless of verification", func() {
			role := AssignRole(db, emailForRoleDecision("any@example.com", true), "")
			Expect(role).To(Equal(RoleUser))
		})
	})
})
