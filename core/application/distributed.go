package application

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/mudler/LocalAI/core/config"
	mcpTools "github.com/mudler/LocalAI/core/http/endpoints/mcp"
	"github.com/mudler/LocalAI/core/services/advisorylock"
	"github.com/mudler/LocalAI/core/services/agents"
	"github.com/mudler/LocalAI/core/services/carrier"
	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/distributed"
	"github.com/mudler/LocalAI/core/services/jobs"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/monitoring"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/nodes/prefixcache"
	"github.com/mudler/LocalAI/core/services/storage"
	"github.com/mudler/LocalAI/internal"
	"github.com/mudler/LocalAI/pkg/distributedhdr"
	"github.com/mudler/LocalAI/pkg/sanitize"
	"github.com/mudler/xlog"
	"gorm.io/gorm"
)

// DistributedServices holds all services initialized for distributed mode.
//
// The seams that cross a process boundary are holders over the active carrier
// set: they take the carrier through an atomic pointer, so a later change of
// carrier does not touch the code that holds them. Only Shutdown reaches the
// set itself.
type DistributedServices struct {
	// Broadcaster is the fan-out seam. Code that only publishes and subscribes
	// takes this, never the connection of a carrier.
	Broadcaster  messaging.Broadcaster
	WorkQueue    messaging.WorkQueue
	AgentControl mcpTools.AgentControl
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
	Unloader     nodes.NodeControl
	ModelCleanup *nodes.ModelCleanupService

	// WorkerHTTPDial reaches a worker's own HTTP server for the admin
	// backend-logs proxy, the same way the HTTP file stager does.
	WorkerHTTPDial nodes.WorkerNetDialerFor

	// Membership keeps the row of this replica in the instances table and
	// sweeps the rows of replicas that stopped answering.
	Membership *cluster.Membership

	// active names the carrier set the holders forward to.
	active *atomic.Pointer[carrier.Set]

	shutdownOnce sync.Once
}

// Shutdown stops all distributed services in reverse initialization order.
// It is safe to call on a nil receiver and is idempotent (uses sync.Once).
func (ds *DistributedServices) Shutdown() {
	if ds == nil {
		return
	}
	ds.shutdownOnce.Do(func() {
		// First, so the peers see this replica leave before its services stop.
		ds.Membership.Stop()
		if ds.Health != nil {
			ds.Health.Stop()
		}
		if ds.Dispatcher != nil {
			ds.Dispatcher.Stop()
		}
		if closer, ok := ds.Store.(io.Closer); ok {
			closer.Close()
		}
		// AgentBridge has no Close method: its subscriptions are cleaned up
		// when the carrier is closed below.
		if ds.active != nil {
			if set := ds.active.Load(); set != nil && set.Close != nil {
				set.Close()
			}
		}
		xlog.Info("Distributed services shut down")
	})
}

// initDistributed validates distributed mode prerequisites and initializes
// NATS, object storage, node registry, and instance identity.
// Returns nil if distributed mode is not enabled.
// configLoader is used by the SmartRouter to compute concurrency-group
// anti-affinity at placement time (#9659); it may be nil in tests.
// pinned, when set, replaces configLoader as the source of models the router
// and reconciler must keep loaded (it adds warm failover targets).
func initDistributed(cfg *config.ApplicationConfig, authDB *gorm.DB, configLoader *config.ModelConfigLoader, pinned nodes.PinnedModelResolver) (*DistributedServices, error) {
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

	if authDB == nil {
		return nil, fmt.Errorf("distributed mode requires auth database to be initialized first")
	}

	// The cluster carrier row says which transport the whole cluster uses.
	// The first replica to start writes it; later replicas, and every restart,
	// follow it. Validate above has already refused a missing NATS URL, so a
	// replica that seeds the row seeds NATS.
	carrierStore, err := cluster.NewCarrierStore(authDB)
	if err != nil {
		return nil, fmt.Errorf("initializing cluster carrier state: %w", err)
	}
	carrierRow, seeded, err := carrierStore.Seed(context.Background(), cluster.CarrierNATS, cfg.Distributed.InstanceID)
	if err != nil {
		return nil, fmt.Errorf("reading cluster carrier state: %w", err)
	}
	if seeded {
		xlog.Info("Cluster carrier seeded", "carrier", carrierRow.Active, "epoch", carrierRow.Epoch)
	}
	if carrierRow.Active != cluster.CarrierNATS {
		// No silent fallback to the carrier this replica has flags for: a
		// cluster that has moved to another carrier would split in two.
		return nil, fmt.Errorf("the cluster carrier is %q (epoch %d, set by %s), and this build runs the %q carrier only",
			carrierRow.Active, carrierRow.Epoch, carrierRow.ChangedBy, cluster.CarrierNATS)
	}

	// Connect to NATS. A URL or credential the client cannot use stops the
	// start. A server that is not up yet does not: the client keeps retrying,
	// so a frontend can start before its broker.
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
	registry, err := nodes.NewNodeRegistry(authDB)
	if err != nil {
		return nil, fmt.Errorf("initializing node registry: %w", err)
	}
	xlog.Info("Node registry initialized")

	// Bound durable heartbeat writes: a beat that only carries a fresher
	// timestamp is what turned backend_nodes into a 460 MB six-row table.
	registry.SetHeartbeatCheckpoint(cfg.Distributed.NodeHeartbeatCheckpointOrDefault())

	// Measure the vacuum horizon. The 42 days it stayed open went unnoticed
	// because no gauge reported it until models started failing to load.
	if err := monitoring.RegisterControlPlaneDBMetrics(authDB, 30*time.Second); err != nil {
		// Metrics are diagnostic; a failure here must not stop the frontend.
		xlog.Warn("Control-plane database metrics unavailable", "error", err)
	}

	// Let scheduling rules be keyed by a model alias. The registry resolves a
	// rule's name through the config loader to find the model it governs, so an
	// operator can pin placement to a stable name like "production" and have it
	// follow the alias when the alias is repointed. Wired before the seed below
	// and before the reconciler starts, so the first tick already resolves.
	if configLoader != nil {
		registry.SetAliasResolver(configLoader)
	}

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

	// Initialize file manager with local cache
	cacheDir := cfg.DataPath + "/cache"
	fileMgr, err := storage.NewFileManager(store, cacheDir)
	if err != nil {
		return nil, fmt.Errorf("initializing file manager: %w", err)
	}
	xlog.Info("File manager initialized", "cacheDir", cacheDir)

	// Build the NATS carrier set and put it behind the holders. Everything
	// below takes a holder, not the connection.
	natsSet, err := carrier.NewNATSSet(carrier.NATSOptions{
		Client:         natsClient,
		Epoch:          carrierRow.Epoch,
		Registry:       registry,
		InstallTimeout: cfg.Distributed.BackendInstallTimeoutOrDefault(),
		UpgradeTimeout: cfg.Distributed.BackendUpgradeTimeoutOrDefault(),
		Token:          cfg.Distributed.RegistrationToken,
		S3Staging:      cfg.Distributed.StorageURL != "",
		FileManager:    fileMgr,
		HTTPAddrFor: func(nodeID string) (string, error) {
			node, err := registry.Get(context.Background(), nodeID)
			if err != nil {
				return "", err
			}
			if node.HTTPAddress == "" {
				return "", fmt.Errorf("node %s has no HTTP address for file transfer", nodeID)
			}
			return node.HTTPAddress, nil
		},
	})
	if err != nil {
		return nil, fmt.Errorf("building the %s carrier: %w", cluster.CarrierNATS, err)
	}
	active := &atomic.Pointer[carrier.Set]{}
	active.Store(natsSet)
	broadcaster := carrier.NewBroadcaster(active)
	workQueue := carrier.NewWorkQueue(active)
	clientFactory := carrier.NewClients(active)
	fileStager := carrier.NewFiles(active)
	remoteUnloader := carrier.NewCommands(active)
	workerHTTPDial := carrier.NewWorkerDialer(active)
	if cfg.Distributed.StorageURL != "" {
		xlog.Info("File stager initialized (S3+NATS)")
	} else {
		xlog.Info("File stager initialized (HTTP direct transfer)")
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
		clientFactory,
	)

	// Initialize job store
	jobStore, err := jobs.NewJobStore(authDB)
	if err != nil {
		return nil, fmt.Errorf("initializing job store: %w", err)
	}
	xlog.Info("Distributed job store initialized")

	// Initialize job dispatcher
	dispatcher := jobs.NewDispatcher(jobStore, workQueue, broadcaster, authDB, cfg.Distributed.InstanceID)

	// Initialize agent store
	agentStore, err := agents.NewAgentStore(authDB)
	if err != nil {
		return nil, fmt.Errorf("initializing agent store: %w", err)
	}
	xlog.Info("Distributed agent store initialized")

	// Initialize agent event bridge
	agentBridge := agents.NewEventBridge(broadcaster, agentStore, cfg.Distributed.InstanceID)

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
		prefixSync := prefixcache.NewSync(idx, broadcaster)
		pressure = prefixcache.NewSyncedPressure(prefixCfg.PressureWindow, broadcaster)
		prefixProvider = prefixSync

		// Invalidate the prefix-cache index whenever a replica row is removed.
		// AddReplicaRemovedHook fires from the single chokepoint all removal paths
		// funnel through (RemoveNodeModel / RemoveAllNodeModelReplicas), so this
		// one hook covers every path: reconciler scale-down, probe reaper,
		// health-monitor reap, RemoteUnloaderAdapter, and the router. Registering
		// it only inside this enabled block keeps the disabled path a true no-op
		// for the prefix cache; other subsystems register their own hooks
		// independently and are unaffected either way.
		registry.AddReplicaRemovedHook(func(model, node string, replica int) {
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
		if _, err := messaging.SubscribeJSON(broadcaster, messaging.SubjectPrefixCacheObserve, func(ev messaging.PrefixCacheObserveEvent) {
			prefixSync.ApplyObserve(ev, time.Now())
		}); err != nil {
			return nil, fmt.Errorf("subscribing to %s: %w", messaging.SubjectPrefixCacheObserve, err)
		}
		if _, err := messaging.SubscribeJSON(broadcaster, messaging.SubjectPrefixCacheInvalidate, func(ev messaging.PrefixCacheInvalidateEvent) {
			prefixSync.ApplyInvalidate(ev)
		}); err != nil {
			return nil, fmt.Errorf("subscribing to %s: %w", messaging.SubjectPrefixCacheInvalidate, err)
		}
		if _, err := messaging.SubscribeJSON(broadcaster, messaging.SubjectPrefixCachePressure, func(ev messaging.PrefixCachePressureEvent) {
			pressure.ApplyPressure(ev, time.Now())
		}); err != nil {
			return nil, fmt.Errorf("subscribing to %s: %w", messaging.SubjectPrefixCachePressure, err)
		}

		// Keep an exact-residency index current so backend producers can report
		// their real KV state without coupling to router internals. Routing stays
		// on the guessed provider until a backend producer is available.
		reportedIndex := prefixcache.NewReportedIndex()
		if _, err := messaging.SubscribeJSON(broadcaster, messaging.SubjectPrefixCacheResidency, reportedIndex.Apply); err != nil {
			return nil, fmt.Errorf("subscribing to %s: %w", messaging.SubjectPrefixCacheResidency, err)
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
	var pinnedResolver nodes.PinnedModelResolver
	var modelFiles func(string) []string
	if configLoader != nil {
		conflictResolver = configLoader
		pinnedResolver = configLoader
		if cfg.SystemState != nil {
			modelFiles = declaredModelFiles(configLoader, cfg.SystemState.Model.ModelsPath)
		}
	}
	if pinned != nil {
		pinnedResolver = pinned
	}
	modelCleanup := nodes.NewModelCleanupService(registry, remoteUnloader)
	router := nodes.NewSmartRouter(registry, nodes.SmartRouterOptions{
		Unloader:         remoteUnloader,
		ModelCleanup:     modelCleanup,
		FileStager:       fileStager,
		ClientFactory:    clientFactory,
		GalleriesJSON:    routerGalleriesJSON,
		AuthToken:        routerAuthToken,
		DB:               authDB,
		ConflictResolver: conflictResolver,
		PinnedResolver:   pinnedResolver,
		ModelFiles:       modelFiles,
		PrefixProvider:   prefixProvider,
		PrefixConfig:     prefixCfg,
		Pressure:         pressure,
		SharedModels:     cfg.Distributed.SharedModels,
		// A closure over the live ApplicationConfig, NOT a snapshot: the
		// runtime setting (distributed_disk_headroom_check) mutates this exact
		// member, so a snapshot here would make the toggle a no-op until
		// restart. env/CLI sets the boot value, POST /api/settings overrides it
		// live, and this is the single member both write.
		DiskHeadroomEnabled: func() bool { return !cfg.Distributed.DiskHeadroomDisabled },
		// RAW, not OrDefault: zero means "derive the budget per model from the
		// checkpoint size" (config.ModelLoadTimeoutForSize), which is what makes
		// a 70 GB video checkpoint work without the operator first hitting a
		// DeadlineExceeded and going looking for a knob. A non-zero value here is
		// an explicit override and is used verbatim.
		ModelLoadTimeout: cfg.Distributed.ModelLoadTimeout,
		// Cap how long a cold load may hold the per-model advisory lock. Derived
		// from BOTH configured budgets it has to cover, so raising either the
		// install timeout (slow links pulling multi-GB images) or the model load
		// timeout (very large checkpoints) widens the ceiling too, instead of
		// letting a stale bound cut a legitimately slow load short.
		ModelLoadCeiling: nodes.ModelLoadCeilingFor(
			cfg.Distributed.BackendInstallTimeoutOrDefault(),
			cfg.Distributed.ModelLoadTimeoutOrDefault(),
		),
		// Bounds the REQUEST, not the load: a caller out of budget gets 503 with
		// live staging progress while the job keeps running underneath.
		ModelLoadWait: cfg.Distributed.ModelLoadWait,
	})

	// Wire staging-progress broadcasting so file-staging shows up on every
	// replica, not just the one performing the transfer. Without this, a
	// /api/operations poll that round-robins onto a peer sees no staging row and
	// the progress flickers. The origin publishes; peers mirror via the wildcard.
	// A silently disabled safety check is how the original incident stayed
	// invisible for sixteen minutes. Say so once, loudly, at startup.
	if cfg.Distributed.DiskHeadroomDisabled {
		xlog.Info("Disk-headroom admission check is DISABLED: node selection will ignore whether a worker can store the model, and staging may fail with ENOSPC partway through a transfer",
			"knob", config.FlagDiskHeadroomCheck, "env", "LOCALAI_DISTRIBUTED_DISK_HEADROOM_CHECK")
	}

	router.StagingTracker().SetPublisher(broadcaster)
	if _, err := router.StagingTracker().SubscribeBroadcasts(broadcaster); err != nil {
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
		ClientFactory:     clientFactory,
		RegistrationToken: cfg.Distributed.RegistrationToken,
		DB:                authDB,
		Interval:          30 * time.Second,
		ScaleDownDelay:    5 * time.Minute,
		ProbeStaleAfter:   2 * time.Minute,
		Pressure:          pressure,
		PressureThreshold: prefixCfg.PressureScaleThreshold,
		PinnedResolver:    pinnedResolver,
	})

	// Create ModelRouterAdapter to wire into ModelLoader
	modelAdapter := nodes.NewModelRouterAdapter(router)

	membership, err := startMembership(cfg.Context, authDB, cfg.Distributed.InstanceID)
	if err != nil {
		return nil, err
	}

	success = true
	return &DistributedServices{
		Membership:   membership,
		Broadcaster:  broadcaster,
		WorkQueue:    workQueue,
		AgentControl: carrier.NewAgents(active),
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
		ModelCleanup: modelCleanup,

		WorkerHTTPDial: workerHTTPDial,

		active: active,
	}, nil
}

// startMembership creates the cluster tables, then records this replica in the
// instances table and keeps its row fresh until the Membership is stopped.
// Every replica does this on every carrier, because the change of carrier needs
// to know which replicas are alive and ready.
func startMembership(ctx context.Context, db *gorm.DB, instanceID string) (*cluster.Membership, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := advisorylock.WithLockCtx(ctx, db, advisorylock.KeySchemaMigrate, func() error {
		return cluster.Migrate(ctx, db)
	}); err != nil {
		return nil, fmt.Errorf("migrating cluster tables: %w", err)
	}
	membership := cluster.NewMembership(cluster.NewRegistry(db), instanceID, internal.PrintableVersion())
	if err := membership.Start(ctx); err != nil {
		return nil, fmt.Errorf("registering this replica: %w", err)
	}
	return membership, nil
}

func isPostgresURL(url string) bool {
	return strings.HasPrefix(url, "postgres://") || strings.HasPrefix(url, "postgresql://")
}
