package application

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	mcpTools "github.com/mudler/LocalAI/core/http/endpoints/mcp"
	"github.com/mudler/LocalAI/core/services/advisorylock"
	"github.com/mudler/LocalAI/core/services/carrier"
	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/distributed"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/monitoring"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/nodes/prefixcache"
	"github.com/mudler/LocalAI/core/services/storage"
	"github.com/mudler/LocalAI/core/services/tunnel"
	"github.com/mudler/LocalAI/internal"
	"github.com/mudler/LocalAI/pkg/distributedhdr"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/sanitize"
	"github.com/mudler/xlog"
	"gorm.io/gorm"
)

// DistributedServices holds all services initialized for distributed mode.
//
// The seams that cross a process boundary are holders. The worker-facing ones
// (control verbs, files, clients, dials, agent RPC) go through the route table,
// which picks the routes of each node (nodes.RoutesOf). The broadcaster listens
// on every bus of the replica (PostgreSQL, and NATS when configured) and
// publishes on one. Request and reply between processes go over the peer link.
// Only Shutdown reaches the routes and the buses themselves.
type DistributedServices struct {
	// Broadcaster is the fan-out seam. Code that only publishes and subscribes
	// takes this, never the connection of a carrier.
	Broadcaster messaging.Broadcaster
	// Replies is request and reply between the frontends and chat workers over
	// the peer link (the chat start, the replay of chat events, the background
	// jobs of a turn), on every deployment.
	Replies      messaging.ProcessReplies
	AgentControl mcpTools.AgentControl
	Store        storage.ObjectStore
	Registry     *nodes.NodeRegistry
	Router       *nodes.SmartRouter
	Health       *nodes.HealthMonitor
	Reconciler   *nodes.ReplicaReconciler
	DistStores   *distributed.Stores
	FileMgr      *storage.FileManager
	FileStager   nodes.FileStager
	ModelAdapter *nodes.ModelRouterAdapter
	Unloader     nodes.NodeControl
	ModelCleanup *nodes.ModelCleanupService
	// Clients builds the gRPC client for a backend of a worker on the route of
	// the worker.
	Clients nodes.BackendClientFactory

	// WorkerHTTPDial reaches a worker's own HTTP server for the admin
	// backend-logs proxy, the same way the HTTP file stager does.
	WorkerHTTPDial nodes.WorkerNetDialerFor

	// Membership keeps the row of this replica in the instances table and
	// sweeps the rows of replicas that stopped answering.
	Membership *cluster.Membership

	// Tunnels holds the sessions that workers opened to this replica. The
	// connect route is registered on every replica and refuses every worker
	// that has no tunnel credential.
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
	// tunnel: directly, or through a peer link. The tunnel route builds the
	// dial seams on it.
	WorkerDialer *tunnel.WorkerDialer

	// Settings holds the settings that decide what the whole cluster does: the
	// NATS addresses and the carrier weights.
	Settings *cluster.SettingsStore
	// NATS is the NATS server of the cluster as this replica uses it: what a
	// worker is handed at registration, the check of a new address, and the
	// bus that this replica publishes on.
	NATS *natsRuntime

	// bus is the broadcaster holder, which Broadcaster names as an interface.
	bus *carrier.Broadcaster
	// peerAddress is the address at which the other processes reach this one
	// over the peer link, "" when none was found or configured.
	peerAddress string
	// routes is the route table of the worker calls.
	routes *carrier.RouteTable
	// tunnelRoutes is the tunnel route, pinned in the table for as long as the
	// replica runs.
	tunnelRoutes *carrier.WorkerRoute

	shutdownOnce sync.Once
}

// Unready reports why this replica must not take traffic now, or nil: the NATS
// server of the cluster is configured and this replica does not reach it, so it
// does not hear the broadcasts of the others. A load balancer that reads
// /readyz drains it until NATS is back; it keeps listening on PostgreSQL.
//
// A process with no peer address is unready too: the chat start, the replay of
// chat events and the background jobs of a turn reach it only over the peer
// link.
func (ds *DistributedServices) Unready() error {
	if ds == nil {
		return nil
	}
	if ds.peerAddress == "" {
		return ErrNoPeerAddress
	}
	if ds.bus == nil {
		return nil
	}
	return ds.bus.Unready()
}

// ErrNoPeerAddress says that a frontend or chat worker publishes no address for
// the peer link, so the other processes cannot send it a chat start, a replay
// or a job request.
var ErrNoPeerAddress = errors.New("this process has no peer address, so the other frontends and chat workers cannot reach it for chat starts, chat replay and background jobs: set LOCALAI_PEER_ADDRESS to a host:port of this process that they can reach")

// Disconnect ends the tunnel that this replica holds for a node, and drops what
// the routes cache for it. The node was removed or its credential was replaced.
// It reports whether this replica held a tunnel of the node.
func (ds *DistributedServices) Disconnect(nodeID string) bool {
	held := ds.Tunnels.Disconnect(nodeID)
	if ds.routes != nil {
		ds.routes.Forget(nodeID)
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
		// The tunnels first: their claims name this replica, and a peer that
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
		if closer, ok := ds.Store.(io.Closer); ok {
			closer.Close()
		}
		// The buses last: the direct route shares the connection of the NATS bus.
		if ds.bus != nil {
			ds.bus.Close()
		}
		if ds.tunnelRoutes != nil && ds.tunnelRoutes.Close != nil {
			ds.tunnelRoutes.Close()
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
		cfg.Distributed.InstanceIDGenerated = true
	}
	xlog.Info("Distributed instance", "id", cfg.Distributed.InstanceID)

	if authDB == nil {
		return nil, fmt.Errorf("distributed mode requires auth database to be initialized first")
	}

	// The NATS address of the cluster is a cluster setting, so that every
	// replica uses the same server. A NATS server is optional: a deployment
	// with only PostgreSQL broadcasts over PostgreSQL, and its workers hold
	// the tunnel.
	settingsStore, err := cluster.NewSettingsStore(authDB)
	if err != nil {
		return nil, fmt.Errorf("initializing cluster settings: %w", err)
	}
	if err := adoptNATSURL(context.Background(), settingsStore, cfg.Distributed.NatsURL, cfg.Distributed.InstanceID); err != nil {
		return nil, err
	}

	// Whatever fails from here on must give back what was built.
	success := false
	var buses []*carrier.Bus
	defer func() {
		if success {
			return
		}
		for _, b := range buses {
			if b != nil && b.Close != nil {
				b.Close()
			}
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

	// The pieces of the tunnel. They exist on every replica, because a worker
	// may hold a tunnel on every deployment, and the routes of the tunnel are
	// registered on every replica.
	//
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
	// The handlers of the requests that other replicas send to this one over the
	// peer link: the chat start, the replay and the background jobs.
	replies := tunnel.NewReplies(cfg.Distributed.InstanceID, messaging.MaxBroadcastBytes)
	peerSessions := tunnel.NewPeerSessions(tunnel.NewRelay(tunnels, tunnel.WithReplies(replies)).Stream)

	// The tunnel route reaches every worker that holds a tunnel, over a websocket
	// or over P2P. It holds no connection until a call needs one, so a cluster
	// with no such worker pays nothing for it. It is pinned in the route table
	// for as long as the replica runs.
	tunnelRoutes, err := carrier.NewTunnelRoutes(carrier.TunnelOptions{
		Dialer:         tunnel.NewWorkerDialer(tunnels, peerPool),
		AgentSelector:  nodes.NewAgentSelector(registry, clusterReg, cfg.Distributed.InstanceID),
		Registry:       registry,
		InstallTimeout: cfg.Distributed.BackendInstallTimeoutOrDefault(),
		UpgradeTimeout: cfg.Distributed.BackendUpgradeTimeoutOrDefault(),
		Token:          cfg.Distributed.RegistrationToken,
		S3Staging:      cfg.Distributed.StorageURL != "",
		FileManager:    fileMgr,
		ExpectedSHA256: managedFileSHA256(cfg),
	})
	if err != nil {
		return nil, fmt.Errorf("building the routes of the tunnel: %w", err)
	}
	defer func() {
		if !success && tunnelRoutes.Close != nil {
			tunnelRoutes.Close()
		}
	}()

	// The route table picks the routes of each node from what the worker
	// reported (nodes.RoutesOf). The direct route is pinned below when the
	// cluster has a NATS server.
	routes := carrier.NewRouteTable(func(ctx context.Context, nodeID string) ([]nodes.Route, error) {
		n, err := registry.Get(ctx, nodeID)
		if err != nil {
			return nil, err
		}
		return nodes.RoutesOf(n), nil
	})
	routes.Pin(tunnelRoutes)

	natsRT := &natsRuntime{
		cfg: cfg, settings: settingsStore, registry: registry,
		instanceID: cfg.Distributed.InstanceID, clusterReg: clusterReg, peerPool: peerPool,
	}

	// The buses of the broadcasts. Every frontend and chat worker listens on
	// PostgreSQL (LISTEN and NOTIFY), and on NATS too when the cluster has a NATS
	// server. Each broadcast goes on one of them: NATS while this replica
	// reaches it, PostgreSQL otherwise. A NATS server that is not up yet does
	// not stop the start (the replica is not ready until it reaches it); a URL
	// or a credential that cannot be used does.
	fanout, err := carrier.NewPgbusFanout(context.Background(), carrier.PgbusOptions{DB: authDB, DSN: cfg.Auth.DatabaseURL})
	if err != nil {
		return nil, fmt.Errorf("opening the PostgreSQL bus (LISTEN and NOTIFY needs a direct connection to the database, or a pooler in session mode): %w", err)
	}
	floor := fanout.Bus()
	buses = append(buses, floor)
	natsBus, err := natsRT.openBusNATS(context.Background())
	if err != nil {
		return nil, fmt.Errorf("opening the NATS bus: %w", err)
	}
	buses = append(buses, natsBus)

	// With a NATS server the direct route reaches the workers that hold NATS. It
	// shares the connection of the NATS bus.
	directRoutes, err := natsRT.directRoutes(fileMgr)
	if err != nil {
		return nil, fmt.Errorf("building the direct route over NATS: %w", err)
	}
	routes.Pin(directRoutes)
	broadcaster, err := carrier.NewBroadcaster(carrier.BroadcasterOptions{Floor: floor, Primary: natsBus})
	if err != nil {
		return nil, err
	}
	peerReplies, err := natsRT.peerReplies(replies)
	if err != nil {
		return nil, err
	}

	clientFactory := carrier.NewClients(routes)
	fileStager := carrier.NewFiles(routes)
	remoteUnloader := carrier.NewCommands(routes)
	workerHTTPDial := carrier.NewRoutedWorkerDialer(routes)
	agentControl := carrier.NewAgents(routes)
	if cfg.Distributed.StorageURL != "" {
		xlog.Info("File stager initialized (S3)", "nats", directRoutes != nil)
	} else {
		xlog.Info("File stager initialized (HTTP direct transfer)", "nats", directRoutes != nil)
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
	var finalizeLoad func(context.Context, string, *pb.ModelOptions) error
	if configLoader != nil {
		// The key of a remote (and an inline proxy api_key) is put into the
		// options only when a load is sent to a worker; the registry never
		// stores it.
		finalizeLoad = backend.LoadOptionsFinalizer(configLoader)
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
		// Object storage means a worker reads the model files from it, so staging
		// sends no file bytes over the route to the worker.
		StagingThroughStore: cfg.Distributed.StorageURL != "",
		// routing.carrier_weights, read from the shared cluster settings: every
		// frontend ranks alike and a change needs no restart.
		CarrierWeights: settingsStore.CarrierWeights,
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
		ModelLoadWait:        cfg.Distributed.ModelLoadWait,
		LoadOptionsFinalizer: finalizeLoad,
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

	// The monitor judges each node by the routes it holds: the tunnel rows for
	// a node that holds a tunnel, the report of the worker for NATS.
	healthMon.UsePresence(clusterReg, cluster.DefaultReconnectGrace)
	broadcaster.OnReconnect(func() {
		go healthMon.CheckNow(context.Background())
	})

	success = true
	return &DistributedServices{
		Membership:   membership,
		Settings:     settingsStore,
		NATS:         natsRT,
		Tunnels:      tunnels,
		Instances:    clusterReg,
		PeerSessions: peerSessions,
		PeerPool:     peerPool,
		WorkerDialer: tunnel.NewWorkerDialer(tunnels, peerPool),
		Broadcaster:  broadcaster,
		Replies:      peerReplies,
		AgentControl: agentControl,
		Store:        store,
		Registry:     registry,
		Router:       router,
		Health:       healthMon,
		Reconciler:   reconciler,
		DistStores:   distStores,
		FileMgr:      fileMgr,
		FileStager:   fileStager,
		ModelAdapter: modelAdapter,
		Unloader:     remoteUnloader,
		ModelCleanup: modelCleanup,
		Clients:      clientFactory,

		WorkerHTTPDial: workerHTTPDial,

		bus:          broadcaster,
		peerAddress:  advertised,
		routes:       routes,
		tunnelRoutes: tunnelRoutes,
	}, nil
}

// adoptNATSURL makes the NATS address of this replica a setting of the cluster.
//
// The NATS address of the cluster is the setting that is stored without a
// credential. The flag of this replica is copied there when none is stored. A
// flag that names another server than the stored address stops the start: the
// stored address is what every replica uses, so a replica that was given another
// one by mistake, or an operator who moved the broker and edited only the flag,
// would otherwise run on the old server and see a warning in a log.
func adoptNATSURL(ctx context.Context, settings *cluster.SettingsStore, flagURL, instanceID string) error {
	// A flag that is not an address is not stored. The connection that is opened
	// later says what is wrong with it.
	flagPublic := ""
	if flagURL != "" {
		flagPublic = cluster.PublicNATSURL(flagURL)
		if cluster.CheckNATSURL(flagPublic) != nil {
			flagPublic = ""
		}
	}
	stored, _, err := settings.Get(ctx, cluster.SettingNATSURL)
	if err != nil {
		return err
	}
	if flagPublic != "" && stored != "" && cluster.PublicNATSURL(stored) != flagPublic {
		return fmt.Errorf(
			"the NATS address of this replica (--nats-url, %s) differs from the one stored for the cluster (%s). "+
				"Every replica uses the stored address. To move the cluster to the new address, store it with PUT /api/cluster/settings "+
				"({\"nats_url\": ...}) and restart; otherwise set --nats-url to the stored address or remove the flag",
			flagPublic, cluster.PublicNATSURL(stored))
	}
	if stored == "" && flagPublic != "" {
		made, err := settings.SetIfAbsent(ctx, cluster.SettingNATSURL, flagPublic, instanceID)
		if err != nil {
			return err
		}
		if made {
			xlog.Info("The NATS URL of this replica is now a setting of the cluster", "url", flagPublic)
		}
	}
	if flagURL != "" && flagPublic != flagURL {
		xlog.Warn("The NATS URL of this replica carries a credential or a query string. This replica keeps it for its own connection; the cluster stores the address without it",
			"url", cluster.PublicNATSURL(flagURL))
	}
	return nil
}

// startMembership creates the cluster tables, then records this replica in the
// instances table and keeps its row fresh until the Membership is stopped.
// Every replica does this: the peer link, the tunnel claims and the queue of the
// chat start read which replicas are alive.
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
//
// A replica that serves the API over TLS itself (--tls-cert or
// --tls-self-signed) dials its peers over wss even without --peer-tls. The
// peer address is the API listener, and the replicas of one cluster run with
// the same settings, so a plain ws dial would only reach a TLS listener and
// fail.
func peerPoolOptions(cfg *config.ApplicationConfig, advertised string) ([]tunnel.PeerPoolOption, error) {
	d := cfg.Distributed
	servesTLS := cfg.TLSFingerprint != ""
	if !d.PeerTLS && d.PeerTLSCA == "" && !servesTLS {
		if addr := advertised; clearTextOnNetwork(addr) {
			xlog.Warn("The peer link is clear text and this replica publishes an address that is not on this host, so the credential of this replica and the requests that other replicas relay cross the network unencrypted. "+
				"Serve the API over TLS (LOCALAI_TLS_CERT) or put the replicas behind TLS and set LOCALAI_PEER_TLS (and LOCALAI_PEER_TLS_CA for a private CA)", "address", addr)
		}
		return nil, nil
	}
	if servesTLS && !d.PeerTLS && d.PeerTLSCA == "" {
		xlog.Info("This replica serves the API over TLS, so the peer link dials the other replicas over wss with the system roots. " +
			"Set LOCALAI_PEER_TLS_CA when their certificates come from a private CA or are self-signed")
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
// A replica that has no usable address is not refused at start. It publishes
// none, and it is not ready (DistributedServices.Unready): the chat start, the
// replay and the jobs reach a process only over the peer link. The log line says
// how to fix it.
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
