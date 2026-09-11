//go:build auth

package http_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	. "github.com/mudler/LocalAI/core/http"
	"github.com/mudler/LocalAI/pkg/system"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Every route registered by API() must either reject anonymous traffic with
// 401 or appear on the explicit public allowlist below. The test fails on
// routes that ship without an auth decision; adding a new public surface
// should be deliberate, not a side effect.
var _ = Describe("Route auth coverage", func() {
	var (
		app     *echo.Echo
		tmpdir  string
		c       context.Context
		cancel  context.CancelFunc
		appInst *application.Application
	)

	BeforeEach(func() {
		var err error
		tmpdir, err = os.MkdirTemp("", "route-coverage-")
		Expect(err).ToNot(HaveOccurred())

		modelDir := filepath.Join(tmpdir, "models")
		Expect(os.Mkdir(modelDir, 0750)).To(Succeed())
		bDir := filepath.Join(tmpdir, "backends")
		Expect(os.Mkdir(bDir, 0750)).To(Succeed())

		c, cancel = context.WithCancel(context.Background())

		systemState, err := system.GetSystemState(
			system.WithBackendPath(bDir),
			system.WithModelPath(modelDir),
		)
		Expect(err).ToNot(HaveOccurred())

		// Auth enabled, no legacy keys, no admin user pre-created. With auth
		// enabled the global middleware MUST reject anonymous API requests
		// regardless of admin presence.
		appInst, err = application.New(
			config.WithContext(c),
			config.WithSystemState(systemState),
			config.WithAuthEnabled(true),
			config.WithAuthDatabaseURL(":memory:"),
			config.WithAuthAPIKeyHMACSecret("test-secret-for-route-coverage"),
		)
		Expect(err).ToNot(HaveOccurred())

		app, err = API(appInst)
		Expect(err).ToNot(HaveOccurred())
	})

	AfterEach(func() {
		cancel()
		Expect(os.RemoveAll(tmpdir)).To(Succeed())
	})

	It("enforces the anonymous-access decision for every registered route", func() {
		type routePattern struct {
			method string
			path   string
		}

		// This allowlist deliberately restates the public contract instead of
		// importing the production registry, so drift in either direction fails.
		expectedPublicRoutes := map[routePattern]struct{}{
			// Discovery used before clients have credentials.
			{method: http.MethodGet, path: "/.well-known/localai.json"}: {},
			{method: http.MethodGet, path: "/api/instructions"}:         {},
			{method: http.MethodGet, path: "/api/instructions/:name"}:   {},
			{method: http.MethodGet, path: "/swagger"}:                  {},
			{method: http.MethodGet, path: "/swagger/"}:                 {},
			{method: http.MethodGet, path: "/swagger/index.html"}:       {},
			{method: http.MethodGet, path: "/swagger/*"}:                {},

			// Orchestrator health probes.
			{method: http.MethodGet, path: "/healthz"}: {},
			{method: http.MethodGet, path: "/readyz"}:  {},

			// Authentication bootstrap endpoints only; authenticated account and
			// admin operations under /api/auth/ remain protected.
			{method: http.MethodGet, path: "/api/auth/status"}:          {},
			{method: http.MethodPost, path: "/api/auth/token-login"}:    {},
			{method: http.MethodPost, path: "/api/auth/register"}:       {},
			{method: http.MethodPost, path: "/api/auth/login"}:          {},
			{method: http.MethodGet, path: "/api/auth/github/login"}:    {},
			{method: http.MethodGet, path: "/api/auth/github/callback"}: {},
			{method: http.MethodGet, path: "/api/auth/oidc/login"}:      {},
			{method: http.MethodGet, path: "/api/auth/oidc/callback"}:   {},

			// SPA shell and client-side navigation before login.
			{method: http.MethodGet, path: "/"}:             {},
			{method: http.MethodHead, path: "/"}:            {},
			{method: http.MethodGet, path: "/app"}:          {},
			{method: http.MethodGet, path: "/app/*"}:        {},
			{method: http.MethodGet, path: "/browse"}:       {},
			{method: http.MethodGet, path: "/browse/*"}:     {},
			{method: http.MethodGet, path: "/login"}:        {},
			{method: http.MethodGet, path: "/invite/:code"}: {},
			{method: http.MethodGet, path: "/explorer"}:     {},

			// Static assets needed to render the pre-authentication UI.
			{method: http.MethodGet, path: "/favicon.svg"}: {},
			{method: http.MethodGet, path: "/assets/*"}:    {},
			{method: http.MethodGet, path: "/locales/*"}:   {},
			{method: http.MethodGet, path: "/static/*"}:    {},

			// Branding reads used by the login screen. Branding mutations are
			// intentionally absent and must receive 401.
			{method: http.MethodGet, path: "/api/branding"}:         {},
			{method: http.MethodGet, path: "/branding/asset/:kind"}: {},
		}

		// Concretize a route pattern into a URL suitable for httptest.
		// Echo path params come back as ":name" and wildcards as "*".
		concretize := func(pattern string) string {
			parts := strings.Split(pattern, "/")
			for i, p := range parts {
				if strings.HasPrefix(p, ":") {
					parts[i] = "test"
				} else if p == "*" {
					parts[i] = "test"
				}
			}
			return strings.Join(parts, "/")
		}

		isAllowlisted := func(method, path string) bool {
			_, ok := expectedPublicRoutes[routePattern{method: method, path: path}]
			if ok {
				return true
			}

			// CORS preflight may be represented as a route by some Echo
			// configurations. The method restriction keeps the rest of the auth
			// namespace private.
			return method == http.MethodOptions && strings.HasPrefix(path, "/api/auth/")
		}

		leaks := []string{}
		blockedPublicRoutes := []string{}
		seen := map[string]bool{}
		for _, r := range app.Routes() {
			// Echo registers automatic HEAD routes for GETs; auth check is
			// identical, so dedupe.
			key := r.Method + " " + r.Path
			if seen[key] {
				continue
			}
			seen[key] = true

			req := httptest.NewRequest(r.Method, concretize(r.Path), nil)
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)

			if isAllowlisted(r.Method, r.Path) {
				if rec.Code == http.StatusUnauthorized {
					blockedPublicRoutes = append(blockedPublicRoutes, "  "+r.Method+" "+r.Path)
				}
				continue
			}

			if rec.Code == http.StatusUnauthorized {
				continue
			}

			leaks = append(leaks, "  "+r.Method+" "+r.Path+
				" → "+http.StatusText(rec.Code)+
				" (got "+strconv.Itoa(rec.Code)+")")
		}

		if len(leaks) > 0 || len(blockedPublicRoutes) > 0 {
			Fail("Routes reachable without authentication:\n" +
				strings.Join(leaks, "\n") +
				"\n\nPublic routes unexpectedly requiring authentication:\n" +
				strings.Join(blockedPublicRoutes, "\n") +
				"\n\nIf a route is intentionally public, add its exact method and Echo " +
				"pattern to expectedPublicRoutes in core/http/route_coverage_test.go " +
				"with a justification comment. Otherwise, keep it behind the " +
				"global auth middleware or RequireAdmin / RequireFeature.")
		}
	})
})
