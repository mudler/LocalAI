// SPDX-License-Identifier: MIT
package application

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/addons/usersettings"
	"github.com/mudler/xlog"
)

// AddonUserSettingsStore returns the store of the per-person add-on settings,
// created once so the routes and the chats share it. An error is cached: a
// store that cannot be opened stays unavailable, and startup stops rather than
// fall back to a copy that another frontend would not see.
func (a *Application) AddonUserSettingsStore() (usersettings.Store, error) {
	a.addonUserSettingsOnce.Do(func() {
		if db := a.authDB; db != nil {
			s, err := usersettings.NewGormStore(db)
			if err != nil {
				a.addonUserSettingsErr = fmt.Errorf("open the add-on user settings table: %w", err)
				return
			}
			a.addonUserSettings = s
			return
		}
		path := ""
		if dp := a.applicationConfig.DataPath; dp != "" {
			path = filepath.Join(dp, "addon-user-settings.json")
		} else {
			xlog.Warn("No data path and no account database: add-on user settings are kept in memory only and do not survive a restart")
		}
		s, err := usersettings.NewFileStore(path)
		if err != nil {
			a.addonUserSettingsErr = fmt.Errorf("open the add-on user settings file %q: %w", path, err)
			return
		}
		a.addonUserSettings = s
	})
	return a.addonUserSettings, a.addonUserSettingsErr
}

// studioUserSetting reads one stored answer for a chat owner. Without an
// account database there is one local owner, whichever name the chat carries;
// the routes write under the same key. A store that cannot answer is an error:
// the caller treats it as the default, never as a looser choice.
func (a *Application) studioUserSetting(ctx context.Context, user, addonID, key string) (string, bool, error) {
	store, err := a.AddonUserSettingsStore()
	if err != nil {
		return "", false, err
	}
	if a.authDB == nil {
		user = auth.AddonLocalOwnerID
	}
	m, err := store.Get(ctx, user, addonID)
	if err != nil {
		return "", false, err
	}
	v, ok := m[key]
	return v, ok, nil
}

// studioSetUserSetting stores one answer for a chat owner, in the store that
// studioUserSetting reads. The host's own settings (the memory switch and the
// save level) go through it.
func (a *Application) studioSetUserSetting(ctx context.Context, user, addonID, key, value string) error {
	store, err := a.AddonUserSettingsStore()
	if err != nil {
		return err
	}
	if a.authDB == nil {
		user = auth.AddonLocalOwnerID
	}
	return store.Set(ctx, user, addonID, key, value)
}
