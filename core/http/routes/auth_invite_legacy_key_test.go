//go:build auth

package routes_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	. "github.com/mudler/LocalAI/core/http"
	authpkg "github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/pkg/system"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The static API key (LOCALAI_API_KEY) authenticates as a synthetic admin
// with the ID "legacy-api-key", which has no row in the users table. The
// auth database enforces foreign keys, so an invite that names that ID as
// its creator was rejected and the admin got a bare 500. In invite mode the
// API key is often the only admin credential that exists before the first
// account, so it must be able to create, list and hand out invites.
var _ = Describe("Admin invites created with the static API key", Label("auth"), func() {
	const apiKey = "invite-legacy-key"

	var (
		app    *echo.Echo
		appAt  *application.Application
		cancel context.CancelFunc
		tmpdir string
	)

	BeforeEach(func() {
		var err error
		tmpdir, err = os.MkdirTemp("", "invite-legacy-key-")
		Expect(err).ToNot(HaveOccurred())
		modelDir := filepath.Join(tmpdir, "models")
		Expect(os.Mkdir(modelDir, 0750)).To(Succeed())
		backendDir := filepath.Join(tmpdir, "backends")
		Expect(os.Mkdir(backendDir, 0750)).To(Succeed())

		var ctx context.Context
		ctx, cancel = context.WithCancel(context.Background())
		systemState, err := system.GetSystemState(system.WithBackendPath(backendDir), system.WithModelPath(modelDir))
		Expect(err).ToNot(HaveOccurred())

		appAt, err = application.New(
			config.WithContext(ctx),
			config.WithSystemState(systemState),
			config.WithDataPath(tmpdir),
			config.WithDisableLocalAIAssistant(true),
			config.WithAuthEnabled(true),
			config.WithAuthDatabaseURL(":memory:"),
			config.WithAuthAPIKeyHMACSecret("invite-legacy-secret"),
			config.WithApiKeys([]string{apiKey}),
		)
		Expect(err).ToNot(HaveOccurred())
		app, err = API(appAt)
		Expect(err).ToNot(HaveOccurred())
	})

	AfterEach(func() {
		cancel()
		_ = appAt.Shutdown()
		Expect(os.RemoveAll(tmpdir)).To(Succeed())
	})

	call := func(method, path string, body any) (int, map[string]any) {
		var reader *bytes.Reader
		if body != nil {
			raw, err := json.Marshal(body)
			Expect(err).ToNot(HaveOccurred())
			reader = bytes.NewReader(raw)
		} else {
			reader = bytes.NewReader(nil)
		}
		req := httptest.NewRequest(method, path, reader)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		req.Header.Set("Authorization", "Bearer "+apiKey)
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}

	It("creates an invite that a new user can register with", func() {
		var fk int
		Expect(appAt.AuthDB().Raw("PRAGMA foreign_keys").Scan(&fk).Error).To(Succeed())
		Expect(fk).To(Equal(1), "the test must run with foreign keys enforced, as production does")

		code, created := call(http.MethodPost, "/api/auth/admin/invites", map[string]any{"expiresInHours": 1})
		Expect(code).To(Equal(http.StatusCreated), "body=%v", created)
		plaintext, _ := created["code"].(string)
		Expect(plaintext).ToNot(BeEmpty())

		code, listed := call(http.MethodGet, "/api/auth/admin/invites", nil)
		Expect(code).To(Equal(http.StatusOK), "body=%v", listed)
		invites, _ := listed["invites"].([]any)
		Expect(invites).To(HaveLen(1))
		Expect(invites[0].(map[string]any)["id"]).To(Equal(created["id"]))

		// The invite must be usable, not only stored.
		invite, err := authpkg.ValidateInvite(appAt.AuthDB(), plaintext, "invite-legacy-secret")
		Expect(err).ToNot(HaveOccurred())
		Expect(invite.ID).To(Equal(created["id"]))
	})

	It("still records the creating account when an admin user creates the invite", func() {
		admin := authpkg.User{ID: "admin-id", Email: "admin@example.test", Name: "Admin", Provider: authpkg.ProviderLocal, Role: authpkg.RoleAdmin, Status: authpkg.StatusActive}
		Expect(appAt.AuthDB().Create(&admin).Error).To(Succeed())
		key, _, err := authpkg.CreateAPIKey(appAt.AuthDB(), admin.ID, "test", authpkg.RoleAdmin, "invite-legacy-secret", nil)
		Expect(err).ToNot(HaveOccurred())

		req := httptest.NewRequest(http.MethodPost, "/api/auth/admin/invites", bytes.NewReader([]byte(`{}`)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		req.Header.Set("Authorization", "Bearer "+key)
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		Expect(rec.Code).To(Equal(http.StatusCreated), "body=%s", rec.Body.String())

		code, listed := call(http.MethodGet, "/api/auth/admin/invites", nil)
		Expect(code).To(Equal(http.StatusOK))
		invites, _ := listed["invites"].([]any)
		Expect(invites).To(HaveLen(1))
		createdBy, _ := invites[0].(map[string]any)["createdBy"].(map[string]any)
		Expect(createdBy).To(HaveKeyWithValue("id", admin.ID))
		Expect(createdBy).To(HaveKeyWithValue("name", admin.Name))
	})
})
