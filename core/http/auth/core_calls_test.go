//go:build auth

// SPDX-License-Identifier: MIT

package auth_test

import (
	"net/http"
	"net/http/httptest"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
)

// An add-on page calls core routes with the signed-in user's own session and
// the browser's same-origin metadata. These cases pin that the existing
// middleware, not the add-on, decides.
var _ = Describe("core routes called from an add-on page", func() {
	var (
		db     *gorm.DB
		e      *echo.Echo
		member *auth.User
		admin  *auth.User
	)
	call := func(method, path string, user *auth.User) int {
		req := httptest.NewRequest(method, path, nil)
		req.AddCookie(&http.Cookie{Name: "session", Value: createTestSession(db, user.ID)})
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		return rec.Code
	}

	BeforeEach(func() {
		db = testDB()
		e = echo.New()
		e.Use(auth.Middleware(db, config.NewApplicationConfig()))
		e.Use(auth.RequireRouteFeature(db))
		e.Use(auth.CSRFMiddleware())
		e.POST("/v1/face/verify", ok)
		e.POST("/api/voice-profiles", ok, auth.RequireAdmin())
		e.POST("/api/fine-tuning/jobs", ok)
		e.POST("/api/quantization/jobs", ok)
		member = createTestUser(db, "member@example.com", auth.RoleUser, auth.ProviderLocal)
		admin = createTestUser(db, "admin@example.com", auth.RoleAdmin, auth.ProviderLocal)
	})

	It("answers 403 for a user whose face_recognition feature is off", func() {
		Expect(auth.UpdateUserPermissions(db, member.ID, auth.PermissionMap{auth.FeatureFaceRecognition: false})).To(Succeed())
		Expect(call(http.MethodPost, "/v1/face/verify", member)).To(Equal(http.StatusForbidden))
	})

	It("answers 403 for a user whose fine_tuning feature is off", func() {
		Expect(auth.UpdateUserPermissions(db, member.ID, auth.PermissionMap{auth.FeatureFineTuning: false})).To(Succeed())
		Expect(call(http.MethodPost, "/api/fine-tuning/jobs", member)).To(Equal(http.StatusForbidden))
	})

	It("answers 403 for a user whose quantization feature is off", func() {
		Expect(auth.UpdateUserPermissions(db, member.ID, auth.PermissionMap{auth.FeatureQuantization: false})).To(Succeed())
		Expect(call(http.MethodPost, "/api/quantization/jobs", member)).To(Equal(http.StatusForbidden))
	})

	It("lets the same user through when the feature is on", func() {
		Expect(call(http.MethodPost, "/v1/face/verify", member)).To(Equal(http.StatusOK))
	})

	It("answers 403 from an administrator-only route for a user without the role", func() {
		Expect(call(http.MethodPost, "/api/voice-profiles", member)).To(Equal(http.StatusForbidden))
	})

	It("keeps an administrator route open to an administrator", func() {
		Expect(call(http.MethodPost, "/api/voice-profiles", admin)).To(Equal(http.StatusOK))
	})

	It("answers 401 when the session is missing", func() {
		req := httptest.NewRequest(http.MethodPost, "/v1/face/verify", nil)
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec.Code).To(Equal(http.StatusUnauthorized))
	})
})
