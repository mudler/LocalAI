package application

import (
	"context"
	"errors"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/clustersettings"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/nodes"
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
		Apply: a.applyClusterMemoryDocument,
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
		xlog.Info("Stored this frontend's memory settings from the environment in the cluster settings")
	}
	if _, rev := svc.Memory(); rev == 0 {
		// No document yet: run the defaults, as every other frontend does.
		a.applyClusterMemoryDocument(config.RuntimeSettings{})
	}
	a.clusterSettings = svc
	if a.distributed != nil && a.distributed.Registry != nil {
		a.distributed.Registry.SetMemoryPolicy(a.ClusterMemoryPolicy)
	}
	return nil
}

// ClusterMemoryPolicy is the memory policy the workers of the cluster run
// with, before per-node values: the memory settings in force on this
// frontend, which in distributed mode are the stored cluster settings.
func (a *Application) ClusterMemoryPolicy() nodes.MemoryPolicy {
	var stated []string
	if a.clusterSettings != nil {
		doc, _ := a.clusterSettings.Memory()
		stated = doc.SetKeys()
	}
	return nodes.MemoryPolicyFromConfig(a.ApplicationConfig(), stated)
}

func (a *Application) stopClusterSettings() {
	if a.clusterSettings != nil {
		if err := a.clusterSettings.Close(); err != nil {
			xlog.Warn("closing the cluster memory settings", "error", err)
		}
	}
}

// applyClusterMemoryDocument puts the memory settings document of the
// cluster into effect on this frontend. A setting the document does not hold
// takes its default, not the value of this frontend's file or env, so every
// frontend runs the same settings.
func (a *Application) applyClusterMemoryDocument(doc config.RuntimeSettings) {
	a.applyClusterMemorySettings(config.MemorySettingsWithDefaults(doc))
}

// applyClusterMemorySettings puts memory settings into effect on this
// frontend, like a POST /api/settings of those keys.
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

// UpdateMemorySettings stores and applies memory settings (only the memory
// keys of patch): in the cluster store in distributed mode, in this
// frontend's runtime_settings.json otherwise. It is the path of the
// assistant tool; POST /api/settings does the same in its own handler.
func (a *Application) UpdateMemorySettings(ctx context.Context, patch config.RuntimeSettings, by string) error {
	patch, _ = config.SplitMemorySettings(patch)
	if patch.IsEmpty() {
		return errors.New("no memory setting to change")
	}
	if err := config.ValidateMemorySettings(patch); err != nil {
		return err
	}
	if a.clusterSettings != nil {
		_, _, err := a.clusterSettings.UpdateMemory(ctx, patch, nil, by)
		return err
	}
	cfg := a.ApplicationConfig()
	if cfg.DynamicConfigsDir != "" {
		persisted, err := cfg.ReadPersistedSettings()
		if err != nil {
			return err
		}
		persisted.MergeNonNil(patch)
		if err := cfg.WritePersistedSettings(persisted); err != nil {
			return err
		}
	}
	a.applyClusterMemorySettings(patch)
	return nil
}
