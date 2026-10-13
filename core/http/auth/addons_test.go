// SPDX-License-Identifier: MIT
//go:build auth

package auth_test

import (
	"context"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"path/filepath"
)

var _ = Describe("addon identity", func() {
	It("rechecks authoritative paused revoked and unavailable credentials", func() {
		db, e := auth.InitDB(filepath.Join(GinkgoT().TempDir(), "auth.db"))
		Expect(e).NotTo(HaveOccurred())
		user := auth.User{ID: "u", Role: auth.RoleAdmin, Status: auth.StatusActive}
		Expect(db.Create(&user).Error).To(Succeed())
		_, key, e := auth.CreateAPIKey(db, user.ID, "test", auth.RoleAdmin, "secret", nil)
		Expect(e).NotTo(HaveOccurred())
		a := contract.Actor{UserID: user.ID, CredentialID: "key:" + key.ID, Mode: "named-user"}
		Expect(auth.ValidateAddonActor(context.Background(), db, a, true)).To(Succeed())
		Expect(auth.SetAPIKeyPause(db, key.ID, user.ID, true, nil)).To(Succeed())
		Expect(auth.ValidateAddonActor(context.Background(), db, a, true)).NotTo(Succeed())
		Expect(auth.SetAPIKeyPause(db, key.ID, user.ID, false, nil)).To(Succeed())
		Expect(auth.RevokeAPIKey(db, key.ID, user.ID)).To(Succeed())
		Expect(auth.ValidateAddonActor(context.Background(), db, a, true)).NotTo(Succeed())
		Expect(auth.ValidateAddonActor(context.Background(), nil, a, true)).NotTo(Succeed())
	})
})
