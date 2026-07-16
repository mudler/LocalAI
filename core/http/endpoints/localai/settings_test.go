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
		// Contain the MITM CA inside tmp too. The partial-save spec flips
		// mitm_listen, which starts the listener and writes a CA; without this
		// it defaults to ./mitm-ca and litters the package source tree.
		app.ApplicationConfig().MITMCADir = filepath.Join(tmp, "mitm-ca")

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

	// Regression: a focused admin page (the Middleware proxy tab) POSTs only
	// the one field it owns — mitm_listen. The old handler wrote the request
	// body verbatim, so every other persisted setting was dropped (and
	// api_keys / pii_default_detectors, which lack omitempty, were written as
	// null). A partial POST must now merge onto what is already on disk.
	It("preserves unrelated persisted settings when a partial POST sets only mitm_listen", func() {
		// First save establishes a fuller settings file (as the full Settings
		// page would): galleries, an API key, and the MITM listener. The
		// listener restart binds a real socket, so use 127.0.0.1:0 for an
		// ephemeral free port rather than a fixed one that may be in use.
		rec := post(`{"mitm_listen":"127.0.0.1:0","galleries":[{"name":"g1","url":"http://example/g1"}],"api_keys":["k1"],"pii_default_detectors":["det-a"]}`)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())

		// The Middleware proxy tab then changes only the listen address — the
		// exact partial body that nulled everything else before the fix.
		rec = post(`{"mitm_listen":"127.0.0.1:0"}`)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())

		raw, err := os.ReadFile(filepath.Join(tmp, "runtime_settings.json"))
		Expect(err).ToNot(HaveOccurred())
		var ondisk config.RuntimeSettings
		Expect(json.Unmarshal(raw, &ondisk)).To(Succeed())

		Expect(ondisk.MITMListen).ToNot(BeNil())
		Expect(*ondisk.MITMListen).To(Equal("127.0.0.1:0"), "the changed field should be saved")
		Expect(ondisk.Galleries).ToNot(BeNil(), "galleries were clobbered by the partial save")
		Expect(*ondisk.Galleries).To(HaveLen(1))
		Expect(ondisk.ApiKeys).ToNot(BeNil(), "api_keys were nulled by the partial save")
		Expect(*ondisk.ApiKeys).To(Equal([]string{"k1"}))
		Expect(ondisk.PIIDefaultDetectors).ToNot(BeNil(), "pii_default_detectors were nulled by the partial save")
		Expect(*ondisk.PIIDefaultDetectors).To(Equal([]string{"det-a"}))
	})

	// The MITM listener resolves its per-host PII detectors once at start
	// (startMITMLocked → ResolvePIIPolicy), and the handler used to restart it
	// only when mitm_listen changed. So an admin toggling a default detector
	// (the Middleware detector table POSTs only pii_default_detectors) left
	// cloud-proxy traffic unredacted until the next reboot. A
	// pii_default_detectors change must now rebuild the listener.
	It("rebuilds the MITM listener when only pii_default_detectors changes", func() {
		rec := post(`{"mitm_listen":"127.0.0.1:0"}`)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		srv1 := app.MITMServer()
		Expect(srv1).ToNot(BeNil(), "listener should be running after mitm_listen is set")

		rec = post(`{"pii_default_detectors":["det-a"]}`)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(app.MITMServer()).ToNot(BeIdenticalTo(srv1),
			"a default-detector change must restart the listener so it picks up the new detectors")
	})

	// Residual #9125: enabling the watchdog from a cold (off) state via the
	// React master toggle must start the live watchdog immediately, without a
	// restart. The toggle posts watchdog_idle_enabled/busy_enabled=true while
	// the vestigial watchdog_enabled stays false (it was loaded false). The
	// old handler keyed its stop decision off that raw watchdog_enabled=false
	// and called StopWatchdog(), so the watchdog never started until restart.
	It("starts the live watchdog on a cold enable even when watchdog_enabled=false", func() {
		Expect(app.ModelLoader().GetWatchDog()).To(BeNil(), "precondition: watchdog should be off")

		rec := post(`{"watchdog_enabled":false,"watchdog_idle_enabled":true,"watchdog_busy_enabled":true,"watchdog_idle_timeout":"15m","watchdog_busy_timeout":"5m","watchdog_interval":"1s"}`)
		Expect(rec.Code).To(Equal(http.StatusOK))

		Expect(app.ModelLoader().GetWatchDog()).ToNot(BeNil(),
			"watchdog should be running after a cold enable, without waiting for a restart")
	})
})
