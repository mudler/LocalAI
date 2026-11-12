//go:build auth

package auth_test

import (
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
)

var _ = Describe("client_inference routes", func() {
	// A key that holds client_inference reaches these routes and no others. The
	// list is spelled out here on purpose: a route added to the registry with
	// the client feature fails this spec until a person adds it below too, so no
	// route widens every key by accident.
	frozen := []string{
		"GET /v1/models",
		"GET /v1/realtime",
		"POST /tts",
		"POST /v1/audio/classification",
		"POST /v1/audio/speech",
		"POST /v1/audio/transcriptions",
		"POST /v1/chat/completions",
	}

	It("freezes the routes of client_inference", func() {
		var got []string
		for _, rf := range auth.RouteFeatureRegistry {
			if rf.Feature == auth.FeatureClientInference {
				got = append(got, rf.Method+" "+rf.Pattern)
			}
		}
		sort.Strings(got)
		Expect(got).To(Equal(frozen))
	})

	It("keeps a user row for every inference route that has one, so users are not widened", func() {
		userRows := map[string]string{}
		for _, rf := range auth.RouteFeatureRegistry {
			if !auth.IsClientFeature(rf.Feature) {
				userRows[rf.Method+" "+rf.Pattern] = rf.Feature
			}
		}
		for _, r := range frozen {
			if r == "GET /v1/models" {
				Expect(userRows).ToNot(HaveKey(r), "listing the models has no user feature")
				continue
			}
			Expect(userRows).To(HaveKey(r), "%s lost its user feature", r)
		}
	})

	Describe("the middleware", func() {
		var (
			db     *gorm.DB
			owner  *auth.User
			e      *echo.Echo
			cfg    *config.ApplicationConfig
			secret = "test-secret"
		)

		BeforeEach(func() {
			db = testDB()
			owner = createTestUser(db, "owner@example.com", auth.RoleAdmin, auth.ProviderLocal)
			cfg = &config.ApplicationConfig{}
			cfg.Auth.Enabled = true
			cfg.Auth.APIKeyHMACSecret = secret
			e = echo.New()
			e.Use(auth.Middleware(db, cfg))
			e.Use(auth.RequireRouteFeature(db))
			e.Use(auth.RequireQuota(db))
			for _, r := range frozen {
				method, path, _ := strings.Cut(r, " ")
				e.Add(method, path, ok)
			}
			for _, r := range []string{
				"POST /v1/embeddings", "POST /v1/audio/diarization", "POST /v1/vad", "POST /v1/sound-generation",
				"POST /audio/transformations", "POST /v1/responses", "POST /v1/realtime/calls",
				"POST /v1/realtime/sessions", "POST /mcp/v1/chat/completions", "GET /api/settings",
				"POST /audio/transcriptions", "GET /models", "POST /v1/voice/embed",
			} {
				method, path, _ := strings.Cut(r, " ")
				e.Add(method, path, ok)
			}
		})

		clientKey := func(features ...string) string {
			var addons, models []string
			for _, f := range features {
				if f == auth.FeatureClientAddons {
					addons = []string{"demo"}
				}
				if f == auth.FeatureClientInference {
					models = []string{"m"}
				}
			}
			code, _, err := auth.CreatePairingCode(db, owner.ID, "c", features, addons, 0, 0, secret, auth.WithModels(models))
			Expect(err).ToNot(HaveOccurred())
			plain, _, err := auth.RedeemPairingCode(db, code, "", secret)
			Expect(err).ToNot(HaveOccurred())
			return plain
		}
		do := func(key, method, path string) int {
			req := httptest.NewRequest(method, path, nil)
			req.Header.Set("Authorization", "Bearer "+key)
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)
			return rec.Code
		}

		doRoute := func(key, route string) int {
			method, path, _ := strings.Cut(route, " ")
			return do(key, method, path)
		}

		It("lets a key with client_inference reach exactly the frozen routes", func() {
			key := clientKey(auth.FeatureClientInference)
			for _, r := range frozen {
				Expect(doRoute(key, r)).To(Equal(http.StatusOK), r)
			}
		})

		It("refuses every other route to that key, deny by default", func() {
			key := clientKey(auth.FeatureClientInference)
			for _, r := range []string{
				"POST /v1/embeddings", "POST /v1/audio/diarization", "POST /v1/vad", "POST /v1/sound-generation",
				"POST /audio/transformations", "POST /v1/responses", "POST /v1/realtime/calls",
				"POST /v1/realtime/sessions", "POST /mcp/v1/chat/completions", "GET /api/settings",
				"POST /audio/transcriptions", "GET /models", "POST /v1/voice/embed", "GET /api/client/self",
			} {
				Expect(doRoute(key, r)).To(Equal(http.StatusForbidden), r)
			}
		})

		It("refuses the inference routes to a key without the feature", func() {
			key := clientKey(auth.FeatureClientSelf)
			for _, r := range frozen {
				Expect(doRoute(key, r)).To(Equal(http.StatusForbidden), r)
			}
		})

		It("does not change what a user needs: the user feature still gates, and listing models is open", func() {
			user := createTestUser(db, "u@example.com", auth.RoleUser, auth.ProviderLocal)
			plain, _, err := auth.CreateAPIKey(db, user.ID, "k", auth.RoleUser, secret, nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(do(plain, "GET", "/v1/models")).To(Equal(http.StatusOK))
			Expect(do(plain, "POST", "/v1/chat/completions")).To(Equal(http.StatusOK)) // default-on feature
			Expect(auth.UpdateUserPermissions(db, user.ID, auth.PermissionMap{auth.FeatureChat: false})).To(Succeed())
			Expect(do(plain, "POST", "/v1/chat/completions")).To(Equal(http.StatusForbidden))
			// A client feature in the map of a user opens nothing for the user.
			Expect(auth.UpdateUserPermissions(db, user.ID, auth.PermissionMap{auth.FeatureChat: false, auth.FeatureClientInference: true})).To(Succeed())
			Expect(do(plain, "POST", "/v1/chat/completions")).To(Equal(http.StatusForbidden))
		})
	})
})
