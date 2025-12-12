package application

import (
	"time"

	"github.com/mudler/LocalAI/pkg/model"
	"github.com/rs/zerolog/log"
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

	// Create watchdog if enabled OR if LRU limit is set
	// LRU eviction requires watchdog infrastructure even without busy/idle checks
	if appConfig.WatchDog || lruLimit > 0 {
		wd := model.NewWatchDog(
			a.modelLoader,
			appConfig.WatchDogBusyTimeout,
			appConfig.WatchDogIdleTimeout,
			appConfig.WatchDogBusy,
			appConfig.WatchDogIdle,
			lruLimit)
		a.modelLoader.SetWatchDog(wd)

		// Create new stop channel
		a.watchdogStop = make(chan bool, 1)

		// Start watchdog goroutine only if busy/idle checks are enabled
		// LRU eviction doesn't need the Run() loop - it's triggered on model load
		if appConfig.WatchDogBusy || appConfig.WatchDogIdle {
			go wd.Run()
		}

		// Setup shutdown handler
		go func() {
			select {
			case <-a.watchdogStop:
				log.Debug().Msg("Watchdog stop signal received")
				wd.Shutdown()
			case <-appConfig.Context.Done():
				log.Debug().Msg("Context canceled, shutting down watchdog")
				wd.Shutdown()
			}
		}()

		log.Info().Int("lruLimit", lruLimit).Bool("busyCheck", appConfig.WatchDogBusy).Bool("idleCheck", appConfig.WatchDogIdle).Msg("Watchdog started with new settings")
	} else {
		log.Info().Msg("Watchdog disabled")
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

	// Shutdown existing watchdog if running
	if a.watchdogStop != nil {
		close(a.watchdogStop)
		a.watchdogStop = nil
	}

	// Shutdown existing watchdog if running
	currentWD := a.modelLoader.GetWatchDog()
	if currentWD != nil {
		currentWD.Shutdown()
		// Wait a bit for shutdown to complete
		time.Sleep(100 * time.Millisecond)
	}

	// Start watchdog with new settings
	return a.startWatchdog()
}
