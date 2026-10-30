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
// (agents, collections, skills, jobs, chat history) receive.
func newStorageScopeApp(db *gorm.DB, appConfig *config.ApplicationConfig) *echo.Echo {
	e := echo.New()
	e.Use(auth.Middleware(db, appConfig))
	scope := func(c echo.Context) error {
		return c.String(http.StatusOK, "["+auth.GetStorageUserID(c)+"]")
	}
	e.GET("/scope", scope)
	// The routes the agent pool exception is about: the one it needs (knowledge
	// base search) and root data it must never reach (chat history).
	e.POST("/api/agents/collections/:name/search", scope)
	e.GET("/api/conversations", scope)
	e.DELETE("/api/conversations", scope)
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

var _ = Describe("Agent pool calls for root agents", func() {
	const hmacSecret = "test-hmac-secret"
	var (
		db      *gorm.DB
		service *auth.User
		poolKey string
		bobKey  string
	)

	BeforeEach(func() {
		db = testDB()
		service = createTestUser(db, "agents@example.test", auth.RoleUser, auth.ProviderLocal)
		bob := createTestUser(db, "bob@example.test", auth.RoleUser, auth.ProviderLocal)
		var err error
		poolKey, _, err = auth.CreateAPIKey(db, service.ID, "agent pool", service.Role, hmacSecret, nil)
		Expect(err).NotTo(HaveOccurred())
		bobKey, _, err = auth.CreateAPIKey(db, bob.ID, "consumer", bob.Role, hmacSecret, nil)
		Expect(err).NotTo(HaveOccurred())
	})

	app := func(owner string) *echo.Echo {
		appConfig := config.NewApplicationConfig()
		appConfig.Auth.APIKeyHMACSecret = hmacSecret
		appConfig.Auth.LegacyOwnerEmail = owner
		appConfig.AgentPool.APIKey = poolKey
		return newStorageScopeApp(db, appConfig)
	}

	const search = "/api/agents/collections/kb/search"

	It("gives the pool's own key the root area for a knowledge base search when a legacy owner is configured", func() {
		rec := doRequest(app("owner@example.test"), http.MethodPost, search, withBearerToken(poolKey))
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(rec.Body.String()).To(Equal("[]"))
	})

	It("never gives the pool's key the root chat history (read or delete) or any other route", func() {
		for _, r := range []struct{ method, path string }{
			{http.MethodGet, "/api/conversations"},
			{http.MethodDelete, "/api/conversations"},
			{http.MethodGet, "/scope"},
		} {
			rec := doRequest(app("owner@example.test"), r.method, r.path, withBearerToken(poolKey))
			Expect(rec.Body.String()).To(Equal("["+service.ID+"]"), r.method+" "+r.path)
		}
	})

	It("keeps the pool's key in its own area when the call names a user", func() {
		rec := doRequest(app("owner@example.test"), http.MethodPost, search+"?user_id=someone", withBearerToken(poolKey))
		Expect(rec.Body.String()).To(Equal("[" + service.ID + "]"))
	})

	It("changes nothing for the pool's key without a legacy owner", func() {
		rec := doRequest(app(""), http.MethodPost, search, withBearerToken(poolKey))
		Expect(rec.Body.String()).To(Equal("[" + service.ID + "]"))
	})

	It("does not extend the root area to other keys of other accounts", func() {
		rec := doRequest(app("owner@example.test"), http.MethodPost, search, withBearerToken(bobKey))
		Expect(rec.Body.String()).NotTo(Equal("[]"))
	})
})
