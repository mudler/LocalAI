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
	. "github.com/mudler/LocalAI/core/http"
	authpkg "github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/pkg/system"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// An invite records the account that created it. The static API key is an
// admin with no account row, so it is refused with a clear answer instead of
// failing the creator foreign key with a bare 500.
var _ = Describe("POST /api/auth/admin/invites", func() {
	const (
		staticKey = "static-key-invites"
		secret    = "test-secret-invites"
	)
	var (
		app    *echo.Echo
		appAt  *application.Application
		cancel context.CancelFunc
	)

	BeforeEach(func() {
		dir := GinkgoT().TempDir()
		models, backends := filepath.Join(dir, "models"), filepath.Join(dir, "backends")
		Expect(os.Mkdir(models, 0750)).To(Succeed())
		Expect(os.Mkdir(backends, 0750)).To(Succeed())
		state, err := system.GetSystemState(system.WithBackendPath(backends), system.WithModelPath(models))
		Expect(err).ToNot(HaveOccurred())

		var ctx context.Context
		ctx, cancel = context.WithCancel(context.Background())
		appAt, err = application.New(
			config.WithContext(ctx),
			config.WithSystemState(state),
			config.WithApiKeys([]string{staticKey}),
			config.WithAuthEnabled(true),
			config.WithAuthDatabaseURL(":memory:"),
			config.WithAuthAPIKeyHMACSecret(secret),
		)
		Expect(err).ToNot(HaveOccurred())
		app, err = API(appAt)
		Expect(err).ToNot(HaveOccurred())
	})

	AfterEach(func() { cancel() })

	create := func(bearer string) (int, map[string]any) {
		req := httptest.NewRequest(http.MethodPost, "/api/auth/admin/invites", strings.NewReader(`{"expiresInHours":24}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		req.Header.Set("Authorization", "Bearer "+bearer)
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}

	It("refuses the static API key, which has no account to record as creator", func() {
		code, out := create(staticKey)
		Expect(code).To(Equal(http.StatusForbidden), "%v", out)
		Expect(out["error"]).To(ContainSubstring("named account"))

		var n int64
		Expect(appAt.AuthDB().Model(&authpkg.InviteCode{}).Count(&n).Error).To(Succeed())
		Expect(n).To(BeZero())
	})

	It("creates the invite for an admin account", func() {
		admin := authpkg.User{ID: "admin-id", Email: "admin@example.com", Name: "Admin",
			Provider: authpkg.ProviderLocal, Role: authpkg.RoleAdmin, Status: authpkg.StatusActive}
		Expect(appAt.AuthDB().Create(&admin).Error).To(Succeed())
		key, _, err := authpkg.CreateAPIKey(appAt.AuthDB(), admin.ID, "test", authpkg.RoleAdmin, secret, nil)
		Expect(err).ToNot(HaveOccurred())

		code, out := create(key)
		Expect(code).To(Equal(http.StatusCreated), "%v", out)
		Expect(out["code"]).ToNot(BeEmpty())

		var invite authpkg.InviteCode
		Expect(appAt.AuthDB().First(&invite, "id = ?", out["id"]).Error).To(Succeed())
		Expect(invite.CreatedBy).To(Equal(admin.ID))
	})
})
