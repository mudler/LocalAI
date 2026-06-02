package localai_test

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
	. "github.com/mudler/LocalAI/core/http/endpoints/localai"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The settings handlers take the concrete *application.Application, so the
// suite builds a minimal real app (no watchdog/p2p — both default off — so
// start() doesn't spawn background services) the same way the in-process HTTP
// suite does, then drives the handlers via httptest.
var _ = Describe("Settings endpoints", func() {
	var (
		tmp    string
		app    *application.Application
		e      *echo.Echo
		cancel context.CancelFunc
	)

	BeforeEach(func() {
		var err error
		tmp, err = os.MkdirTemp("", "settings-test-*")
		Expect(err).ToNot(HaveOccurred())

		var ctx context.Context
		ctx, cancel = context.WithCancel(context.Background())

		st, err := system.GetSystemState(
			system.WithModelPath(filepath.Join(tmp, "models")),
			system.WithBackendPath(filepath.Join(tmp, "backends")),
		)
		Expect(err).ToNot(HaveOccurred())

		app, err = application.New(
			config.WithContext(ctx),
			config.WithSystemState(st),
		)
		Expect(err).ToNot(HaveOccurred())
		// Settings are persisted here; set after construction since there's no
		// dedicated AppOption for it.
		app.ApplicationConfig().DynamicConfigsDir = tmp

		e = echo.New()
		e.GET("/api/settings", GetSettingsEndpoint(app))
		e.POST("/api/settings", UpdateSettingsEndpoint(app))
	})

	AfterEach(func() {
		cancel()
		Expect(os.RemoveAll(tmp)).To(Succeed())
	})

	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/settings", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		return rec
	}

	It("GET returns the current runtime settings", func() {
		req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)

		Expect(rec.Code).To(Equal(http.StatusOK))
		var settings map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &settings)).To(Succeed())
		Expect(settings).ToNot(BeEmpty())
	})

	It("rejects malformed JSON", func() {
		rec := post("{not json")
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
		Expect(rec.Body.String()).To(ContainSubstring("Failed to parse JSON"))
	})

	It("rejects an invalid watchdog timeout duration", func() {
		rec := post(`{"watchdog_idle_timeout":"notaduration"}`)
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
		Expect(rec.Body.String()).To(ContainSubstring("Invalid watchdog_idle_timeout"))
	})

	It("errors when DynamicConfigsDir is unset", func() {
		app.ApplicationConfig().DynamicConfigsDir = ""
		rec := post(`{}`)
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
		Expect(rec.Body.String()).To(ContainSubstring("DynamicConfigsDir is not set"))
	})

	It("saves valid settings to runtime_settings.json", func() {
		rec := post(`{}`)
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(rec.Body.String()).To(ContainSubstring("Settings updated successfully"))
		_, err := os.Stat(filepath.Join(tmp, "runtime_settings.json"))
		Expect(err).ToNot(HaveOccurred())
	})
})
