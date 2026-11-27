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

// A user's stored permission map overrides the default of a feature only for
// the keys it names. These specs cover what the admin Users sheet relies on:
// it reads the stored keys, sends them back with only the changed ones, and
// leaves every other feature following its default.
var _ = Describe("Admin user permissions routes", Label("auth"), func() {
	var (
		db           *gorm.DB
		e            *echo.Echo
		adminSession string
		user         *auth.User
	)

	BeforeEach(func() {
		var err error
		db, err = auth.InitDB(":memory:")
		Expect(err).NotTo(HaveOccurred())
		appConfig := config.NewApplicationConfig()
		appConfig.Auth.Enabled = true

		admin := createRouteTestUser(db, "up-admin@test.com", auth.RoleAdmin)
		user = createRouteTestUser(db, "up-user@test.com", auth.RoleUser)
		adminSession, err = auth.CreateSession(db, admin.ID, "")
		Expect(err).NotTo(HaveOccurred())

		e = echo.New()
		e.Use(auth.Middleware(db, appConfig))
		adminMw := auth.RequireAdmin()
		e.GET("/api/auth/admin/users", routes.AdminListUsersHandler(db), adminMw)
		e.GET("/api/auth/admin/users/:id/permissions", routes.AdminUserPermissionsHandler(db), adminMw)
		e.PUT("/api/auth/admin/users/:id/permissions", routes.AdminSetUserPermissionsHandler(db), adminMw)
		e.PUT("/api/auth/admin/features/:feature/default", routes.AdminSetFeatureDefaultHandler(db), adminMw)
	})

	type permissionsResponse struct {
		Permissions         map[string]bool `json:"permissions"`
		ExplicitPermissions map[string]bool `json:"explicit_permissions"`
	}

	getPermissions := func() permissionsResponse {
		rec := doAuthRequest(e, "GET", "/api/auth/admin/users/"+user.ID+"/permissions", nil, withSession(adminSession))
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		var resp permissionsResponse
		Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
		return resp
	}

	putPermissions := func(body string) {
		rec := doAuthRequest(e, "PUT", "/api/auth/admin/users/"+user.ID+"/permissions", []byte(body), withSession(adminSession))
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
	}

	setDefault := func(enabled bool) {
		body := `{"enabled":false}`
		if enabled {
			body = `{"enabled":true}`
		}
		rec := doAuthRequest(e, "PUT", "/api/auth/admin/features/p2p_pair/default", []byte(body), withSession(adminSession))
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
	}

	It("returns the stored explicit keys beside the effective map", func() {
		Expect(auth.UpdateUserPermissions(db, user.ID, auth.PermissionMap{auth.FeatureChat: false})).To(Succeed())

		resp := getPermissions()
		Expect(resp.ExplicitPermissions).To(Equal(map[string]bool{auth.FeatureChat: false}))
		Expect(resp.Permissions).To(HaveKeyWithValue(auth.FeatureChat, false))
		Expect(resp.Permissions).To(HaveKeyWithValue(auth.FeatureP2PPair, false))
		Expect(resp.Permissions).To(HaveKey(auth.FeatureMCP))
	})

	It("returns an empty explicit map for a user with no stored permissions", func() {
		resp := getPermissions()
		Expect(resp.ExplicitPermissions).NotTo(BeNil())
		Expect(resp.ExplicitPermissions).To(BeEmpty())
	})

	It("lists the stored explicit keys of each user", func() {
		Expect(auth.UpdateUserPermissions(db, user.ID, auth.PermissionMap{auth.FeatureMCP: true})).To(Succeed())

		rec := doAuthRequest(e, "GET", "/api/auth/admin/users", nil, withSession(adminSession))
		Expect(rec.Code).To(Equal(http.StatusOK))
		var resp struct {
			Users []struct {
				ID                  string          `json:"id"`
				ExplicitPermissions map[string]bool `json:"explicit_permissions"`
			} `json:"users"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &resp)).To(Succeed())
		found := false
		for _, u := range resp.Users {
			if u.ID == user.ID {
				found = true
				Expect(u.ExplicitPermissions).To(Equal(map[string]bool{auth.FeatureMCP: true}))
			}
		}
		Expect(found).To(BeTrue())
	})

	It("leaves a feature the PUT does not name following the default", func() {
		putPermissions(`{"mcp":true}`)
		Expect(getPermissions().ExplicitPermissions).NotTo(HaveKey(auth.FeatureP2PPair))

		setDefault(true)
		Expect(auth.HasFeatureAccess(db, user, auth.FeatureP2PPair)).To(BeTrue())
		Expect(getPermissions().Permissions).To(HaveKeyWithValue(auth.FeatureP2PPair, true))

		setDefault(false)
		Expect(auth.HasFeatureAccess(db, user, auth.FeatureP2PPair)).To(BeFalse())
	})

	It("returns the user to the default when a key is removed", func() {
		putPermissions(`{"mcp":true,"p2p_pair":false}`)
		setDefault(true)
		Expect(auth.HasFeatureAccess(db, user, auth.FeatureP2PPair)).To(BeFalse())

		putPermissions(`{"mcp":true}`)
		resp := getPermissions()
		Expect(resp.ExplicitPermissions).To(Equal(map[string]bool{auth.FeatureMCP: true}))
		Expect(resp.Permissions).To(HaveKeyWithValue(auth.FeatureP2PPair, true))
		Expect(auth.HasFeatureAccess(db, user, auth.FeatureP2PPair)).To(BeTrue())
	})

	It("treats a null value as a removed key", func() {
		putPermissions(`{"mcp":true,"p2p_pair":false}`)
		putPermissions(`{"mcp":true,"p2p_pair":null}`)
		Expect(getPermissions().ExplicitPermissions).To(Equal(map[string]bool{auth.FeatureMCP: true}))
	})
})
