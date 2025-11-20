package application

import (
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services"
	"github.com/mudler/LocalAI/core/templates"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/rs/zerolog/log"
)

type Application struct {
	backendLoader      *config.ModelConfigLoader
	modelLoader        *model.ModelLoader
	applicationConfig  *config.ApplicationConfig
	startupConfig      *config.ApplicationConfig // Stores original config from env vars (before file loading)
	templatesEvaluator *templates.Evaluator
	galleryService     *services.GalleryService
	watchdogMutex      sync.Mutex
	watchdogStop       chan bool
}

func newApplication(appConfig *config.ApplicationConfig) *Application {
	return &Application{
		backendLoader:      config.NewModelConfigLoader(appConfig.SystemState.Model.ModelsPath),
		modelLoader:        model.NewModelLoader(appConfig.SystemState, appConfig.SingleBackend),
		applicationConfig:  appConfig,
		templatesEvaluator: templates.NewEvaluator(appConfig.SystemState.Model.ModelsPath),
	}
}

func (a *Application) ModelConfigLoader() *config.ModelConfigLoader {
	return a.backendLoader
}

func (a *Application) ModelLoader() *model.ModelLoader {
	return a.modelLoader
}

func (a *Application) ApplicationConfig() *config.ApplicationConfig {
	return a.applicationConfig
}

func (a *Application) TemplatesEvaluator() *templates.Evaluator {
	return a.templatesEvaluator
}

func (a *Application) GalleryService() *services.GalleryService {
	return a.galleryService
}

// StartupConfig returns the original startup configuration (from env vars, before file loading)
func (a *Application) StartupConfig() *config.ApplicationConfig {
	return a.startupConfig
}

// RestartWatchdog restarts the watchdog with current ApplicationConfig settings
func (a *Application) RestartWatchdog() error {
	a.watchdogMutex.Lock()
	defer a.watchdogMutex.Unlock()

	appConfig := a.ApplicationConfig()

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

	// Create new watchdog if enabled
	if appConfig.WatchDog {
		wd := model.NewWatchDog(
			a.modelLoader,
			appConfig.WatchDogBusyTimeout,
			appConfig.WatchDogIdleTimeout,
			appConfig.WatchDogBusy,
			appConfig.WatchDogIdle)
		a.modelLoader.SetWatchDog(wd)

		// Create new stop channel
		a.watchdogStop = make(chan bool, 1)

		// Start watchdog goroutine
		go wd.Run()

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

		log.Info().Msg("Watchdog restarted with new settings")
	} else {
		log.Info().Msg("Watchdog disabled")
	}

	return nil
}

func (a *Application) start() error {
	galleryService := services.NewGalleryService(a.ApplicationConfig(), a.ModelLoader())
	err := galleryService.Start(a.ApplicationConfig().Context, a.ModelConfigLoader(), a.ApplicationConfig().SystemState)
	if err != nil {
		return err
	}

	a.galleryService = galleryService

	return nil
}
