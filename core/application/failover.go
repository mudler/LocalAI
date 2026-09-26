package application

import (
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/xlog"
)

// applyFailoverWarmTargets pins warm failover targets in the watchdog and
// loads them, so a switch does not wait for a cold load.
func (a *Application) applyFailoverWarmTargets(warm []string) {
	a.SyncPinnedModelsToWatchdog()
	for _, name := range warm {
		if _, err := backend.PreloadModelByName(a.ApplicationConfig().Context, a.ModelConfigLoader(), a.ModelLoader(), a.ApplicationConfig(), name); err != nil {
			xlog.Warn("failover: could not preload warm target", "model", name, "error", err)
		}
	}
}
