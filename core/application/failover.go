package application

import (
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/failover"
	"github.com/mudler/LocalAI/pkg/grpc"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/xlog"
)

// preloadModelByName is a seam over backend.PreloadModelByName so tests can
// substitute a controllable loader instead of touching real models/disk.
var preloadModelByName = backend.PreloadModelByName

// applyFailoverWarmTargets pins warm failover targets in the watchdog and
// loads them, so a switch does not wait for a cold load.
//
// SyncPinnedModelsToWatchdog runs synchronously: it is a cheap in-memory
// update, and the pin must land before the watchdog can evict a target that
// is about to become (or stay) a chain's active path. Preloading is not
// cheap — it can download or load a multi-GB model — and this callback runs
// on the failover manager's single scheduler goroutine (Sync, called from
// Tick, called from Run), before that tick's probes fire. A slow or hung
// load here would freeze probing and fail-back for every chain, so it runs
// in its own goroutine instead of blocking the scheduler loop.
func (a *Application) applyFailoverWarmTargets(warm []string) {
	a.SyncPinnedModelsToWatchdog()
	go func() {
		for _, name := range warm {
			if _, err := preloadModelByName(a.ApplicationConfig().Context, a.ModelConfigLoader(), a.ModelLoader(), a.ApplicationConfig(), name); err != nil {
				xlog.Warn("failover: could not preload warm target", "model", name, "error", err)
			}
		}
	}()
}

// failoverLoadedBackend gives the failover prober the running backend of a
// local target without ever loading it. CheckIsLoaded may run the loader's
// own health check and drop a dead process; the target is then "not loaded"
// and the next real request loads and judges it.
func failoverLoadedBackend(ml *model.ModelLoader) failover.LoadedFunc {
	return func(cfg config.ModelConfig) grpc.Backend {
		m := ml.CheckIsLoaded(cfg.ModelID())
		if m == nil {
			return nil
		}
		// Load always enables parallel requests; match it in case this is
		// the first client built for the model.
		return m.GRPC(true, ml.GetWatchDog())
	}
}
