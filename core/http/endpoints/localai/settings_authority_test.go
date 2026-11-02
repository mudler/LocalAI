// SPDX-License-Identifier: MIT
package localai_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	. "github.com/mudler/LocalAI/core/http/endpoints/localai"
	"github.com/mudler/LocalAI/pkg/system"
)

func TestAddonSettingsAuthorityLegacyWrites(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dir := t.TempDir()
	st, err := system.GetSystemState(system.WithModelPath(filepath.Join(dir, "models")), system.WithBackendPath(filepath.Join(dir, "backends")))
	if err != nil {
		t.Fatal(err)
	}
	// Disable unrelated chat background pruning: it reads audit retention while
	// this handler test deliberately changes that local setting.
	app, err := application.New(config.WithContext(ctx), config.WithSystemState(st), config.DisableChats)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Shutdown()
	cfg := app.ApplicationConfig()
	cfg.DynamicConfigsDir = dir
	cfg.Addons.Enabled = true
	cfg.ChatsEgressCeiling = "all"
	cfg.AutoUpgradeAddons = true
	// Keep the mixed patch's logging toggle distinct from the startup default.
	cfg.EnableBackendLogging = false
	e := echo.New()
	e.POST("/api/settings", UpdateSettingsEndpoint(app))
	e.GET("/api/settings", GetSettingsEndpoint(app))
	post := func(body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/api/settings", bytes.NewBufferString(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		e.ServeHTTP(w, r)
		return w
	}
	if w := post(`{"chats_egress_audit_days":17}`); w.Code != 200 {
		t.Fatalf("local setting: %d %s", w.Code, w.Body)
	}
	path := filepath.Join(dir, "runtime_settings.json")
	before, err := cfg.ReadPersistedSettings()
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := json.Marshal(before)
	if err != nil {
		t.Fatal(err)
	}
	disk, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	keys := []string{"chats_egress_ceiling", "chats_egress_default", "chats_egress_allow_private", "chats_egress_ports", "chats_egress_extra_registries", "chats_egress_allowed_hosts", "chats_tool_providers", "auto_upgrade_addons"}
	for _, key := range keys {
		for _, alias := range []string{key, strings.ToUpper(key), strings.ReplaceAll(key, "s", "ſ")} {
			for _, value := range []string{"null", `false`, `"all"`, `{}`} {
				t.Run(alias+"="+value, func(t *testing.T) {
					// Also include side-effectful settings; none may apply on conflict.
					w := post(`{"chats_egress_audit_days":19,"p2p_token":"0","enable_backend_logging":true,"` + alias + `":` + value + `}`)
					if w.Code != http.StatusConflict {
						t.Errorf("got %d %s", w.Code, w.Body)
					}
					var response struct {
						Success bool
						Error   string
					}
					if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || response.Success || !strings.Contains(response.Error, "add-on owner") {
						t.Errorf("missing owner conflict: %s (decode: %v)", w.Body, err)
					}
					after, err := cfg.ReadPersistedSettings()
					if err != nil {
						t.Fatal(err)
					}
					raw, err := json.Marshal(after)
					if err != nil {
						t.Fatal(err)
					}
					current, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(raw, baseline) || !bytes.Equal(current, disk) || cfg.ChatsEgressAuditDays != 17 || !cfg.AutoUpgradeAddons || cfg.P2PToken != "" || cfg.EnableBackendLogging {
						t.Fatalf("rejected patch changed local state: persistedEqual=%t diskEqual=%t after: auditDays=%d autoUpgrade=%t tokenEmpty=%t backendLogging=%t", bytes.Equal(raw, baseline), bytes.Equal(current, disk), cfg.ChatsEgressAuditDays, cfg.AutoUpgradeAddons, cfg.P2PToken == "", cfg.EnableBackendLogging)
					}
				})
			}
		}
	}
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/settings", nil))
	var got map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || string(got["addon_host_settings_authority"]) != `"owner_required"` {
		t.Fatalf("GET unavailable owner must retain local settings and explicit authority status: %s", w.Body)
	}
	if string(got["chats_egress_audit_days"]) != "17" {
		t.Fatalf("local settings unavailable: %s", w.Body)
	}
	for _, key := range keys {
		if _, ok := got[key]; ok {
			t.Errorf("stale migrated field %s exposed", key)
		}
	}
	// Duplicate keys and JSON escapes cannot disguise presence either.
	for _, body := range []string{
		`{"auto_upgrade_addons":false,"auto_upgrade_addons":null}`,
		`{"auto_\u0075pgrade_addons":null}`,
	} {
		if w := post(body); w.Code != http.StatusConflict {
			t.Fatalf("presence bypass: %s: %d %s", body, w.Code, w.Body)
		}
	}
	cfg.Addons.Enabled = false
	if w := post(`{"auto_upgrade_addons":false}`); w.Code != 200 || cfg.AutoUpgradeAddons {
		t.Fatalf("disabled compatibility: %d %s", w.Code, w.Body)
	}
	w = httptest.NewRecorder()
	e.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/settings", nil))
	got = nil
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusOK || string(got["auto_upgrade_addons"]) != "false" || got["addon_host_settings_authority"] != nil {
		t.Fatalf("disabled GET compatibility: %d %s", w.Code, w.Body)
	}
}
