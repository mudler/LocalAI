//go:build auth

package http_test

import (
	"bytes"
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
	. "github.com/mudler/LocalAI/core/http"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/pkg/system"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Remotes are not part of the model gallery. A server started with the
// gallery endpoints off must still serve them to an administrator and refuse
// everyone else.
var _ = Describe("Remote routes with the gallery endpoints off", func() {
	const secret = "gallery-off-secret"

	var (
		e     *echo.Echo
		admin string
		user  string
		up    *httptest.Server
	)

	do := func(method, path, key string, body any) (int, []byte) {
		var rd *bytes.Reader
		if body != nil {
			b, err := json.Marshal(body)
			Expect(err).NotTo(HaveOccurred())
			rd = bytes.NewReader(b)
		} else {
			rd = bytes.NewReader(nil)
		}
		req := httptest.NewRequest(method, path, rd)
		req.Header.Set("Content-Type", "application/json")
		if key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		return rec.Code, rec.Body.Bytes()
	}

	BeforeEach(func() {
		dir, err := filepath.EvalSymlinks(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		models, backends := filepath.Join(dir, "models"), filepath.Join(dir, "backends")
		Expect(os.Mkdir(models, 0o750)).To(Succeed())
		Expect(os.Mkdir(backends, 0o750)).To(Succeed())
		state, err := system.GetSystemState(system.WithModelPath(models), system.WithBackendPath(backends))
		Expect(err).NotTo(HaveOccurred())
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		DeferCleanup(func() { config.SetProxyKeyResolver(nil) })

		app, err := application.New(
			config.WithContext(ctx), config.WithSystemState(state), config.WithDataPath(filepath.Join(dir, "data")),
			config.DisableGalleryEndpoint,
			config.WithAuthEnabled(true), config.WithAuthDatabaseURL(":memory:"), config.WithAuthAPIKeyHMACSecret(secret),
		)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = app.Shutdown() })
		Expect(app.Remotes()).NotTo(BeNil())

		db := app.AuthDB()
		Expect(db).NotTo(BeNil())
		keyFor := func(id, role string) string {
			Expect(db.Create(&auth.User{ID: id, Email: id + "@example.test", Name: id, Role: role, Status: auth.StatusActive, Provider: auth.ProviderLocal}).Error).To(Succeed())
			plain, _, err := auth.CreateAPIKey(db, id, "test", role, secret, nil)
			Expect(err).NotTo(HaveOccurred())
			return plain
		}
		admin, user = keyFor("root", auth.RoleAdmin), keyFor("plain", auth.RoleUser)

		up = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch {
			case strings.HasSuffix(r.URL.Path, "/models/capabilities"):
				_, _ = w.Write([]byte(`{"data":[{"id":"m1","capabilities":["FLAG_CHAT"]},{"id":"m2","capabilities":["FLAG_CHAT"]}]}`))
			case strings.HasSuffix(r.URL.Path, "/models"):
				_, _ = w.Write([]byte(`{"data":[{"id":"m1"},{"id":"m2"}]}`))
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		DeferCleanup(up.Close)

		e, err = API(app)
		Expect(err).NotTo(HaveOccurred())
	})

	It("keeps the gallery routes off", func() {
		code, _ := do(http.MethodGet, "/models/available", admin, nil)
		Expect(code).To(Equal(http.StatusNotFound))
	})

	It("lists, creates, adopts and deletes for an administrator", func() {
		code, body := do(http.MethodGet, "/api/remotes", admin, nil)
		Expect(code).To(Equal(http.StatusOK), string(body))
		Expect(string(body)).To(MatchJSON(`{"data":[]}`))

		code, body = do(http.MethodPost, "/api/remotes", admin, map[string]any{
			"kind": "localai", "name": "upstream", "url": up.URL, "api_key": "k1",
			"models": []map[string]any{{"id": "m1", "usecases": []string{"chat"}}},
		})
		Expect(code).To(Equal(http.StatusCreated), string(body))
		var created struct {
			Remote struct {
				ID string `json:"id"`
			} `json:"remote"`
			Created []string `json:"created"`
		}
		Expect(json.Unmarshal(body, &created)).To(Succeed())
		Expect(created.Remote.ID).NotTo(BeEmpty())
		Expect(created.Created).To(ConsistOf("m1"))

		code, body = do(http.MethodGet, "/api/remotes", admin, nil)
		Expect(code).To(Equal(http.StatusOK))
		Expect(string(body)).To(ContainSubstring(created.Remote.ID))
		Expect(string(body)).NotTo(ContainSubstring("k1"))

		code, body = do(http.MethodPost, "/api/remotes/"+created.Remote.ID+"/adopt", admin, map[string]any{
			"models": []map[string]any{{"id": "m2", "usecases": []string{"chat"}}},
		})
		Expect(code).To(Equal(http.StatusOK), string(body))
		Expect(string(body)).To(ContainSubstring("m2"))

		code, body = do(http.MethodDelete, "/api/remotes/"+created.Remote.ID, admin, nil)
		Expect(code).To(Equal(http.StatusNoContent), string(body))
		code, _ = do(http.MethodGet, "/api/remotes/"+created.Remote.ID, admin, nil)
		Expect(code).To(Equal(http.StatusNotFound))
	})

	It("refuses a user who is not an administrator and an anonymous caller", func() {
		for _, r := range []struct{ method, path string }{
			{http.MethodGet, "/api/remotes"},
			{http.MethodPost, "/api/remotes"},
			{http.MethodPost, "/api/remotes/x/adopt"},
			{http.MethodDelete, "/api/remotes/x"},
		} {
			code, _ := do(r.method, r.path, user, map[string]any{"kind": "localai", "name": "n", "url": up.URL})
			Expect(code).To(Equal(http.StatusForbidden), r.method+" "+r.path)
			code, _ = do(r.method, r.path, "", nil)
			Expect(code).To(Equal(http.StatusUnauthorized), r.method+" "+r.path)
		}
		code, body := do(http.MethodGet, "/api/remotes", admin, nil)
		Expect(code).To(Equal(http.StatusOK))
		Expect(string(body)).To(MatchJSON(`{"data":[]}`))
	})
})
