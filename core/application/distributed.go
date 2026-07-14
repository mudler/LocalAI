package application

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/agents"
	"github.com/mudler/LocalAI/core/services/distributed"
	"github.com/mudler/LocalAI/core/services/jobs"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/nodes/prefixcache"
	"github.com/mudler/LocalAI/core/services/storage"
	"github.com/mudler/LocalAI/pkg/distributedhdr"
	"github.com/mudler/LocalAI/pkg/sanitize"
	"github.com/mudler/xlog"
	"gorm.io/gorm"
)

// DistributedServices holds all services initialized for distributed mode.
type DistributedServices struct {
	Nats         *messaging.Client
	Store        storage.ObjectStore
	Registry     *nodes.NodeRegistry
	Router       *nodes.SmartRouter
	Health       *nodes.HealthMonitor
	Reconciler   *nodes.ReplicaReconciler
	JobStore     *jobs.JobStore
	Dispatcher   *jobs.Dispatcher
	AgentStore   *agents.AgentStore
	AgentBridge  *agents.EventBridge
	DistStores   *distributed.Stores
	FileMgr      *storage.FileManager
	FileStager   nodes.FileStager
	ModelAdapter *nodes.ModelRouterAdapter
	Unloader     *nodes.RemoteUnloaderAdapter

	shutdownOnce sync.Once
}

// Shutdown stops all distributed services in reverse initialization order.
// It is safe to call on a nil receiver and is idempotent (uses sync.Once).
func (ds *DistributedServices) Shutdown() {
	if ds == nil {
		return
	}
	ds.shutdownOnce.Do(func() {
		if ds.Health != nil {
			ds.Health.Stop()
		}
		if ds.Dispatcher != nil {
			ds.Dispatcher.Stop()
		}
		if closer, ok := ds.Store.(io.Closer); ok {
			closer.Close()
		}
		// AgentBridge has no Close method — its NATS subscriptions are cleaned up
		// when the NATS client is closed below.
		if ds.Nats != nil {
			ds.Nats.Close()
		}
		xlog.Info("Distributed services shut down")
	})
}

// initDistributed validates distributed mode prerequisites and initializes
// NATS, object storage, node registry, and instance identity.
// Returns nil if distributed mode is not enabled.
// configLoader is used by the SmartRouter to compute concurrency-group
// anti-affinity at placement time (#9659); it may be nil in tests.
func initDistributed(cfg *config.ApplicationConfig, authDB *gorm.DB, configLoader *config.ModelConfigLoader) (*DistributedServices, error) {
	if !cfg.Distributed.Enabled {
		return nil, nil
	}

	xlog.Info("Distributed mode enabled — validating prerequisites")

	// Validate distributed config (NATS URL, S3 credential pairing, durations, etc.)
	if err := cfg.Distributed.Validate(); err != nil {
		return nil, err
	}

	// Validate PostgreSQL is configured (auth DB must be PostgreSQL for distributed mode)
	if !cfg.Auth.Enabled {
		return nil, fmt.Errorf("distributed mode requires authentication to be enabled (--auth / LOCALAI_AUTH=true)")
	}
	if !isPostgresURL(cfg.Auth.DatabaseURL) {
		return nil, fmt.Errorf("distributed mode requires PostgreSQL for auth database (got %q)", sanitize.URL(cfg.Auth.DatabaseURL))
	}

	// Generate instance ID if not set
	if cfg.Distributed.InstanceID == "" {
		cfg.Distributed.InstanceID = uuid.New().String()
	}
	xlog.Info("Distributed instance", "id", cfg.Distributed.InstanceID)

	// Connect to NATS
	natsAuth := cfg.Distributed.NatsAuthConfig()
	if natsAuth.RequireAuth && (natsAuth.ServiceUserJWT == "" || natsAuth.ServiceUserSeed == "") {
		return nil, fmt.Errorf("LOCALAI_NATS_REQUIRE_AUTH requires LOCALAI_NATS_SERVICE_JWT and LOCALAI_NATS_SERVICE_SEED")
	}
	natsOpts := cfg.Distributed.NatsMessagingOptions("", "")
	natsClient, err := messaging.New(cfg.Distributed.NatsURL, natsOpts...)
	if err != nil {
		return nil, fmt.Errorf("connecting to NATS: %w", err)
	}
	xlog.Info("Connected to NATS", "url", sanitize.URL(cfg.Distributed.NatsURL))

	// Ensure NATS is closed if any subsequent initialization step fails.
	success := false
	defer func() {
		if !success {
			natsClient.Close()
		}
	}()

	// Initialize object storage
	var store storage.ObjectStore
	if cfg.Distributed.StorageURL != "" {
		if cfg.Distributed.StorageBucket == "" {
			return nil, fmt.Errorf("distributed storage bucket must be set when storage URL is configured")
		}
		s3Store, err := storage.NewS3Store(context.Background(), storage.S3Config{
			Endpoint:        cfg.Distributed.StorageURL,
			Region:          cfg.Distributed.StorageRegion,
			Bucket:          cfg.Distributed.StorageBucket,
			AccessKeyID:     cfg.Distributed.StorageAccessKey,
			SecretAccessKey: cfg.Distributed.StorageSecretKey,
			ForcePathStyle:  true, // required for MinIO
		})
		if err != nil {
			return nil, fmt.Errorf("initializing S3 storage: %w", err)
		}
		xlog.Info("Object storage initialized (S3)", "endpoint", cfg.Distributed.StorageURL, "bucket", cfg.Distributed.StorageBucket)
		store = s3Store
	} else {
		// Fallback to filesystem storage in distributed mode (useful for single-node testing)
		fsStore, err := storage.NewFilesystemStore(cfg.DataPath + "/objectstore")
		if err != nil {
			return nil, fmt.Errorf("initializing filesystem storage: %w", err)
		}
		xlog.Info("Object storage initialized (filesystem fallback)", "path", cfg.DataPath+"/objectstore")
		store = fsStore
	}

	// Initialize node registry (requires the auth DB which is PostgreSQL)
	if authDB == nil {
		return nil, fmt.Errorf("distributed mode requires auth database to be initialized first")
	}

	registry, err := nodes.NewNodeRegistry(authDB)
	if err != nil {
		return nil, fmt.Errorf("initializing node registry: %w", err)
	}
	xlog.Info("Node registry initialized")

	// Seed declarative per-model scheduling config (LOCALAI_MODEL_SCHEDULING /
	// LOCALAI_MODEL_SCHEDULING_CONFIG). Authoritative: overwrites matching models
	// on every boot. Runs before the reconciler starts so the first tick already
	// sees the desired state. Models not listed are left untouched.
	if cfg.Distributed.ModelSchedulingJSON != "" || cfg.Distributed.ModelSchedulingConfigPath != "" {
		schedConfigs, err := nodes.ParseSchedulingSeed(cfg.Distributed.ModelSchedulingJSON, cfg.Distributed.ModelSchedulingConfigPath)
		if err != nil {
			return nil, fmt.Errorf("parsing declarative model scheduling config: %w", err)
		}
		if err := registry.SeedModelScheduling(context.Background(), schedConfigs); err != nil {
			return nil, fmt.Errorf("seeding declarative model scheduling config: %w", err)
		}
		xlog.Info("Applied declarative model scheduling config", "models", len(schedConfigs))
	}

	// Collect SmartRouter option values; the router itself is created after all
	// dependencies (including FileStager and Unloader) are ready.
	var routerAuthToken string
	if cfg.Distributed.RegistrationToken != "" {
		routerAuthToken = cfg.Distributed.RegistrationToken
	}
	var routerGalleriesJSON string
	if galleriesJSON, err := json.Marshal(cfg.BackendGalleries); err == nil {
		routerGalleriesJSON = string(galleriesJSON)
	}

	healthMon := nodes.NewHealthMonitor(registry, authDB,
		cfg.Distributed.HealthCheckIntervalOrDefault(),
		cfg.Distributed.StaleNodeThresholdOrDefault(),
		routerAuthToken,
		!cfg.Distributed.DisablePerModelHealthCheck,
	)

	// Initialize job store
	jobStore, err := jobs.NewJobStore(authDB)
	if err != nil {
		return nil, fmt.Errorf("initializing job store: %w", err)
	}
	xlog.Info("Distributed job store initialized")

	// Initialize job dispatcher
	dispatcher := jobs.NewDispatcher(jobStore, natsClient, authDB, cfg.Distributed.InstanceID, cfg.Distributed.JobWorkerConcurrency)

	// Initialize agent store
	agentStore, err := agents.NewAgentStore(authDB)
	if err != nil {
		return nil, fmt.Errorf("initializing agent store: %w", err)
	}
	xlog.Info("Distributed agent store initialized")

	// Initialize agent event bridge
	agentBridge := agents.NewEventBridge(natsClient, agentStore, cfg.Distributed.InstanceID)

	// Start observable persister — captures observable_update events from workers
	// (which have no DB access) and persists them to PostgreSQL.
	if err := agentBridge.StartObservablePersister(); err != nil {
		xlog.Warn("Failed to start observable persister", "error", err)
	} else {
		xlog.Info("Observable persister started")
	}

	// Initialize Phase 4 stores (MCP, Gallery, FineTune, Skills)
	distStores, err := distributed.InitStores(authDB)
	if err != nil {
		return nil, fmt.Errorf("initializing distributed stores: %w", err)
	}

	// Initialize file manager with local cache
	cacheDir := cfg.DataPath + "/cache"
	fileMgr, err := storage.NewFileManager(store, cacheDir)
	if err != nil {
		return nil, fmt.Errorf("initializing file manager: %w", err)
	}
	xlog.Info("File manager initialized", "cacheDir", cacheDir)

	// Create FileStager for distributed file transfer
	var fileStager nodes.FileStager
	if cfg.Distributed.StorageURL != "" {
		fileStager = nodes.NewS3NATSFileStager(fileMgr, natsClient)
		xlog.Info("File stager initialized (S3+NATS)")
	} else {
		fileStager = nodes.NewHTTPFileStager(func(nodeID string) (string, error) {
			node, err := registry.Get(context.Background(), nodeID)
			if err != nil {
				return "", err
			}
			if node.HTTPAddress == "" {
				return "", fmt.Errorf("node %s has no HTTP address for file transfer", nodeID)
			}
			return node.HTTPAddress, nil
		}, cfg.Distributed.RegistrationToken)
		xlog.Info("File stager initialized (HTTP direct transfer)")
	}
	// Create RemoteUnloaderAdapter — needed by SmartRouter and startup.go
	remoteUnloader := nodes.NewRemoteUnloaderAdapter(
		registry,
		natsClient,
		cfg.Distributed.BackendInstallTimeoutOrDefault(),
		cfg.Distributed.BackendUpgradeTimeoutOrDefault(),
	)

	// Prefix-cache-aware routing. Enabled by default; an operator can opt out
	// with --distributed-prefix-cache=false, which leaves prefixProvider and
	// pressure nil so the SmartRouter and reconciler behave exactly as the
	// round-robin floor (true no-op). When enabled we build the local index,
	// wrap it in a NATS-backed Sync (publishes our observations, applies peers'
	// via the subscriptions below), install the extraction hook used by
	// core/backend/llm.go, and run a background eviction ticker on the app ctx.
	var prefixProvider prefixcache.Provider
	var pressure *prefixcache.Pressure
	var prefixCfg prefixcache.Config
	if !cfg.Distributed.PrefixCacheDisabled {
		prefixCfg = prefixcache.DefaultConfig()
		if cfg.Distributed.PrefixCacheTTL > 0 {
			prefixCfg.TTL = cfg.Distributed.PrefixCacheTTL
		}
		if err := prefixCfg.Validate(); err != nil {
			return nil, fmt.Errorf("invalid prefix-cache configuration: %w", err)
		}
		idx := prefixcache.NewIndex(prefixCfg)
		prefixSync := prefixcache.NewSync(idx, natsClient)
		pressure = prefixcache.NewPressure(prefixCfg.PressureWindow)
		prefixProvider = prefixSync

		// Invalidate the prefix-cache index whenever a replica row is removed.
		// SetReplicaRemovedHook fires from the single chokepoint all removal paths
		// funnel through (RemoveNodeModel / RemoveAllNodeModelReplicas), so this
		// one hook covers every path: reconciler scale-down, probe reaper,
		// health-monitor reap, RemoteUnloaderAdapter, and the router. Registering
		// it only inside this enabled block keeps the disabled path a true no-op
		// (the registry stays hook-less).
		registry.SetReplicaRemovedHook(func(model, node string, replica int) {
			if replica < 0 {
				prefixSync.InvalidateNode(model, node)
			} else {
				prefixSync.Invalidate(model, prefixcache.ReplicaKey{NodeID: node, Replica: replica})
			}
		})

		distributedhdr.PrefixChainHook = func(model, prompt string) []uint64 {
			return prefixcache.ExtractChain(model, prompt, prefixCfg)
		}

		// Apply peers' observations/invalidations to the same Sync. ApplyObserve
		// and ApplyInvalidate update only the local index and do not re-publish,
		// so there is no broadcast loop.
		if _, err := messaging.SubscribeJSON(natsClient, messaging.SubjectPrefixCacheObserve, func(ev messaging.PrefixCacheObserveEvent) {
			prefixSync.ApplyObserve(ev, time.Now())
		}); err != nil {
			return nil, fmt.Errorf("subscribing to %s: %w", messaging.SubjectPrefixCacheObserve, err)
		}
		if _, err := messaging.SubscribeJSON(natsClient, messaging.SubjectPrefixCacheInvalidate, func(ev messaging.PrefixCacheInvalidateEvent) {
			prefixSync.ApplyInvalidate(ev)
		}); err != nil {
			return nil, fmt.Errorf("subscribing to %s: %w", messaging.SubjectPrefixCacheInvalidate, err)
		}

		// Background eviction: sweep idle entries on the app context. Stopped
		// when the app context is cancelled (mirrors the reconciler loop which
		// also runs on options.Context). TTL/2 keeps stale entries from
		// outliving their idle window by more than half a TTL.
		evictInterval := prefixCfg.TTL / 2
		go func() {
			ticker := time.NewTicker(evictInterval)
			defer ticker.Stop()
			for {
				select {
				case <-cfg.Context.Done():
					return
				case <-ticker.C:
					prefixSync.Evict(time.Now())
				}
			}
		}()
		xlog.Info("Prefix-cache-aware routing enabled", "ttl", prefixCfg.TTL, "evictInterval", evictInterval)
	} else {
		xlog.Info("Prefix-cache-aware routing disabled: using round-robin routing")
	}

	// All dependencies ready — build SmartRouter with all options at once
	var conflictResolver nodes.ConcurrencyConflictResolver
	if configLoader != nil {
		conflictResolver = configLoader
	}
	router := nodes.NewSmartRouter(registry, nodes.SmartRouterOptions{
		Unloader:         remoteUnloader,
		FileStager:       fileStager,
		GalleriesJSON:    routerGalleriesJSON,
		AuthToken:        routerAuthToken,
		DB:               authDB,
		ConflictResolver: conflictResolver,
		PrefixProvider:   prefixProvider,
		PrefixConfig:     prefixCfg,
		Pressure:         pressure,
		SharedModels:     cfg.Distributed.SharedModels,
		// Cap how long a cold load may hold the per-model advisory lock: the
		// configured backend.install deadline plus a margin for file staging and
		// the remote LoadModel. Derived from the install timeout so raising it
		// (for slow links pulling multi-GB images) widens the ceiling too,
		// instead of letting the static default cut a legitimately slow load.
		ModelLoadCeiling: cfg.Distributed.BackendInstallTimeoutOrDefault() + 10*time.Minute,
	})

	// Wire staging-progress broadcasting so file-staging shows up on every
	// replica, not just the one performing the transfer. Without this, a
	// /api/operations poll that round-robins onto a peer sees no staging row and
	// the progress flickers. The origin publishes; peers mirror via the wildcard.
	router.StagingTracker().SetPublisher(natsClient)
	if _, err := router.StagingTracker().SubscribeBroadcasts(natsClient); err != nil {
		xlog.Warn("Failed to subscribe to staging progress broadcasts", "error", err)
	}

	// Create ReplicaReconciler for auto-scaling model replicas. Adapter +
	// RegistrationToken feed the state-reconciliation passes: pending op
	// drain uses the adapter, and model health probes use the token to auth
	// against workers' gRPC HealthCheck.
	reconciler := nodes.NewReplicaReconciler(nodes.ReplicaReconcilerOptions{
		Registry:          registry,
		Scheduler:         router,
		Unloader:          remoteUnloader,
		Adapter:           remoteUnloader,
		RegistrationToken: cfg.Distributed.RegistrationToken,
		DB:                authDB,
		Interval:          30 * time.Second,
		ScaleDownDelay:    5 * time.Minute,
		ProbeStaleAfter:   2 * time.Minute,
		Pressure:          pressure,
		PressureThreshold: prefixCfg.PressureScaleThreshold,
	})

	// Create ModelRouterAdapter to wire into ModelLoader
	modelAdapter := nodes.NewModelRouterAdapter(router)

	success = true
	return &DistributedServices{
		Nats:         natsClient,
		Store:        store,
		Registry:     registry,
		Router:       router,
		Health:       healthMon,
		Reconciler:   reconciler,
		JobStore:     jobStore,
		Dispatcher:   dispatcher,
		AgentStore:   agentStore,
		AgentBridge:  agentBridge,
		DistStores:   distStores,
		FileMgr:      fileMgr,
		FileStager:   fileStager,
		ModelAdapter: modelAdapter,
		Unloader:     remoteUnloader,
	}, nil
}

func isPostgresURL(url string) bool {
	return strings.HasPrefix(url, "postgres://") || strings.HasPrefix(url, "postgresql://")
}
