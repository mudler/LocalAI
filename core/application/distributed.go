package application

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
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
	"github.com/mudler/LocalAI/core/services/tunnel"
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
	// Clients builds the gRPC client for a backend of a worker through the
	// carrier that is active.
	Clients nodes.BackendClientFactory

	// WorkerHTTPDial reaches a worker's own HTTP server for the admin
	// backend-logs proxy, the same way the HTTP file stager does.
	WorkerHTTPDial nodes.WorkerNetDialerFor

	// Membership keeps the row of this replica in the instances table and
	// sweeps the rows of replicas that stopped answering.
	Membership *cluster.Membership

	// Carriers reads the row that names the active carrier. The registration
	// of a worker answers from it.
	Carriers *cluster.CarrierStore

	// Tunnels holds the sessions that workers opened to this replica. It is
	// empty while the cluster runs on NATS: the connect route is registered on
	// every replica and refuses every worker that has no tunnel credential.
	Tunnels *tunnel.Registry

	// Instances reads the instances table. The peer route checks the
	// credential of a dialling replica against it.
	Instances *cluster.Registry
	// PeerSessions holds the links that other replicas dialled into this one,
	// and relays their streams onto the tunnels that this replica holds.
	PeerSessions *tunnel.PeerSessions
	// PeerPool holds the links that this replica dialled to its peers.
	PeerPool *tunnel.PeerPool
	// WorkerDialer opens a stream to a worker through the replica that holds its
	// tunnel: directly, or through a peer link. The tunnel carrier builds the
	// dial seams on it; nothing calls it while the cluster runs on NATS.
	WorkerDialer *tunnel.WorkerDialer

	// Settings holds the settings that decide what the whole cluster does: the
	// NATS addresses and the waits of a change of carrier.
	Settings *cluster.SettingsStore
	// Switch is the protocol of a change of carrier. The admin API calls it, and
	// the leader drives it.
	Switch *cluster.Switch
	// Swapper is what this replica does when the cluster changes its carrier.
	Swapper *carrier.Swapper
	// Runtime builds the carrier sets of this replica and reports what it could
	// build.
	Runtime *carrierRuntime

	// active names the carrier set the holders forward to.
	active *atomic.Pointer[carrier.Set]
	// window routes the calls to workers while two carriers are attached.
	window *carrier.Window
	// workers answers the questions about workers that a change asks.
	workers *nodes.SwitchWorkers

	shutdownOnce sync.Once
}

// Disconnect ends the tunnel that this replica holds for a node, and drops what
// the active carrier caches for it. The node was removed or its credential was
// replaced. It reports whether this replica held a tunnel of the node.
func (ds *DistributedServices) Disconnect(nodeID string) bool {
	held := ds.Tunnels.Disconnect(nodeID)
	if ds.active != nil {
		if set := ds.active.Load(); set != nil && set.ForgetNode != nil {
			set.ForgetNode(nodeID)
		}
	}
	// The carrier that is draining caches the same per-node state.
	if ds.window != nil {
		if prev := ds.window.Previous(); prev != nil && prev.ForgetNode != nil {
			prev.ForgetNode(nodeID)
		}
	}
	return held
}

// Shutdown stops all distributed services in reverse initialization order.
// It is safe to call on a nil receiver and is idempotent (uses sync.Once).
func (ds *DistributedServices) Shutdown() {
	if ds == nil {
		return
	}
	ds.shutdownOnce.Do(func() {
		// Stop following the cluster carrier and stop the work that a carrier started,
		// such as the claims this replica drives, before anything is torn down.
		if ds.Swapper != nil {
			ds.Swapper.Close()
		}
		// The tunnels next: their claims name this replica, and a peer that
		// asks who owns a worker must not be told a replica that is leaving.
		if ds.Tunnels != nil {
			ds.Tunnels.Close()
		}
		if ds.PeerSessions != nil {
			ds.PeerSessions.Close()
		}
		if ds.PeerPool != nil {
			ds.PeerPool.Close()
		}
		// Then the membership, so the peers see this replica leave before its
		// services stop.
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

	// The cluster carrier row says which transport the whole cluster uses. The
	// first replica to start writes it; later replicas, and every restart, follow
	// it and never their own flags. A deployment that has a NATS URL (the flag, or
	// a URL stored in the cluster settings) is seeded on NATS, so an existing
	// deployment keeps what it has. One that has only PostgreSQL is seeded on the
	// tunnel.
	carrierStore, err := cluster.NewCarrierStore(authDB)
	if err != nil {
		return nil, fmt.Errorf("initializing cluster carrier state: %w", err)
	}
	settingsStore, err := cluster.NewSettingsStore(authDB)
	if err != nil {
		return nil, fmt.Errorf("initializing cluster settings: %w", err)
	}
	carrierRow, err := seedCarrier(context.Background(), carrierStore, settingsStore, cfg.Distributed.NatsURL, cfg.Distributed.InstanceID)
	if err != nil {
		return nil, err
	}

	// Whatever fails from here on must give back what was built.
	success := false
	var startSet *carrier.Set
	defer func() {
		if !success && startSet != nil && startSet.Close != nil {
			startSet.Close()
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

	// The pieces of the tunnel carrier that do not depend on a set. They exist on
	// every replica, whatever the carrier in use: a change of carrier needs them,
	// and the routes of the tunnel are registered on every replica.
	jobStore, err := jobs.NewJobStore(authDB)
	if err != nil {
		return nil, fmt.Errorf("initializing job store: %w", err)
	}
	// The claim queue is the queue of the tunnel carrier. Its table exists on
	// every carrier, so that a change of carrier finds it.
	if err := jobs.MigrateClaims(context.Background(), authDB); err != nil {
		return nil, err
	}
	xlog.Info("Distributed job store initialized")

	// A replica proves who it is to its peers with a credential that it mints
	// itself. Only the hash is published, in the same row as the address that
	// the peers dial.
	peerCred := cluster.NewPeerCredential()
	advertised := peerAddress(cfg)
	membership, err := startMembership(cfg.Context, authDB, cfg.Distributed.InstanceID, advertised, peerCred)
	if err != nil {
		return nil, err
	}
	defer func() {
		if !success {
			membership.Stop()
		}
	}()
	clusterReg := cluster.NewRegistry(authDB)
	tunnels := tunnel.NewRegistry(clusterReg, cfg.Distributed.InstanceID)
	// If a peer sweeps this replica while it stalls, the claims of the tunnels
	// that it still holds are written again when it registers again.
	membership.SetReclaimer(tunnels)
	peerOpts, err := peerPoolOptions(cfg, advertised)
	if err != nil {
		return nil, err
	}
	peerPool := tunnel.NewPeerPool(cfg.Distributed.InstanceID, peerCred, clusterReg, peerOpts...)
	peerSessions := tunnel.NewPeerSessions(tunnel.NewRelay(tunnels).Stream)

	// Build the set of the carrier that the row names, and put it behind the
	// holders. Everything below takes a holder, not a carrier. A NATS server that
	// is not up yet does not stop the start; a URL or a credential that cannot be
	// used does.
	active := &atomic.Pointer[carrier.Set]{}
	runtime := &carrierRuntime{
		cfg: cfg, db: authDB, settings: settingsStore, registry: registry, fileMgr: fileMgr,
		instanceID: cfg.Distributed.InstanceID, jobStore: jobStore, clusterReg: clusterReg,
		tunnels: tunnels, peerPool: peerPool, active: active,
		probeWake: make(chan struct{}, 1),
	}
	startSet, err = runtime.build(context.Background(), carrierRow, carrierRow.Active, false)
	if err != nil {
		return nil, fmt.Errorf("building the %s carrier that the cluster uses: %w", carrierRow.Active, err)
	}
	active.Store(startSet)
	broadcaster := carrier.NewBroadcaster(active)
	runtime.bus = broadcaster

	// While two carriers are attached, a call to a worker goes to the carrier the
	// worker is attached to.
	workers := nodes.NewSwitchWorkers(registry, clusterReg, cluster.DefaultReconnectGrace, cfg.Distributed.StaleNodeThresholdOrDefault())
	window := carrier.NewWindow(
		func(ctx context.Context, nodeID string) (carrier.Attachment, error) {
			attached, err := workers.AttachedCarriers(ctx, nodeID)
			if err != nil {
				return carrier.Attachment{}, err
			}
			return attachmentOf(attached), nil
		},
		workers.AgentsAttached,
	)
	workQueue := carrier.NewWorkQueue(active)
	clientFactory := carrier.NewClients(active)
	clientFactory.UseWindow(window)
	fileStager := carrier.NewFiles(active)
	fileStager.UseWindow(window)
	remoteUnloader := carrier.NewCommands(active)
	remoteUnloader.UseWindow(window)
	workerHTTPDial := carrier.NewRoutedWorkerDialer(active, window)
	agentControl := carrier.NewAgents(active)
	agentControl.UseWindow(window)
	if cfg.Distributed.StorageURL != "" {
		xlog.Info("File stager initialized (S3)", "carrier", carrierRow.Active)
	} else {
		xlog.Info("File stager initialized (HTTP direct transfer)", "carrier", carrierRow.Active)
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

	// While the tunnel is the active carrier, a node that heartbeats and holds no
	// tunnel is demoted once its departure is older than the grace. On NATS no
	// node holds a tunnel, and the monitor reads nothing.
	healthMon.UsePresence(clusterReg, cluster.DefaultReconnectGrace, func() bool {
		set := active.Load()
		return set != nil && set.Name == cluster.CarrierTunnel
	})

	// The protocol of a change of carrier, and what this replica does in one. The
	// switch tells the other replicas of each move with a hint, so that they look
	// at the row at once. The hint is a courtesy; the poll is what counts.
	fallbackTimings := cluster.Timings{
		PrepareTimeout:   cfg.Distributed.CarrierPrepareTimeout,
		TransitionWindow: cfg.Distributed.CarrierTransitionWindow,
		MaxDrain:         cfg.Distributed.CarrierMaxDrain,
	}
	carrierSwitch, err := cluster.NewSwitch(cluster.SwitchOptions{
		Store:    carrierStore,
		Registry: clusterReg,
		Workers:  workers,
		Work:     workCounts{db: authDB},
		Timings: func() cluster.Timings {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			t, err := settingsStore.Timings(ctx, fallbackTimings)
			if err != nil {
				xlog.Warn("Could not read the waits of a change of carrier from the cluster settings; using the ones of this replica", "error", err)
				return fallbackTimings
			}
			return t
		},
		AvailabilityMaxAge: availabilityMaxAge,
		OnChange: func(row cluster.CarrierRow) {
			go func() {
				if err := broadcaster.Publish(messaging.SubjectCarrierChanged, row); err != nil {
					xlog.Debug("Could not send the hint that the cluster carrier row moved; the other replicas read it on their poll", "error", err)
				}
			}()
		},
	})
	if err != nil {
		return nil, err
	}
	swapper, err := carrier.NewSwapper(carrier.SwapperOptions{
		Cur: active, Bus: broadcaster, Window: window,
		Rows: carrierStore, Ready: membership, Build: runtime.Build,
	})
	if err != nil {
		return nil, err
	}

	success = true
	return &DistributedServices{
		Membership:   membership,
		Carriers:     carrierStore,
		Settings:     settingsStore,
		Switch:       carrierSwitch,
		Swapper:      swapper,
		Runtime:      runtime,
		Tunnels:      tunnels,
		Instances:    clusterReg,
		PeerSessions: peerSessions,
		PeerPool:     peerPool,
		WorkerDialer: tunnel.NewWorkerDialer(tunnels, peerPool),
		Broadcaster:  broadcaster,
		WorkQueue:    workQueue,
		AgentControl: agentControl,
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
		Clients:      clientFactory,

		WorkerHTTPDial: workerHTTPDial,

		active:  active,
		window:  window,
		workers: workers,
	}, nil
}

// StartCarrierSwitch starts what this replica does about changes of carrier: it
// follows the cluster carrier row, drives the protocol when it holds the
// leadership, and reports which carriers it could build. Call it once, after the
// services that use the carrier have started.
func (ds *DistributedServices) StartCarrierSwitch(ctx context.Context, db *gorm.DB) {
	// The hints. Both are a courtesy that shortens a wait.
	if _, err := ds.Broadcaster.Subscribe(messaging.SubjectCarrierChanged, func([]byte) { ds.Swapper.Wake() }); err != nil {
		xlog.Warn("Could not listen for the hint that the cluster carrier changed; the poll still follows the row", "error", err)
	}
	if _, err := ds.Broadcaster.Subscribe(messaging.SubjectCarrierProbe, func([]byte) { ds.Runtime.wakeProbe() }); err != nil {
		xlog.Warn("Could not listen for the request to report which carriers this replica can build", "error", err)
	}
	go ds.Swapper.Run(ctx)
	go ds.Runtime.RunAvailability(ctx)
	// One replica leads the protocol at a time. If it dies, the next tick on
	// another replica goes on from the row.
	go advisorylock.RunLeaderLoop(ctx, db, advisorylock.KeyCarrierSwitch, leaderTick, func() {
		if err := ds.Switch.Drive(ctx); err != nil && ctx.Err() == nil {
			xlog.Warn("The change of carrier could not advance", "error", err)
		}
	})
}

// leaderTick is how often the replica that leads makes the move that is due.
const leaderTick = 2 * time.Second

// seedCarrier reads the cluster carrier row, and writes it when no replica has
// yet. It returns the row as it is after the call.
func seedCarrier(ctx context.Context, store *cluster.CarrierStore, settings *cluster.SettingsStore, flagURL, instanceID string) (cluster.CarrierRow, error) {
	stored, _, err := settings.Get(ctx, cluster.SettingNATSURL)
	if err != nil {
		return cluster.CarrierRow{}, err
	}
	seedWith := cluster.CarrierTunnel
	if flagURL != "" || stored != "" {
		seedWith = cluster.CarrierNATS
	}
	row, created, err := store.Seed(ctx, seedWith, instanceID)
	if err != nil {
		return cluster.CarrierRow{}, fmt.Errorf("reading cluster carrier state: %w", err)
	}
	if created {
		xlog.Info("Cluster carrier seeded", "carrier", row.Active, "epoch", row.Epoch)
	}
	if flagURL != "" {
		// The URL of the flag is copied into the cluster settings when none is
		// stored, so that every replica uses the same one. A stored URL wins.
		made, err := settings.SetIfAbsent(ctx, cluster.SettingNATSURL, flagURL, instanceID)
		if err != nil {
			return cluster.CarrierRow{}, err
		}
		switch {
		case made:
			xlog.Info("The NATS URL of this replica is now a setting of the cluster", "url", sanitize.URL(flagURL))
		case stored != "" && stored != flagURL:
			xlog.Warn("The NATS URL of this replica differs from the one stored for the cluster; the stored one is used",
				"flag", sanitize.URL(flagURL), "cluster", sanitize.URL(stored))
		}
	}
	if row.Active == cluster.CarrierTunnel && flagURL != "" {
		xlog.Info("The cluster runs on the tunnel carrier. The NATS URL of this replica is kept for a change back to NATS, and no NATS connection is opened")
	}
	return row, nil
}

// attachmentOf turns the carriers a worker is attached to into the routing form.
func attachmentOf(cs []cluster.Carrier) carrier.Attachment {
	var a carrier.Attachment
	for _, c := range cs {
		switch c {
		case cluster.CarrierNATS:
			a.NATS = true
		case cluster.CarrierTunnel:
			a.Tunnel = true
		}
	}
	return a
}

// startMembership creates the cluster tables, then records this replica in the
// instances table and keeps its row fresh until the Membership is stopped.
// Every replica does this on every carrier, because the change of carrier needs
// to know which replicas are alive and ready.
func startMembership(ctx context.Context, db *gorm.DB, instanceID, advertisedAddr string, peerCred cluster.PeerCredential) (*cluster.Membership, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := advisorylock.WithLockCtx(ctx, db, advisorylock.KeySchemaMigrate, func() error {
		return cluster.Migrate(ctx, db)
	}); err != nil {
		return nil, fmt.Errorf("migrating cluster tables: %w", err)
	}
	membership := cluster.NewMembership(cluster.NewRegistry(db), instanceID, internal.PrintableVersion())
	membership.SetPeer(advertisedAddr, peerCred)
	if err := membership.Start(ctx); err != nil {
		return nil, fmt.Errorf("registering this replica: %w", err)
	}
	return membership, nil
}

func isPostgresURL(url string) bool {
	return strings.HasPrefix(url, "postgres://") || strings.HasPrefix(url, "postgresql://")
}

// peerPoolOptions reads the TLS settings of the peer link. It also warns when the
// link would cross a network in clear text: the credential of this replica and
// every relayed request, prompts included, go over it.
func peerPoolOptions(cfg *config.ApplicationConfig, advertised string) ([]tunnel.PeerPoolOption, error) {
	d := cfg.Distributed
	if !d.PeerTLS && d.PeerTLSCA == "" {
		if addr := advertised; clearTextOnNetwork(addr) {
			xlog.Warn("The peer link is clear text and this replica publishes an address that is not on this host, so the credential of this replica and the requests that other replicas relay cross the network unencrypted. "+
				"Put the replicas behind TLS and set LOCALAI_PEER_TLS (and LOCALAI_PEER_TLS_CA for a private CA)", "address", addr)
		}
		return nil, nil
	}
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if d.PeerTLSCA != "" {
		pem, err := os.ReadFile(d.PeerTLSCA)
		if err != nil {
			return nil, fmt.Errorf("reading the CA file of the peer link: %w", err)
		}
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("the CA file of the peer link, %s, holds no certificate", d.PeerTLSCA)
		}
		tlsCfg.RootCAs = roots
	}
	return []tunnel.PeerPoolOption{tunnel.WithPeerTLS(tlsCfg)}, nil
}

// clearTextOnNetwork reports whether an address published for the peer link
// names a host other than this one. An empty address is not published, so
// nothing is dialled.
func clearTextOnNetwork(addr string) bool {
	if addr == "" {
		return false
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	if strings.EqualFold(host, "localhost") {
		return false
	}
	ip := net.ParseIP(host)
	return ip == nil || !ip.IsLoopback()
}

// peerAddress returns the address at which the other replicas dial this one. An
// address that the operator configured is checked and used as it is. Without
// one, it comes from the route to the database, which all replicas share.
//
// A replica that has no usable address is not refused. It publishes none, and
// the replicas that need to relay through it find it unreachable, which is
// reported when they try. The log line says how to fix it.
func peerAddress(cfg *config.ApplicationConfig) string {
	if configured := cfg.Distributed.PeerAddress; configured != "" {
		reason, err := cluster.CheckAdvertisedAddr(configured)
		if err != nil {
			xlog.Warn("The configured peer address is unusable, so this replica publishes none and the other replicas cannot reach it", "address", configured, "error", err)
			return ""
		}
		if reason != "" {
			xlog.Warn("The configured peer address may not work for the other replicas", "address", configured, "reason", reason)
		}
		return configured
	}
	_, portText, err := net.SplitHostPort(cfg.APIAddress)
	if err != nil {
		xlog.Warn("The listen address has no port, so this replica publishes no peer address; set LOCALAI_PEER_ADDRESS", "address", cfg.APIAddress, "error", err)
		return ""
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		xlog.Warn("The listen port is not a number, so this replica publishes no peer address; set LOCALAI_PEER_ADDRESS", "port", portText, "error", err)
		return ""
	}
	addr, err := cluster.DiscoverAdvertisedAddr(cfg.Auth.DatabaseURL, port)
	if err != nil {
		xlog.Warn("This replica publishes no peer address, so a worker tunnel that it holds cannot be reached through the other replicas; set LOCALAI_PEER_ADDRESS", "error", err)
		return ""
	}
	return addr
}
