package application

import (
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/xlog"
)

func (a *Application) StopWatchdog() error {
	if a.watchdogStop != nil {
		close(a.watchdogStop)
		a.watchdogStop = nil
	}
	return nil
}

// startWatchdog starts the watchdog with current ApplicationConfig settings
// This is an internal method that assumes the caller holds the watchdogMutex
func (a *Application) startWatchdog() error {
	appConfig := a.ApplicationConfig()

	// Get effective max active backends (considers both MaxActiveBackends and deprecated SingleBackend)
	lruLimit := appConfig.GetEffectiveMaxActiveBackends()

	// Create watchdog if enabled OR if LRU limit is set OR if memory reclaimer is enabled
	// LRU eviction requires watchdog infrastructure even without busy/idle checks
	if appConfig.WatchDog || lruLimit > 0 || appConfig.MemoryReclaimerEnabled {
		wd := model.NewWatchDog(
			model.WithProcessManager(a.modelLoader),
			model.WithBusyTimeout(appConfig.WatchDogBusyTimeout),
			model.WithIdleTimeout(appConfig.WatchDogIdleTimeout),
			model.WithWatchdogInterval(appConfig.WatchDogInterval),
			model.WithBusyCheck(appConfig.WatchDogBusy),
			model.WithIdleCheck(appConfig.WatchDogIdle),
			model.WithLRULimit(lruLimit),
			model.WithMemoryReclaimer(appConfig.MemoryReclaimerEnabled, appConfig.MemoryReclaimerThreshold),
			model.WithForceEvictionWhenBusy(appConfig.ForceEvictionWhenBusy),
		)

		// Create new stop channel BEFORE setting up any goroutines
		// This prevents race conditions where the old shutdown handler might
		// receive the closed channel and try to shut down the new watchdog
		a.watchdogStop = make(chan bool, 1)

		// Set the watchdog on the model loader
		a.modelLoader.SetWatchDog(wd)

		// Start watchdog goroutine if any periodic checks are enabled
		// LRU eviction doesn't need the Run() loop - it's triggered on model load
		// But memory reclaimer needs the Run() loop for periodic checking
		if appConfig.WatchDogBusy || appConfig.WatchDogIdle || appConfig.MemoryReclaimerEnabled {
			go wd.Run()
		}

		// Setup shutdown handler - this goroutine will wait on a.watchdogStop
		// which is now a fresh channel, so it won't receive any stale signals
		// Note: We capture wd in a local variable to ensure this handler operates
		// on the correct watchdog instance (not a later one that gets assigned to wd)
		wdForShutdown := wd
		go func() {
			select {
			case <-a.watchdogStop:
				xlog.Debug("Watchdog stop signal received")
				wdForShutdown.Shutdown()
			case <-appConfig.Context.Done():
				xlog.Debug("Context canceled, shutting down watchdog")
				wdForShutdown.Shutdown()
			}
		}()

		xlog.Info("Watchdog started with new settings", "lruLimit", lruLimit, "busyCheck", appConfig.WatchDogBusy, "idleCheck", appConfig.WatchDogIdle, "memoryReclaimer", appConfig.MemoryReclaimerEnabled, "memoryThreshold", appConfig.MemoryReclaimerThreshold, "interval", appConfig.WatchDogInterval)
	} else {
		xlog.Info("Watchdog disabled")
	}

	return nil
}

// StartWatchdog starts the watchdog with current ApplicationConfig settings
func (a *Application) StartWatchdog() error {
	a.watchdogMutex.Lock()
	defer a.watchdogMutex.Unlock()

	return a.startWatchdog()
}

// RestartWatchdog restarts the watchdog with current ApplicationConfig settings
func (a *Application) RestartWatchdog() error {
	a.watchdogMutex.Lock()
	defer a.watchdogMutex.Unlock()

	// Get the old watchdog before we shut it down
	oldWD := a.modelLoader.GetWatchDog()

	// Get the state from the old watchdog before shutting it down
	// This preserves information about loaded models
	var oldState model.WatchDogState
	if oldWD != nil {
		oldState = oldWD.GetState()
	}

	// Signal all handlers to stop by closing the stop channel
	// This will cause any goroutine waiting on <-a.watchdogStop to unblock
	if a.watchdogStop != nil {
		close(a.watchdogStop)
		a.watchdogStop = nil
	}

	// Shutdown existing watchdog - this triggers the stop signal
	if oldWD != nil {
		oldWD.Shutdown()
		// Wait for the old watchdog's Run() goroutine to fully shut down
		oldWD.WaitDone()
	}

	// Start watchdog with new settings
	if err := a.startWatchdog(); err != nil {
		return err
	}

	// Restore the model state from the old watchdog to the new one
	// This ensures the new watchdog knows about already-loaded models
	newWD := a.modelLoader.GetWatchDog()
	if newWD != nil && len(oldState.AddressModelMap) > 0 {
		newWD.RestoreState(oldState)
	}

	return nil
}
