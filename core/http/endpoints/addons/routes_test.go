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
	It("registers only the dispatch, relay and proposal mutations", func() {
		got := map[string]bool{}
		for _, r := range e.Routes() {
			if strings.HasPrefix(r.Path, "/api/addons") && r.Method == http.MethodPost {
				got[r.Method+" "+r.Path] = true
			}
		}
		Expect(got).To(Equal(map[string]bool{
			"POST /api/addons/dispatch":                true,
			"POST /api/addons/relay":                   true,
			"POST /api/addons/proposals":               true,
			"POST /api/addons/proposals/:id/confirm":   true,
			"POST /api/addons/proposals/:id/cancel":    true,
			"POST /api/addons/proposals/:id/reconcile": true,
			"POST /api/addons/proposals/:id/resolve":   true,
		}))
		// Reading a proposal and the owner's list of unresolved ones are the
		// only registered GETs.
		reads := map[string]bool{}
		for _, r := range e.Routes() {
			if strings.HasPrefix(r.Path, "/api/addons") && r.Method == http.MethodGet {
				reads[r.Path] = true
			}
		}
		Expect(reads).To(Equal(map[string]bool{"/api/addons/proposals/:id": true, "/api/addons/resources/:resourceId/proposals": true}))
	})
	It("gates the proposal routes by feature and refuses a confirmation from a key before the owner is asked", func() {
		do := func(method, path, body string, mutate func(*http.Request)) int {
			r := httptest.NewRequest(method, path, bytes.NewBufferString(body))
			r.Header.Set("Content-Type", "application/json")
			mutate(r)
			w := httptest.NewRecorder()
			e.ServeHTTP(w, r)
			return w.Code
		}
		propose := `{"addonId":"sample","resourceId":"r","operationId":"op","generation":1,"action":"write","arguments":{}}`
		Expect(do("POST", "/api/addons/proposals", propose, func(r *http.Request) {})).To(Equal(http.StatusUnauthorized))
		Expect(do("POST", "/api/addons/proposals", propose, bearer("plain"))).To(Equal(http.StatusForbidden))
		Expect(do("GET", "/api/addons/proposals/op", "", bearer("plain"))).To(Equal(http.StatusForbidden))
		Expect(do("GET", "/api/addons/resources/r/proposals?state=unresolved", "", bearer("plain"))).To(Equal(http.StatusForbidden))
		// A key, even a granted one, is never a confirmation surface.
		Expect(do("GET", "/api/addons/resources/r/proposals?state=unresolved", "", bearer("granted"))).To(Equal(http.StatusForbidden))
		// A granted key clears the gates; the test application has no owner.
		Expect(do("POST", "/api/addons/proposals", propose, bearer("granted"))).To(Equal(reached))
		Expect(do("GET", "/api/addons/proposals/op", "", bearer("granted"))).To(Equal(reached))
		Expect(do("POST", "/api/addons/proposals/op/reconcile", "", bearer("granted"))).To(Equal(reached))
		// A confirmation by a key, even an administrator's, is a denial and is
		// never forwarded.
		for _, id := range []string{"admin", "granted"} {
			Expect(do("POST", "/api/addons/proposals/op/confirm", `{"digest":"d"}`, bearer(id))).To(Equal(http.StatusForbidden), id)
		}
	})
})

var _ = Describe("explicit local-owner mode on the registered routes", func() {
	build := func(mode string, authOn bool) (*echo.Echo, *application.Application) {
		dir := GinkgoT().TempDir()
		models, backends := filepath.Join(dir, "models"), filepath.Join(dir, "backends")
		Expect(os.Mkdir(models, 0750)).To(Succeed())
		Expect(os.Mkdir(backends, 0750)).To(Succeed())
		state, err := system.GetSystemState(system.WithBackendPath(backends), system.WithModelPath(models))
		Expect(err).NotTo(HaveOccurred())
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		opts := []config.AppOption{config.WithContext(ctx), config.WithSystemState(state), config.WithAddons(config.AddonConfig{OwnershipMode: mode})}
		if authOn {
			opts = append(opts, config.WithAuthEnabled(true), config.WithAuthDatabaseURL(":memory:"), config.WithAuthAPIKeyHMACSecret(routeSecret))
		}
		app, err := application.New(opts...)
		Expect(err).NotTo(HaveOccurred())
		e := echo.New()
		e.Use(auth.Middleware(app.AuthDB(), app.ApplicationConfig()))
		routes.RegisterAddonRoutes(e, app)
		return e, app
	}
	post := func(e *echo.Echo, action string, mutate func(*http.Request)) int {
		body := `{"addonId":"sample","operationId":"op","generation":1,"action":"` + action + `","arguments":{}}`
		r := httptest.NewRequest(http.MethodPost, "/api/addons/dispatch", bytes.NewBufferString(body))
		r.Header.Set("Content-Type", "application/json")
		if mutate != nil {
			mutate(r)
		}
		w := httptest.NewRecorder()
		e.ServeHTTP(w, r)
		return w.Code
	}
	// The test application has no addon gateway: a request that clears every
	// gate reaches the handler and reports the addons as unavailable.
	const reached = http.StatusServiceUnavailable

	It("lets callers through only when the deployment selected local-owner mode", func() {
		named, app := build("", false)
		Expect(app.AddonOwnershipMode()).To(Equal("named-user"))
		Expect(post(named, "read", nil)).To(Equal(http.StatusUnauthorized))
		Expect(post(named, "enable", nil)).To(Equal(http.StatusUnauthorized))
		explicit, app := build("named-user", false)
		Expect(app.AddonOwnershipMode()).To(Equal("named-user"))
		Expect(post(explicit, "read", nil)).To(Equal(http.StatusUnauthorized))

		local, app := build("local-owner", false)
		Expect(app.AddonOwnershipMode()).To(Equal("local-owner"))
		Expect(post(local, "read", nil)).To(Equal(reached))
		Expect(post(local, "enable", nil)).To(Equal(reached))
		// A forged named actor header changes nothing in either mode.
		forged := func(r *http.Request) { r.Header.Set("X-Forwarded-Actor", "admin") }
		Expect(post(named, "read", forged)).To(Equal(http.StatusUnauthorized))
		// An unknown mode never grants anything.
		bogus, _ := build("everyone", false)
		Expect(post(bogus, "read", nil)).To(Equal(http.StatusUnauthorized))
	})

	It("keeps authentication, the feature gate and administration when accounts exist", func() {
		e, app := build("local-owner", true)
		gdb := app.AuthDB()
		keys := map[string]string{}
		for id, role := range map[string]string{"admin": auth.RoleAdmin, "plain": auth.RoleUser, "granted": auth.RoleUser} {
			Expect(gdb.Create(&auth.User{ID: id, Email: id + "@example.test", Name: id, Provider: auth.ProviderLocal, Role: role, Status: auth.StatusActive}).Error).To(Succeed())
			token, _, err := auth.CreateAPIKey(gdb, id, "test", role, routeSecret, nil)
			Expect(err).NotTo(HaveOccurred())
			keys[id] = token
		}
		Expect(gdb.Create(&auth.UserPermission{ID: uuid.NewString(), UserID: "granted", Permissions: auth.PermissionMap{auth.FeatureAddons: true}}).Error).To(Succeed())
		bearer := func(id string) func(*http.Request) {
			return func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+keys[id]) }
		}
		Expect(post(e, "read", nil)).To(Equal(http.StatusUnauthorized))
		Expect(post(e, "read", bearer("plain"))).To(Equal(http.StatusForbidden))
		Expect(post(e, "read", bearer("granted"))).To(Equal(reached))
		Expect(post(e, "enable", bearer("granted"))).To(Equal(http.StatusForbidden))
		Expect(post(e, "enable", bearer("admin"))).To(Equal(reached))
	})
})
