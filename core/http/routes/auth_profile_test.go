//go:build auth

package routes_test

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/mudler/LocalAI/core/http/routes"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	. "github.com/mudler/LocalAI/core/http"
	authpkg "github.com/mudler/LocalAI/core/http/auth"
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

// Capability visibility stays ID-based even when an alias inherits its target's flags.
func TestUIAliasCapabilityAllowlist(t *testing.T) {
	for _, tc := range []struct {
		name, role    string
		allowed, want []string
	}{
		{"alias only", authpkg.RoleUser, []string{"alias"}, []string{"alias"}},
		{"target only", authpkg.RoleUser, []string{"target"}, []string{"target"}},
		{"admin", authpkg.RoleAdmin, []string{"alias"}, []string{"alias", "target"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			st, err := system.GetSystemState(system.WithModelPath(root), system.WithBackendPath(root))
			if err != nil {
				t.Fatal(err)
			}
			appAt, err := application.New(config.WithContext(context.Background()), config.WithSystemState(st), config.WithDataPath(root), config.WithDisableLocalAIAssistant(true), config.WithAuthEnabled(true), config.WithAuthDatabaseURL(":memory:"), config.WithAuthAPIKeyHMACSecret("alias-test-secret"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = appAt.Shutdown() })
			cl := appAt.ModelConfigLoader()
			for name, body := range map[string]string{"target": "backend: llama-cpp\nknown_usecases: [chat]\n", "alias": "alias: target\n"} {
				path := filepath.Join(root, name+".yaml")
				if err := os.WriteFile(path, []byte("name: "+name+"\n"+body), 0600); err != nil {
					t.Fatal(err)
				}
				if err := cl.ReadModelConfig(path); err != nil {
					t.Fatal(err)
				}
			}
			user := authpkg.User{ID: "alias-user", Email: "alias@example.test", Role: tc.role, Status: authpkg.StatusActive, Provider: authpkg.ProviderLocal}
			db := appAt.AuthDB()
			if err := db.Create(&user).Error; err != nil {
				t.Fatal(err)
			}
			if err := authpkg.UpdateModelAllowlist(db, user.ID, authpkg.ModelAllowlist{Enabled: true, Models: tc.allowed}); err != nil {
				t.Fatal(err)
			}
			session, err := authpkg.CreateSession(db, user.ID, "alias-test-secret")
			if err != nil {
				t.Fatal(err)
			}
			e := echo.New()
			e.Use(authpkg.Middleware(db, appAt.ApplicationConfig()))
			gs := galleryop.NewGalleryService(appAt.ApplicationConfig(), appAt.ModelLoader())
			routes.RegisterUIAPIRoutes(e, cl, appAt.ModelLoader(), appAt.ApplicationConfig(), gs, galleryop.NewOpCache(gs), appAt, func(next echo.HandlerFunc) echo.HandlerFunc { return next })
			req := httptest.NewRequest(http.MethodGet, "/api/models/capabilities", nil)
			req.AddCookie(&http.Cookie{Name: "session", Value: session})
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
			}
			var response struct {
				Data []struct {
					ID           string
					Capabilities []string
				}
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			var ids []string
			for _, entry := range response.Data {
				ids = append(ids, entry.ID)
				if !slices.Equal(entry.Capabilities, []string{"FLAG_CHAT"}) {
					t.Errorf("unexpected flags: %+v", entry)
				}
			}
			slices.Sort(ids)
			if !slices.Equal(ids, tc.want) {
				t.Errorf("IDs %v, want %v", ids, tc.want)
			}
		})
	}
}
