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
	"github.com/mudler/LocalAI/core/gallery"
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

	It("rejects bad ports and a bad SearXNG address for the chats' web tools", func() {
		rec := post(`{"chats_web_ports":"80,http"}`)
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
		Expect(rec.Body.String()).To(ContainSubstring("Invalid chats_web_ports"))
		rec = post(`{"chats_web_searxng_url":"file:///etc/passwd"}`)
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
		Expect(rec.Body.String()).To(ContainSubstring("Invalid chats_web_searxng_url"))
		rec = post(`{"chats_web_searxng_url":"https://u:p@searx.example.org"}`)
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
		rec = post(`{"chats_web_search_providers":"duckduckgo,altavista"}`)
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
		Expect(rec.Body.String()).To(ContainSubstring("Invalid chats_web_search_providers"))
	})

	It("switches the chats' web tools and keeps them on by default", func() {
		cfg := app.ApplicationConfig()
		Expect(cfg.DisableChatsWebSearch || cfg.DisableChatsWebFetch).To(BeFalse())
		rec := post(`{"chats_web_search":false,"chats_web_ports":"80, 443, 8080","chats_web_searxng_url":"https://searx.example.org","chats_web_search_providers":"searxng,brave_html"}`)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(cfg.DisableChatsWebSearch).To(BeTrue())
		Expect(cfg.DisableChatsWebFetch).To(BeFalse())
		Expect(cfg.ChatsWebPorts).To(Equal("80, 443, 8080"))
		Expect(cfg.ChatsWebSearXNGURL).To(Equal("https://searx.example.org"))
		Expect(cfg.ChatsWebSearchProviders).To(Equal("searxng,brave_html"))
	})

	It("rejects a host-commands mode and pins the chats do not understand", func() {
		rec := post(`{"chats_host_exec":"always"}`)
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
		Expect(rec.Body.String()).To(ContainSubstring("Invalid chats_host_exec"))
		rec = post(`{"chats_tool_providers":{"write_file":"files"}}`)
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
		Expect(rec.Body.String()).To(ContainSubstring("Invalid chats_tool_providers"))
		rec = post(`{"chats_tool_providers":{"bash":"Not An ID"}}`)
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
		rec = post(`{"chats_tool_providers":{"writing-skills":"office"}}`)
		Expect(rec.Code).To(Equal(http.StatusBadRequest), "a built-in skill an add-on may not replace")
	})

	It("saves a pin for a built-in skill in the same map as the tools", func() {
		rec := post(`{"chats_tool_providers":{"bash":"sandbox","xlsx":"office"}}`)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(app.ApplicationConfig().ChatsToolProviders).To(Equal(map[string]string{"bash": "sandbox", "xlsx": "office"}))
	})

	It("saves the host-commands mode and a pin", func() {
		rec := post(`{"chats_host_exec":"ask","chats_tool_providers":{"bash":"sandbox"}}`)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(app.ApplicationConfig().ChatsHostExec).To(Equal("ask"))
		Expect(app.ApplicationConfig().ChatsToolProviders).To(Equal(map[string]string{"bash": "sandbox"}))
	})

	It("checks the Sandbox network settings and asks for the phrase to reach private addresses", func() {
		for body, want := range map[string]string{
			`{"chats_egress_ceiling":"wide"}`:                                          "Invalid chats_egress_ceiling",
			`{"chats_egress_default":"everything"}`:                                    "Invalid chats_egress_default",
			`{"chats_egress_ports":"443,22"}`:                                          "Invalid chats_egress_ports",
			`{"chats_egress_ports":"443,25"}`:                                          "Invalid chats_egress_ports",
			`{"chats_egress_extra_registries":"*.example.com"}`:                        "Invalid chats_egress_extra_registries",
			`{"chats_egress_extra_registries":"0x7f.1"}`:                               "Invalid chats_egress_extra_registries",
			`{"chats_egress_allowed_hosts":"*.example.com"}`:                           "Invalid chats_egress_allowed_hosts",
			`{"chats_egress_allowed_hosts":"https://example.com"}`:                     "Invalid chats_egress_allowed_hosts",
			`{"chats_egress_allowed_hosts":"example.com:22"}`:                          "Invalid chats_egress_allowed_hosts",
			`{"chats_egress_allowed_hosts":"10.0.0.5"}`:                                "Invalid chats_egress_allowed_hosts",
			`{"chats_egress_audit_days":0}`:                                            "Invalid chats_egress_audit_days",
			`{"chats_egress_audit_days":366}`:                                          "Invalid chats_egress_audit_days",
			`{"chats_index_max_text_bytes":1024}`:                                      "Invalid chats_index_max_text_bytes",
			`{"chats_index_max_text_bytes":33554432}`:                                  "Invalid chats_index_max_text_bytes",
			`{"chats_egress_allow_private":true}`:                                      "chats_egress_private_confirm",
			`{"chats_egress_allow_private":true,"chats_egress_private_confirm":"yes"}`: "chats_egress_private_confirm",
		} {
			rec := post(body)
			Expect(rec.Code).To(Equal(http.StatusBadRequest), body)
			Expect(rec.Body.String()).To(ContainSubstring(want), body)
		}
		cfg := app.ApplicationConfig()
		Expect(cfg.ChatsEgressAllowPrivate).To(BeFalse())
		rec := post(`{"chats_egress_ceiling":"registries","chats_egress_default":"registries","chats_egress_ports":"443","chats_egress_extra_registries":"Mirror.Example.org, pypi.internal.example","chats_egress_allowed_hosts":"API.example.com, git.example.org:8443, 10.0.0.5","chats_egress_audit_days":7,` +
			`"chats_egress_allow_private":true,"chats_egress_private_confirm":"` + config.EgressPrivatePhrase + `"}`)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(cfg.ChatsEgressCeiling).To(Equal("registries"))
		Expect(cfg.ChatsEgressDefault).To(Equal("registries"))
		Expect(cfg.ChatsEgressPorts).To(Equal("443"))
		Expect(cfg.ChatsEgressAllowedHosts).To(Equal("API.example.com, git.example.org:8443, 10.0.0.5"), "an IP literal is accepted in the same request that turns the private switch on")
		Expect(cfg.ChatsEgressAuditDays).To(Equal(7))
		Expect(cfg.ChatsEgressAllowPrivate).To(BeTrue())
		raw, err := os.ReadFile(filepath.Join(tmp, "runtime_settings.json"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(raw)).NotTo(ContainSubstring(config.EgressPrivatePhrase), "the phrase is not stored")
		// Already on: saving it again needs no phrase.
		rec = post(`{"chats_egress_allow_private":true}`)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
	})

	It("checks and saves the step limit of a chat turn", func() {
		Expect(app.ApplicationConfig().EffectiveChatsMaxIterations()).To(Equal(config.DefaultChatsMaxIterations), "unset: the default")
		for _, body := range []string{`{"chats_max_iterations":0}`, `{"chats_max_iterations":501}`} {
			rec := post(body)
			Expect(rec.Code).To(Equal(http.StatusBadRequest), body)
			Expect(rec.Body.String()).To(ContainSubstring("Invalid chats_max_iterations"), body)
		}
		rec := post(`{"chats_max_iterations":80}`)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(app.ApplicationConfig().ChatsMaxIterations).To(Equal(80))
		Expect(app.ApplicationConfig().EffectiveChatsMaxIterations()).To(Equal(80))
		raw, err := os.ReadFile(filepath.Join(tmp, "runtime_settings.json"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(raw)).To(ContainSubstring(`"chats_max_iterations": 80`))
	})

	It("checks and saves the limits of a chat's goal", func() {
		cfg := app.ApplicationConfig()
		Expect(cfg.EffectiveChatsGoalMaxTurns()).To(Equal(config.DefaultChatsGoalMaxTurns), "unset: the default")
		Expect(cfg.EffectiveChatsGoalMaxSteps()).To(Equal(config.DefaultChatsGoalMaxSteps), "unset: the default")
		for body, msg := range map[string]string{
			`{"chats_goal_max_turns":0}`:     "Invalid chats_goal_max_turns",
			`{"chats_goal_max_turns":201}`:   "Invalid chats_goal_max_turns",
			`{"chats_goal_max_steps":0}`:     "Invalid chats_goal_max_steps",
			`{"chats_goal_max_steps":10001}`: "Invalid chats_goal_max_steps",
		} {
			rec := post(body)
			Expect(rec.Code).To(Equal(http.StatusBadRequest), body)
			Expect(rec.Body.String()).To(ContainSubstring(msg), body)
		}
		rec := post(`{"chats_goal_max_turns":5,"chats_goal_max_steps":90}`)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(cfg.EffectiveChatsGoalMaxTurns()).To(Equal(5))
		Expect(cfg.EffectiveChatsGoalMaxSteps()).To(Equal(90))
		raw, err := os.ReadFile(filepath.Join(tmp, "runtime_settings.json"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(raw)).To(ContainSubstring(`"chats_goal_max_turns": 5`))
		Expect(string(raw)).To(ContainSubstring(`"chats_goal_max_steps": 90`))
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
	It("signals a development preference restart without cycling the watchdog or resolver", func() {
		Expect(post(`{"watchdog_idle_enabled":true}`).Code).To(Equal(http.StatusOK))
		watchdog := app.ModelLoader().GetWatchDog()
		Expect(watchdog).ToNot(BeNil())
		cfg := app.ApplicationConfig()
		cfg.PreferDevelopmentBackends = false
		cfg.SystemState.PreferDevelopmentBackends = false
		app.ModelLoader().SetExternalBackend("example-engine", filepath.Join(tmp, "stable", "run.sh"))
		paths := app.ModelLoader().GetAllExternalBackends(nil)

		rec := post(`{"prefer_development_backends":true}`)
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		// Check lifecycle isolation before the response so the regression also
		// catches the old handler's unrelated watchdog restart.
		Expect(app.ModelLoader().GetWatchDog()).To(BeIdenticalTo(watchdog))
		Expect(cfg.PreferDevelopmentBackends).To(BeTrue())
		Expect(cfg.SystemState.PreferDevelopmentBackends).To(BeFalse())
		Expect(app.ModelLoader().GetAllExternalBackends(nil)).To(Equal(paths))
		persisted, err := cfg.ReadPersistedSettings()
		Expect(err).ToNot(HaveOccurred())
		Expect(persisted.PreferDevelopmentBackends).ToNot(BeNil())
		Expect(*persisted.PreferDevelopmentBackends).To(BeTrue())
		var response map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &response)).To(Succeed())
		Expect(response).To(HaveKeyWithValue("success", true))
		Expect(response).To(HaveKeyWithValue("message", "Settings updated successfully"))
		Expect(response).To(HaveKeyWithValue("restart_required", true))

		// Re-saving the same value is not a new resolver change.
		rec = post(`{"prefer_development_backends":true}`)
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(json.Unmarshal(rec.Body.Bytes(), &response)).To(Succeed())
		Expect(response).To(HaveKeyWithValue("restart_required", false))
		Expect(app.ModelLoader().GetWatchDog()).To(BeIdenticalTo(watchdog))

		// A genuine watchdog update still replaces the live instance and does
		// not request a server restart.
		rec = post(`{"watchdog_interval":"3s"}`)
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(app.ModelLoader().GetWatchDog()).ToNot(BeIdenticalTo(watchdog))
		Expect(json.Unmarshal(rec.Body.Bytes(), &response)).To(Succeed())
		Expect(response).To(HaveKeyWithValue("restart_required", false))

		watchdog = app.ModelLoader().GetWatchDog()
		rec = post(`{"prefer_development_backends":false}`)
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(json.Unmarshal(rec.Body.Bytes(), &response)).To(Succeed())
		Expect(response).To(HaveKeyWithValue("restart_required", true))
		Expect(cfg.PreferDevelopmentBackends).To(BeFalse())
		Expect(app.ModelLoader().GetWatchDog()).To(BeIdenticalTo(watchdog))

		// A mixed save must independently perform both actions.
		rec = post(`{"prefer_development_backends":true,"watchdog_interval":"4s"}`)
		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(json.Unmarshal(rec.Body.Bytes(), &response)).To(Succeed())
		Expect(response).To(HaveKeyWithValue("restart_required", true))
		Expect(app.ModelLoader().GetWatchDog()).ToNot(BeIdenticalTo(watchdog))
		Expect(cfg.PreferDevelopmentBackends).To(BeTrue())
		Expect(cfg.SystemState.PreferDevelopmentBackends).To(BeFalse())
		Expect(app.ModelLoader().GetAllExternalBackends(nil)).To(Equal(paths))
	})
	// The UI lists from the cached model listing, which is keyed by nothing.
	// A gallery change through this endpoint (for example a tightened
	// verification policy) must drop it, or the UI keeps showing what the old
	// configuration admitted until the next background refresh, or forever
	// when the new policy refuses the gallery.
	It("drops the cached model listing when the galleries change", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("- name: acme-model\n"))
		}))
		DeferCleanup(srv.Close)
		gallery.ResetGalleryModelCache()
		DeferCleanup(gallery.ResetGalleryModelCache)

		loose := config.GalleryVerification{Issuer: "https://token.actions.githubusercontent.com", IdentityRegex: "^https://github.com/acme/.*$"}
		appConfig := app.ApplicationConfig()
		appConfig.Galleries = []config.Gallery{{Name: "acme", URL: srv.URL, Verification: &loose}}

		listedPolicy := func() config.GalleryVerification {
			GinkgoHelper()
			models, err := gallery.AvailableGalleryModelsCached(appConfig.Galleries, appConfig.SystemState)
			Expect(err).ToNot(HaveOccurred())
			Expect(models).To(HaveLen(1))
			Expect(models[0].Gallery.Verification).ToNot(BeNil())
			return *models[0].Gallery.Verification
		}
		Expect(listedPolicy()).To(Equal(loose))

		tightened := loose
		tightened.SourceRepository = "https://github.com/acme/gallery"
		body, err := json.Marshal(map[string]any{
			"galleries": []config.Gallery{{Name: "acme", URL: srv.URL, Verification: &tightened}},
		})
		Expect(err).ToNot(HaveOccurred())
		rec := post(string(body))
		Expect(rec.Code).To(Equal(http.StatusOK), rec.Body.String())
		Expect(appConfig.Galleries[0].Verification.SourceRepository).To(Equal(tightened.SourceRepository), "precondition: the setting was applied")

		Expect(listedPolicy()).To(Equal(tightened), "the listing cached under the old gallery configuration was served")
	})
})
