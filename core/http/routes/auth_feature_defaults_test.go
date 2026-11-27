//go:build auth

package routes_test

import (
	"encoding/json"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/http/routes"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
)

var _ = Describe("Admin feature defaults routes", Label("auth"), func() {
	var (
		db           *gorm.DB
		e            *echo.Echo
		adminSession string
		userSession  string
		user         *auth.User
	)

	BeforeEach(func() {
		var err error
		db, err = auth.InitDB(":memory:")
		Expect(err).NotTo(HaveOccurred())
		appConfig := config.NewApplicationConfig()
		appConfig.Auth.Enabled = true

		admin := createRouteTestUser(db, "fd-admin@test.com", auth.RoleAdmin)
		user = createRouteTestUser(db, "fd-user@test.com", auth.RoleUser)
		adminSession, err = auth.CreateSession(db, admin.ID, "")
		Expect(err).NotTo(HaveOccurred())
		userSession, err = auth.CreateSession(db, user.ID, "")
		Expect(err).NotTo(HaveOccurred())

		e = echo.New()
		e.Use(auth.Middleware(db, appConfig))
		adminMw := auth.RequireAdmin()
		e.GET("/api/auth/admin/features", routes.AdminFeaturesHandler(db, func() []string { return []string{"m1"} }), adminMw)
		e.PUT("/api/auth/admin/features/:feature/default", routes.AdminSetFeatureDefaultHandler(db), adminMw)
	})

	findMeta := func(body []byte, group, key string) map[string]any {
		var resp map[string]any
		Expect(json.Unmarshal(body, &resp)).To(Succeed())
		list, ok := resp[group].([]any)
		Expect(ok).To(BeTrue(), group)
		for _, item := range list {
			m := item.(map[string]any)
			if m["key"] == key {
				return m
			}
		}
		Fail("feature " + key + " not found in " + group)
		return nil
	}

	It("reports the effective default and the configurable flag", func() {
		rec := doAuthRequest(e, "GET", "/api/auth/admin/features", nil, withSession(adminSession))
		Expect(rec.Code).To(Equal(http.StatusOK))
		pair := findMeta(rec.Body.Bytes(), "general_features", auth.FeatureP2PPair)
		Expect(pair["default"]).To(BeFalse())
		Expect(pair["configurable"]).To(BeTrue())
		chat := findMeta(rec.Body.Bytes(), "api_features", auth.FeatureChat)
		Expect(chat["default"]).To(BeTrue())
		Expect(chat["configurable"]).To(BeFalse())

		rec = doAuthRequest(e, "PUT", "/api/auth/admin/features/p2p_pair/default", []byte(`{"enabled":true}`), withSession(adminSession))
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())

		rec = doAuthRequest(e, "GET", "/api/auth/admin/features", nil, withSession(adminSession))
		pair = findMeta(rec.Body.Bytes(), "general_features", auth.FeatureP2PPair)
		Expect(pair["default"]).To(BeTrue())
		Expect(auth.HasFeatureAccess(db, user, auth.FeatureP2PPair)).To(BeTrue())
	})

	It("refuses a feature whose default is not configurable", func() {
		rec := doAuthRequest(e, "PUT", "/api/auth/admin/features/chat/default", []byte(`{"enabled":false}`), withSession(adminSession))
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
		Expect(rec.Body.String()).To(ContainSubstring("not configurable"))
		Expect(auth.FeatureDefault(db, auth.FeatureChat)).To(BeTrue())
	})

	It("answers 404 for an unknown feature", func() {
		rec := doAuthRequest(e, "PUT", "/api/auth/admin/features/nope/default", []byte(`{"enabled":true}`), withSession(adminSession))
		Expect(rec.Code).To(Equal(http.StatusNotFound))
	})

	It("needs the enabled field", func() {
		rec := doAuthRequest(e, "PUT", "/api/auth/admin/features/p2p_pair/default", []byte(`{}`), withSession(adminSession))
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
	})

	It("answers 403 to a non-admin and changes nothing", func() {
		rec := doAuthRequest(e, "PUT", "/api/auth/admin/features/p2p_pair/default", []byte(`{"enabled":true}`), withSession(userSession))
		Expect(rec.Code).To(Equal(http.StatusForbidden))
		Expect(auth.FeatureDefault(db, auth.FeatureP2PPair)).To(BeFalse())

		rec = doAuthRequest(e, "GET", "/api/auth/admin/features", nil, withSession(userSession))
		Expect(rec.Code).To(Equal(http.StatusForbidden))
	})

	It("answers 401 without credentials", func() {
		rec := doAuthRequest(e, "PUT", "/api/auth/admin/features/p2p_pair/default", []byte(`{"enabled":true}`))
		Expect(rec.Code).To(Equal(http.StatusUnauthorized))
	})
})
