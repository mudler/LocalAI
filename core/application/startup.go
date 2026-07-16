package application

import (
	"crypto/rand"
	"encoding/hex"
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
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/modeladmin"
	"github.com/mudler/LocalAI/core/services/monitoring"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/routing/admission"
	"github.com/mudler/LocalAI/core/services/routing/billing"
	"github.com/mudler/LocalAI/core/services/routing/pii"
	"github.com/mudler/LocalAI/core/services/routing/router"
	"github.com/mudler/LocalAI/core/services/storage"
	coreStartup "github.com/mudler/LocalAI/core/startup"
	"github.com/mudler/LocalAI/internal"
	"github.com/mudler/LocalAI/pkg/downloader"
	"github.com/mudler/LocalAI/pkg/signals"
	"github.com/mudler/LocalAI/pkg/vram"

	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/sanitize"
	"github.com/mudler/LocalAI/pkg/xsysinfo"
	"github.com/mudler/xlog"
)

func New(opts ...config.AppOption) (*Application, error) {
	options := config.NewApplicationConfig(opts...)

	// Store a copy of the startup config (env/CLI only, before file
	// loading): the settings endpoint uses it to tell env-provided API
	// keys apart from runtime-managed ones.
	startupConfigCopy := *options

	// Merge persisted runtime settings BEFORE anything consumes options:
	// model-config defaults (ToConfigLoaderOptions), gallery services, the
	// watchdog, and the MITM listener are all configured from options
	// further down. Loading late (the old call site, after model configs
	// were read) meant boot-loaded models saw pre-file defaults for one
	// full restart. Env/CLI values win over the file (see
	// ApplyRuntimeSettingsAtStartup).
	loadRuntimeSettingsFromFile(options)

	// WithThreads no longer eagerly resolves 0 (so the settings merge can
	// tell "unset" from "env-set"); resolve the physical-core default now
	// that env, CLI, and file have all had their say.
	if options.Threads == 0 {
		options.Threads = xsysinfo.CPUPhysicalCores()
	}

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

	err = os.MkdirAll(options.SystemState.Model.ModelsPath, 0o750)
	if err != nil {
		return nil, fmt.Errorf("unable to create ModelPath: %q", err)
	}

	// Reap *.partial downloads abandoned by a previous run (killed mid-transfer
	// by an OOM/restart, or stalled before cleanup could run). The 24h window
	// is well beyond any legitimate in-flight download, so this never trims an
	// active transfer; it just stops dead partials accumulating on the volume.
	if removed, cErr := downloader.CleanupStalePartialFiles(options.SystemState.Model.ModelsPath, 24*time.Hour); cErr != nil {
		xlog.Warn("Failed to reap stale partial downloads", "error", cErr)
	} else if removed > 0 {
		xlog.Info("Reaped stale partial downloads", "count", removed)
	}
	if options.GeneratedContentDir != "" {
		err := os.MkdirAll(options.GeneratedContentDir, 0o750)
		if err != nil {
			return nil, fmt.Errorf("unable to create ImageDir: %q", err)
		}
	}
	if options.UploadDir != "" {
		err := os.MkdirAll(options.UploadDir, 0o750)
		if err != nil {
			return nil, fmt.Errorf("unable to create UploadDir: %q", err)
		}
	}

	// Create and migrate data directory
	if options.DataPath != "" {
		if err := os.MkdirAll(options.DataPath, 0o750); err != nil {
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

	// Initialize the OTel + Prometheus metric pipeline before any
	// counter is created. monitoring.NewLocalAIMetricsService calls
	// otel.SetMeterProvider, so any subsequent otel.Meter() call —
	// including billing.NewRecorder below — sees the real provider
	// rather than the no-op global. Initialising metrics later (in
	// core/http/app.go) leaves billing's counters bound to a no-op
	// meter and never reaches /metrics. We deliberately ignore
	// DisableMetrics here for ordering purposes; the HTTP middleware
	// that records api_call histograms is still gated.
	if !options.DisableMetrics {
		ms, err := monitoring.NewLocalAIMetricsService()
		if err != nil {
			xlog.Error("failed to initialize metrics provider", "error", err)
		} else {
			application.metricsService = ms
			// Bind the billing package's counters to the same meter the
			// metrics service exports. Without this, billing's counters
			// resolve via the OTel global and never reach /metrics.
			billing.SetMeter(ms.Meter)
		}
	}

	// Wire the routing-module billing recorder. The recorder runs in
	// every mode (auth on/off, distributed/single-node) so that token
	// tracking is not gated on auth — a no-auth single-user box still
	// gets dashboards and `/api/usage` populated.
	//
	// fallbackUser is wired *unconditionally* when stats are enabled.
	// UsageMiddleware uses it as the attribution source whenever
	// auth.GetUser(c) is nil — that covers (a) no-auth deployments and
	// (b) internal callers under auth-on (cron flushers, distributed
	// worker callbacks) that hit a recordable endpoint without a user
	// in context. The billing.user_id_present invariant still rejects
	// empty IDs; LocalUser() returns a stable UUID per data path.
	if !options.DisableStats {
		var statsBackend billing.StatsBackend
		switch {
		case application.authDB != nil:
			statsBackend = billing.NewGormBackend(application.authDB, 0, 0)
			xlog.Info("stats: using auth DB for usage records")
		default:
			statsBackend = billing.NewMemoryBackend(0)
			xlog.Info("stats: using in-memory ring buffer (no-auth single-user mode)")
		}
		application.fallbackUser = billing.LocalUser(options.DataPath)
		application.statsRecorder = billing.NewRecorder(statsBackend)
		// Drain pending records on SIGTERM. The GORM backend buffers up
		// to maxPending (5k) records across a 5s flush tick, so without
		// this the last few seconds of usage disappear on graceful exit.
		signals.RegisterGracefulTerminationHandler(func() {
			_ = application.statsRecorder.Close()
		})
		xlog.Info("stats: fallback user wired", "local_user_id", application.fallbackUser.ID)
	} else {
		xlog.Info("stats: disabled by --disable-stats")
	}

	// Wire the PII filter subsystem. The redactor is now a stateless
	// handle — detection is driven by per-model NER detectors
	// (pii.detectors → the detector model's pii_detection policy), run
	// request-side by the chat middleware and the MITM input path. The
	// regex tier was removed; redaction is opt-in per model via
	// PIIIsEnabled(). The event store backs the /api/pii/events audit log.
	application.piiRedactor = &pii.Redactor{}
	application.piiEvents = pii.NewMemoryEventStore(0)

	// Wire the routing decision log. Always-on when stats are enabled —
	// the per-router admin page reads this as the live activity feed
	// and as input to drift checks for subsystem 5.
	if !options.DisableStats {
		application.routerDecisions = router.NewMemoryDecisionStore(0)
	}
	// Process-wide classifier cache shared across all route middlewares so
	// the embedding-cache stats endpoint sees a single source of truth.
	application.routerRegistry = router.NewRegistry()

	// Subsystem 5: admission control. Limiter is always wired so a
	// model that gains a limits: block via gallery install or YAML
	// edit takes effect on the next restart without conditional plumbing.
	application.admissionLimiter = admission.New()

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
	distSvc, err := initDistributed(options, application.authDB, application.ModelConfigLoader())
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
			// Keep agent tasks consistent across replicas (jobs already sync via the
			// dispatcher + DB read-through). Same NATS client the dispatcher uses.
			application.agentJobService.SetTaskSyncNATS(distSvc.Nats)
		}
		// Wire skill store into AgentPoolService (wired at pool start time via closure)
		// The actual wiring happens in StartAgentPool since the pool doesn't exist yet.

		// Wire NATS and gallery store into GalleryService for cross-instance progress/cancel
		if application.galleryService != nil {
			application.galleryService.SetNATSClient(distSvc.Nats)
			if distSvc.DistStores != nil && distSvc.DistStores.Gallery != nil {
				// Clean up stale in-progress operations from previous crashed instances
				if _, err := distSvc.DistStores.Gallery.CleanStale(30 * time.Minute); err != nil {
					xlog.Warn("Failed to clean stale gallery operations", "error", err)
				}
				application.galleryService.SetGalleryStore(distSvc.DistStores.Gallery)

				// Reap stale ops periodically, not just at boot: an op orphaned by
				// a replica that died mid-install (its foreground handler goroutine
				// gone) would otherwise linger "processing" in the UI until the next
				// restart. 30m matches the install/upgrade ceiling so a genuinely
				// slow op is never reaped out from under itself.
				gsvc := application.galleryService
				go func() {
					ticker := time.NewTicker(15 * time.Minute)
					defer ticker.Stop()
					for {
						select {
						case <-options.Context.Done():
							return
						case <-ticker.C:
							if _, err := gsvc.ReapStaleOperations(30 * time.Minute); err != nil {
								xlog.Warn("Failed to reap stale gallery operations", "error", err)
							}
						}
					}
				}()
			}
			// Hydrate from the store first so the wildcard subscriber finds an
			// already-populated statuses map for any operations still in flight
			// on a peer replica.
			if err := application.galleryService.Hydrate(); err != nil {
				xlog.Warn("Gallery service hydrate failed", "error", err)
			}
			// Bind cache-invalidation handler before SubscribeBroadcasts so the
			// first inbound event is already routed. Peer replicas install a
			// model and broadcast on SubjectCacheInvalidateModels; this
			// callback re-runs LoadModelConfigsFromPath so a subsequent chat
			// completion that load-balances onto this replica finds the new
			// config. The originating replica reloads inline in modelHandler
			// and never enters this path.
			gs := application.galleryService
			sys := options.SystemState
			cfgLoaderOpts := options.ToConfigLoaderOptions()
			gs.OnModelsChanged = func(evt messaging.CacheInvalidateEvent) {
				// ApplyRemoteChange honors the op: a "delete" prunes the element
				// (a reload-from-path is additive and cannot drop it), anything
				// else reloads from disk; a named element's running instance is
				// shut down so the new config takes effect. The originating
				// replica reloads inline and never depends on this path.
				if err := modeladmin.ApplyRemoteChange(application.ModelConfigLoader(), application.modelLoader, sys.Model.ModelsPath, evt, cfgLoaderOpts...); err != nil {
					xlog.Warn("Failed to apply peer model config change", "error", err)
				}
			}
			if err := application.galleryService.SubscribeBroadcasts(); err != nil {
				xlog.Warn("Gallery service subscribe failed", "error", err)
			}
			// Wire distributed model/backend managers so delete propagates to workers
			application.galleryService.SetModelManager(
				nodes.NewDistributedModelManager(options, application.modelLoader, distSvc.Unloader),
			)
			application.galleryService.SetBackendManager(
				nodes.NewDistributedBackendManager(options, application.modelLoader, distSvc.Unloader, distSvc.Registry, application.galleryService),
			)
		}
	}

	// Start AgentJobService (after distributed wiring so it knows whether to use local or NATS)
	if application.agentJobService != nil {
		if err := application.agentJobService.Start(options.Context); err != nil {
			return nil, fmt.Errorf("starting agent job service: %w", err)
		}
	}

	if err := coreStartup.InstallModels(options.Context, application.GalleryService(), options.Galleries, options.BackendGalleries, options.SystemState, application.ModelLoader(), options.EnforcePredownloadScans, options.AutoloadBackendGalleries, options.RequireBackendIntegrity, nil, options.ModelsURL...); err != nil {
		xlog.Error("error installing models", "error", err)
	}

	for _, backend := range options.ExternalBackends {
		if err := galleryop.InstallExternalBackend(options.Context, options.BackendGalleries, options.SystemState, application.ModelLoader(), nil, backend, "", "", false, options.RequireBackendIntegrity); err != nil {
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
		// Refresh the upgrade cache the moment a backend op finishes — otherwise
		// the UI keeps showing a just-upgraded backend as upgradeable until the
		// next 6-hour tick. TriggerCheck is non-blocking.
		if gs := application.GalleryService(); gs != nil {
			gs.OnBackendOpCompleted = uc.TriggerCheck
		}
		go uc.Run(options.Context)
	}

	// Wire gallery generation counter into VRAM caches so they invalidate
	// when gallery data refreshes instead of using a fixed TTL.
	vram.SetGalleryGenerationFunc(gallery.GalleryGeneration)

	if options.ConfigFile != "" {
		if err := application.ModelConfigLoader().LoadMultipleModelConfigsSingleFile(options.ConfigFile, configLoaderOpts...); err != nil {
			xlog.Error("error loading config file", "error", err)
		}
	}

	if err := application.ModelConfigLoader().PreloadWithContext(options.Context, options.SystemState.Model.ModelsPath); err != nil {
		xlog.Error("error downloading models", "error", err)
	}

	if options.PreloadJSONModels != "" {
		if err := galleryop.ApplyGalleryFromString(options.SystemState, application.ModelLoader(), options.EnforcePredownloadScans, options.AutoloadBackendGalleries, options.Galleries, options.BackendGalleries, options.PreloadJSONModels, options.RequireBackendIntegrity, gallery.WithArtifactMaterializer(options.ModelArtifactMaterializer)); err != nil {
			return nil, err
		}
	}

	if options.PreloadModelsFromPath != "" {
		if err := galleryop.ApplyGalleryFromFile(options.SystemState, application.ModelLoader(), options.EnforcePredownloadScans, options.AutoloadBackendGalleries, options.Galleries, options.BackendGalleries, options.PreloadModelsFromPath, options.RequireBackendIntegrity, gallery.WithArtifactMaterializer(options.ModelArtifactMaterializer)); err != nil {
			return nil, err
		}
	}

	if options.Debug {
		for _, v := range application.ModelConfigLoader().GetAllModelsConfigs() {
			xlog.Debug("Model", "name", v.Name, "config", v)
		}
	}

	// Wire the cloudproxy MITM listener. Opt-in: empty MITMListen
	// means "no MITM" — operators must explicitly choose to start
	// it because clients have to install the generated CA cert.
	// The handler reuses the global redactor + event store so an
	// admin who's already configured PII filtering for direct API
	// traffic doesn't need a parallel config for MITM traffic.
	// Runs after loadRuntimeSettingsFromFile so a listener configured
	// via /api/settings is brought back up across restarts.
	startMITMIfConfigured(application, options)

	application.ModelLoader().SetBackendLoggingEnabled(options.EnableBackendLogging)

	// Safety-net cleanup if the application context is cancelled without
	// the caller invoking Shutdown directly. This is fire-and-forget — it
	// races binary exit and is unreliable in tests; the deterministic path
	// is application.Shutdown(), which Shutdown's sync.Once dedupes with
	// this goroutine.
	go func() {
		<-options.Context.Done()
		xlog.Debug("Context canceled, shutting down")
		if err := application.Shutdown(); err != nil {
			xlog.Error("error while stopping all grpc backends", "error", err)
		}
	}()

	// Initialize watchdog with current settings (after loading from file)
	initializeWatchdog(application, options)

	if options.LoadToMemory != nil && !options.SingleBackend {
		for _, m := range options.LoadToMemory {
			xlog.Debug("Auto loading model into memory from file", "model", m)
			// Same path as POST /backend/load: a realtime pipeline model expands
			// to its sub-models, and load failures are recorded as model_load
			// traces.
			if _, err := backend.PreloadModelByName(options.Context, application.ModelConfigLoader(), application.ModelLoader(), options, m); err != nil {
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
			if err := os.MkdirAll(options.DynamicConfigsDir, 0o700); err != nil {
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

// loadRuntimeSettingsFromFile merges runtime_settings.json into options
// with env-over-file precedence. Field coverage and the precedence rules
// live in the config registry (ApplyRuntimeSettingsAtStartup); this wrapper
// only handles the file read. No-op when DynamicConfigsDir is unset.
func loadRuntimeSettingsFromFile(options *config.ApplicationConfig) {
	if options.DynamicConfigsDir == "" {
		return
	}
	settings, err := options.ReadPersistedSettings()
	if err != nil {
		xlog.Warn("failed to read runtime_settings.json", "error", err)
		return
	}
	options.ApplyRuntimeSettingsAtStartup(&settings)
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
			model.WithSizeAwareEviction(options.SizeAwareEviction),
		)
		application.ModelLoader().SetWatchDog(wd)

		// Initialize ModelLoader LRU eviction retry settings
		application.ModelLoader().SetLRUEvictionRetrySettings(
			options.LRUEvictionMaxRetries,
			options.LRUEvictionRetryInterval,
		)

		// Sync per-model state from configs to the watchdog. Without this,
		// `pinned: true` and `concurrency_groups:` are only honored after a
		// settings-driven RestartWatchdog and never at boot.
		application.SyncPinnedModelsToWatchdog()
		application.SyncModelGroupsToWatchdog()

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

	if err := os.WriteFile(path, []byte(secret), 0o600); err != nil {
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
