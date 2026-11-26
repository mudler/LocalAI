package application

import (
	"context"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/clustersettings"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/xlog"
)

// ClusterSettings returns the shared memory settings of a distributed
// cluster, or nil in standalone mode (where runtime_settings.json holds
// them).
func (a *Application) ClusterSettings() *clustersettings.Service {
	return a.clusterSettings
}

// startClusterSettings makes the memory settings cluster-wide: stored in the
// database, replicated over the bus, and put into effect on every frontend.
// The frontend's env only seeds an empty store. Without it the frontend
// keeps its local settings, as in standalone mode, and says so.
func (a *Application) startClusterSettings(ctx context.Context) {
	store, err := clustersettings.NewDBStore(a.distributedDB())
	if err != nil {
		xlog.Error("memory settings will not be shared between frontends: could not prepare the store", "error", err)
		return
	}
	if err := a.StartClusterSettings(ctx, a.distributed.Broadcaster, store); err != nil {
		xlog.Error("memory settings will not be shared between frontends", "error", err)
	}
}

// StartClusterSettings attaches the shared memory settings on bus and store
// to this frontend: it applies the stored document, seeds an empty store
// from this frontend's env, and from then on routes the memory keys of
// POST /api/settings to the store. Distributed startup calls it; tests call
// it with a fake bus.
func (a *Application) StartClusterSettings(ctx context.Context, bus messaging.Broadcaster, store *clustersettings.DBStore) error {
	svc, err := clustersettings.New(ctx, clustersettings.Options{
		Bus:   bus,
		Store: store,
		Apply: a.applyClusterMemorySettings,
	})
	if err != nil {
		return err
	}
	envCfg := a.startupConfig
	if envCfg == nil {
		envCfg = a.ApplicationConfig()
	}
	if seeded, err := svc.Seed(ctx, config.EnvMemorySettings(envCfg), "env"); err != nil {
		xlog.Warn("could not seed the cluster memory settings from the environment", "error", err)
	} else if seeded {
		xlog.Info("Stored this frontend's memory settings from the environment as the cluster settings")
	}
	a.clusterSettings = svc
	return nil
}

func (a *Application) stopClusterSettings() {
	if a.clusterSettings != nil {
		if err := a.clusterSettings.Close(); err != nil {
			xlog.Warn("closing the cluster memory settings", "error", err)
		}
	}
}

// applyClusterMemorySettings puts a memory settings document of the cluster
// into effect on this frontend, like a POST /api/settings of those keys.
func (a *Application) applyClusterMemorySettings(rs config.RuntimeSettings) {
	cfg := a.ApplicationConfig()
	watchdogChanged := cfg.ApplyRuntimeSettings(&rs)
	a.modelLoader.SetModelSwap(cfg.ModelSwapEnabled())
	a.modelLoader.SetLRUEvictionRetrySettings(cfg.LRUEvictionMaxRetries, cfg.LRUEvictionRetryInterval)
	if wd := a.modelLoader.GetWatchDog(); wd != nil {
		wd.SetForceEvictionWhenBusy(cfg.ForceEvictionWhenBusy)
		wd.SetSizeAwareEviction(cfg.SizeAwareEviction)
	}
	if !watchdogChanged || !a.watchdogReady.Load() {
		return
	}
	var err error
	if cfg.WatchdogShouldRun() {
		err = a.RestartWatchdog()
	} else {
		err = a.StopWatchdog()
	}
	if err != nil {
		xlog.Error("applying the cluster memory settings to the watchdog", "error", err)
	}
}
