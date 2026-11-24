//go:build auth

// SPDX-License-Identifier: MIT
package auth_test

import (
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"net/http"
	"net/http/httptest"
)

var _ = Describe("Motion feature authorization", func() {
	It("denies revoked feature access before a WebSocket upgrade", func() {
		db := testDB()
		user := createTestUser(db, "motion@example.test", auth.RoleUser, auth.ProviderLocal)
		token := createTestSession(db, user.ID)
		Expect(auth.UpdateUserPermissions(db, user.ID, auth.PermissionMap{auth.FeatureMotion: false})).To(Succeed())
		e := newAuthTestApp(db, &config.ApplicationConfig{})
		e.GET("/api/motion/sessions/:id/poses", func(c echo.Context) error { return c.NoContent(204) })
		request := func() int {
			r := httptest.NewRequest(http.MethodGet, "/api/motion/sessions/test/poses", nil)
			r.Header.Set("Authorization", "Bearer "+token)
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, r)
			return rec.Code
		}
		Expect(request()).To(Equal(403))
		Expect(auth.UpdateUserPermissions(db, user.ID, auth.PermissionMap{auth.FeatureMotion: true})).To(Succeed())
		Expect(request()).To(Equal(204))
	})
})

var _ = Describe("WebSocket ticket authorization", func() {
	It("rechecks feature permission and session revocation at redemption", func() {
		db := testDB()
		user := createTestUser(db, "ticket@example.test", auth.RoleUser, auth.ProviderLocal)
		token := createTestSession(db, user.ID)
		alternateToken := createTestSession(db, user.ID)
		cfg := config.NewApplicationConfig()
		tickets := auth.NewWebSocketTickets()
		e := echo.New()
		e.Use(auth.WithWebSocketTickets(tickets, auth.Middleware(db, cfg)))
		e.Use(auth.RequireRouteFeature(db))
		var issued auth.WebSocketTicketResponse
		e.POST("/api/motion/sessions/:id/tickets", func(c echo.Context) error {
			var err error
			issued, err = tickets.Issue(c, "/api/motion/sessions/test/poses", "https://consumer.example")
			if err != nil {
				return err
			}
			return c.NoContent(201)
		})
		e.GET("/api/motion/sessions/:id/poses", func(c echo.Context) error { return c.NoContent(204) })
		issue := func() {
			r := httptest.NewRequest("POST", "/api/motion/sessions/test/tickets", nil)
			// The cookie authenticates first. The unrelated valid bearer must not
			// be retained as a fallback when that cookie session is revoked.
			r.AddCookie(&http.Cookie{Name: "session", Value: token})
			r.Header.Set("Authorization", "Bearer "+alternateToken)
			w := httptest.NewRecorder()
			e.ServeHTTP(w, r)
			Expect(w.Code).To(Equal(201))
		}
		redeem := func() int {
			r := httptest.NewRequest("GET", "/api/motion/sessions/test/poses", nil)
			r.Header.Set("Origin", "https://consumer.example")
			r.Header.Set("Connection", "Upgrade")
			r.Header.Set("Upgrade", "websocket")
			r.Header.Set("Sec-WebSocket-Protocol", "localai.motion.v2, "+auth.WebSocketTicketProtocolPrefix+issued.Ticket)
			w := httptest.NewRecorder()
			e.ServeHTTP(w, r)
			return w.Code
		}
		issue()
		Expect(auth.UpdateUserPermissions(db, user.ID, auth.PermissionMap{auth.FeatureMotion: false})).To(Succeed())
		Expect(redeem()).To(Equal(403))
		Expect(auth.UpdateUserPermissions(db, user.ID, auth.PermissionMap{auth.FeatureMotion: true})).To(Succeed())
		issue()
		Expect(auth.DeleteSession(db, token, "")).To(Succeed())
		Expect(redeem()).To(Equal(401))
	})
})
