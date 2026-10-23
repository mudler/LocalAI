// SPDX-License-Identifier: MIT
//go:build auth

package application

import (
	"path/filepath"

	"github.com/mudler/LocalAI/core/http/auth"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the feature check", func() {
	It("follows the account permissions", func() {
		db, err := auth.InitDB(filepath.Join(GinkgoT().TempDir(), "auth.db"))
		Expect(err).ToNot(HaveOccurred())
		mk := func(id, role, status string) {
			Expect(db.Create(&auth.User{ID: id, Email: id + "@example.com", Role: role, Status: status}).Error).To(Succeed())
		}
		mk("admin", auth.RoleAdmin, auth.StatusActive)
		mk("plain", auth.RoleUser, auth.StatusActive)
		mk("granted", auth.RoleUser, auth.StatusActive)
		mk("gone", auth.RoleAdmin, "disabled")
		Expect(auth.UpdateUserPermissions(db, "granted", auth.PermissionMap{"fine_tuning": true})).To(Succeed())
		Expect(auth.UpdateUserPermissions(db, "plain", auth.PermissionMap{"fine_tuning": false})).To(Succeed())
		has := addonFeatureCheck(db)
		for user, want := range map[string]bool{"admin": true, "granted": true, "plain": false, "gone": false, "nobody": false, "": false} {
			Expect(has(user, "fine_tuning")).To(Equal(want), "user %q", user)
		}
	})
})
