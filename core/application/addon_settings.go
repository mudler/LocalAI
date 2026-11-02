// SPDX-License-Identifier: MIT
package application

import (
	"os"
	"path/filepath"
	"reflect"
	"sync"

	"github.com/mudler/LocalAI/core/services/addons"
	"github.com/mudler/LocalAI/core/services/addons/hostsettings"
)

// Reject symlinks in every existing ancestor before creating owner state.
func validateAddonSettingsRoot(root string) error {
	if !filepath.IsAbs(root) {
		return addons.ErrConfiguration
	}
	for p := filepath.Clean(root); ; p = filepath.Dir(p) {
		info, err := os.Lstat(p)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if err == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
			return addons.ErrConfiguration
		}
		if p == filepath.Dir(p) {
			return nil
		}
	}
}

// Called only after NewManager has acquired the owner's storage guard.
func (a *Application) openAddonHostSettings() error {
	c := a.applicationConfig
	legacy := hostsettings.Policy{
		Ceiling: c.ChatsEgressCeiling, Default: c.ChatsEgressDefault,
		AllowPrivate: c.ChatsEgressAllowPrivate, Ports: c.ChatsEgressPorts,
		ExtraRegistries: c.ChatsEgressExtraRegistries, AllowedHosts: c.ChatsEgressAllowedHosts,
		ToolProviders: c.ChatsToolProviders, AutoUpgradeAddons: c.AutoUpgradeAddons,
	}
	migration := c.Addons.HostSettingsMigration
	if !c.Addons.DedicatedOwner {
		// The combined host has one effective configuration, already merged from
		// startup and runtime settings. There are no frontend candidates to merge.
		migration = &hostsettings.Migration{Mode: hostsettings.Standalone, Source: "effective-host-config", Version: "1", Candidates: []hostsettings.Policy{legacy}}
	} else if migration == nil && c.Addons.MigrateHostSettings {
		migration = &hostsettings.Migration{Mode: hostsettings.Distributed, OwnerSelected: true, Source: "effective-owner-config", Version: "1", Candidates: []hostsettings.Policy{legacy}}
	} else if migration == nil && reflect.DeepEqual(legacy, hostsettings.Policy{}) {
		migration = &hostsettings.Migration{Mode: hostsettings.Distributed, OwnerSelected: true, Source: "fresh-owner-defaults", Version: "1", Candidates: []hostsettings.Policy{{}}}
	} else if migration != nil && (migration.Mode != hostsettings.Distributed || !migration.OwnerSelected) {
		return hostsettings.ErrMigrationRequired
	}
	store, err := hostsettings.Open(filepath.Join(c.Addons.Root, "host-settings.json"), migration)
	if err != nil {
		return err
	}
	var once sync.Once
	a.addonHostSettings = store
	done := make(chan struct{})
	a.addonHostSettingsDone = done
	a.addonHostSettingsClose = func() {
		once.Do(func() {
			if a.addonHostSettingsOnClose != nil {
				a.addonHostSettingsOnClose()
			}
			_ = store.Close()
			close(done)
		})
	}
	return nil
}
