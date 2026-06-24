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
	authpkg "github.com/mudler/LocalAI/core/http/auth"
	. "github.com/mudler/LocalAI/core/http"
	"github.com/mudler/LocalAI/pkg/system"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// PUT /api/auth/profile must accept ONLY name and avatar_url. A user that
// posts {"role":"admin","email":"...","status":"active","password_hash":"..."}
// must not be able to mutate any of those fields. The current handler uses
// an explicit local body struct + gorm Updates(map) with a column allowlist;
// this test pins that contract so a future refactor (e.g. c.Bind(&user))
// can't silently regress to mass-assignment.
var _ = Describe("PUT /api/auth/profile field-tampering", func() {
	var (
		app    *echo.Echo
		appCtx context.Context
		cancel context.CancelFunc
		tmpdir string
		alice  authpkg.User
		appAt  *application.Application
	)

	BeforeEach(func() {
		var err error
		tmpdir, err = os.MkdirTemp("", "profile-tamper-")
		Expect(err).ToNot(HaveOccurred())

		modelDir := filepath.Join(tmpdir, "models")
		Expect(os.Mkdir(modelDir, 0750)).To(Succeed())
		bDir := filepath.Join(tmpdir, "backends")
		Expect(os.Mkdir(bDir, 0750)).To(Succeed())

		appCtx, cancel = context.WithCancel(context.Background())

		systemState, err := system.GetSystemState(
			system.WithBackendPath(bDir),
			system.WithModelPath(modelDir),
		)
		Expect(err).ToNot(HaveOccurred())

		appAt, err = application.New(
			config.WithContext(appCtx),
			config.WithSystemState(systemState),
			config.WithAuthEnabled(true),
			config.WithAuthDatabaseURL(":memory:"),
			config.WithAuthAPIKeyHMACSecret("test-secret-profile-tamper"),
		)
		Expect(err).ToNot(HaveOccurred())

		app, err = API(appAt)
		Expect(err).ToNot(HaveOccurred())

		// Seed a non-admin user directly in the DB.
		alice = authpkg.User{
			ID:           "alice-id",
			Email:        "alice@example.com",
			Name:         "Alice",
			Provider:     authpkg.ProviderLocal,
			PasswordHash: "$2a$10$abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGHIJKLMNOPQRSTUV",
			Role:         authpkg.RoleUser,
			Status:       "active",
		}
		Expect(appAt.AuthDB().Create(&alice).Error).To(Succeed())
	})

	AfterEach(func() {
		cancel()
		Expect(os.RemoveAll(tmpdir)).To(Succeed())
	})

	// Mint a real API key for alice once per spec — the auth middleware
	// resolves Bearer tokens against the DB, which is the same path a
	// browser session takes after extraction. This avoids forging session
	// cookies and exercises the production auth flow end-to-end.
	callProfile := func(body map[string]any) (int, map[string]any) {
		key, _, err := authpkg.CreateAPIKey(appAt.AuthDB(), alice.ID, "test", authpkg.RoleUser, "test-secret-profile-tamper", nil)
		Expect(err).ToNot(HaveOccurred())

		raw, err := json.Marshal(body)
		Expect(err).ToNot(HaveOccurred())
		req := httptest.NewRequest(http.MethodPut, "/api/auth/profile", bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		req.Header.Set("Authorization", "Bearer "+key)
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}

	It("ignores attempts to set role: admin", func() {
		code, _ := callProfile(map[string]any{
			"name":       "Alice 2",
			"avatar_url": "https://example.com/a.png",
			"role":       "admin",
		})
		Expect(code).To(Equal(http.StatusOK))

		var fresh authpkg.User
		Expect(appAt.AuthDB().First(&fresh, "id = ?", alice.ID).Error).To(Succeed())
		Expect(fresh.Role).To(Equal(authpkg.RoleUser),
			"role must remain RoleUser; mass-assignment of role is forbidden")
		Expect(fresh.Name).To(Equal("Alice 2"))
		Expect(fresh.AvatarURL).To(Equal("https://example.com/a.png"))
	})

	It("ignores attempts to override email, status, password_hash, provider, and id", func() {
		body := map[string]any{
			"name":          "Alice 3",
			"avatar_url":    "",
			"email":         "attacker@example.com",
			"status":        "frozen",
			"password_hash": "$2a$10$attacker-controlled-hash",
			"provider":      "github",
			"id":            "different-id",
		}
		code, _ := callProfile(body)
		Expect(code).To(Equal(http.StatusOK))

		var fresh authpkg.User
		Expect(appAt.AuthDB().First(&fresh, "id = ?", alice.ID).Error).To(Succeed())
		Expect(fresh.Email).To(Equal(alice.Email), "email must be immutable through profile update")
		Expect(fresh.Status).To(Equal(alice.Status), "status must be immutable")
		Expect(fresh.PasswordHash).To(Equal(alice.PasswordHash), "password_hash must be immutable")
		Expect(fresh.Provider).To(Equal(alice.Provider), "provider must be immutable")
		Expect(fresh.ID).To(Equal(alice.ID), "id must be immutable")
	})

	It("requires a non-empty name", func() {
		code, body := callProfile(map[string]any{"name": ""})
		Expect(code).To(Equal(http.StatusBadRequest))
		Expect(body["error"]).To(ContainSubstring("name is required"))
	})

	It("rejects oversized avatar_url", func() {
		long := make([]byte, 1024)
		for i := range long {
			long[i] = 'x'
		}
		code, _ := callProfile(map[string]any{
			"name":       "Alice",
			"avatar_url": "https://" + string(long),
		})
		Expect(code).To(Equal(http.StatusBadRequest))
	})
})
