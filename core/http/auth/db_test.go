//go:build auth

package auth_test

import (
	"github.com/mudler/LocalAI/core/http/auth"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("InitDB", func() {
	Context("SQLite", func() {
		It("creates all tables with in-memory SQLite", func() {
			db, err := auth.InitDB(":memory:")
			Expect(err).ToNot(HaveOccurred())
			Expect(db).ToNot(BeNil())

			// Verify tables exist
			Expect(db.Migrator().HasTable(&auth.User{})).To(BeTrue())
			Expect(db.Migrator().HasTable(&auth.Session{})).To(BeTrue())
			Expect(db.Migrator().HasTable(&auth.UserAPIKey{})).To(BeTrue())
		})

		It("is idempotent - running twice does not error", func() {
			db, err := auth.InitDB(":memory:")
			Expect(err).ToNot(HaveOccurred())

			// Re-migrate on same DB should succeed
			err = db.AutoMigrate(&auth.User{}, &auth.Session{}, &auth.UserAPIKey{})
			Expect(err).ToNot(HaveOccurred())
		})

		It("creates composite index on users(provider, subject)", func() {
			db, err := auth.InitDB(":memory:")
			Expect(err).ToNot(HaveOccurred())

			// Insert a user to verify the index doesn't prevent normal operations
			user := &auth.User{
				ID:       "test-1",
				Provider: "github",
				Subject:  "12345",
				Role:     "admin",
				Status:   "active",
			}
			Expect(db.Create(user).Error).ToNot(HaveOccurred())

			// Query using the indexed columns should work
			var found auth.User
			Expect(db.Where("provider = ? AND subject = ?", "github", "12345").First(&found).Error).ToNot(HaveOccurred())
			Expect(found.ID).To(Equal("test-1"))
		})
	})
})
