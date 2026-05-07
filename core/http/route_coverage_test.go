//go:build auth

package http_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
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

// Every API-prefixed route registered by API() must either reject anonymous
// traffic with 401 or appear on the explicit public allowlist below. The
// test fails on routes that ship without an auth decision; adding a new
// public surface should be deliberate, not a side effect.
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

	It("rejects anonymous traffic on every API route except the explicit allowlist", func() {
		// Routes that are intentionally reachable without authentication.
		// Each entry needs a justification comment.
		expectedPublicPrefixes := []string{
			// Auth flow itself — login, registration, OAuth callbacks
			"/api/auth/",

			// Distributed-mode node self-service: authenticated by a
			// registration token presented in the request body, not by the
			// global auth middleware. Verified separately in node tests.
			"/api/node/register",

			// Branding read for login screen + public branding asset server.
			// Mutating /api/branding/asset/* routes are exempt from global
			// auth but admin-gated by route-level middleware
			// (TestBrandingRoutes_AdminGatingHolds pins that contract).
			"/api/branding",
			"/branding/",

			// Health and metadata used by orchestrators / load balancers
			"/healthz",
			"/readyz",

			// Static asset surfaces used by the UI before login
			"/favicon.svg",
			"/static/",
			"/assets/",
			"/locales/",
			"/generated-audio/",
			"/generated-images/",
			"/generated-videos/",
		}
		expectedPublicExact := map[string]bool{
			// SPA shell + redirects — UI handles login client-side
			"/":            true,
			"/app":         true,
			"/browse":      true,
			"/swagger":     true,
			"/swagger/":    true,
			"/swagger/*":   true,
			"/oauth/start": true,
		}

		// Per-route exemptions for distributed-mode node endpoints whose
		// pattern carries a path param (so they don't fit a flat prefix).
		// These authenticate via registration token at the handler layer.
		nodeSelfPattern := regexp.MustCompile(`^/api/node/[^/]+/(heartbeat|drain|deregister)$`)

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

		isAPI := func(p string) bool {
			apiPrefixes := []string{
				"/api/", "/v1/", "/models/", "/backends/", "/backend/",
				"/tts", "/vad", "/video", "/stores/", "/system",
				"/ws/", "/generated-", "/chat/", "/completions",
				"/edits", "/embeddings", "/audio/", "/images/",
				"/messages", "/responses",
			}
			if p == "/metrics" {
				return true
			}
			for _, pre := range apiPrefixes {
				if strings.HasPrefix(p, pre) {
					return true
				}
			}
			return false
		}

		isAllowlisted := func(p string) bool {
			if expectedPublicExact[p] {
				return true
			}
			for _, pre := range expectedPublicPrefixes {
				if strings.HasPrefix(p, pre) {
					return true
				}
			}
			if nodeSelfPattern.MatchString(p) {
				return true
			}
			return false
		}

		var leaks []string
		seen := map[string]bool{}
		for _, r := range app.Routes() {
			// Echo registers automatic HEAD routes for GETs; auth check is
			// identical, so dedupe.
			key := r.Method + " " + r.Path
			if seen[key] {
				continue
			}
			seen[key] = true

			// Only inspect API surface — UI/static paths are intentionally
			// reachable for SPA hydration before login.
			if !isAPI(r.Path) {
				continue
			}
			if isAllowlisted(r.Path) {
				continue
			}

			req := httptest.NewRequest(r.Method, concretize(r.Path), nil)
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)

			// We accept:
			//   401: middleware rejected — the desired outcome
			//   404: route not actually reachable in this minimal config
			//        (e.g. distributed-only routes); not an auth leak
			//   405: method not allowed; auth never ran but isn't a leak
			if rec.Code == http.StatusUnauthorized ||
				rec.Code == http.StatusNotFound ||
				rec.Code == http.StatusMethodNotAllowed {
				continue
			}

			leaks = append(leaks, "  "+r.Method+" "+r.Path+
				" → "+http.StatusText(rec.Code)+
				" (got "+strconv.Itoa(rec.Code)+")")
		}

		if len(leaks) > 0 {
			Fail("Routes reachable without authentication:\n" +
				strings.Join(leaks, "\n") +
				"\n\nIf the route is intentionally public, add it to " +
				"expectedPublicPrefixes or expectedPublicExact in " +
				"core/http/route_coverage_test.go with a justification " +
				"comment. Otherwise, gate it behind the auth middleware " +
				"(automatic for /api/, /v1/, /models/, /backends/, etc.) " +
				"or RequireAdmin / RequireFeature.")
		}
	})
})
