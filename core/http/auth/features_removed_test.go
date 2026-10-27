// SPDX-License-Identifier: MIT
package auth_test

import (
	. "github.com/mudler/LocalAI/core/http/auth"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The agents, skills, mcp_jobs and chat_history features are gone. A permission
// map stored before they were removed must still load and keep what is left.
var _ = Describe("Permissions stored with removed features", func() {
	It("ignores the removed keys and keeps the others", func() {
		db, err := InitDB(":memory:")
		Expect(err).ToNot(HaveOccurred())
		user := &User{ID: "old-user", Email: "old@example.test", Name: "Old", Role: RoleUser, Status: StatusActive, Provider: ProviderLocal}
		Expect(db.Create(user).Error).To(Succeed())
		Expect(UpdateUserPermissions(db, user.ID, PermissionMap{"agents": true, "skills": true, "mcp_jobs": true, "chat_history": false,
			FeatureCollections: true, FeatureChats: false})).To(Succeed())

		effective := GetPermissionMapForUser(db, user)
		for _, gone := range []string{"agents", "skills", "mcp_jobs", "chat_history"} {
			Expect(effective).ToNot(HaveKey(gone))
			Expect(AllFeatures).ToNot(ContainElement(gone))
		}
		Expect(effective).To(HaveKeyWithValue(FeatureCollections, true))
		Expect(HasFeatureAccess(db, user, FeatureChats)).To(BeFalse())
	})

	It("registers no route for the removed APIs", func() {
		for _, r := range RouteFeatureRegistry {
			Expect(r.Pattern).ToNot(HavePrefix("/api/conversations"))
			Expect(r.Pattern).ToNot(HavePrefix("/api/agent/"))
		}
	})
})
