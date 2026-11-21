package worker

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"syscall"
	"time"

	cliContext "github.com/mudler/LocalAI/core/cli/context"
	"github.com/mudler/LocalAI/core/cli/workerregistry"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/workerctl"
	grpc "github.com/mudler/LocalAI/pkg/grpc"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/signals"
	"github.com/mudler/LocalAI/pkg/splitenv"
	"github.com/mudler/LocalAI/pkg/system"
	"github.com/mudler/xlog"
)

// Run starts the distributed backend worker: registers with the frontend,
// holds the routes its operator gave it (NATS, the tunnel, or both), and blocks
// on signals.
func Run(ctx *cliContext.Context, cfg *Config) error {
	xlog.Info("Starting worker", "advertise", cfg.advertiseAddr(), "basePort", cfg.effectiveBasePort())
	if err := cfg.validateFrontend(); err != nil {
		return err
	}

	// Fail fast (before prefetch/registration/NATS) when enforcement is on but no
	// registration token is set: the worker's HTTP file-transfer server fails
	// open on an empty token (see nodes.checkBearerToken), so refuse to start
	// rather than register and then die mid-boot.
	if cfg.RegistrationAuthRequired() && cfg.RegistrationToken == "" {
		return fmt.Errorf("registration auth is required (LOCALAI_REGISTRATION_REQUIRE_AUTH or LOCALAI_DISTRIBUTED_REQUIRE_AUTH) but LOCALAI_REGISTRATION_TOKEN is empty — refusing to start an unauthenticated file-transfer server")
	}

	systemState, err := cfg.systemState()
	if err != nil {
		return fmt.Errorf("getting system state: %w", err)
	}

	ml := model.NewModelLoader(systemState)
	ml.SetBackendLoggingEnabled(true)

	// Register already-installed backends
	if err := gallery.RegisterBackends(systemState, ml); err != nil {
		return fmt.Errorf("registering installed backends: %w", err)
	}

	// Parse galleries config
	var galleries []config.Gallery
	if err := json.Unmarshal([]byte(cfg.BackendGalleries), &galleries); err != nil {
		xlog.Warn("Failed to parse backend galleries", "error", err)
	}

	// Prefetch gallery models over the worker's outbound internet before we
	// start accepting backend.install events. Non-fatal on every failure path:
	// if the gallery is unreachable, an ID is unknown, or LOCALAI_GALLERIES is
	// malformed, the worker still starts and the master can push files on
	// demand (existing fallback behaviour). Placed BEFORE registration so a
	// large download doesn't delay heartbeat — registration happens after.
	// Actually: keep it before registration so a worker that's still warming
	// the cache isn't yet announced as ready. The fast no-op path on a hot
	// PVC keeps restarts cheap.
	prefetchModels(context.Background(), cfg, systemState, ml, galleries, nil)

	// A worker with an invite joins over P2P: it redeems the invite (once),
	// and every call to the frontend goes over the P2P layer from here on.
	if cfg.Join != "" {
		// A signal during the redeem or the wait for a hole punch ends it,
		// so the worker does not have to wait out the join timeout to stop.
		joinCtx, stopJoin := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		join, err := joinP2P(joinCtx, cfg)
		stopJoin()
		if err != nil {
			return err
		}
		defer func() { _ = join.Close() }()
		cfg.join = join
		cfg.RegisterTo = join.FrontendURL()
	}

	// The routes this worker holds: the choice of its operator, read from its
	// flags. The frontend says nothing about them.
	routes, err := cfg.routes()
	if err != nil {
		return err
	}
	holdsNATS := slices.Contains(routes, RouteNATS)

	// Self-registration with frontend (with retry)
	regClient := &workerregistry.RegistrationClient{
		FrontendURL:       cfg.RegisterTo,
		RegistrationToken: cfg.RegistrationToken,
	}
	if cfg.join != nil {
		// Longer than over TCP: the first call may wait for a hole punch.
		regClient.HTTPClient = cfg.join.HTTPClient(p2pCallTimeout)
	}

	// Context cancelled on shutdown — used by registration waits, heartbeat, and
	// other background goroutines.
	shutdownCtx, shutdownCancel := context.WithCancel(context.Background())
	defer shutdownCancel()

	// Backends of a worker on NATS listen on every interface, and the frontends
	// dial the address it registered. Such a worker can be dialled whether or not
	// the operator gave it an address. A worker that holds only a tunnel and has
	// no address says that nothing can dial it, and the frontend drops its
	// address.
	baseBody := cfg.registrationBody()
	baseBody["routable"] = cfg.hasRoutableAddress() || holdsNATS
	registrationBody := func() map[string]any {
		b := make(map[string]any, len(baseBody)+1)
		maps.Copy(b, baseBody)
		return b
	}
	natsLocal := NATSLocal{
		URL: cfg.NatsURL, JWT: cfg.NatsJWT, Seed: cfg.NatsUserSeed, Required: cfg.NatsAuthRequired(),
		TLS: messaging.TLSFiles{CA: cfg.NatsTLSCA, Cert: cfg.NatsTLSCert, Key: cfg.NatsTLSKey},
	}

	// Register once for the identity of the node, with no credential. Each route
	// then gets its own credential from a registration that asks for that route
	// only (see the route holder below), so that a route that waits, for an
	// approval or for a frontend, holds back no other route, and so that the
	// refresh of the NATS credential never mints a new tunnel token.
	idMgr := workerregistry.NewCredentialManager(registerFor(regClient, registrationBody), false)
	res, regErr := idMgr.Acquire(shutdownCtx)
	if regErr != nil {
		return fmt.Errorf("failed to register with frontend: %w", regErr)
	}
	nodeID := res.ID
	xlog.Info("Registered with frontend", "nodeID", nodeID, "frontend", cfg.RegisterTo, "routes", routes)
	heartbeatInterval, err := time.ParseDuration(cfg.HeartbeatInterval)
	if err != nil && cfg.HeartbeatInterval != "" {
		xlog.Warn("invalid heartbeat interval, using default 10s", "input", cfg.HeartbeatInterval, "error", err)
	}
	heartbeatInterval = cmp.Or(heartbeatInterval, 10*time.Second)

	// Start HTTP file transfer server. (Empty-token enforcement is handled at
	// the top of Run so the worker fails before registering.)
	bindHost := cfg.bindHost(holdsNATS)
	httpAddr := cfg.resolveHTTPAddr(bindHost)
	stagingDir := filepath.Join(cfg.ModelsPath, "..", "staging")
	cacheDir := filepath.Join(cfg.ModelsPath, "..", "cache")
	dataDir := filepath.Join(cfg.ModelsPath, "..", "data")
	ephemeralRoots := []string{
		filepath.Join(stagingDir, "ephemeral"),
		filepath.Join(cacheDir, "ephemeral"),
	}
	byteLimit, minFreeBytes, err := effectiveEphemeralCapacity(
		ephemeralRoots,
		cfg.EphemeralStagingByteLimit,
		cfg.EphemeralStagingMinFreeBytes,
	)
	if err != nil {
		return fmt.Errorf("resolving ephemeral staging capacity: %w", err)
	}
	ephemeralCapacity, err := NewEphemeralCapacityGuard(ephemeralRoots, byteLimit, minFreeBytes)
	if err != nil {
		return fmt.Errorf("initializing ephemeral staging capacity: %w", err)
	}
	xlog.Info("Ephemeral staging capacity configured", "roots", ephemeralRoots, "byteLimit", byteLimit, "minFreeBytes", minFreeBytes)
	StartEphemeralRootsCleanup(shutdownCtx, ephemeralRoots, ephemeralCapacity, 0, 0)
	// The readiness gate is created here but only armed once the routes are held
	// and the backend supervisor exists, below, because the gate probes both.
	// Until then /readyz reports ready, which is correct: reaching this line
	// means the worker has already registered with the frontend, so it is
	// mid-startup rather than broken.
	readiness := &nodes.WorkerReadiness{}
	// The control verbs are served over HTTP on this server when the worker holds
	// a tunnel, because the tunnel reaches it with the http stream tag. The
	// control plane is a slot on the server, empty until the tunnel is attached.
	// While it is empty the path answers as a path that does not exist. A worker
	// that has no frontend URL a tunnel can use mounts no plane at all.
	plane := NewBackendPlane(cfg, nodeID, httpAddr, natsLocal)
	if slices.Contains(routes, RouteTunnel) && !plane.CanTunnel() {
		_, terr := tunnelEndpoint(cfg.RegisterTo, nodeID)
		return terr
	}
	controlMux := plane.Control()
	httpServer, err := nodes.StartFileTransferServerWithControl(httpAddr, stagingDir, cfg.ModelsPath, dataDir, cfg.RegistrationToken, config.DefaultMaxUploadSize, readiness, ephemeralCapacity, controlMux, ml.BackendLogs())
	if err != nil {
		return fmt.Errorf("starting HTTP file transfer server: %w", err)
	}

	// Process supervisor — manages multiple backend gRPC processes on different ports
	basePort := cfg.effectiveBasePort()
	// Buffered so NATS stop handler can send without blocking
	sigCh := make(chan os.Signal, 1)
	// NotifyTermination also covers a stop request from the Windows service
	// manager, which is not a signal.
	signals.NotifyTermination(sigCh)

	// Set the registration token once before any backends are started
	if cfg.RegistrationToken != "" {
		if err := os.Setenv(grpc.AuthTokenEnvVar, cfg.RegistrationToken); err != nil {
			nodes.ShutdownFileTransferServer(httpServer)
			return fmt.Errorf("setting backend authentication token: %w", err)
		}
	}

	supervisor := &backendSupervisor{
		cfg:          cfg,
		ml:           ml,
		systemState:  systemState,
		galleries:    galleries,
		nodeID:       nodeID,
		sigCh:        sigCh,
		processes:    make(map[string]*backendProcess),
		portAffinity: make(map[string]portOwnership),
		nextPort:     basePort,
		minPort:      basePort,
		maxPort:      cfg.effectiveMaxPort(basePort),
		bindHost:     bindHost,
	}

	// registerVerbs claims every verb of the worker on a control server: the
	// NATS one, or the HTTP one behind the tunnel. It is called for each route
	// the worker attaches.
	var registerVerbs func(control controlServer) error

	// The routes of the worker. Each is attached on its own and attached again
	// when it fails, and the heartbeat reports which of them the worker holds.
	staticNATS := cfg.NatsJWT != "" || cfg.NatsUserSeed != ""
	held := NewRouteHolder(RouteHolderOptions{
		Routes:    routes,
		Attachers: plane.Attachers(),
		Credentials: func(r Route) *workerregistry.CredentialManager {
			return workerregistry.NewCredentialManagerFor(
				registerFor(regClient, registrationBody, r), r == RouteNATS && cfg.NatsAuthRequired() && !staticNATS, string(r))
		},
		Heartbeat: func(ctx context.Context, body map[string]any) error {
			return regClient.SendHeartbeat(ctx, nodeID, body)
		},
		Body:     cfg.heartbeatBody,
		Interval: heartbeatInterval,
	})
	defer held.Close()

	// Arm the readiness gate now that the worker can actually receive work. A
	// worker that is up but cut off from every route reports 503 instead of a
	// meaningless 200 (#10987).
	//
	// Readiness also covers the data path: a worker whose link is fine but whose
	// backend processes have died is up and useless, and the scheduler cannot
	// tell the difference from the link alone.
	readiness.Set(nodes.CompositeReadiness(
		held.Ready,
		nodes.BackendDataPathReadiness(supervisor, func(addr string) error {
			conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
			if err != nil {
				return err
			}
			return conn.Close()
		}),
	))

	// A previous worker that was killed left its backends running. Kill them
	// now, before this worker hands out ports or reports an incarnation.
	supervisor.ledger = newProcessLedger(filepath.Join(dataDir, "worker-processes.json"))
	if n := supervisor.ledger.sweepStale(); n > 0 {
		xlog.Warn("Killed backend process groups left behind by a previous worker", "count", n)
	}

	// The watchdog stops load operations the controller no longer renews.
	go supervisor.runOperationWatchdog(shutdownCtx)

	// One prefetcher for every control server, so its requests run one at a
	// time whichever route delivers them.
	prefetcher := newModelPrefetcher(cfg)
	registerVerbs = func(control controlServer) error {
		if err := supervisor.registerLifecycleVerbs(control); err != nil {
			return fmt.Errorf("serving the worker lifecycle verbs: %w", err)
		}
		if err := prefetcher.register(control); err != nil {
			return fmt.Errorf("serving model.prefetch: %w", err)
		}
		// Serve the file staging verbs only when S3 is configured
		if cfg.StorageURL != "" {
			if err := cfg.registerFileStagingVerbs(control, ephemeralCapacity); err != nil {
				return fmt.Errorf("serving the file staging verbs: %w", err)
			}
		}
		return nil
	}
	plane.setServe(registerVerbs)

	// Hold every route from here on. A route that cannot be attached now is
	// tried again in the background, and the worker serves on the others.
	go held.Run(shutdownCtx)

	// Model-split workers attached to this node: the backends it starts read
	// their addresses from the environment, as on a standalone server.
	splitDone := make(chan struct{})
	split := newSplitNode(splitConfig{
		Sync: func(ctx context.Context, rep workerctl.SplitReport) (*workerctl.SplitAssignment, error) {
			return regClient.SplitSync(ctx, nodeID, rep)
		},
		Interval: heartbeatInterval,
		Join:     cfg.join,
		NewHost:  cfg.splitHost,
		OnLlama: func(addrs []string) {
			if err := splitenv.SetLlama(addrs); err != nil {
				xlog.Error("Could not set the llama.cpp RPC servers", "error", err)
			}
		},
		OnMLX: func(hostfile []string) {
			if err := splitenv.SetMLX(dataDir, hostfile); err != nil {
				xlog.Error("Could not write the MLX hostfile", "error", err)
			}
		},
	})
	go func() {
		defer close(splitDone)
		split.run(shutdownCtx)
	}()

	xlog.Info("Worker ready, waiting for backend.install events")
	<-sigCh

	xlog.Info("Shutting down worker")
	shutdownCancel() // stop heartbeat loop immediately
	<-splitDone
	regClient.GracefulDeregister(nodeID)
	supervisor.stopAllBackends(false)
	nodes.ShutdownFileTransferServer(httpServer)
	return nil
}

func (cfg *Config) systemState() (*system.SystemState, error) {
	return system.GetSystemState(
		system.WithModelPath(cfg.ModelsPath),
		system.WithBackendPath(cfg.BackendsPath),
		system.WithBackendSystemPath(cfg.BackendsSystemPath),
		system.WithRequireBackendIntegrity(cfg.RequireBackendIntegrity),
		system.WithPreferDevelopmentBackends(cfg.PreferDevelopmentBackends),
		system.WithDisableBackendInstall(cfg.DisableBackendInstall),
	)
}
