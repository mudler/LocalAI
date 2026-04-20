package application

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/core/services/jobs"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/storage"
	coreStartup "github.com/mudler/LocalAI/core/startup"
	"github.com/mudler/LocalAI/internal"

	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/sanitize"
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

	// Create and migrate data directory
	if options.DataPath != "" {
		if err := os.MkdirAll(options.DataPath, 0750); err != nil {
			return nil, fmt.Errorf("unable to create DataPath: %q", err)
		}
		// Migrate data from DynamicConfigsDir to DataPath if needed
		if options.DynamicConfigsDir != "" && options.DataPath != options.DynamicConfigsDir {
			migrateDataFiles(options.DynamicConfigsDir, options.DataPath)
		}
	}

	// Initialize auth database if auth is enabled
	if options.Auth.Enabled {
		// Auto-generate HMAC secret if not provided
		if options.Auth.APIKeyHMACSecret == "" {
			secretFile := filepath.Join(options.DataPath, ".hmac_secret")
			secret, err := loadOrGenerateHMACSecret(secretFile)
			if err != nil {
				return nil, fmt.Errorf("failed to initialize HMAC secret: %w", err)
			}
			options.Auth.APIKeyHMACSecret = secret
		}

		authDB, err := auth.InitDB(options.Auth.DatabaseURL)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize auth database: %w", err)
		}
		application.authDB = authDB
		xlog.Info("Auth enabled", "database", sanitize.URL(options.Auth.DatabaseURL))

		// Start session and expired API key cleanup goroutine
		go func() {
			ticker := time.NewTicker(1 * time.Hour)
			defer ticker.Stop()
			for {
				select {
				case <-options.Context.Done():
					return
				case <-ticker.C:
					if err := auth.CleanExpiredSessions(authDB); err != nil {
						xlog.Error("failed to clean expired sessions", "error", err)
					}
					if err := auth.CleanExpiredAPIKeys(authDB); err != nil {
						xlog.Error("failed to clean expired API keys", "error", err)
					}
				}
			}
		}()
	}

	// Wire JobStore for DB-backed task/job persistence whenever auth DB is available.
	// This ensures tasks and jobs survive restarts in both single-node and distributed modes.
	if application.authDB != nil && application.agentJobService != nil {
		dbJobStore, err := jobs.NewJobStore(application.authDB)
		if err != nil {
			xlog.Error("Failed to create job store for auth DB", "error", err)
		} else {
			application.agentJobService.SetDistributedJobStore(dbJobStore)
		}
	}

	// Initialize distributed mode services (NATS, object storage, node registry)
	distSvc, err := initDistributed(options, application.authDB)
	if err != nil {
		return nil, fmt.Errorf("distributed mode initialization failed: %w", err)
	}
	if distSvc != nil {
		application.distributed = distSvc
		// Wire remote model unloader so ShutdownModel works for remote nodes
		// Uses NATS to tell serve-backend nodes to Free + kill their backend process
		application.modelLoader.SetRemoteUnloader(distSvc.Unloader)
		// Wire ModelRouter so grpcModel() delegates to SmartRouter in distributed mode
		application.modelLoader.SetModelRouter(distSvc.ModelAdapter.AsModelRouter())
		// Wire DistributedModelStore so shutdown/list/watchdog can find remote models
		distStore := nodes.NewDistributedModelStore(
			model.NewInMemoryModelStore(),
			distSvc.Registry,
		)
		application.modelLoader.SetModelStore(distStore)
		// Start health monitor
		distSvc.Health.Start(options.Context)
		// Start replica reconciler for auto-scaling model replicas
		if distSvc.Reconciler != nil {
			go distSvc.Reconciler.Run(options.Context)
		}
		// In distributed mode, MCP CI jobs are executed by agent workers (not the frontend)
		// because the frontend can't create MCP sessions (e.g., stdio servers using docker).
		// The dispatcher still subscribes to jobs.new for persistence (result/progress subs)
		// but does NOT set a workerFn — agent workers consume jobs from the same NATS queue.

		// Wire model config loader so job events include model config for agent workers
		distSvc.Dispatcher.SetModelConfigLoader(application.backendLoader)

		// Start job dispatcher — abort startup if it fails, as jobs would be accepted but never dispatched
		if err := distSvc.Dispatcher.Start(options.Context); err != nil {
			return nil, fmt.Errorf("starting job dispatcher: %w", err)
		}
		// Start ephemeral file cleanup
		storage.StartEphemeralCleanup(options.Context, distSvc.FileMgr, 0, 0)
		// Wire distributed backends into AgentJobService (before Start)
		if application.agentJobService != nil {
			application.agentJobService.SetDistributedBackends(distSvc.Dispatcher)
			application.agentJobService.SetDistributedJobStore(distSvc.JobStore)
		}
		// Wire skill store into AgentPoolService (wired at pool start time via closure)
		// The actual wiring happens in StartAgentPool since the pool doesn't exist yet.

		// Wire NATS and gallery store into GalleryService for cross-instance progress/cancel
		if application.galleryService != nil {
			application.galleryService.SetNATSClient(distSvc.Nats)
			if distSvc.DistStores != nil && distSvc.DistStores.Gallery != nil {
				// Clean up stale in-progress operations from previous crashed instances
				if err := distSvc.DistStores.Gallery.CleanStale(30 * time.Minute); err != nil {
					xlog.Warn("Failed to clean stale gallery operations", "error", err)
				}
				application.galleryService.SetGalleryStore(distSvc.DistStores.Gallery)
			}
			// Wire distributed model/backend managers so delete propagates to workers
			application.galleryService.SetModelManager(
				nodes.NewDistributedModelManager(options, application.modelLoader, distSvc.Unloader),
			)
			application.galleryService.SetBackendManager(
				nodes.NewDistributedBackendManager(options, application.modelLoader, distSvc.Unloader, distSvc.Registry),
			)
		}
	}

	// Start AgentJobService (after distributed wiring so it knows whether to use local or NATS)
	if application.agentJobService != nil {
		if err := application.agentJobService.Start(options.Context); err != nil {
			return nil, fmt.Errorf("starting agent job service: %w", err)
		}
	}

	if err := coreStartup.InstallModels(options.Context, application.GalleryService(), options.Galleries, options.BackendGalleries, options.SystemState, application.ModelLoader(), options.EnforcePredownloadScans, options.AutoloadBackendGalleries, nil, options.ModelsURL...); err != nil {
		xlog.Error("error installing models", "error", err)
	}

	for _, backend := range options.ExternalBackends {
		if err := galleryop.InstallExternalBackend(options.Context, options.BackendGalleries, options.SystemState, application.ModelLoader(), nil, backend, "", ""); err != nil {
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

	// Start background upgrade checker for backends.
	// In distributed mode, uses PostgreSQL advisory lock so only one frontend
	// instance runs periodic checks (avoids duplicate upgrades across replicas).
	if len(options.BackendGalleries) > 0 {
		// Pass a lazy getter for the backend manager so the checker always
		// uses the active one — DistributedBackendManager is swapped in above
		// and asks workers for their installed backends, which is what
		// upgrade detection needs in distributed mode.
		bmFn := func() galleryop.BackendManager { return application.GalleryService().BackendManager() }
		uc := NewUpgradeChecker(options, application.ModelLoader(), application.distributedDB(), bmFn)
		application.upgradeChecker = uc
		go uc.Run(options.Context)
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
		if err := galleryop.ApplyGalleryFromString(options.SystemState, application.ModelLoader(), options.EnforcePredownloadScans, options.AutoloadBackendGalleries, options.Galleries, options.BackendGalleries, options.PreloadJSONModels); err != nil {
			return nil, err
		}
	}

	if options.PreloadModelsFromPath != "" {
		if err := galleryop.ApplyGalleryFromFile(options.SystemState, application.ModelLoader(), options.EnforcePredownloadScans, options.AutoloadBackendGalleries, options.Galleries, options.BackendGalleries, options.PreloadModelsFromPath); err != nil {
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

	application.ModelLoader().SetBackendLoggingEnabled(options.EnableBackendLogging)

	// turn off any process that was started by GRPC if the context is canceled
	go func() {
		<-options.Context.Done()
		xlog.Debug("Context canceled, shutting down")
		application.distributed.Shutdown()
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
				return nil, backendErr
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
	if settings.ForceEvictionWhenBusy != nil {
		// Only apply if current value is default (false), suggesting it wasn't set from env var
		if !options.ForceEvictionWhenBusy {
			options.ForceEvictionWhenBusy = *settings.ForceEvictionWhenBusy
		}
	}
	if settings.LRUEvictionMaxRetries != nil {
		// Only apply if current value is default (30), suggesting it wasn't set from env var
		if options.LRUEvictionMaxRetries == 0 {
			options.LRUEvictionMaxRetries = *settings.LRUEvictionMaxRetries
		}
	}
	if settings.LRUEvictionRetryInterval != nil {
		// Only apply if current value is default (1s), suggesting it wasn't set from env var
		if options.LRUEvictionRetryInterval == 0 {
			dur, err := time.ParseDuration(*settings.LRUEvictionRetryInterval)
			if err == nil {
				options.LRUEvictionRetryInterval = dur
			} else {
				xlog.Warn("invalid LRU eviction retry interval in runtime_settings.json", "error", err, "interval", *settings.LRUEvictionRetryInterval)
			}
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

	// P2P settings
	if settings.P2PToken != nil {
		if options.P2PToken == "" {
			options.P2PToken = *settings.P2PToken
		}
	}
	if settings.P2PNetworkID != nil {
		if options.P2PNetworkID == "" {
			options.P2PNetworkID = *settings.P2PNetworkID
		}
	}
	if settings.Federated != nil {
		if !options.Federated {
			options.Federated = *settings.Federated
		}
	}

	if settings.EnableBackendLogging != nil {
		if !options.EnableBackendLogging {
			options.EnableBackendLogging = *settings.EnableBackendLogging
		}
	}

	// Tracing settings
	if settings.EnableTracing != nil {
		if !options.EnableTracing {
			options.EnableTracing = *settings.EnableTracing
		}
	}
	if settings.TracingMaxItems != nil {
		if options.TracingMaxItems == 0 {
			options.TracingMaxItems = *settings.TracingMaxItems
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

// loadOrGenerateHMACSecret loads an HMAC secret from the given file path,
// or generates a random 32-byte secret and persists it if the file doesn't exist.
func loadOrGenerateHMACSecret(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		secret := string(data)
		if len(secret) >= 32 {
			return secret, nil
		}
	}

	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to generate HMAC secret: %w", err)
	}
	secret := hex.EncodeToString(b)

	if err := os.WriteFile(path, []byte(secret), 0600); err != nil {
		return "", fmt.Errorf("failed to persist HMAC secret: %w", err)
	}

	xlog.Info("Generated new HMAC secret for API key hashing", "path", path)
	return secret, nil
}

// migrateDataFiles moves persistent data files from the old config directory
// to the new data directory. Only moves files that exist in src but not in dst.
func migrateDataFiles(srcDir, dstDir string) {
	// Files and directories to migrate
	items := []string{
		"agent_tasks.json",
		"agent_jobs.json",
		"collections",
		"assets",
	}

	migrated := false
	for _, item := range items {
		srcPath := filepath.Join(srcDir, item)
		dstPath := filepath.Join(dstDir, item)

		// Only migrate if source exists and destination does not
		if _, err := os.Stat(srcPath); os.IsNotExist(err) {
			continue
		}
		if _, err := os.Stat(dstPath); err == nil {
			continue // destination already exists, skip
		}

		if err := os.Rename(srcPath, dstPath); err != nil {
			xlog.Warn("Failed to migrate data file, will copy instead", "src", srcPath, "dst", dstPath, "error", err)
			// os.Rename fails across filesystems, fall back to leaving in place
			// and log a warning for the user to manually move
			xlog.Warn("Data file remains in old location, please move manually", "src", srcPath, "dst", dstPath)
			continue
		}
		migrated = true
		xlog.Info("Migrated data file to new data path", "src", srcPath, "dst", dstPath)
	}

	if migrated {
		xlog.Info("Data migration complete", "from", srcDir, "to", dstDir)
	}
}
