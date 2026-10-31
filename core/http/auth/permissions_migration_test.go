//go:build auth

// SPDX-License-Identifier: MIT
package auth_test

import (
	. "github.com/mudler/LocalAI/core/http/auth"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("MigrateAutomationFeatures", func() {
	newUser := func(id, role string) *User {
		return &User{ID: id, Email: id + "@example.test", Name: id, Role: role, Status: StatusActive, Provider: ProviderLocal}
	}

	It("keeps access where it is in use or was granted, and nowhere else", func() {
		db, err := InitDB(":memory:")
		Expect(err).NotTo(HaveOccurred())
		for _, u := range []*User{newUser("idle", RoleUser), newUser("owner", RoleUser), newUser("master", RoleUser),
			newUser("jobs", RoleUser), newUser("revoked", RoleUser), newUser("root", RoleAdmin)} {
			Expect(db.Create(u).Error).To(Succeed())
		}
		Expect(UpdateUserPermissions(db, "master", PermissionMap{FeatureAgents: true})).To(Succeed())
		Expect(UpdateUserPermissions(db, "jobs", PermissionMap{FeatureMCPJobs: true})).To(Succeed())
		Expect(UpdateUserPermissions(db, "revoked", PermissionMap{FeatureTriggers: false})).To(Succeed())

		owned := map[string]map[string]bool{
			"owner":   {FeatureAgents: true, FeatureConnectors: true},
			"revoked": {FeatureTriggers: true},
		}
		Expect(MigrateAutomationFeatures(db, owned)).To(Succeed())

		perm := func(id string) PermissionMap { return GetPermissionMapForUser(db, &User{ID: id, Role: RoleUser}) }
		Expect(perm("idle")).To(And(HaveKeyWithValue(FeatureAgents, false), HaveKeyWithValue(FeatureTriggers, false), HaveKeyWithValue(FeatureConnectors, false)))
		Expect(perm("owner")).To(And(HaveKeyWithValue(FeatureAgents, true), HaveKeyWithValue(FeatureConnectors, true), HaveKeyWithValue(FeatureTriggers, false)))
		Expect(perm("master")).To(And(HaveKeyWithValue(FeatureAgents, true), HaveKeyWithValue(FeatureTriggers, true), HaveKeyWithValue(FeatureConnectors, true)))
		Expect(perm("jobs")).To(And(HaveKeyWithValue(FeatureTriggers, true), HaveKeyWithValue(FeatureMCPJobs, true), HaveKeyWithValue(FeatureAgents, false)))
		Expect(perm("revoked")).To(HaveKeyWithValue(FeatureTriggers, false), "a stored off stays off")
	})

	It("runs once: a user who registers later gets the defaults", func() {
		db, err := InitDB(":memory:")
		Expect(err).NotTo(HaveOccurred())
		Expect(MigrateAutomationFeatures(db, nil)).To(Succeed())
		Expect(db.Create(newUser("late", RoleUser)).Error).To(Succeed())
		Expect(MigrateAutomationFeatures(db, map[string]map[string]bool{"late": {FeatureAgents: true}})).To(Succeed())
		Expect(GetPermissionMapForUser(db, &User{ID: "late", Role: RoleUser})).To(HaveKeyWithValue(FeatureAgents, false))
	})

	It("leaves the new features off by default for a new user", func() {
		db, err := InitDB(":memory:")
		Expect(err).NotTo(HaveOccurred())
		u := newUser("new", RoleUser)
		Expect(db.Create(u).Error).To(Succeed())
		for _, f := range []string{FeatureAgents, FeatureTriggers, FeatureConnectors, FeatureMCPJobs} {
			Expect(HasFeatureAccess(db, u, f)).To(BeFalse(), f)
		}
		Expect(HasFeatureAccess(db, u, FeatureChats)).To(BeTrue())
		Expect(HasFeatureAccess(db, newUser("admin", RoleAdmin), FeatureTriggers)).To(BeTrue())
	})
})
