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

	It("matches the owner by e-mail only", func() {
		Expect(auth.IsLegacyOwner(&auth.User{Email: "owner@example.test"}, "Owner@Example.test")).To(BeTrue())
		Expect(auth.IsLegacyOwner(&auth.User{Email: ""}, "")).To(BeFalse())
		Expect(auth.IsLegacyOwner(&auth.User{Email: "owner@example.test.evil"}, "owner@example.test")).To(BeFalse())
		Expect(auth.IsLegacyOwner(nil, "owner@example.test")).To(BeFalse())
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

	It("gives the pool's own key the root storage area when a legacy owner is configured", func() {
		rec := doRequest(app("owner@example.test"), http.MethodGet, "/scope", withBearerToken(poolKey))
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(rec.Body.String()).To(Equal("[]"))
	})

	It("keeps the pool's key in its own area when the call names a user", func() {
		rec := doRequest(app("owner@example.test"), http.MethodGet, "/scope?user_id=someone", withBearerToken(poolKey))
		Expect(rec.Body.String()).To(Equal("[" + service.ID + "]"))
	})

	It("changes nothing for the pool's key without a legacy owner", func() {
		rec := doRequest(app(""), http.MethodGet, "/scope", withBearerToken(poolKey))
		Expect(rec.Body.String()).To(Equal("[" + service.ID + "]"))
	})

	It("does not extend the root area to other keys of other accounts", func() {
		rec := doRequest(app("owner@example.test"), http.MethodGet, "/scope", withBearerToken(bobKey))
		Expect(rec.Body.String()).NotTo(Equal("[]"))
	})
})
