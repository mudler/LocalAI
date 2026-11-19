//go:build auth

package routes

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	clienthttp "github.com/mudler/LocalAI/core/http/endpoints/client"
	"github.com/mudler/LocalAI/internal"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
)

// testModelCheck stands in for the check of the model configurations: "m1",
// "m2" and "voice" exist, "tools" has tools attached, and nothing else exists.
func testModelCheck(name string) error {
	switch name {
	case "m1", "m2", "voice":
		return nil
	case "tools":
		return config.ErrClientModelTools
	}
	return config.ErrClientModelUnknown
}

var _ = Describe("client routes", func() {
	var (
		db      *gorm.DB
		e       *echo.Echo
		admin   string // session token of the owner
		revoked []string
	)

	do := func(method, path, token string, body any) (int, map[string]any) {
		var rd *bytes.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = bytes.NewReader(b)
		} else {
			rd = bytes.NewReader(nil)
		}
		req := httptest.NewRequest(method, path, rd)
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		e.ServeHTTP(w, req)
		var out map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out
	}

	// pair creates a code through the admin API and redeems it as a client.
	addonsFor := func(features []string) []string {
		if slices.Contains(features, auth.FeatureClientAddons) {
			return []string{"demo"}
		}
		return nil
	}
	pair := func(features ...string) (key, id string) {
		code, resp := do(http.MethodPost, clientCodesPath, admin, map[string]any{"name": "box", "features": features, "addons": addonsFor(features)})
		Expect(code).To(Equal(http.StatusCreated))
		status, out := do(http.MethodPost, clientPairPath, "", map[string]string{"code": resp["code"].(string)})
		Expect(status).To(Equal(http.StatusCreated))
		return out["key"].(string), out["id"].(string)
	}

	BeforeEach(func() {
		revoked = nil
		var err error
		db, err = auth.InitDB(":memory:")
		Expect(err).ToNot(HaveOccurred())
		owner := &auth.User{ID: "owner-1", Email: "o@example.com", Role: auth.RoleAdmin, Status: auth.StatusActive, Provider: auth.ProviderLocal}
		Expect(db.Create(owner).Error).To(Succeed())
		admin, err = auth.CreateSession(db, owner.ID, "")
		Expect(err).ToNot(HaveOccurred())

		e = echo.New()
		e.Use(auth.Middleware(db, &config.ApplicationConfig{}))
		e.Use(auth.RequireRouteFeature(db))
		registerClientRoutes(context.Background(), e, db, "", auth.RequireAdmin(), func() clienthttp.Host { return nil }, func(_ context.Context, o, d string) { revoked = append(revoked, o+"/"+d) }, testModelCheck)
		// Routes that exist elsewhere in the app: none may be open to a client.
		okh := func(c echo.Context) error { return c.String(http.StatusOK, "ok") }
		e.GET("/api/auth/api-keys", okh)
		e.GET("/healthz", okh)
		e.DELETE("/api/auth/api-keys/:id", revokeAPIKeyHandler(db, func(_ context.Context, o, d string) { revoked = append(revoked, o+"/"+d) }))
		e.POST("/api/auth/api-keys", okh)
		e.GET("/api/settings", okh, auth.RequireAdmin())
		e.GET("/v1/models", okh)
		e.POST("/v1/chat/completions", okh)
		e.POST("/api/addons/dispatch", okh)
		e.GET("/api/client/unregistered", okh) // a client route nobody registered
	})

	It("pairs a client and lets it read its own identity", func() {
		key, id := pair(auth.FeatureClientSelf)
		code, out := do(http.MethodGet, clientSelfPath, key, nil)
		Expect(code).To(Equal(http.StatusOK))
		Expect(out["id"]).To(Equal(id))
		Expect(out).ToNot(HaveKey("key"))
	})

	It("refuses a second redemption of the same code with the same answer as a wrong one", func() {
		_, resp := do(http.MethodPost, clientCodesPath, admin, map[string]any{})
		c := resp["code"].(string)
		s1, _ := do(http.MethodPost, clientPairPath, "", map[string]string{"code": c})
		s2, o2 := do(http.MethodPost, clientPairPath, "", map[string]string{"code": c})
		s3, o3 := do(http.MethodPost, clientPairPath, "", map[string]string{"code": "AAAAA-AAAAA"})
		Expect(s1).To(Equal(http.StatusCreated))
		Expect(s2).To(Equal(http.StatusUnauthorized))
		Expect(s3).To(Equal(http.StatusUnauthorized))
		Expect(o2).To(Equal(o3))
	})

	It("refuses a client key everywhere that is not allowed, whatever the owner is", func() {
		key, _ := pair(auth.FeatureClientSelf, auth.FeatureClientAddons)
		for _, r := range []struct{ method, path string }{
			{"GET", "/api/auth/api-keys"},       // would let a client read the keys of its owner
			{"POST", "/api/auth/api-keys"},      // would let a client mint a user key
			{"GET", "/api/settings"},            // admin route
			{"GET", "/v1/models"},               // user route
			{"POST", "/v1/chat/completions"},    // inference
			{"POST", "/api/addons/dispatch"},    // add-on gateway
			{"GET", "/api/client/unregistered"}, // unregistered client route
			{"POST", "/api/client/pairing-codes"},
			{"GET", "/api/client/keys"},
			{"GET", "/no/such/route"},
		} {
			code, _ := do(r.method, r.path, key, map[string]any{})
			Expect(code).To(Equal(http.StatusForbidden), r.method+" "+r.path)
		}
	})

	It("keeps a client to the features of its allow-list", func() {
		key, _ := pair(auth.FeatureClientSelf)
		code, _ := do(http.MethodGet, "/api/client/addons/demo/ping", key, nil)
		Expect(code).To(Equal(http.StatusForbidden))
		key2, _ := pair(auth.FeatureClientSelf, auth.FeatureClientAddons)
		// Past the gate; the test has no add-on manager, so the proxy says so.
		code, _ = do(http.MethodGet, "/api/client/addons/demo/ping", key2, nil)
		Expect(code).To(Equal(http.StatusServiceUnavailable))
	})

	It("cannot turn a client key into a session", func() {
		key, _ := pair()
		e.POST("/api/auth/token-login-probe", func(c echo.Context) error {
			_, err := auth.ValidateAPIKey(db, key, "")
			if err != nil {
				return c.NoContent(http.StatusUnauthorized)
			}
			return c.NoContent(http.StatusOK)
		}, func(next echo.HandlerFunc) echo.HandlerFunc { return next })
		// The probe route is unregistered, so the gate refuses it for the client
		// key; call the check directly, as token-login does.
		_, err := auth.ValidateAPIKey(db, key, "")
		Expect(err).To(HaveOccurred())
	})

	It("stops a revoked key and lists clients without secrets", func() {
		key, id := pair()
		code, out := do(http.MethodGet, clientKeysPath, admin, nil)
		Expect(code).To(Equal(http.StatusOK))
		clients := out["clients"].([]any)
		Expect(clients).To(HaveLen(1))
		Expect(clients[0].(map[string]any)["features"]).To(ConsistOf(auth.FeatureClientSelf))
		Expect(clients[0]).ToNot(HaveKey("key"))
		Expect(clients[0]).ToNot(HaveKey("keyHash"))

		code, _ = do(http.MethodDelete, "/api/client/keys/"+id, admin, nil)
		Expect(code).To(Equal(http.StatusOK))
		Expect(revoked).To(ConsistOf("owner-1/" + id))
		code, _ = do(http.MethodGet, clientSelfPath, key, nil)
		Expect(code).To(Equal(http.StatusUnauthorized))
	})

	It("stops the jobs of a client whose key is deleted on the user key route, and only then", func() {
		key, id := pair()
		code, _ := do(http.MethodDelete, "/api/auth/api-keys/"+id, admin, nil)
		Expect(code).To(Equal(http.StatusOK))
		Expect(revoked).To(ConsistOf("owner-1/" + id))
		code, _ = do(http.MethodGet, clientSelfPath, key, nil)
		Expect(code).To(Equal(http.StatusUnauthorized))

		// An ordinary key does not start the job cancel.
		revoked = nil
		_, rec, err := auth.CreateAPIKey(db, "owner-1", "mine", auth.RoleAdmin, "", nil)
		Expect(err).ToNot(HaveOccurred())
		code, _ = do(http.MethodDelete, "/api/auth/api-keys/"+rec.ID, admin, nil)
		Expect(code).To(Equal(http.StatusOK))
		Expect(revoked).To(BeEmpty())
	})

	It("lets a client key reach the public routes, which ignore it", func() {
		key, _ := pair(auth.FeatureClientSelf)
		code, _ := do(http.MethodGet, "/healthz", key, nil)
		Expect(code).To(Equal(http.StatusOK))
		// Pairing again with the old key still sent reaches the handler.
		_, resp := do(http.MethodPost, clientCodesPath, admin, map[string]any{})
		code, out := do(http.MethodPost, clientPairPath, key, map[string]string{"code": resp["code"].(string)})
		Expect(code).To(Equal(http.StatusCreated))
		Expect(out).To(HaveKey("key"))
		code, _ = do(http.MethodPost, clientPairPath, key, map[string]string{"code": "AAAAA-AAAAA"})
		Expect(code).To(Equal(http.StatusUnauthorized))
	})

	It("keeps the admin routes to admins", func() {
		user := &auth.User{ID: "u2", Email: "u@example.com", Role: auth.RoleUser, Status: auth.StatusActive, Provider: auth.ProviderLocal}
		Expect(db.Create(user).Error).To(Succeed())
		tok, err := auth.CreateSession(db, user.ID, "")
		Expect(err).ToNot(HaveOccurred())
		for _, r := range ClientAdminRoutes {
			method, path, _ := strings.Cut(r, " ")
			code, _ := do(method, strings.ReplaceAll(path, ":id", "x"), tok, map[string]any{})
			Expect(code).To(Equal(http.StatusForbidden), r)
		}
		code, _ := do(http.MethodPost, clientCodesPath, "", map[string]any{})
		Expect(code).To(Equal(http.StatusUnauthorized))
	})

	It("rejects unknown features in a code and a bad key expiry", func() {
		code, _ := do(http.MethodPost, clientCodesPath, admin, map[string]any{"features": []string{"chats"}})
		Expect(code).To(Equal(http.StatusBadRequest))
		code, _ = do(http.MethodPost, clientCodesPath, admin, map[string]any{"keyExpiresIn": "soon"})
		Expect(code).To(Equal(http.StatusBadRequest))
	})

	It("rate limits pairing attempts per address", func() {
		var last int
		for range pairingAttemptsPerIP + 1 {
			last, _ = do(http.MethodPost, clientPairPath, "", map[string]string{"code": "AAAAA-AAAAA"})
		}
		Expect(last).To(Equal(http.StatusTooManyRequests))
	})

	It("carries the add-ons on the code and on the key, shows them, and lets the owner change them", func() {
		code, _ := do(http.MethodPost, clientCodesPath, admin, map[string]any{"features": []string{auth.FeatureClientAddons}})
		Expect(code).To(Equal(http.StatusBadRequest), "client_addons without an add-on")
		code, _ = do(http.MethodPost, clientCodesPath, admin, map[string]any{"features": []string{auth.FeatureClientSelf}, "addons": []string{"demo"}})
		Expect(code).To(Equal(http.StatusBadRequest), "add-ons without client_addons")
		code, _ = do(http.MethodPost, clientCodesPath, admin, map[string]any{"features": []string{auth.FeatureClientAddons}, "addons": []string{"Bad Id"}})
		Expect(code).To(Equal(http.StatusBadRequest))

		code, made := do(http.MethodPost, clientCodesPath, admin, map[string]any{"features": []string{auth.FeatureClientSelf, auth.FeatureClientAddons}, "addons": []string{"zeta", "alpha"}})
		Expect(code).To(Equal(http.StatusCreated))
		Expect(made["addons"]).To(Equal([]any{"alpha", "zeta"}))
		code, listed := do(http.MethodGet, clientCodesPath, admin, nil)
		Expect(code).To(Equal(http.StatusOK))
		Expect(listed["codes"].([]any)[0].(map[string]any)["addons"]).To(Equal([]any{"alpha", "zeta"}))

		_, paired := do(http.MethodPost, clientPairPath, "", map[string]string{"code": made["code"].(string)})
		key, id := paired["key"].(string), paired["id"].(string)
		Expect(paired["addons"]).To(Equal([]any{"alpha", "zeta"}))
		_, self := do(http.MethodGet, clientSelfPath, key, nil)
		Expect(self["addons"]).To(Equal([]any{"alpha", "zeta"}))

		code, put := do(http.MethodPut, "/api/client/keys/"+id, admin, map[string]any{"addons": []string{"beta"}})
		Expect(code).To(Equal(http.StatusOK))
		Expect(put["addons"]).To(Equal([]any{"beta"}))
		_, self = do(http.MethodGet, clientSelfPath, key, nil)
		Expect(self["addons"]).To(Equal([]any{"beta"}))
		_, keys := do(http.MethodGet, clientKeysPath, admin, nil)
		Expect(keys["clients"].([]any)[0].(map[string]any)["addons"]).To(Equal([]any{"beta"}))
		code, _ = do(http.MethodPut, "/api/client/keys/"+id, admin, map[string]any{"addons": []string{"Bad Id"}})
		Expect(code).To(Equal(http.StatusBadRequest))
		code, _ = do(http.MethodPut, "/api/client/keys/"+id, admin, map[string]any{})
		Expect(code).To(Equal(http.StatusBadRequest), "addons is required")
		code, _ = do(http.MethodPut, "/api/client/keys/nope", admin, map[string]any{"addons": []string{}})
		Expect(code).To(Equal(http.StatusNotFound))
		// The key cannot change its own list.
		code, _ = do(http.MethodPut, "/api/client/keys/"+id, key, map[string]any{"addons": []string{"alpha"}})
		Expect(code).To(Equal(http.StatusForbidden))
	})

	It("carries the models on the code and on the key, checks them, shows them, and lets the owner change them", func() {
		inference := []string{auth.FeatureClientInference}
		for name, body := range map[string]map[string]any{
			"client_inference without a model": {"features": inference},
			"models without client_inference":  {"features": []string{auth.FeatureClientSelf}, "models": []string{"m1"}},
			"a model that does not exist":      {"features": inference, "models": []string{"ghost"}},
			"a model with tools attached":      {"features": inference, "models": []string{"m1", "tools"}},
			"a bad name":                       {"features": inference, "models": []string{" m1"}},
		} {
			code, out := do(http.MethodPost, clientCodesPath, admin, body)
			Expect(code).To(Equal(http.StatusBadRequest), name)
			Expect(out["error"]).ToNot(BeEmpty(), name)
		}

		code, made := do(http.MethodPost, clientCodesPath, admin, map[string]any{"features": inference, "models": []string{"m2", "m1", "m1"}})
		Expect(code).To(Equal(http.StatusCreated))
		Expect(made["models"]).To(Equal([]any{"m1", "m2"}))
		_, listed := do(http.MethodGet, clientCodesPath, admin, nil)
		Expect(listed["codes"].([]any)[0].(map[string]any)["models"]).To(Equal([]any{"m1", "m2"}))

		_, paired := do(http.MethodPost, clientPairPath, "", map[string]string{"code": made["code"].(string)})
		key, id := paired["key"].(string), paired["id"].(string)
		Expect(paired["models"]).To(Equal([]any{"m1", "m2"}))
		code, self := do(http.MethodGet, clientSelfPath, key, nil)
		Expect(code).To(Equal(http.StatusForbidden), "client_inference alone does not give client_self")
		_ = self

		code, put := do(http.MethodPut, "/api/client/keys/"+id, admin, map[string]any{"models": []string{"voice"}})
		Expect(code).To(Equal(http.StatusOK))
		Expect(put["models"]).To(Equal([]any{"voice"}))
		_, keys := do(http.MethodGet, clientKeysPath, admin, nil)
		Expect(keys["clients"].([]any)[0].(map[string]any)["models"]).To(Equal([]any{"voice"}))
		for name, models := range map[string][]string{"empty list": {}, "missing": {"ghost"}, "tools": {"tools"}} {
			code, _ = do(http.MethodPut, "/api/client/keys/"+id, admin, map[string]any{"models": models})
			Expect(code).To(Equal(http.StatusBadRequest), name)
		}
		// A refused change leaves the list as it was, and a change of both fields is all or nothing.
		code, _ = do(http.MethodPut, "/api/client/keys/"+id, admin, map[string]any{"models": []string{"m1"}, "addons": []string{"Bad Id"}})
		Expect(code).To(Equal(http.StatusBadRequest))
		_, keys = do(http.MethodGet, clientKeysPath, admin, nil)
		Expect(keys["clients"].([]any)[0].(map[string]any)["models"]).To(Equal([]any{"voice"}))
		code, _ = do(http.MethodPut, "/api/client/keys/"+id, key, map[string]any{"models": []string{"m1"}})
		Expect(code).To(Equal(http.StatusForbidden), "the key cannot change its own list")
	})

	It("carries the folder grant on the code and on the key, checks it, shows it, and lets the owner change it", func() {
		folders := []string{auth.FeatureClientSelf, auth.FeatureClientFolders}
		for name, body := range map[string]map[string]any{
			"client_folders without a folder":    {"features": folders},
			"folders without client_folders":     {"features": []string{auth.FeatureClientSelf}, "readFolders": []string{"Notes"}},
			"a write folder without the feature": {"features": []string{auth.FeatureClientSelf}, "writeFolder": "Notes"},
			"an absolute folder":                 {"features": folders, "readFolders": []string{"/Notes"}},
			"a parent reference":                 {"features": folders, "readFolders": []string{"Notes/../Secrets"}},
			"a backslash":                        {"features": folders, "writeFolder": "Notes\\x"},
			"a NUL":                              {"features": folders, "writeFolder": "Notes\u0000x"},
			"too many read folders":              {"features": folders, "readFolders": []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"}},
		} {
			code, out := do(http.MethodPost, clientCodesPath, admin, body)
			Expect(code).To(Equal(http.StatusBadRequest), name)
			Expect(out["error"]).ToNot(BeEmpty(), name)
		}

		code, made := do(http.MethodPost, clientCodesPath, admin, map[string]any{"features": folders, "readFolders": []string{"Reports", "Notes", "Notes"}, "writeFolder": "Memory/clients/box"})
		Expect(code).To(Equal(http.StatusCreated))
		Expect(made["readFolders"]).To(Equal([]any{"Notes", "Reports"}))
		Expect(made["writeFolder"]).To(Equal("Memory/clients/box"))
		_, listed := do(http.MethodGet, clientCodesPath, admin, nil)
		Expect(listed["codes"].([]any)[0].(map[string]any)["writeFolder"]).To(Equal("Memory/clients/box"))

		_, paired := do(http.MethodPost, clientPairPath, "", map[string]string{"code": made["code"].(string)})
		key, id := paired["key"].(string), paired["id"].(string)
		Expect(paired["readFolders"]).To(Equal([]any{"Notes", "Reports"}))
		Expect(paired["writeFolder"]).To(Equal("Memory/clients/box"))
		_, self := do(http.MethodGet, clientSelfPath, key, nil)
		Expect(self["readFolders"]).To(Equal([]any{"Notes", "Reports"}))

		// A field that is absent stays as it is.
		code, put := do(http.MethodPut, "/api/client/keys/"+id, admin, map[string]any{"readFolders": []string{"Journal"}})
		Expect(code).To(Equal(http.StatusOK))
		Expect(put["readFolders"]).To(Equal([]any{"Journal"}))
		Expect(put["writeFolder"]).To(Equal("Memory/clients/box"))
		code, put = do(http.MethodPut, "/api/client/keys/"+id, admin, map[string]any{"writeFolder": ""})
		Expect(code).To(Equal(http.StatusOK))
		Expect(put["writeFolder"]).To(Equal(""))
		_, keys := do(http.MethodGet, clientKeysPath, admin, nil)
		got := keys["clients"].([]any)[0].(map[string]any)
		Expect(got["readFolders"]).To(Equal([]any{"Journal"}))
		Expect(got["writeFolder"]).To(Equal(""))
		// Empty lists are the way to take the access away.
		code, put = do(http.MethodPut, "/api/client/keys/"+id, admin, map[string]any{"readFolders": []string{}})
		Expect(code).To(Equal(http.StatusOK))
		Expect(put["readFolders"]).To(Equal([]any{}))
		// A refused change leaves the grant as it was.
		code, _ = do(http.MethodPut, "/api/client/keys/"+id, admin, map[string]any{"readFolders": []string{"Notes"}, "writeFolder": "../x"})
		Expect(code).To(Equal(http.StatusBadRequest))
		_, keys = do(http.MethodGet, clientKeysPath, admin, nil)
		Expect(keys["clients"].([]any)[0].(map[string]any)["readFolders"]).To(Equal([]any{}))
		// A key that was paired without the feature takes no folder.
		plain, plainID := pair(auth.FeatureClientSelf)
		code, _ = do(http.MethodPut, "/api/client/keys/"+plainID, admin, map[string]any{"readFolders": []string{"Notes"}})
		Expect(code).To(Equal(http.StatusBadRequest))
		// Only an administrator edits the grant: not the client, and not the client of another feature set.
		code, _ = do(http.MethodPut, "/api/client/keys/"+id, key, map[string]any{"readFolders": []string{"Notes"}})
		Expect(code).To(Equal(http.StatusForbidden))
		code, _ = do(http.MethodPut, "/api/client/keys/"+id, plain, map[string]any{"readFolders": []string{"Notes"}})
		Expect(code).To(Equal(http.StatusForbidden))
		code, _ = do(http.MethodPut, "/api/client/keys/"+id, "", map[string]any{"readFolders": []string{"Notes"}})
		Expect(code).To(BeNumerically(">=", 400))
	})

	It("tells a client in one call what it may use: features, add-ons, models and the version of the instance", func() {
		code, made := do(http.MethodPost, clientCodesPath, admin, map[string]any{
			"features": []string{auth.FeatureClientSelf, auth.FeatureClientAddons, auth.FeatureClientInference},
			"addons":   []string{"zeta", "alpha"}, "models": []string{"voice", "m1"},
		})
		Expect(code).To(Equal(http.StatusCreated))
		_, paired := do(http.MethodPost, clientPairPath, "", map[string]string{"code": made["code"].(string)})
		key := paired["key"].(string)
		code, self := do(http.MethodGet, clientSelfPath, key, nil)
		Expect(code).To(Equal(http.StatusOK))
		Expect(self["features"]).To(Equal([]any{auth.FeatureClientAddons, auth.FeatureClientInference, auth.FeatureClientSelf}))
		Expect(self["addons"]).To(Equal([]any{"alpha", "zeta"}))
		Expect(self["models"]).To(Equal([]any{"m1", "voice"}))
		Expect(self["version"]).To(Equal("dev"), "a source build has no tag")
		Expect(self).To(HaveKey("commit"))
		saved := internal.Version
		internal.Version = "v9.9.9"
		DeferCleanup(func() { internal.Version = saved })
		_, self = do(http.MethodGet, clientSelfPath, key, nil)
		Expect(self["version"]).To(Equal("v9.9.9"))
		Expect(self).ToNot(HaveKey("key"))
		// The list of clients of the owner has the same fields, without the version.
		_, keys := do(http.MethodGet, clientKeysPath, admin, nil)
		Expect(keys["clients"].([]any)[0].(map[string]any)).ToNot(HaveKey("version"))
		// A key without a list gets an empty list, not null.
		plain, _ := pair(auth.FeatureClientSelf)
		_, self = do(http.MethodGet, clientSelfPath, plain, nil)
		Expect(self["addons"]).To(Equal([]any{}))
		Expect(self["models"]).To(Equal([]any{}))
	})

	It("carries the fingerprint of the certificate and its digest on the codes when TLS is on, and not otherwise", func() {
		code, made := do(http.MethodPost, clientCodesPath, admin, map[string]any{})
		Expect(code).To(Equal(http.StatusCreated))
		Expect(made).ToNot(HaveKey("tlsFingerprint"))
		Expect(made).ToNot(HaveKey("tlsDigest"))

		// The same store, served by an instance that has a certificate.
		fingerprint := "ab12cd34" + strings.Repeat("0", 56)
		tlsOn := echo.New()
		tlsOn.Use(auth.Middleware(db, &config.ApplicationConfig{}))
		tlsOn.Use(auth.RequireRouteFeature(db))
		registerClientRoutes(context.Background(), tlsOn, db, "", auth.RequireAdmin(), func() clienthttp.Host { return nil }, nil, testModelCheck,
			WithTLSFingerprint(func() string { return fingerprint }))
		e = tlsOn
		code, made = do(http.MethodPost, clientCodesPath, admin, map[string]any{})
		Expect(code).To(Equal(http.StatusCreated))
		Expect(made["tlsFingerprint"]).To(Equal(fingerprint))
		Expect(made["tlsDigest"]).To(Equal("AB12-CD34"))
		_, listed := do(http.MethodGet, clientCodesPath, admin, nil)
		for _, c := range listed["codes"].([]any) {
			Expect(c.(map[string]any)["tlsFingerprint"]).To(Equal(fingerprint))
			Expect(c.(map[string]any)["tlsDigest"]).To(Equal("AB12-CD34"))
		}
		// The code that was made without it is in the list too, and gets the fingerprint of the server now.
		Expect(listed["codes"]).To(HaveLen(2))
		// A function that answers empty means plain HTTP.
		off := echo.New()
		off.Use(auth.Middleware(db, &config.ApplicationConfig{}))
		registerClientRoutes(context.Background(), off, db, "", auth.RequireAdmin(), func() clienthttp.Host { return nil }, nil, testModelCheck,
			WithTLSFingerprint(func() string { return "" }))
		e = off
		_, listed = do(http.MethodGet, clientCodesPath, admin, nil)
		Expect(listed["codes"].([]any)[0].(map[string]any)).ToNot(HaveKey("tlsFingerprint"))
	})

	It("answers a malformed code with 400 and does not count it, so it cannot lock pairing", func() {
		for range 3 * pairingAttemptsPerIP {
			code, _ := do(http.MethodPost, clientPairPath, "", map[string]string{"code": "short"})
			Expect(code).To(Equal(http.StatusBadRequest))
		}
		code, _ := do(http.MethodPost, clientPairPath, "", map[string]string{"code": "O0O0O-1I1I1"})
		Expect(code).To(Equal(http.StatusBadRequest), "symbols outside the alphabet")
		// Wrong codes use up the tries of this address, and nothing else: a
		// malformed body is still a 400, not a 429, and no state is shared
		// with other addresses.
		for range pairingAttemptsPerIP + 1 {
			do(http.MethodPost, clientPairPath, "", map[string]string{"code": "AAAAA-AAAAA"})
		}
		code, _ = do(http.MethodPost, clientPairPath, "", map[string]string{"code": "short"})
		Expect(code).To(Equal(http.StatusBadRequest))
	})

	It("answers that sign-in is needed when authentication is off", func() {
		off := echo.New()
		registerClientRoutes(context.Background(), off, nil, "", auth.NoopMiddleware(), nil, nil, nil)
		for _, r := range append(append(append([]string{}, ClientPublicRoutes...), ClientAdminRoutes...), ClientKeyRoutes...) {
			method, path, _ := strings.Cut(r, " ")
			w := httptest.NewRecorder()
			off.ServeHTTP(w, httptest.NewRequest(method, path, nil))
			Expect(w.Code).To(Equal(http.StatusConflict), r)
		}
	})
})

var _ = Describe("client route registry", func() {
	// An unregistered route is not checked for a user key, and it is refused for a
	// client key; either way a route under /api/client that nobody classified is a
	// mistake. Every route is public, admin or client, and every client route is
	// in the registry with a client feature.
	It("has every client route classified and every client key route registered", func() {
		e := echo.New()
		registerClientRoutes(context.Background(), e, nil, "", auth.NoopMiddleware(), nil, nil, nil)
		classified := map[string]string{}
		for _, r := range ClientPublicRoutes {
			classified[r] = "public"
		}
		for _, r := range ClientAdminRoutes {
			classified[r] = "admin"
		}
		for _, r := range ClientKeyRoutes {
			classified[r] = "client"
		}
		registry := map[string]string{}
		for _, rf := range auth.RouteFeatureRegistry {
			// client_inference rows name routes outside /api/client: they have
			// their own frozen list (features_client_test.go in core/http/auth).
			if rf.Feature == auth.FeatureClientInference {
				continue
			}
			registry[rf.Method+" "+rf.Pattern] = rf.Feature
		}
		for _, r := range e.Routes() {
			if !strings.HasPrefix(r.Path, "/api/client") {
				continue
			}
			key := r.Method + " " + r.Path
			kind, ok := classified[key]
			Expect(ok).To(BeTrue(), "%s is not classified as public, admin or client in routes/client.go", key)
			feature, registered := registry[key]
			if kind == "client" {
				Expect(registered).To(BeTrue(), "%s is a client route but is not in auth.RouteFeatureRegistry", key)
				Expect(auth.IsClientFeature(feature)).To(BeTrue(), "%s must use a client feature, got %q", key, feature)
			} else {
				Expect(registered).To(BeFalse(), "%s is %s and must not be in the registry", key, kind)
			}
		}
		// And the other direction: every registry entry for a client feature is a
		// route that exists in the catch-all or client group.
		for key, feature := range registry {
			if auth.IsClientFeature(feature) {
				Expect(strings.Contains(key, " /api/client/")).To(BeTrue(), key)
			}
		}
	})
})
