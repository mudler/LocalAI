package application

import (
	"context"
	"time"

	"github.com/mudler/xlog"
)

// runAddonAutoUpgrade runs the add-on upgrade pass on the schedule of the
// backend update check: once after a short delay, then on every interval. The
// setting is read at each pass, so a change in the Settings page takes effect
// without a restart.
func (a *Application) runAddonAutoUpgrade(ctx context.Context, initialDelay, interval time.Duration) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(initialDelay):
	}
	a.autoUpgradeAddons(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.autoUpgradeAddons(ctx)
		}
	}
}

// autoUpgradeAddons is one pass. With the setting off it does nothing at all:
// no library fetch and no upgrade. With it on, the owner runs the same pinned
// upgrade an administrator starts by hand.
func (a *Application) autoUpgradeAddons(ctx context.Context) {
	m := a.addonManager
	if m == nil || !a.applicationConfig.AutoUpgradeAddons {
		return
	}
	results := m.AutoUpgrade(ctx)
	upgraded := 0
	for _, r := range results {
		if r.Upgraded {
			upgraded++
		}
	}
	xlog.Debug("Add-on auto-upgrade pass finished", "candidates", len(results), "upgraded", upgraded)
}
