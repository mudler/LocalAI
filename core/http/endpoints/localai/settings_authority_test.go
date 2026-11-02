// SPDX-License-Identifier: MIT
package localai_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	. "github.com/mudler/LocalAI/core/http/endpoints/localai"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("TestAddonSettingsAuthorityLegacyWrites", Label("addon-settings-authority"), func() {
	var cfg *config.ApplicationConfig
	var e *echo.Echo
	var path string
	var baseline, disk []byte
	keys := []string{"chats_egress_ceiling", "chats_egress_default", "chats_egress_allow_private", "chats_egress_ports", "chats_egress_extra_registries", "chats_egress_allowed_hosts", "chats_tool_providers", "auto_upgrade_addons"}
	post := func(body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/api/settings", bytes.NewBufferString(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		e.ServeHTTP(w, r)
		return w
	}
	BeforeEach(func() {
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		dir := GinkgoT().TempDir()
		st, err := system.GetSystemState(system.WithModelPath(filepath.Join(dir, "models")), system.WithBackendPath(filepath.Join(dir, "backends")))
		Expect(err).NotTo(HaveOccurred())
		// Disable unrelated pruning while this test changes audit retention.
		app, err := application.New(config.WithContext(ctx), config.WithSystemState(st), config.DisableChats)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(app.Shutdown)
		cfg = app.ApplicationConfig()
		cfg.DynamicConfigsDir = dir
		cfg.Addons.Enabled = true
		cfg.ChatsEgressCeiling = "all"
		cfg.AutoUpgradeAddons = true
		cfg.EnableBackendLogging = false
		e = echo.New()
		e.POST("/api/settings", UpdateSettingsEndpoint(app))
		e.GET("/api/settings", GetSettingsEndpoint(app))
		w := post(`{"chats_egress_audit_days":17}`)
		Expect(w.Code).To(Equal(http.StatusOK), w.Body.String())
		path = filepath.Join(dir, "runtime_settings.json")
		before, err := cfg.ReadPersistedSettings()
		Expect(err).NotTo(HaveOccurred())
		baseline, err = json.Marshal(before)
		Expect(err).NotTo(HaveOccurred())
		disk, err = os.ReadFile(path)
		Expect(err).NotTo(HaveOccurred())
	})
	assertUnchanged := func() {
		GinkgoHelper()
		after, err := cfg.ReadPersistedSettings()
		Expect(err).NotTo(HaveOccurred())
		raw, err := json.Marshal(after)
		Expect(err).NotTo(HaveOccurred())
		current, err := os.ReadFile(path)
		Expect(err).NotTo(HaveOccurred())
		Expect(raw).To(Equal(baseline), "persisted settings must remain atomic")
		Expect(current).To(Equal(disk), "file bytes must remain unchanged")
		Expect(cfg.ChatsEgressAuditDays).To(Equal(17))
		Expect(cfg.AutoUpgradeAddons).To(BeTrue())
		Expect(cfg.P2PToken).To(BeEmpty())
		Expect(cfg.EnableBackendLogging).To(BeFalse())
	}
	for _, key := range keys {
		for _, alias := range []struct{ name, key string }{{"canonical", key}, {"uppercase", strings.ToUpper(key)}, {"unicode-long-s", strings.ReplaceAll(key, "s", "ſ")}} {
			for _, value := range []string{"null", `false`, `"all"`, `{}`} {
				It(fmt.Sprintf("rejects %s %s=%s atomically with unchanged file bytes", alias.name, key, value), func() {
					w := post(`{"chats_egress_audit_days":19,"p2p_token":"0","enable_backend_logging":true,"` + alias.key + `":` + value + `}`)
					Expect(w.Code).To(Equal(http.StatusConflict), w.Body.String())
					var response struct {
						Success bool
						Error   string
					}
					Expect(json.Unmarshal(w.Body.Bytes(), &response)).To(Succeed())
					Expect(response.Success).To(BeFalse())
					Expect(response.Error).To(ContainSubstring("add-on owner"))
					assertUnchanged()
				})
			}
		}
	}
	It("GET retains local settings but omits migrated fields when the owner is unavailable", func() {
		w := httptest.NewRecorder()
		e.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/settings", nil))
		var got map[string]json.RawMessage
		Expect(json.Unmarshal(w.Body.Bytes(), &got)).To(Succeed())
		Expect(w.Code).To(Equal(http.StatusOK))
		Expect(string(got["addon_host_settings_authority"])).To(Equal(`"owner_required"`))
		Expect(string(got["chats_egress_audit_days"])).To(Equal("17"))
		for _, key := range keys {
			Expect(got).NotTo(HaveKey(key))
		}
	})
	for _, test := range []struct{ name, body string }{
		{"duplicate keys", `{"auto_upgrade_addons":false,"auto_upgrade_addons":null}`},
		{"JSON escaped keys", `{"auto_\u0075pgrade_addons":null}`},
	} {
		It("rejects presence disguised with "+test.name, func() {
			w := post(test.body)
			Expect(w.Code).To(Equal(http.StatusConflict), w.Body.String())
			assertUnchanged()
		})
	}
	It("preserves no-addons POST and GET compatibility", func() {
		cfg.Addons.Enabled = false
		w := post(`{"auto_upgrade_addons":false}`)
		Expect(w.Code).To(Equal(http.StatusOK), w.Body.String())
		Expect(cfg.AutoUpgradeAddons).To(BeFalse())
		w = httptest.NewRecorder()
		e.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/settings", nil))
		var got map[string]json.RawMessage
		Expect(json.Unmarshal(w.Body.Bytes(), &got)).To(Succeed())
		Expect(w.Code).To(Equal(http.StatusOK))
		Expect(string(got["auto_upgrade_addons"])).To(Equal("false"))
		Expect(got).NotTo(HaveKey("addon_host_settings_authority"))
	})
})
