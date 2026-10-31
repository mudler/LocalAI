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

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	. "github.com/mudler/LocalAI/core/http"
	authpkg "github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("local authentication administrator email", func() {
	var (
		app    *echo.Echo
		appAt  *application.Application
		cancel context.CancelFunc
		tmpdir string
	)

	BeforeEach(func() {
		var err error
		tmpdir, err = os.MkdirTemp("", "auth-admin-email-")
		Expect(err).ToNot(HaveOccurred())

		modelDir := filepath.Join(tmpdir, "models")
		backendDir := filepath.Join(tmpdir, "backends")
		Expect(os.Mkdir(modelDir, 0750)).To(Succeed())
		Expect(os.Mkdir(backendDir, 0750)).To(Succeed())

		ctx, stop := context.WithCancel(context.Background())
		cancel = stop
		state, err := system.GetSystemState(
			system.WithBackendPath(backendDir),
			system.WithModelPath(modelDir),
		)
		Expect(err).ToNot(HaveOccurred())

		appAt, err = application.New(
			config.WithContext(ctx),
			config.WithSystemState(state),
			config.WithAuthEnabled(true),
			config.WithAuthDatabaseURL(":memory:"),
			config.WithAuthAPIKeyHMACSecret("test-secret-admin-email"),
			config.WithAuthAdminEmail("admin@example.com"),
			config.WithAuthRegistrationMode("approval"),
		)
		Expect(err).ToNot(HaveOccurred())

		app, err = API(appAt)
		Expect(err).ToNot(HaveOccurred())

		Expect(appAt.AuthDB().Create(&authpkg.User{
			ID:       uuid.NewString(),
			Email:    "operator@example.com",
			Name:     "Operator",
			Provider: authpkg.ProviderLocal,
			Subject:  "operator@example.com",
			Role:     authpkg.RoleAdmin,
			Status:   authpkg.StatusActive,
		}).Error).To(Succeed())
	})

	AfterEach(func() {
		cancel()
		Expect(os.RemoveAll(tmpdir)).To(Succeed())
	})

	request := func(method, path string, body map[string]any) *httptest.ResponseRecorder {
		raw, err := json.Marshal(body)
		Expect(err).ToNot(HaveOccurred())
		req := httptest.NewRequest(method, path, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		return rec
	}

	It("does not trust an unverified local registration for administrator promotion", func() {
		rec := request(http.MethodPost, "/api/auth/register", map[string]any{
			"email":    "admin@example.com",
			"password": "Correct-Horse-Battery-42!",
			"name":     "Claimed Admin",
		})
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(rec.Result().Cookies()).To(BeEmpty())

		var user authpkg.User
		Expect(appAt.AuthDB().Where("email = ? AND provider = ?", "admin@example.com", authpkg.ProviderLocal).First(&user).Error).To(Succeed())
		Expect(user.Role).To(Equal(authpkg.RoleUser))
		Expect(user.Status).To(Equal(authpkg.StatusPending))
	})

	It("does not promote an active local account merely because its email matches", func() {
		hash, err := authpkg.HashPassword("Correct-Horse-Battery-42!")
		Expect(err).ToNot(HaveOccurred())
		user := authpkg.User{
			ID:           uuid.NewString(),
			Email:        "admin@example.com",
			Name:         "Local User",
			Provider:     authpkg.ProviderLocal,
			Subject:      "admin@example.com",
			PasswordHash: hash,
			Role:         authpkg.RoleUser,
			Status:       authpkg.StatusActive,
		}
		Expect(appAt.AuthDB().Create(&user).Error).To(Succeed())

		rec := request(http.MethodPost, "/api/auth/login", map[string]any{
			"email":    "admin@example.com",
			"password": "Correct-Horse-Battery-42!",
		})
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())

		var fresh authpkg.User
		Expect(appAt.AuthDB().First(&fresh, "id = ?", user.ID).Error).To(Succeed())
		Expect(fresh.Role).To(Equal(authpkg.RoleUser))
	})
})
