// SPDX-License-Identifier: MIT
//go:build auth

package routes_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/http/routes"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
)

const navSecret = "addon-navigation-secret"

var _ = Describe("Add-on navigation routes with real authentication", func() {
	var (
		e     *echo.Echo
		app   *application.Application
		token string
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
		app, err = application.New(config.WithContext(ctx), config.WithSystemState(state), config.WithAuthEnabled(true), config.WithAuthDatabaseURL(":memory:"), config.WithAuthAPIKeyHMACSecret(navSecret))
		Expect(err).NotTo(HaveOccurred())
		gdb := app.AuthDB()
		// A member with no permission row and no addons grant.
		Expect(gdb.Create(&auth.User{ID: "member", Email: "member@example.test", Name: "member", Provider: auth.ProviderLocal, Role: auth.RoleUser, Status: auth.StatusActive}).Error).To(Succeed())
		token, _, err = auth.CreateAPIKey(gdb, "member", "test", auth.RoleUser, navSecret, nil)
		Expect(err).NotTo(HaveOccurred())
		e = echo.New()
		e.Use(auth.Middleware(gdb, app.ApplicationConfig()))
		// The order of app.go: the feature group first, then the navigation routes.
		routes.RegisterAddonRoutes(e, app)
		Expect(routes.RegisterAddonNavigationRoutes(e, app)).To(Succeed())
	})
	gdbOf := func(a *application.Application) *gorm.DB { return a.AuthDB() }
	get := func(path string, withKey bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		if withKey {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		e.ServeHTTP(w, r)
		return w
	}

	It("lists studio and talk for a member who lacks the addons feature", func() {
		w := get("/api/addons/builtin", true)
		Expect(w.Code).To(Equal(http.StatusOK), w.Body.String())
		var body struct {
			Builtin []struct {
				ID string `json:"id"`
			} `json:"builtin"`
		}
		Expect(json.Unmarshal(w.Body.Bytes(), &body)).To(Succeed())
		var ids []string
		for _, b := range body.Builtin {
			ids = append(ids, b.ID)
		}
		Expect(ids).To(Equal([]string{"studio", "talk"}))
	})
	It("keeps the addons feature gate on the group and the static route unshadowed", func() {
		Expect(get("/api/addons/apps", true).Code).To(Equal(http.StatusForbidden))
		Expect(get("/api/addons/builtin", true).Code).To(Equal(http.StatusOK))
	})
	It("lets a member without the addons feature read and save pins", func() {
		r := httptest.NewRequest(http.MethodGet, "/api/addons/pins", nil)
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		e.ServeHTTP(w, r)
		Expect(w.Code).To(Equal(http.StatusOK), w.Body.String())
		var got []string
		Expect(json.Unmarshal(w.Body.Bytes(), &got)).To(Succeed())
		// The member existed when the routes registered, so the one-time upgrade
		// seed pinned Studio and Talk for it.
		Expect(got).To(Equal([]string{"studio", "talk"}))

		put := func(sameOrigin bool) *httptest.ResponseRecorder {
			r := httptest.NewRequest(http.MethodPut, "/api/addons/pins", strings.NewReader(`["talk","studio"]`))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Authorization", "Bearer "+token)
			r.AddCookie(&http.Cookie{Name: "session", Value: "x"})
			if sameOrigin {
				r.Header.Set("Sec-Fetch-Site", "same-origin")
			}
			w := httptest.NewRecorder()
			e.ServeHTTP(w, r)
			return w
		}
		Expect(put(false).Code).To(Equal(http.StatusForbidden))
		Expect(put(true).Code).To(Equal(http.StatusOK))

		r = httptest.NewRequest(http.MethodPut, "/api/addons/pins", strings.NewReader(`["talk","studio"]`))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		e.ServeHTTP(w, r)
		Expect(w.Code).To(Equal(http.StatusOK), w.Body.String())
		Expect(json.Unmarshal(w.Body.Bytes(), &got)).To(Succeed())
		Expect(got).To(Equal([]string{"talk", "studio"}))
		Expect(get("/api/addons/apps", true).Code).To(Equal(http.StatusForbidden))
	})
	It("keeps the pins of two users apart", func() {
		Expect(gdbOf(app).Create(&auth.User{ID: "other", Email: "other@example.test", Name: "other", Provider: auth.ProviderLocal, Role: auth.RoleUser, Status: auth.StatusActive}).Error).To(Succeed())
		tokenB, _, err := auth.CreateAPIKey(gdbOf(app), "other", "test", auth.RoleUser, navSecret, nil)
		Expect(err).NotTo(HaveOccurred())
		do := func(method, tok, body string) []string {
			r := httptest.NewRequest(method, "/api/addons/pins", strings.NewReader(body))
			r.Header.Set("Authorization", "Bearer "+tok)
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			e.ServeHTTP(w, r)
			Expect(w.Code).To(Equal(http.StatusOK), w.Body.String())
			var out []string
			Expect(json.Unmarshal(w.Body.Bytes(), &out)).To(Succeed())
			return out
		}
		Expect(do(http.MethodPut, token, `["talk"]`)).To(Equal([]string{"talk"}))
		Expect(do(http.MethodGet, tokenB, "")).To(BeEmpty())
		Expect(do(http.MethodPut, tokenB, `["studio"]`)).To(Equal([]string{"studio"}))
		Expect(do(http.MethodGet, token, "")).To(Equal([]string{"talk"}))
		Expect(do(http.MethodGet, tokenB, "")).To(Equal([]string{"studio"}))
	})
	It("refuses an anonymous pins request", func() {
		Expect(get("/api/addons/pins", false).Code).To(Equal(http.StatusUnauthorized))
	})
	It("fails to register when the pins file is corrupt", func() {
		// The application writes into its data path in the background, so the
		// directory is removed with a retry, not by the one-shot TempDir cleanup.
		dir, err := os.MkdirTemp("", "pins-corrupt")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			Eventually(func() error { return os.RemoveAll(dir) }, "5s", "50ms").Should(Succeed())
		})
		Expect(os.WriteFile(filepath.Join(dir, "addon-pins.json"), []byte("{broken"), 0o600)).To(Succeed())
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		models, backends := filepath.Join(dir, "m"), filepath.Join(dir, "b")
		Expect(os.Mkdir(models, 0750)).To(Succeed())
		Expect(os.Mkdir(backends, 0750)).To(Succeed())
		state, err := system.GetSystemState(system.WithBackendPath(backends), system.WithModelPath(models))
		Expect(err).NotTo(HaveOccurred())
		plain, err := application.New(config.WithContext(ctx), config.WithSystemState(state), config.WithDataPath(dir))
		Expect(err).NotTo(HaveOccurred())
		Expect(routes.RegisterAddonNavigationRoutes(echo.New(), plain)).NotTo(Succeed())
	})
	It("refuses an anonymous caller", func() {
		Expect(get("/api/addons/builtin", false).Code).To(Equal(http.StatusUnauthorized))
	})
	It("reserves every first path segment the navigation routes register", func() {
		nav := echo.New()
		Expect(routes.RegisterAddonNavigationRoutes(nav, app)).To(Succeed())
		Expect(nav.Routes()).NotTo(BeEmpty())
		for _, r := range nav.Routes() {
			rest, ok := strings.CutPrefix(r.Path, "/api/addons/")
			if !ok {
				continue
			}
			first, _, _ := strings.Cut(rest, "/")
			Expect(contract.ReservedAddonID(first)).To(BeTrue(), r.Method+" "+r.Path)
		}
	})
})
