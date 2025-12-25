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
	coreStartup "github.com/mudler/LocalAI/core/startup"
	"github.com/mudler/LocalAI/internal"

	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/xsysinfo"
	"github.com/mudler/xlog"
)

func New(opts ...config.AppOption) (*Application, error) {
	options := config.NewApplicationConfig(opts...)

	// Store a copy of the startup config (from env vars, before file loading)
	// This is used to determine if settings came from env vars vs file
	startupConfigCopy := *options
	application := newApplication(options)
	application.startupConfig = &startupConfigCopy

	xlog.Info("Starting LocalAI", "threads", options.Threads, "modelsPath", options.SystemState.Model.ModelsPath)
	xlog.Info("LocalAI version", "version", internal.PrintableVersion())

	if err := application.start(); err != nil {
		return nil, err
	}

	caps, err := xsysinfo.CPUCapabilities()
	if err == nil {
		xlog.Debug("CPU capabilities", "capabilities", caps)

	}
	gpus, err := xsysinfo.GPUs()
	if err == nil {
		xlog.Debug("GPU count", "count", len(gpus))
		for _, gpu := range gpus {
			xlog.Debug("GPU", "gpu", gpu.String())
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
		xlog.Error("error installing models", "error", err)
	}

	for _, backend := range options.ExternalBackends {
		if err := services.InstallExternalBackend(options.Context, options.BackendGalleries, options.SystemState, application.ModelLoader(), nil, backend, "", ""); err != nil {
			xlog.Error("error installing external backend", "error", err)
		}
	}

	configLoaderOpts := options.ToConfigLoaderOptions()

	if err := application.ModelConfigLoader().LoadModelConfigsFromPath(options.SystemState.Model.ModelsPath, configLoaderOpts...); err != nil {
		xlog.Error("error loading config files", "error", err)
	}

	if err := gallery.RegisterBackends(options.SystemState, application.ModelLoader()); err != nil {
		xlog.Error("error registering external backends", "error", err)
	}

	if options.ConfigFile != "" {
		if err := application.ModelConfigLoader().LoadMultipleModelConfigsSingleFile(options.ConfigFile, configLoaderOpts...); err != nil {
			xlog.Error("error loading config file", "error", err)
		}
	}

	if err := application.ModelConfigLoader().Preload(options.SystemState.Model.ModelsPath); err != nil {
		xlog.Error("error downloading models", "error", err)
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
			xlog.Debug("Model", "name", v.Name, "config", v)
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
		xlog.Debug("Context canceled, shutting down")
		err := application.ModelLoader().StopAllGRPC()
		if err != nil {
			xlog.Error("error while stopping all grpc backends", "error", err)
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

			xlog.Debug("Auto loading model into memory from file", "model", m, "file", cfg.Model)

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

	xlog.Info("core/startup process completed!")
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
				xlog.Error("failed creating DynamicConfigsDir", "error", err)
			}
		} else {
			// something else happened, we log the error and don't start the watcher
			xlog.Error("failed to read DynamicConfigsDir, watcher will not be started", "error", err)
			return
		}
	}

	configHandler := newConfigFileHandler(options)
	if err := configHandler.Watch(); err != nil {
		xlog.Error("failed creating watcher", "error", err)
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
			xlog.Debug("runtime_settings.json not found, using defaults")
			return
		}
		xlog.Warn("failed to read runtime_settings.json", "error", err)
		return
	}

	var settings config.RuntimeSettings

	if err := json.Unmarshal(fileContent, &settings); err != nil {
		xlog.Warn("failed to parse runtime_settings.json", "error", err)
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
				xlog.Warn("invalid watchdog idle timeout in runtime_settings.json", "error", err, "timeout", *settings.WatchdogIdleTimeout)
			}
		}
	}
	if settings.WatchdogBusyTimeout != nil {
		if options.WatchDogBusyTimeout == 0 {
			dur, err := time.ParseDuration(*settings.WatchdogBusyTimeout)
			if err == nil {
				options.WatchDogBusyTimeout = dur
			} else {
				xlog.Warn("invalid watchdog busy timeout in runtime_settings.json", "error", err, "timeout", *settings.WatchdogBusyTimeout)
			}
		}
	}
	if settings.WatchdogInterval != nil {
		if options.WatchDogInterval == 0 {
			dur, err := time.ParseDuration(*settings.WatchdogInterval)
			if err == nil {
				options.WatchDogInterval = dur
			} else {
				xlog.Warn("invalid watchdog interval in runtime_settings.json", "error", err, "interval", *settings.WatchdogInterval)
				options.WatchDogInterval = model.DefaultWatchdogInterval
			}
		}
	}
	// Handle MaxActiveBackends (new) and SingleBackend (deprecated)
	if settings.MaxActiveBackends != nil {
		// Only apply if current value is default (0), suggesting it wasn't set from env var
		if options.MaxActiveBackends == 0 {
			options.MaxActiveBackends = *settings.MaxActiveBackends
			// For backward compatibility, also set SingleBackend if MaxActiveBackends == 1
			options.SingleBackend = (*settings.MaxActiveBackends == 1)
		}
	} else if settings.SingleBackend != nil {
		// Legacy: SingleBackend maps to MaxActiveBackends = 1
		if !options.SingleBackend {
			options.SingleBackend = *settings.SingleBackend
			if *settings.SingleBackend {
				options.MaxActiveBackends = 1
			}
		}
	}
	if settings.ParallelBackendRequests != nil {
		if !options.ParallelBackendRequests {
			options.ParallelBackendRequests = *settings.ParallelBackendRequests
		}
	}
	if settings.MemoryReclaimerEnabled != nil {
		// Only apply if current value is default (false), suggesting it wasn't set from env var
		if !options.MemoryReclaimerEnabled {
			options.MemoryReclaimerEnabled = *settings.MemoryReclaimerEnabled
			if options.MemoryReclaimerEnabled {
				options.WatchDog = true // Memory reclaimer requires watchdog
			}
		}
	}
	if settings.MemoryReclaimerThreshold != nil {
		// Only apply if current value is default (0), suggesting it wasn't set from env var
		if options.MemoryReclaimerThreshold == 0 {
			options.MemoryReclaimerThreshold = *settings.MemoryReclaimerThreshold
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

	xlog.Debug("Runtime settings loaded from runtime_settings.json")
}

// initializeWatchdog initializes the watchdog with current ApplicationConfig settings
func initializeWatchdog(application *Application, options *config.ApplicationConfig) {
	// Get effective max active backends (considers both MaxActiveBackends and deprecated SingleBackend)
	lruLimit := options.GetEffectiveMaxActiveBackends()

	// Create watchdog if enabled OR if LRU limit is set OR if memory reclaimer is enabled
	if options.WatchDog || lruLimit > 0 || options.MemoryReclaimerEnabled {
		wd := model.NewWatchDog(
			model.WithProcessManager(application.ModelLoader()),
			model.WithBusyTimeout(options.WatchDogBusyTimeout),
			model.WithIdleTimeout(options.WatchDogIdleTimeout),
			model.WithWatchdogInterval(options.WatchDogInterval),
			model.WithBusyCheck(options.WatchDogBusy),
			model.WithIdleCheck(options.WatchDogIdle),
			model.WithLRULimit(lruLimit),
			model.WithMemoryReclaimer(options.MemoryReclaimerEnabled, options.MemoryReclaimerThreshold),
			model.WithForceEvictionWhenBusy(options.ForceEvictionWhenBusy),
		)
		application.ModelLoader().SetWatchDog(wd)

		// Initialize ModelLoader LRU eviction retry settings
		application.ModelLoader().SetLRUEvictionRetrySettings(
			options.LRUEvictionMaxRetries,
			options.LRUEvictionRetryInterval,
		)

		// Start watchdog goroutine if any periodic checks are enabled
		// LRU eviction doesn't need the Run() loop - it's triggered on model load
		// But memory reclaimer needs the Run() loop for periodic checking
		if options.WatchDogBusy || options.WatchDogIdle || options.MemoryReclaimerEnabled {
			go wd.Run()
		}

		go func() {
			<-options.Context.Done()
			xlog.Debug("Context canceled, shutting down")
			wd.Shutdown()
		}()
	}
}
