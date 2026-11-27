//go:build auth

package auth_test

import (
	"context"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
)

var _ = Describe("Device sessions", func() {
	const secret = "device-session-secret"
	var (
		db   *gorm.DB
		user *auth.User
	)

	BeforeEach(func() {
		db = testDB()
		user = createTestUser(db, "phone@example.com", auth.RoleUser, auth.ProviderLocal)
	})

	It("creates a session that carries the device and validates like any other", func() {
		token, err := auth.CreateDeviceSession(db, user.ID, "dev-1", secret)
		Expect(err).ToNot(HaveOccurred())
		Expect(token).To(HaveLen(64))

		u, s := auth.ValidateSession(db, token, secret)
		Expect(u).ToNot(BeNil())
		Expect(u.ID).To(Equal(user.ID))
		Expect(s.DeviceID).ToNot(BeNil())
		Expect(*s.DeviceID).To(Equal("dev-1"))
	})

	It("keeps one live session per device: a new one replaces the earlier ones", func() {
		first, err := auth.CreateDeviceSession(db, user.ID, "dev-1", secret)
		Expect(err).ToNot(HaveOccurred())
		other, err := auth.CreateDeviceSession(db, user.ID, "dev-2", secret)
		Expect(err).ToNot(HaveOccurred())
		web, err := auth.CreateSession(db, user.ID, secret)
		Expect(err).ToNot(HaveOccurred())

		second, err := auth.CreateDeviceSession(db, user.ID, "dev-1", secret)
		Expect(err).ToNot(HaveOccurred())

		u, _ := auth.ValidateSession(db, first, secret)
		Expect(u).To(BeNil(), "the earlier session of the device ends")
		u, _ = auth.ValidateSession(db, second, secret)
		Expect(u).ToNot(BeNil())
		u, _ = auth.ValidateSession(db, other, secret)
		Expect(u).ToNot(BeNil(), "another device keeps its session")
		u, _ = auth.ValidateSession(db, web, secret)
		Expect(u).ToNot(BeNil(), "a normal session stays")
		var n int64
		Expect(db.Model(&auth.Session{}).Where("device_id = ?", "dev-1").Count(&n).Error).To(Succeed())
		Expect(n).To(BeEquivalentTo(1))
	})

	It("leaves the device empty on a normal session", func() {
		token, err := auth.CreateSession(db, user.ID, secret)
		Expect(err).ToNot(HaveOccurred())
		_, s := auth.ValidateSession(db, token, secret)
		Expect(s).ToNot(BeNil())
		Expect(s.DeviceID).To(BeNil())
	})

	It("refuses an empty device", func() {
		_, err := auth.CreateDeviceSession(db, user.ID, "", secret)
		Expect(err).To(HaveOccurred())
	})

	It("ends the sessions of one device and no other", func() {
		a, err := auth.CreateDeviceSession(db, user.ID, "dev-1", secret)
		Expect(err).ToNot(HaveOccurred())
		b, err := auth.CreateDeviceSession(db, user.ID, "dev-2", secret)
		Expect(err).ToNot(HaveOccurred())
		c, err := auth.CreateSession(db, user.ID, secret)
		Expect(err).ToNot(HaveOccurred())

		Expect(auth.DeleteDeviceSessions(db, "dev-1")).To(Succeed())

		u, _ := auth.ValidateSession(db, a, secret)
		Expect(u).To(BeNil())
		u, _ = auth.ValidateSession(db, b, secret)
		Expect(u).ToNot(BeNil())
		u, _ = auth.ValidateSession(db, c, secret)
		Expect(u).ToNot(BeNil())
	})

	It("does nothing for an empty device", func() {
		c, err := auth.CreateSession(db, user.ID, secret)
		Expect(err).ToNot(HaveOccurred())
		Expect(auth.DeleteDeviceSessions(db, "")).To(Succeed())
		u, _ := auth.ValidateSession(db, c, secret)
		Expect(u).ToNot(BeNil())
	})

	It("keeps the device across a rotation, so a revoke still ends the session", func() {
		token, err := auth.CreateDeviceSession(db, user.ID, "dev-1", secret)
		Expect(err).ToNot(HaveOccurred())
		_, s := auth.ValidateSession(db, token, secret)
		Expect(s).ToNot(BeNil())
		s.RotatedAt = time.Now().Add(-2 * time.Hour)

		rotated, err := auth.RotateSession(db, s, secret)
		Expect(err).ToNot(HaveOccurred())
		_, rs := auth.ValidateSession(db, rotated, secret)
		Expect(rs).ToNot(BeNil())
		Expect(rs.DeviceID).ToNot(BeNil())
		Expect(*rs.DeviceID).To(Equal("dev-1"))

		Expect(auth.DeleteDeviceSessions(db, "dev-1")).To(Succeed())
		u, _ := auth.ValidateSession(db, rotated, secret)
		Expect(u).To(BeNil())
	})

	It("creates nothing when the session was deleted before the rotation", func() {
		token, err := auth.CreateDeviceSession(db, user.ID, "dev-1", secret)
		Expect(err).ToNot(HaveOccurred())
		_, s := auth.ValidateSession(db, token, secret)
		Expect(s).ToNot(BeNil())

		// The revoke runs after the middleware loaded the session.
		Expect(auth.DeleteDeviceSessions(db, "dev-1")).To(Succeed())

		rotated, err := auth.RotateSession(db, s, secret)
		Expect(err).To(HaveOccurred())
		Expect(rotated).To(BeEmpty())
		var n int64
		Expect(db.Model(&auth.Session{}).Where("device_id = ?", "dev-1").Count(&n).Error).To(Succeed())
		Expect(n).To(BeZero())
	})
})

var _ = Describe("Device sessions in the auth middleware", func() {
	var (
		db        *gorm.DB
		user      *auth.User
		appConfig *config.ApplicationConfig
		app       *echo.Echo
		token     string
		// check is the device check that the P2P handler puts in the context
		// of a request; nil means the request came on the normal listener.
		check auth.DeviceSessionCheck
	)

	BeforeEach(func() {
		db = testDB()
		user = createTestUser(db, "phone-mw@example.com", auth.RoleUser, auth.ProviderLocal)
		appConfig = config.NewApplicationConfig()
		appConfig.Auth.Enabled = true
		appConfig.Auth.APIKeyHMACSecret = "device-session-mw-secret"
		check = nil
		app = echo.New()
		app.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
			return func(c echo.Context) error {
				if check != nil {
					r := c.Request()
					c.SetRequest(r.WithContext(auth.WithDeviceSessionCheck(r.Context(), check)))
				}
				return next(c)
			}
		})
		app.Use(auth.Middleware(db, appConfig))
		app.GET("/v1/models", ok)
		var err error
		token, err = auth.CreateDeviceSession(db, user.ID, "dev-1", appConfig.Auth.APIKeyHMACSecret)
		Expect(err).ToNot(HaveOccurred())
	})

	It("refuses a device session cookie on the normal listener", func() {
		rec := doRequest(app, http.MethodGet, "/v1/models", withSessionCookie(token))
		Expect(rec.Code).To(Equal(http.StatusUnauthorized))
	})

	It("refuses a device session bearer token on the normal listener", func() {
		rec := doRequest(app, http.MethodGet, "/v1/models", withBearerToken(token))
		Expect(rec.Code).To(Equal(http.StatusUnauthorized))
	})

	It("accepts a device session when the device check passes", func() {
		var asked string
		check = func(_ context.Context, deviceID string) bool { asked = deviceID; return true }
		rec := doRequest(app, http.MethodGet, "/v1/models", withSessionCookie(token))
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(asked).To(Equal("dev-1"))
	})

	It("refuses a device session when the device check fails", func() {
		check = func(context.Context, string) bool { return false }
		rec := doRequest(app, http.MethodGet, "/v1/models", withSessionCookie(token))
		Expect(rec.Code).To(Equal(http.StatusUnauthorized))
		rec = doRequest(app, http.MethodGet, "/v1/models", withBearerToken(token))
		Expect(rec.Code).To(Equal(http.StatusUnauthorized))
	})

	It("does not ask the device check for a normal session", func() {
		normal, err := auth.CreateSession(db, user.ID, appConfig.Auth.APIKeyHMACSecret)
		Expect(err).ToNot(HaveOccurred())
		rec := doRequest(app, http.MethodGet, "/v1/models", withSessionCookie(normal))
		Expect(rec.Code).To(Equal(http.StatusOK))
		check = func(context.Context, string) bool { Fail("asked for a normal session"); return false }
		rec = doRequest(app, http.MethodGet, "/v1/models", withSessionCookie(normal))
		Expect(rec.Code).To(Equal(http.StatusOK))
	})
})
