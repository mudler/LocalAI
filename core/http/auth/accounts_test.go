// SPDX-License-Identifier: MIT
//go:build auth

package auth_test

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/google/uuid"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the account check of resource kinds that are not add-ons", func() {
	bg := context.Background()

	It("checks the account and the credential, and a feature only when one is named", func() {
		db, e := auth.InitDB(filepath.Join(GinkgoT().TempDir(), "auth.db"))
		Expect(e).NotTo(HaveOccurred())
		actors := map[string]contract.Actor{}
		for id, perms := range map[string]auth.PermissionMap{
			"plain":  {auth.FeatureAddons: false},
			"chat":   {auth.FeatureAddons: false, auth.FeatureChat: true},
			"nochat": {auth.FeatureChat: false},
		} {
			Expect(db.Create(&auth.User{ID: id, Role: auth.RoleUser, Status: auth.StatusActive}).Error).To(Succeed())
			Expect(db.Create(&auth.UserPermission{ID: uuid.NewString(), UserID: id, Permissions: perms}).Error).To(Succeed())
			_, key, e := auth.CreateAPIKey(db, id, "test", auth.RoleUser, "secret", nil)
			Expect(e).NotTo(HaveOccurred())
			actors[id] = contract.Actor{UserID: id, CredentialID: "key:" + key.ID, Mode: auth.AddonModeNamedUser}
		}
		// An account without any permission row still gets the defaults.
		Expect(db.Create(&auth.User{ID: "fresh", Role: auth.RoleUser, Status: auth.StatusActive}).Error).To(Succeed())
		_, key, e := auth.CreateAPIKey(db, "fresh", "test", auth.RoleUser, "secret", nil)
		Expect(e).NotTo(HaveOccurred())
		actors["fresh"] = contract.Actor{UserID: "fresh", CredentialID: "key:" + key.ID, Mode: auth.AddonModeNamedUser}

		check := auth.AccountRevalidate(db, auth.AddonModeNamedUser)
		addon := auth.AddonRevalidate(db, auth.AddonModeNamedUser)
		for _, id := range []string{"plain", "chat", "nochat", "fresh"} {
			Expect(check(bg, actors[id], "")).To(Succeed(), id)
			Expect(addon(bg, actors[id])).To(MatchError(gateway.ErrForbidden), id)
		}
		Expect(check(bg, actors["chat"], auth.FeatureChat)).To(Succeed())
		Expect(check(bg, actors["nochat"], auth.FeatureChat)).To(MatchError(gateway.ErrForbidden))
		// chat is on by default for an account that has no setting for it.
		Expect(check(bg, actors["plain"], auth.FeatureChat)).To(Succeed())
		Expect(check(bg, actors["fresh"], auth.FeatureChat)).To(Succeed())
		Expect(check(bg, actors["plain"], auth.FeatureAddons)).To(MatchError(gateway.ErrForbidden))

		// A credential that is not the account's, a paused account and the
		// synthetic principals are refused whatever the feature.
		wrong := actors["plain"]
		wrong.CredentialID = actors["chat"].CredentialID
		Expect(check(bg, wrong, "")).To(MatchError(gateway.ErrForbidden))
		Expect(db.Model(&auth.User{}).Where("id = ?", "chat").Update("status", auth.StatusDisabled).Error).To(Succeed())
		Expect(check(bg, actors["chat"], "")).To(MatchError(gateway.ErrForbidden))
		for _, id := range []string{"legacy-api-key", auth.AddonLocalOwnerID, ""} {
			Expect(check(bg, contract.Actor{UserID: id, CredentialID: "key:x", Mode: auth.AddonModeNamedUser}, "")).To(MatchError(gateway.ErrForbidden), id)
		}

		// The local owner is the only caller of local-owner mode.
		local := auth.AccountRevalidate(nil, auth.AddonModeLocalOwner)
		Expect(local(bg, contract.Actor{UserID: auth.AddonLocalOwnerID, CredentialID: auth.AddonLocalOwnerID, Mode: auth.AddonModeLocalOwner}, "")).To(Succeed())
		Expect(local(bg, actors["plain"], "")).To(MatchError(gateway.ErrForbidden))
		Expect(auth.AccountRevalidate(db, "other")(bg, actors["plain"], "")).To(MatchError(gateway.ErrForbidden))

		// A store that cannot answer is unavailable, never a refusal.
		sqlDB, e := db.DB()
		Expect(e).NotTo(HaveOccurred())
		Expect(sqlDB.Close()).To(Succeed())
		err := check(bg, actors["plain"], "")
		Expect(errors.Is(err, gateway.ErrUnavailable)).To(BeTrue(), "%v", err)
	})

	It("searches every active named account by a prefix of the display name only, without the add-ons filter", func() {
		db, e := auth.InitDB(filepath.Join(GinkgoT().TempDir(), "auth.db"))
		Expect(e).NotTo(HaveOccurred())
		for _, u := range []auth.User{
			{ID: "frank", Name: "Frank", Email: "frank@example.test", Status: auth.StatusActive, Role: auth.RoleUser},
			{ID: "fred", Name: "", Email: "fred@example.test", Status: auth.StatusActive, Role: auth.RoleUser},
			{ID: "blank", Name: "   ", Email: "ceo@example.test", Status: auth.StatusActive, Role: auth.RoleUser},
			{ID: "fay", Name: "Fay", Email: "fay@example.test", Status: auth.StatusDisabled, Role: auth.RoleUser},
			{ID: "admin", Name: "Fiona Admin", Email: "root@example.test", Status: auth.StatusActive, Role: auth.RoleAdmin},
			{ID: auth.AddonLocalOwnerID, Name: "Fake", Email: "f@example.test", Status: auth.StatusActive, Role: auth.RoleUser},
			{ID: "legacy-api-key", Name: "Fake", Email: "fl@example.test", Status: auth.StatusActive, Role: auth.RoleUser},
			{ID: "pct", Name: "F%x", Email: "pct@example.test", Status: auth.StatusActive, Role: auth.RoleUser},
		} {
			Expect(db.Create(&u).Error).To(Succeed())
		}
		users := auth.NewAddonUsers(db)
		ids := func(q string, limit int) []string {
			found, err := users.Accounts(bg, q, limit)
			Expect(err).NotTo(HaveOccurred())
			out := []string{}
			for _, u := range found {
				out = append(out, u.ID)
			}
			return out
		}
		Expect(ids("f", 20)).To(ConsistOf("frank", "admin", "pct"))
		Expect(ids("FR", 20)).To(ConsistOf("frank"))
		Expect(ids("f%", 20)).To(Equal([]string{"pct"}))
		Expect(ids("f", 2)).To(HaveLen(2))
		// An address prefix finds nobody, so the search is no oracle of who
		// has an account.
		for _, q := range []string{"fred", "fred@", "fred@example", "ceo@", "ceo@exam", "root", "frank@"} {
			Expect(ids(q, 20)).To(BeEmpty(), q)
		}
		// An account without a display name is found by its whole address
		// only, and shown with no name: the dialog gives it a neutral label.
		found, err := users.Accounts(bg, "FRED@example.test", 20)
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(Equal([]gateway.UserRef{{ID: "fred", DisplayName: ""}}))
		Expect(ids("ceo@example.test", 20)).To(Equal([]string{"blank"}))
		// An account with a name is not found by its address.
		Expect(ids("frank@example.test", 20)).To(BeEmpty())
		// None of them may use add-ons, so the add-on search finds only the
		// administrator.
		addon, err := users.Search(bg, "f", 20)
		Expect(err).NotTo(HaveOccurred())
		Expect(addon).To(Equal([]gateway.UserRef{{ID: "admin", DisplayName: "Fiona Admin"}}))

		var nilUsers *auth.AddonUsers
		_, err = nilUsers.Accounts(bg, "fr", 20)
		Expect(err).To(MatchError(gateway.ErrUnavailable))
	})
})
