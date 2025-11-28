package application

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/services"
	"github.com/mudler/LocalAI/internal"

	coreStartup "github.com/mudler/LocalAI/core/startup"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/xsysinfo"
	"github.com/rs/zerolog/log"
)

func New(opts ...config.AppOption) (*Application, error) {
	options := config.NewApplicationConfig(opts...)

	// Store a copy of the startup config (from env vars, before file loading)
	// This is used to determine if settings came from env vars vs file
	startupConfigCopy := *options
	application := newApplication(options)
	application.startupConfig = &startupConfigCopy

	log.Info().Msgf("Starting LocalAI using %d threads, with models path: %s", options.Threads, options.SystemState.Model.ModelsPath)
	log.Info().Msgf("LocalAI version: %s", internal.PrintableVersion())

	if err := application.start(); err != nil {
		return nil, err
	}

	caps, err := xsysinfo.CPUCapabilities()
	if err == nil {
		log.Debug().Msgf("CPU capabilities: %v", caps)

	}
	gpus, err := xsysinfo.GPUs()
	if err == nil {
		log.Debug().Msgf("GPU count: %d", len(gpus))
		for _, gpu := range gpus {
			log.Debug().Msgf("GPU: %s", gpu.String())
		}
	}

	// Make sure directories exists
	if options.SystemState.Model.ModelsPath == "" {
		return nil, fmt.Errorf("models path cannot be empty")
	}

	err = os.MkdirAll(options.SystemState.Model.ModelsPath, 0750)
	if err != nil {
		return nil, fmt.Errorf("unable to create ModelPath: %q", err)
	}
	if options.GeneratedContentDir != "" {
		err := os.MkdirAll(options.GeneratedContentDir, 0750)
		if err != nil {
			return nil, fmt.Errorf("unable to create ImageDir: %q", err)
		}
	}
	if options.UploadDir != "" {
		err := os.MkdirAll(options.UploadDir, 0750)
		if err != nil {
			return nil, fmt.Errorf("unable to create UploadDir: %q", err)
		}
	}

	if err := coreStartup.InstallModels(options.Context, application.GalleryService(), options.Galleries, options.BackendGalleries, options.SystemState, application.ModelLoader(), options.EnforcePredownloadScans, options.AutoloadBackendGalleries, nil, options.ModelsURL...); err != nil {
		log.Error().Err(err).Msg("error installing models")
	}

	for _, backend := range options.ExternalBackends {
		if err := coreStartup.InstallExternalBackends(options.Context, options.BackendGalleries, options.SystemState, application.ModelLoader(), nil, backend, "", ""); err != nil {
			log.Error().Err(err).Msg("error installing external backend")
		}
	}

	configLoaderOpts := options.ToConfigLoaderOptions()

	if err := application.ModelConfigLoader().LoadModelConfigsFromPath(options.SystemState.Model.ModelsPath, configLoaderOpts...); err != nil {
		log.Error().Err(err).Msg("error loading config files")
	}

	if err := gallery.RegisterBackends(options.SystemState, application.ModelLoader()); err != nil {
		log.Error().Err(err).Msg("error registering external backends")
	}

	if options.ConfigFile != "" {
		if err := application.ModelConfigLoader().LoadMultipleModelConfigsSingleFile(options.ConfigFile, configLoaderOpts...); err != nil {
			log.Error().Err(err).Msg("error loading config file")
		}
	}

	if err := application.ModelConfigLoader().Preload(options.SystemState.Model.ModelsPath); err != nil {
		log.Error().Err(err).Msg("error downloading models")
	}

	if options.PreloadJSONModels != "" {
		if err := services.ApplyGalleryFromString(options.SystemState, application.ModelLoader(), options.EnforcePredownloadScans, options.AutoloadBackendGalleries, options.Galleries, options.BackendGalleries, options.PreloadJSONModels); err != nil {
			return nil, err
		}
	}

	if options.PreloadModelsFromPath != "" {
		if err := services.ApplyGalleryFromFile(options.SystemState, application.ModelLoader(), options.EnforcePredownloadScans, options.AutoloadBackendGalleries, options.Galleries, options.BackendGalleries, options.PreloadModelsFromPath); err != nil {
			return nil, err
		}
	}

	if options.Debug {
		for _, v := range application.ModelConfigLoader().GetAllModelsConfigs() {
			log.Debug().Msgf("Model: %s (config: %+v)", v.Name, v)
		}
	}

	// Load runtime settings from file if DynamicConfigsDir is set
	// This applies file settings with env var precedence (env vars take priority)
	// Note: startupConfigCopy was already created above, so it has the original env var values
	if options.DynamicConfigsDir != "" {
		loadRuntimeSettingsFromFile(options)
	}

	// turn off any process that was started by GRPC if the context is canceled
	go func() {
		<-options.Context.Done()
		log.Debug().Msgf("Context canceled, shutting down")
		err := application.ModelLoader().StopAllGRPC()
		if err != nil {
			log.Error().Err(err).Msg("error while stopping all grpc backends")
		}
	}()

	// Initialize watchdog with current settings (after loading from file)
	initializeWatchdog(application, options)

	if options.LoadToMemory != nil && !options.SingleBackend {
		for _, m := range options.LoadToMemory {
			cfg, err := application.ModelConfigLoader().LoadModelConfigFileByNameDefaultOptions(m, options)
			if err != nil {
				return nil, err
			}

			log.Debug().Msgf("Auto loading model %s into memory from file: %s", m, cfg.Model)

			o := backend.ModelOptions(*cfg, options)

			var backendErr error
			_, backendErr = application.ModelLoader().Load(o...)
			if backendErr != nil {
				return nil, err
			}
		}
	}

	// Watch the configuration directory
	startWatcher(options)

	log.Info().Msg("core/startup process completed!")
	return application, nil
}

func startWatcher(options *config.ApplicationConfig) {
	if options.DynamicConfigsDir == "" {
		// No need to start the watcher if the directory is not set
		return
	}

	if _, err := os.Stat(options.DynamicConfigsDir); err != nil {
		if os.IsNotExist(err) {
			// We try to create the directory if it does not exist and was specified
			if err := os.MkdirAll(options.DynamicConfigsDir, 0700); err != nil {
				log.Error().Err(err).Msg("failed creating DynamicConfigsDir")
			}
		} else {
			// something else happened, we log the error and don't start the watcher
			log.Error().Err(err).Msg("failed to read DynamicConfigsDir, watcher will not be started")
			return
		}
	}

	configHandler := newConfigFileHandler(options)
	if err := configHandler.Watch(); err != nil {
		log.Error().Err(err).Msg("failed creating watcher")
	}
}

// loadRuntimeSettingsFromFile loads settings from runtime_settings.json with env var precedence
// This function is called at startup, before env vars are applied via AppOptions.
// Since env vars are applied via AppOptions in run.go, we need to check if they're set.
// We do this by checking if the current options values differ from defaults, which would
// indicate they were set from env vars. However, a simpler approach is to just apply
// file settings here, and let the AppOptions (which are applied after this) override them.
// But actually, this is called AFTER AppOptions are applied in New(), so we need to check env vars.
// The cleanest solution: Store original values before applying file, or check if values match
// what would be set from env vars. For now, we'll apply file settings and they'll be
// overridden by AppOptions if env vars were set (but AppOptions are already applied).
// Actually, this function is called in New() before AppOptions are fully processed for watchdog.
// Let's check the call order: New() -> loadRuntimeSettingsFromFile() -> initializeWatchdog()
// But AppOptions are applied in NewApplicationConfig() which is called first.
// So at this point, options already has values from env vars. We should compare against
// defaults to see if env vars were set. But we don't have defaults stored.
// Simplest: Just apply file settings. If env vars were set, they're already in options.
// The file watcher handler will handle runtime changes properly by comparing with startupAppConfig.
func loadRuntimeSettingsFromFile(options *config.ApplicationConfig) {
	settingsFile := filepath.Join(options.DynamicConfigsDir, "runtime_settings.json")
	fileContent, err := os.ReadFile(settingsFile)
	if err != nil {
		if os.IsNotExist(err) {
			log.Debug().Msg("runtime_settings.json not found, using defaults")
			return
		}
		log.Warn().Err(err).Msg("failed to read runtime_settings.json")
		return
	}

	var settings struct {
		WatchdogEnabled         *bool   `json:"watchdog_enabled,omitempty"`
		WatchdogIdleEnabled     *bool   `json:"watchdog_idle_enabled,omitempty"`
		WatchdogBusyEnabled     *bool   `json:"watchdog_busy_enabled,omitempty"`
		WatchdogIdleTimeout     *string `json:"watchdog_idle_timeout,omitempty"`
		WatchdogBusyTimeout     *string `json:"watchdog_busy_timeout,omitempty"`
		SingleBackend           *bool   `json:"single_backend,omitempty"`
		ParallelBackendRequests *bool   `json:"parallel_backend_requests,omitempty"`
		AgentJobRetentionDays   *int    `json:"agent_job_retention_days,omitempty"`
	}

	if err := json.Unmarshal(fileContent, &settings); err != nil {
		log.Warn().Err(err).Msg("failed to parse runtime_settings.json")
		return
	}

	// At this point, options already has values from env vars (via AppOptions in run.go).
	// To avoid env var duplication, we determine if env vars were set by checking if
	// current values differ from defaults. Defaults are: false for bools, 0 for durations.
	// If current value is at default, it likely wasn't set from env var, so we can apply file.
	// If current value is non-default, it was likely set from env var, so we preserve it.
	// Note: This means env vars explicitly setting to false/0 won't be distinguishable from defaults,
	// but that's an acceptable limitation to avoid env var duplication.

	if settings.WatchdogIdleEnabled != nil {
		// Only apply if current value is default (false), suggesting it wasn't set from env var
		if !options.WatchDogIdle {
			options.WatchDogIdle = *settings.WatchdogIdleEnabled
			if options.WatchDogIdle {
				options.WatchDog = true
			}
		}
	}
	if settings.WatchdogBusyEnabled != nil {
		if !options.WatchDogBusy {
			options.WatchDogBusy = *settings.WatchdogBusyEnabled
			if options.WatchDogBusy {
				options.WatchDog = true
			}
		}
	}
	if settings.WatchdogIdleTimeout != nil {
		// Only apply if current value is default (0), suggesting it wasn't set from env var
		if options.WatchDogIdleTimeout == 0 {
			dur, err := time.ParseDuration(*settings.WatchdogIdleTimeout)
			if err == nil {
				options.WatchDogIdleTimeout = dur
			} else {
				log.Warn().Err(err).Str("timeout", *settings.WatchdogIdleTimeout).Msg("invalid watchdog idle timeout in runtime_settings.json")
			}
		}
	}
	if settings.WatchdogBusyTimeout != nil {
		if options.WatchDogBusyTimeout == 0 {
			dur, err := time.ParseDuration(*settings.WatchdogBusyTimeout)
			if err == nil {
				options.WatchDogBusyTimeout = dur
			} else {
				log.Warn().Err(err).Str("timeout", *settings.WatchdogBusyTimeout).Msg("invalid watchdog busy timeout in runtime_settings.json")
			}
		}
	}
	if settings.SingleBackend != nil {
		if !options.SingleBackend {
			options.SingleBackend = *settings.SingleBackend
		}
	}
	if settings.ParallelBackendRequests != nil {
		if !options.ParallelBackendRequests {
			options.ParallelBackendRequests = *settings.ParallelBackendRequests
		}
	}
	if settings.AgentJobRetentionDays != nil {
		// Only apply if current value is default (0), suggesting it wasn't set from env var
		if options.AgentJobRetentionDays == 0 {
			options.AgentJobRetentionDays = *settings.AgentJobRetentionDays
		}
	}
	if !options.WatchDogIdle && !options.WatchDogBusy {
		if settings.WatchdogEnabled != nil && *settings.WatchdogEnabled {
			options.WatchDog = true
		}
	}

	log.Debug().Msg("Runtime settings loaded from runtime_settings.json")
}

// initializeWatchdog initializes the watchdog with current ApplicationConfig settings
func initializeWatchdog(application *Application, options *config.ApplicationConfig) {
	if options.WatchDog {
		wd := model.NewWatchDog(
			application.ModelLoader(),
			options.WatchDogBusyTimeout,
			options.WatchDogIdleTimeout,
			options.WatchDogBusy,
			options.WatchDogIdle)
		application.ModelLoader().SetWatchDog(wd)
		go wd.Run()
		go func() {
			<-options.Context.Done()
			log.Debug().Msgf("Context canceled, shutting down")
			wd.Shutdown()
		}()
	}
}
