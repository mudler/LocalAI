// SPDX-License-Identifier: MIT
package application

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/addons/hostsettings"
)

func TestAddonSettingsAuthorityUpgradePolicy(t *testing.T) {
	ctx := context.Background()
	owner, err := hostsettings.Open(filepath.Join(t.TempDir(), "policy.json"), &hostsettings.Migration{Mode: hostsettings.Standalone, Source: "test", Version: "1", Candidates: []hostsettings.Policy{{}}})
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	a := &Application{applicationConfig: &config.ApplicationConfig{Addons: config.AddonConfig{Enabled: true}, AutoUpgradeAddons: true}, addonGateway: &gateway.Gateway{HostSettings: owner}}
	check := func(want bool, wantErr bool) {
		t.Helper()
		enabled, err := a.addonUpgradePolicy(ctx)
		if enabled != want || (err != nil) != wantErr {
			t.Fatalf("policy = %v, %v; want %v error=%v", enabled, err, want, wantErr)
		}
	}
	check(false, false)
	s, err := owner.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	on := true
	if _, err = owner.Update(ctx, hostsettings.Patch{AutoUpgradeAddons: &on}, s.Revision, "enable"); err != nil {
		t.Fatal(err)
	}
	a.applicationConfig.AutoUpgradeAddons = false
	check(true, false)
	if err = owner.Close(); err != nil {
		t.Fatal(err)
	}
	a.applicationConfig.AutoUpgradeAddons = true
	check(false, true)
	a.applicationConfig.Addons.Enabled = false
	check(true, false)
	a.applicationConfig.AutoUpgradeAddons = false
	check(false, false)
}
