// SPDX-License-Identifier: MIT
//go:build auth

package addons_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/http/routes"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const routeSecret = "addon-route-secret"

var _ = Describe("registered addon routes with real authentication", func() {
	var (
		e    *echo.Echo
		app  *application.Application
		keys map[string]string
	)
	BeforeEach(func() {
		dir := GinkgoT().TempDir()
		models, backends := filepath.Join(dir, "models"), filepath.Join(dir, "backends")
		Expect(os.Mkdir(models, 0750)).To(Succeed())
		Expect(os.Mkdir(backends, 0750)).To(Succeed())
		state, err := system.GetSystemState(system.WithBackendPath(backends), system.WithModelPath(models))
		Expect(err).NotTo(HaveOccurred())
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		app, err = application.New(config.WithContext(ctx), config.WithSystemState(state), config.WithAuthEnabled(true), config.WithAuthDatabaseURL(":memory:"), config.WithAuthAPIKeyHMACSecret(routeSecret))
		Expect(err).NotTo(HaveOccurred())
		gdb := app.AuthDB()
		keys = map[string]string{}
		for id, role := range map[string]string{"admin": auth.RoleAdmin, "plain": auth.RoleUser, "granted": auth.RoleUser} {
			Expect(gdb.Create(&auth.User{ID: id, Email: id + "@example.test", Name: id, Provider: auth.ProviderLocal, Role: role, Status: auth.StatusActive}).Error).To(Succeed())
			token, _, err := auth.CreateAPIKey(gdb, id, "test", role, routeSecret, nil)
			Expect(err).NotTo(HaveOccurred())
			keys[id] = token
		}
		Expect(gdb.Create(&auth.UserPermission{ID: uuid.NewString(), UserID: "granted", Permissions: auth.PermissionMap{auth.FeatureAddons: true}}).Error).To(Succeed())
		e = echo.New()
		e.Use(auth.Middleware(gdb, app.ApplicationConfig()))
		routes.RegisterAddonRoutes(e, app)
	})
	call := func(path, action string, mutate func(*http.Request)) *httptest.ResponseRecorder {
		body := `{"addonId":"sample","operationId":"op","generation":1,"action":"` + action + `","arguments":{}}`
		if path == "/api/addons/relay" {
			body = `{"request":{"addonId":"sample","operationId":"op","generation":1,"action":"operation-status","arguments":{}},"kind":"status"}`
		}
		r := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
		r.Header.Set("Content-Type", "application/json")
		mutate(r)
		w := httptest.NewRecorder()
		e.ServeHTTP(w, r)
		return w
	}
	bearer := func(id string) func(*http.Request) {
		return func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+keys[id]) }
	}
	forged := func(r *http.Request) {
		for _, h := range []string{"X-Forwarded-Actor", "X-Addon-Actor", "X-Forwarded-User", "X-Addon-Peer", "X-Remote-User"} {
			r.Header.Set(h, "admin")
		}
	}
	// The test application has no addon gateway: a request that clears every
	// gate reaches the handler and reports the addons as unavailable.
	const reached = http.StatusServiceUnavailable

	It("rejects anonymous callers and forged forwarding headers", func() {
		for _, path := range []string{"/api/addons/dispatch", "/api/addons/relay"} {
			Expect(call(path, "read", forged).Code).To(Equal(http.StatusUnauthorized), path)
		}
		w := call("/api/addons/dispatch", "enable", func(r *http.Request) { forged(r); bearer("plain")(r) })
		Expect(w.Code).To(Equal(http.StatusForbidden))
	})
	It("gates the feature and then administration for named keys", func() {
		for _, path := range []string{"/api/addons/dispatch", "/api/addons/relay"} {
			Expect(call(path, "read", bearer("plain")).Code).To(Equal(http.StatusForbidden), "feature gate "+path)
			Expect(call(path, "read", bearer("granted")).Code).To(Equal(reached), path)
		}
		Expect(call("/api/addons/dispatch", "enable", bearer("granted")).Code).To(Equal(http.StatusForbidden))
		for _, path := range []string{"/api/addons/dispatch", "/api/addons/relay"} {
			Expect(call(path, "enable", bearer("admin")).Code).To(Equal(reached), path)
		}
		Expect(call("/api/addons/dispatch", "enable", func(r *http.Request) { forged(r); bearer("admin")(r) }).Code).To(Equal(reached))
	})
	It("requires same-origin browser requests for cookie mutations", func() {
		session, err := auth.CreateSession(app.AuthDB(), "admin", routeSecret)
		Expect(err).NotTo(HaveOccurred())
		browser := func(site string) func(*http.Request) {
			return func(r *http.Request) {
				r.AddCookie(&http.Cookie{Name: "session", Value: session})
				if site != "" {
					r.Header.Set("Sec-Fetch-Site", site)
				}
			}
		}
		for _, site := range []string{"cross-site", "same-site", "none", ""} {
			// CSRF protection answers 400 or 403; either way the handler is not reached.
			Expect(call("/api/addons/dispatch", "enable", browser(site)).Code).To(BeElementOf(http.StatusBadRequest, http.StatusForbidden), "Sec-Fetch-Site "+site)
		}
		Expect(call("/api/addons/dispatch", "enable", browser("same-origin")).Code).To(Equal(reached))
		// Bearer credentials are not ambient browser authority.
		Expect(call("/api/addons/dispatch", "enable", func(r *http.Request) { bearer("admin")(r); r.Header.Set("Sec-Fetch-Site", "cross-site") }).Code).To(Equal(reached))
	})
	It("registers only the dispatch and relay mutations", func() {
		got := map[string]bool{}
		for _, r := range e.Routes() {
			if strings.HasPrefix(r.Path, "/api/addons") && r.Method == http.MethodPost {
				got[r.Method+" "+r.Path] = true
			}
		}
		Expect(got).To(Equal(map[string]bool{"POST /api/addons/dispatch": true, "POST /api/addons/relay": true}))
	})
})
