// SPDX-License-Identifier: MIT
//go:build auth

package auth_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
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

var _ = Describe("addon account store failures", func() {
	It("reports a store outage as unavailable and a missing or disabled account as forbidden", func() {
		db, e := auth.InitDB(filepath.Join(GinkgoT().TempDir(), "auth.db"))
		Expect(e).NotTo(HaveOccurred())
		active := auth.User{ID: "active", Role: auth.RoleAdmin, Status: auth.StatusActive}
		disabled := auth.User{ID: "disabled", Role: auth.RoleAdmin, Status: "disabled"}
		Expect(db.Create(&active).Error).To(Succeed())
		Expect(db.Create(&disabled).Error).To(Succeed())
		_, key, e := auth.CreateAPIKey(db, active.ID, "test", auth.RoleAdmin, "secret", nil)
		Expect(e).NotTo(HaveOccurred())
		ctx := context.Background()
		actor := func(user, credential string) contract.Actor {
			return contract.Actor{UserID: user, CredentialID: credential, Mode: "named-user"}
		}
		forbidden := func(a contract.Actor) {
			err := auth.ValidateAddonActor(ctx, db, a, false)
			Expect(err).To(HaveOccurred())
			Expect(errors.Is(err, gateway.ErrUnavailable)).To(BeFalse(), a.UserID+" "+a.CredentialID)
		}
		forbidden(actor("missing", "key:"+key.ID))
		forbidden(actor(disabled.ID, "key:"+key.ID))
		forbidden(actor(active.ID, "key:unknown"))
		forbidden(actor(active.ID, "session:unknown"))
		forbidden(actor(auth.AddonLocalOwnerID, "key:"+key.ID))
		Expect(auth.ValidateAddonActor(ctx, db, actor(active.ID, "key:"+key.ID), false)).To(Succeed())
		sqlDB, e := db.DB()
		Expect(e).NotTo(HaveOccurred())
		Expect(sqlDB.Close()).To(Succeed())
		err := auth.ValidateAddonActor(ctx, db, actor(active.ID, "key:"+key.ID), false)
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, gateway.ErrUnavailable)).To(BeTrue())
		Expect(gateway.StatusFor(err)).To(Equal(503))
	})
})

var _ = Describe("addon identity denial classification", func() {
	It("marks every definite account refusal as the shared forbidden sentinel and never as unavailable", func() {
		db, e := auth.InitDB(filepath.Join(GinkgoT().TempDir(), "auth.db"))
		Expect(e).NotTo(HaveOccurred())
		ghost := contract.Actor{UserID: "ghost", CredentialID: "key:none", Mode: "named-user"}
		for name, err := range map[string]error{
			"unknown user":   auth.ValidateAddonActor(context.Background(), db, ghost, false),
			"local owner id": auth.ValidateAddonActor(context.Background(), db, contract.Actor{UserID: auth.AddonLocalOwnerID, CredentialID: "key:1", Mode: "named-user"}, false),
			"bogus mode":     auth.AddonRevalidate(db, "bogus")(context.Background(), ghost),
			"forged owner":   auth.ValidateLocalOwnerActor(ghost),
		} {
			Expect(err).To(HaveOccurred(), name)
			Expect(errors.Is(err, gateway.ErrForbidden)).To(BeTrue(), name)
			Expect(errors.Is(err, gateway.ErrUnavailable)).To(BeFalse(), name)
		}
	})
})

var _ = Describe("addon local-owner identity", func() {
	It("builds the local owner actor only for the explicit mode", func() {
		c := echo.New().NewContext(httptest.NewRequest(http.MethodPost, "/", nil), httptest.NewRecorder())
		// Without a named credential the named mode has no actor at all.
		_, err := auth.AddonActorForMode(c, auth.AddonModeNamedUser)
		Expect(err).To(HaveOccurred())
		_, err = auth.AddonActorForMode(c, "")
		Expect(err).To(HaveOccurred())
		_, err = auth.AddonActorForMode(c, "something-else")
		Expect(err).To(HaveOccurred())
		a, err := auth.AddonActorForMode(c, auth.AddonModeLocalOwner)
		Expect(err).NotTo(HaveOccurred())
		Expect(a).To(Equal(contract.Actor{UserID: auth.AddonLocalOwnerID, CredentialID: auth.AddonLocalOwnerID, Mode: "local-owner"}))
		Expect(auth.ValidateLocalOwnerActor(a)).To(Succeed())
		// Named mode never accepts the synthetic principal, and the local mode
		// never accepts a named one.
		db, e := auth.InitDB(filepath.Join(GinkgoT().TempDir(), "auth.db"))
		Expect(e).NotTo(HaveOccurred())
		Expect(auth.ValidateAddonActor(context.Background(), db, a, false)).NotTo(Succeed())
		named := contract.Actor{UserID: "u", CredentialID: "key:1", Mode: "named-user"}
		Expect(auth.ValidateLocalOwnerActor(named)).NotTo(Succeed())
		forged := a
		forged.UserID = "someone"
		Expect(auth.ValidateLocalOwnerActor(forged)).NotTo(Succeed())
		Expect(auth.AddonRevalidate(db, auth.AddonModeLocalOwner)(context.Background(), a)).To(Succeed())
		Expect(auth.AddonRevalidate(db, auth.AddonModeNamedUser)(context.Background(), a)).NotTo(Succeed())
		Expect(auth.AddonRevalidate(nil, auth.AddonModeLocalOwner)(context.Background(), a)).To(Succeed())
		Expect(auth.AddonRevalidate(db, auth.AddonModeLocalOwner)(context.Background(), named)).NotTo(Succeed())
		Expect(auth.AddonRevalidate(db, "bogus")(context.Background(), a)).NotTo(Succeed())
	})
})
