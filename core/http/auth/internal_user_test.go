//go:build auth

package auth_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
)

// In-process callers (the studio loop) reach routes as a user they already
// authenticated, with no credential to present. The marker lives in the Go
// request context, which a network request cannot carry. It replaces
// authentication only: every authorization check still runs.
var _ = Describe("internal user marker", func() {
	var (
		db   *gorm.DB
		e    *echo.Echo
		user *auth.User
	)

	whoami := func(c echo.Context) error {
		u := auth.GetUser(c)
		if u == nil {
			return c.String(http.StatusOK, "anonymous")
		}
		return c.String(http.StatusOK, u.ID+"|"+auth.GetSource(c))
	}

	withInternalUser := func(u *auth.User) func(*http.Request) {
		return func(req *http.Request) {
			*req = *req.WithContext(auth.WithInternalUser(context.Background(), u))
		}
	}

	BeforeEach(func() {
		db = testDB()
		user = createTestUser(db, "alice@example.com", auth.RoleUser, "local")
		e = newAuthTestApp(db, &config.ApplicationConfig{})
		e.POST("/v1/studio-whoami", whoami)
		e.POST("/api/motion/sessions", ok)
	})

	It("rejects a request that has no credential and no marker", func() {
		rec := doRequest(e, http.MethodPost, "/v1/studio-whoami")
		Expect(rec.Code).To(Equal(http.StatusUnauthorized))
	})

	It("lets an in-process request through as the given user, with the chats source", func() {
		rec := doRequest(e, http.MethodPost, "/v1/studio-whoami", withInternalUser(user))
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(rec.Body.String()).To(Equal(user.ID + "|" + auth.UsageSourceChats))
	})

	It("ignores identity headers that a client could send", func() {
		rec := doRequest(e, http.MethodPost, "/v1/studio-whoami", func(req *http.Request) {
			req.Header.Set("X-Auth-User", user.ID)
			req.Header.Set("X-Forwarded-User", user.ID)
		})
		Expect(rec.Code).To(Equal(http.StatusUnauthorized))
	})

	It("does not trust a marker for a user who is not active", func() {
		user.Status = auth.StatusPending
		rec := doRequest(e, http.MethodPost, "/v1/studio-whoami", withInternalUser(user))
		Expect(rec.Code).To(Equal(http.StatusUnauthorized))
	})

	It("still applies the feature gate: a user denied a feature cannot reach it", func() {
		Expect(auth.UpdateUserPermissions(db, user.ID, auth.PermissionMap{auth.FeatureMotion: false})).To(Succeed())
		viaMarker := doRequest(e, http.MethodPost, "/api/motion/sessions", withInternalUser(user))
		token := createTestSession(db, user.ID)
		viaSession := doRequest(e, http.MethodPost, "/api/motion/sessions", withBearerToken(token))
		Expect(viaSession.Code).To(Equal(http.StatusForbidden))
		Expect(viaMarker.Code).To(Equal(viaSession.Code))
	})

	It("works over a real http.Request built by a client transport", func() {
		req := httptest.NewRequest(http.MethodPost, "/v1/studio-whoami", strings.NewReader("{}"))
		req = req.WithContext(auth.WithInternalUser(req.Context(), user))
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec.Code).To(Equal(http.StatusOK))
	})
})
