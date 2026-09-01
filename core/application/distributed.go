package application

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/agents"
	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/distributed"
	"github.com/mudler/LocalAI/core/services/jobs"
	"github.com/mudler/LocalAI/core/services/messaging"
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
	ModelCleanup *nodes.ModelCleanupService

	// Cluster is the replica-membership registry: which frontend replicas are
	// alive, at which address, and which of them holds a given worker's tunnel.
	Cluster *cluster.Registry
	// Membership publishes this replica's row and reaps the dead. Nil when no
	// peer-reachable address could be determined, which leaves this replica
	// invisible to its peers but otherwise fully functional.
	Membership *cluster.Membership
	// PeerSessions owns the peer links other replicas dialled into this one,
	// and relays the streams that arrive on them onto the worker tunnels this
	// replica holds.
	PeerSessions *cluster.SessionStore
	// Peers owns the peer links this replica dialled OUT, the mirror of
	// PeerSessions. It is what the relaying dialer opens a stream on when a
	// request arrives here for a worker another replica holds.
	Peers *cluster.PeerPool
	// Tunnels holds the worker tunnels this replica has accepted and keeps the
	// node_connections table agreeing with them. It is handed to the membership
	// loop, which re-claims what it holds after this replica has been reaped,
	// and to the route that accepts a worker's dial.
	Tunnels *cluster.TunnelRegistry
	// WorkerDialer is how anything in this process reaches a worker: locally
	// when this replica holds the tunnel, and through the owning replica when
	// it does not. The HTTP layer takes its WebSocket log proxy from here.
	WorkerDialer *cluster.WorkerDialer
	// BackendClients builds the gRPC clients for worker backend processes, over
	// WorkerDialer. Exposed so the model store built in startup.go reaches
	// remote models the same way every other caller does.
	BackendClients nodes.BackendClientFactory

	shutdownOnce sync.Once
}

// Shutdown stops all distributed services in reverse initialization order.
// It is safe to call on a nil receiver and is idempotent (uses sync.Once).
func (ds *DistributedServices) Shutdown() {
	if ds == nil {
		return
	}
	ds.shutdownOnce.Do(func() {
		// Peer state first: a replica that is going away should stop claiming
		// to be alive before it stops answering, so peers re-home rather than
		// dial a process in teardown.
		if ds.Membership != nil {
			ds.Membership.Stop()
		}
		if ds.PeerSessions != nil {
			ds.PeerSessions.CloseAll()
		}
		// Both halves of the peer mesh go down together. A pool left open
		// holds a WebSocket and two yamux loop goroutines per peer for as long
		// as the process lives, and an Open after this reports ErrPoolClosed,
		// which is a fact about this process and never node absence.
		if ds.Peers != nil {
			ds.Peers.Close()
		}
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

	// Replica membership. NewNodeRegistry has just migrated the tables this
	// reads, so it has to come after it.
	clusterRegistry := cluster.NewRegistry(authDB)
	var membership *cluster.Membership
	if advertised, err := advertisedPeerAddr(cfg); err != nil {
		// Not fatal. A replica that cannot publish an address still serves
		// every request that reaches it directly; what it cannot do is have
		// another replica relay to it. Failing startup here would take out
		// every existing single-host deployment, whose route to a local
		// database is loopback.
		xlog.Warn("This replica will not be reachable by its peers: no advertised address",
			"error", err, "knob", "LOCALAI_DISTRIBUTED_ADVERTISE_ADDR")
	} else {
		membership = cluster.NewMembership(clusterRegistry, cfg.Distributed.InstanceID, advertised, internal.PrintableVersion())
		if err := membership.Start(cfg.Context); err != nil {
			return nil, fmt.Errorf("registering this replica in the cluster: %w", err)
		}
	}

	// The worker tunnels this replica accepts. It claims as the SAME instance
	// ID membership registers under, because that is the ID a peer's Owner
	// lookup joins a claim against to decide the owner is alive; two IDs here
	// would make every claim this replica writes look like it belongs to a
	// replica that does not exist.
	tunnels := cluster.NewTunnelRegistry(clusterRegistry, cfg.Distributed.InstanceID)
	// Without this the re-claim in the heartbeat loop is dead code: a replica
	// stalled long enough to be swept loses the connection rows it owned, and
	// nothing would ever write them back, so every other replica would answer
	// "not connected" for workers that are connected right here.
	//
	// Nil when no peer-reachable address could be determined above. There is no
	// heartbeat loop to hand it to in that case, and no other replica can reach
	// this one anyway; the registry is still built, because it is what the
	// tunnel endpoint attaches to and what this replica opens its own streams
	// through.
	if membership != nil {
		membership.SetTunnels(tunnels)
	}

	// The links peers dial IN, with the relay installed on them. This is what
	// makes more than one replica work: a worker holds one tunnel, it lands on
	// one replica, and every request that arrives anywhere else reaches the
	// worker through this handler. Passing nil here would leave every such
	// request refused, promptly and only at debug level, which presents as a
	// worker that is connected and unusable from most of the deployment.
	peerSessions := cluster.NewSessionStore(cluster.NewRelay(tunnels).Stream)
	// The links this replica dials OUT, the other half of the same mesh. It
	// authenticates with the registration token because that is the token the
	// peer route checks (see RegisterClusterRoutes); two different tokens here
	// would make every peer dial 401 with nothing naming the mismatch.
	peers := cluster.NewPeerPool(cfg.Distributed.InstanceID, cfg.Distributed.RegistrationToken, clusterRegistry)
	// The one door to every worker. Nothing in the frontend may dial a worker's
	// advertised address any more: a worker holds ONE tunnel, it lands on ONE
	// replica, and this resolves which replica that is and relays through it
	// when it is not this one. The three transports the frontend speaks to a
	// worker (gRPC to backend processes, HTTP for file staging and logs, a
	// WebSocket for live log streaming) are all pointed at it below.
	workerDialer := cluster.NewWorkerDialer(tunnels, peers)
	backendClients, err := nodes.NewTunnelClientFactory(cfg.Distributed.RegistrationToken, workerDialer.GRPCDialerFor)
	if err != nil {
		return nil, fmt.Errorf("wiring the worker backend client factory: %w", err)
	}
	// Bound to the http tag: the worker ignores the target for it and routes to
	// its own file-transfer and log server, wherever that bound.
	workerHTTPDialer := nodes.WorkerNetDialerFor(func(nodeID string) func(ctx context.Context, network, addr string) (net.Conn, error) {
		return workerDialer.DialerFor(nodeID, cluster.StreamTagHTTP)
	})

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
		backendClients,
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
			// An empty HTTPAddress is no longer a refusal. A tunnel-only worker
			// reports none and does not need one: the http stream tag ignores
			// the target and the worker routes to its own server. The host is
			// only ever the URL's host component here, and WorkerHTTPHost
			// supplies one that resolves nowhere so it cannot become a dial.
			return nodes.WorkerHTTPHost(nodeID, node.HTTPAddress), nil
		}, cfg.Distributed.RegistrationToken, workerHTTPDialer)
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
	modelCleanup := nodes.NewModelCleanupService(registry, remoteUnloader)
	router := nodes.NewSmartRouter(registry, nodes.SmartRouterOptions{
		Unloader:         remoteUnloader,
		ModelCleanup:     modelCleanup,
		FileStager:       fileStager,
		GalleriesJSON:    routerGalleriesJSON,
		AuthToken:        routerAuthToken,
		ClientFactory:    backendClients,
		DB:               authDB,
		ConflictResolver: conflictResolver,
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
		ClientFactory:     backendClients,
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
		Nats:           natsClient,
		Store:          store,
		Registry:       registry,
		Router:         router,
		Health:         healthMon,
		Reconciler:     reconciler,
		JobStore:       jobStore,
		Dispatcher:     dispatcher,
		AgentStore:     agentStore,
		AgentBridge:    agentBridge,
		DistStores:     distStores,
		FileMgr:        fileMgr,
		FileStager:     fileStager,
		ModelAdapter:   modelAdapter,
		Unloader:       remoteUnloader,
		ModelCleanup:   modelCleanup,
		Cluster:        clusterRegistry,
		Membership:     membership,
		PeerSessions:   peerSessions,
		Peers:          peers,
		Tunnels:        tunnels,
		WorkerDialer:   workerDialer,
		BackendClients: backendClients,
	}, nil
}

// advertisedPeerAddr is the host:port peers dial to reach this replica.
//
// The operator's value wins outright. Otherwise it is derived from the port
// this process serves on and the local address that routes to PostgreSQL, which
// is only a peer-reachable answer when the database is on another host;
// DiscoverAdvertisedAddr refuses rather than guessing when it is not.
func advertisedPeerAddr(cfg *config.ApplicationConfig) (string, error) {
	if configured := cfg.Distributed.AdvertiseAddr; configured != "" {
		// A configured address skips discovery, so it also skips every check
		// discovery makes. Unusable is refused; merely questionable (a
		// loopback address, correct on one host and wrong on three) is said
		// once and honoured, because refusing it would refuse single-host
		// deployments that use it correctly.
		reason, err := cluster.CheckAdvertisedAddr(configured)
		if err != nil {
			return "", err
		}
		if reason != "" {
			xlog.Warn("Configured peer address is not one another host can dial",
				"address", configured, "reason", reason, "knob", "LOCALAI_DISTRIBUTED_ADVERTISE_ADDR")
		}
		return configured, nil
	}
	if cfg.APIAddress == "" {
		return "", fmt.Errorf("no API address to derive a peer port from")
	}
	_, port, err := net.SplitHostPort(cfg.APIAddress)
	if err != nil {
		return "", fmt.Errorf("reading the peer port out of API address %q: %w", cfg.APIAddress, err)
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil {
		return "", fmt.Errorf("API address %q has a non-numeric port: %w", cfg.APIAddress, err)
	}
	return cluster.DiscoverAdvertisedAddr(cfg.Auth.DatabaseURL, portNumber)
}

func isPostgresURL(url string) bool {
	return strings.HasPrefix(url, "postgres://") || strings.HasPrefix(url, "postgresql://")
}
