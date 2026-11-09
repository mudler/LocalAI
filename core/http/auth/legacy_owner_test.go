// SPDX-License-Identifier: MIT
//go:build auth

package auth_test

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
)

// newStorageScopeApp answers every request with the storage user ID the
// middleware resolved, so the specs observe exactly what the per-user stores
// (collections, fine-tuning and quantization jobs) receive.
func newStorageScopeApp(db *gorm.DB, appConfig *config.ApplicationConfig) *echo.Echo {
	e := echo.New()
	e.Use(auth.Middleware(db, appConfig))
	e.GET("/scope", func(c echo.Context) error {
		return c.String(http.StatusOK, "["+auth.GetStorageUserID(c)+"]")
	})
	return e
}

var _ = Describe("Legacy owner storage scope", func() {
	const hmacSecret = "test-hmac-secret"
	var (
		db    *gorm.DB
		owner *auth.User
		bob   *auth.User
	)

	BeforeEach(func() {
		db = testDB()
		owner = createTestUser(db, "Owner@Example.test", auth.RoleAdmin, auth.ProviderLocal)
		bob = createTestUser(db, "bob@example.test", auth.RoleUser, auth.ProviderLocal)
	})

	session := func(userID string) string {
		id, err := auth.CreateSession(db, userID, hmacSecret)
		Expect(err).NotTo(HaveOccurred())
		return id
	}

	appWithOwner := func(email string) *echo.Echo {
		appConfig := config.NewApplicationConfig()
		appConfig.Auth.APIKeyHMACSecret = hmacSecret
		appConfig.Auth.LegacyOwnerEmail = email
		return newStorageScopeApp(db, appConfig)
	}

	It("finds the legacy owner's account for the chats conversion, and only an admin", func() {
		id, err := auth.LegacyOwnerID(db, " owner@EXAMPLE.test ")
		Expect(err).NotTo(HaveOccurred())
		Expect(id).To(Equal(owner.ID))
		id, err = auth.LegacyOwnerID(db, "bob@example.test")
		Expect(err).NotTo(HaveOccurred())
		Expect(id).To(BeEmpty(), "a regular user must not receive the root items")
		id, err = auth.LegacyOwnerID(db, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(id).To(BeEmpty())
	})

	It("gives the legacy owner the root storage area over a session", func() {
		rec := doRequest(appWithOwner("owner@example.test"), http.MethodGet, "/scope",
			withSessionCookie(session(owner.ID)))
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(rec.Body.String()).To(Equal("[]"))
	})

	It("gives the legacy owner the root storage area over one of their API keys", func() {
		key, _, err := auth.CreateAPIKey(db, owner.ID, "consumer", owner.Role, hmacSecret, nil)
		Expect(err).NotTo(HaveOccurred())
		rec := doRequest(appWithOwner(" OWNER@example.test "), http.MethodGet, "/scope", withBearerToken(key))
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(rec.Body.String()).To(Equal("[]"))
	})

	It("keeps every other account in its own storage area", func() {
		rec := doRequest(appWithOwner("owner@example.test"), http.MethodGet, "/scope",
			withSessionCookie(session(bob.ID)))
		Expect(rec.Body.String()).To(Equal("[" + bob.ID + "]"))
	})

	It("changes nothing when no legacy owner is configured", func() {
		rec := doRequest(appWithOwner(""), http.MethodGet, "/scope",
			withSessionCookie(session(owner.ID)))
		Expect(rec.Body.String()).To(Equal("[" + owner.ID + "]"))
	})

	It("does not treat the synthetic legacy API key user as the owner", func() {
		appConfig := config.NewApplicationConfig()
		appConfig.ApiKeys = []string{"legacy-key"}
		appConfig.Auth.LegacyOwnerEmail = "owner@example.test"
		rec := doRequest(newStorageScopeApp(db, appConfig), http.MethodGet, "/scope", withBearerToken("legacy-key"))
		Expect(rec.Body.String()).To(Equal("[legacy-api-key]"))
	})

	It("matches the owner by e-mail and the admin role", func() {
		Expect(auth.IsLegacyOwner(&auth.User{Email: "owner@example.test", Role: auth.RoleAdmin}, "Owner@Example.test")).To(BeTrue())
		Expect(auth.IsLegacyOwner(&auth.User{Email: "owner@example.test", Role: auth.RoleUser}, "owner@example.test")).To(BeFalse())
		Expect(auth.IsLegacyOwner(&auth.User{Email: "", Role: auth.RoleAdmin}, "")).To(BeFalse())
		Expect(auth.IsLegacyOwner(&auth.User{Email: "owner@example.test.evil", Role: auth.RoleAdmin}, "owner@example.test")).To(BeFalse())
		Expect(auth.IsLegacyOwner(nil, "owner@example.test")).To(BeFalse())
	})

	It("keeps an OIDC identity with the owner's unverified e-mail in its own area", func() {
		// The OAuth callback stores the IdP's e-mail even when it is not
		// verified, but only grants the admin role on a verified one. Such an
		// identity is an active user with the owner's address and role "user".
		mallory := createTestUser(db, "owner@example.test", auth.RoleUser, auth.ProviderOIDC)
		rec := doRequest(appWithOwner("owner@example.test"), http.MethodGet, "/scope",
			withSessionCookie(session(mallory.ID)))
		Expect(rec.Body.String()).To(Equal("[" + mallory.ID + "]"))
		key, _, err := auth.CreateAPIKey(db, mallory.ID, "k", mallory.Role, hmacSecret, nil)
		Expect(err).NotTo(HaveOccurred())
		rec = doRequest(appWithOwner("owner@example.test"), http.MethodGet, "/scope", withBearerToken(key))
		Expect(rec.Body.String()).To(Equal("[" + mallory.ID + "]"))
	})
})
