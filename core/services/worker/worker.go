package worker

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"time"

	cliContext "github.com/mudler/LocalAI/core/cli/context"
	"github.com/mudler/LocalAI/core/cli/workerregistry"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"

	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/nodes"
	grpc "github.com/mudler/LocalAI/pkg/grpc"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	"github.com/mudler/xlog"
)

// Run starts the distributed agent worker: registers with the frontend,
// subscribes to NATS lifecycle subjects, and blocks on signals.
func Run(ctx *cliContext.Context, cfg *Config) error {
	xlog.Info("Starting worker", "advertise", cfg.advertiseAddr(), "basePort", cfg.effectiveBasePort())

	// Fail fast (before prefetch/registration/NATS) when enforcement is on but no
	// registration token is set: the worker's HTTP file-transfer server fails
	// open on an empty token (see nodes.checkBearerToken), so refuse to start
	// rather than register and then die mid-boot.
	if cfg.RegistrationAuthRequired() && cfg.RegistrationToken == "" {
		return fmt.Errorf("registration auth is required (LOCALAI_REGISTRATION_REQUIRE_AUTH or LOCALAI_DISTRIBUTED_REQUIRE_AUTH) but LOCALAI_REGISTRATION_TOKEN is empty — refusing to start an unauthenticated file-transfer server")
	}

	systemState, err := system.GetSystemState(
		system.WithModelPath(cfg.ModelsPath),
		system.WithBackendPath(cfg.BackendsPath),
		system.WithBackendSystemPath(cfg.BackendsSystemPath),
		system.WithRequireBackendIntegrity(cfg.RequireBackendIntegrity),
	)
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

	// Self-registration with frontend (with retry)
	regClient := &workerregistry.RegistrationClient{
		FrontendURL:       cfg.RegisterTo,
		RegistrationToken: cfg.RegistrationToken,
	}

	// Context cancelled on shutdown — used by registration waits, heartbeat, and
	// other background goroutines.
	shutdownCtx, shutdownCancel := context.WithCancel(context.Background())
	defer shutdownCancel()

	// The body of a registration. A worker that follows a change registers again
	// and names the carrier it wants the credential of.
	baseBody := cfg.registrationBody()
	var routable atomic.Bool
	routable.Store(cfg.hasRoutableAddress())
	registrationBody := func() map[string]any {
		b := make(map[string]any, len(baseBody)+2)
		maps.Copy(b, baseBody)
		b["routable"] = routable.Load()
		return b
	}
	natsLocal := NATSLocal{
		URL: cfg.NatsURL, JWT: cfg.NatsJWT, Seed: cfg.NatsUserSeed, Required: cfg.NatsAuthRequired(),
		TLS: messaging.TLSFiles{CA: cfg.NatsTLSCA, Cert: cfg.NatsTLSCert, Key: cfg.NatsTLSKey},
	}

	// Register, and learn from the answer which carrier the cluster runs on.
	//
	// Static NATS credentials from the environment cannot be minted again, so
	// the worker does not wait for a minted one. Otherwise the credential manager
	// registers, waits through admin approval, and for NATS it refreshes the
	// credentials before the minted JWT expires, so that the connection survives
	// the expiry through a reconnect that the client does by itself. For the
	// tunnel it holds the token of the node.
	staticNATS := cfg.NatsJWT != "" || cfg.NatsUserSeed != ""
	credMgr := workerregistry.NewCredentialManager(
		func(ctx context.Context) (*workerregistry.RegisterResponse, error) {
			return regClient.RegisterFull(ctx, registrationBody())
		},
		cfg.NatsAuthRequired() && !staticNATS,
	)
	res, regErr := credMgr.Acquire(shutdownCtx)
	if regErr != nil {
		return fmt.Errorf("failed to register with frontend: %w", regErr)
	}
	nodeID := res.ID
	onTunnel, err := carrierOf(res.Carrier, res.NatsURL, cfg)
	if err != nil {
		return err
	}
	bootCarrier := cluster.CarrierNATS
	if onTunnel {
		bootCarrier = cluster.CarrierTunnel
	} else if res.NatsClientTLS && (cfg.NatsTLSCert == "" || cfg.NatsTLSKey == "") {
		return errors.New("the NATS server asks for a client certificate, and this worker has none: set LOCALAI_NATS_TLS_CERT and LOCALAI_NATS_TLS_KEY")
	}
	// Backends of a worker that booted on NATS listen on every interface, and the
	// frontends dial the address it registered. Such a worker can be dialled
	// whether or not the operator gave it an address.
	routable.Store(cfg.hasRoutableAddress() || !onTunnel)

	xlog.Info("Registered with frontend", "nodeID", nodeID, "frontend", cfg.RegisterTo, "carrier", cmp.Or(res.Carrier, "nats"))
	heartbeatInterval, err := time.ParseDuration(cfg.HeartbeatInterval)
	if err != nil && cfg.HeartbeatInterval != "" {
		xlog.Warn("invalid heartbeat interval, using default 10s", "input", cfg.HeartbeatInterval, "error", err)
	}
	heartbeatInterval = cmp.Or(heartbeatInterval, 10*time.Second)

	// Start HTTP file transfer server. (Empty-token enforcement is handled at
	// the top of Run so the worker fails before registering.)
	bindHost := cfg.bindHost(onTunnel)
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
	// The readiness gate is created here but only armed once a carrier is up and
	// the backend supervisor exists, below, because the gate probes both.
	// Until then /readyz reports ready, which is correct: reaching this line
	// means the worker has already registered with the frontend, so it is
	// mid-startup rather than broken.
	readiness := &nodes.WorkerReadiness{}
	// The control verbs are served over HTTP on this server when the worker holds
	// a tunnel, because the tunnel reaches it with the http stream tag. The
	// control plane is a slot on the server, empty until the worker attaches to a
	// tunnel and empty again when it releases it. While it is empty the path
	// answers as a path that does not exist, which is what a worker on NATS has
	// always answered. A worker that has no frontend URL a tunnel can use mounts
	// no plane at all.
	follower := newTunnelFollower(tunnelFollowerOptions{
		Config:   cfg,
		NodeID:   nodeID,
		HTTPAddr: httpAddr,
	})
	if onTunnel && !follower.CanAttach() {
		_, terr := tunnelEndpoint(cfg.RegisterTo, nodeID)
		return terr
	}
	var controlMux http.Handler
	if follower.CanAttach() {
		controlSlot := &nodes.ControlSlot{}
		follower.slot = controlSlot
		controlMux = controlSlot
	}
	httpServer, err := nodes.StartFileTransferServerWithControl(httpAddr, stagingDir, cfg.ModelsPath, dataDir, cfg.RegistrationToken, config.DefaultMaxUploadSize, readiness, ephemeralCapacity, controlMux, ml.BackendLogs())
	if err != nil {
		return fmt.Errorf("starting HTTP file transfer server: %w", err)
	}

	// Process supervisor — manages multiple backend gRPC processes on different ports
	basePort := cfg.effectiveBasePort()
	// Buffered so NATS stop handler can send without blocking
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

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
	// NATS one, or the HTTP one behind the tunnel. It is called for each carrier
	// the worker attaches to, at start and when the cluster changes.
	var registerVerbs func(control controlServer) error

	// The carriers of the worker. It follows the one the frontend names, so the
	// attachments are made by the same code at start and when the cluster changes.
	carriers := NewFollower(FollowerOptions{
		Attachers: map[cluster.Carrier]Attacher{
			cluster.CarrierNATS:   natsAttacher(natsLocal, nodeID, func(cs controlServer) error { return registerVerbs(cs) }),
			cluster.CarrierTunnel: tunnelAttacher(follower),
		},
		Credentials: func(c cluster.Carrier) *workerregistry.CredentialManager {
			return workerregistry.NewCredentialManagerFor(
				registerFor(regClient, registrationBody, c), c == cluster.CarrierNATS && cfg.NatsAuthRequired() && !staticNATS, string(c))
		},
		Heartbeat: func(ctx context.Context, body map[string]any) (*Beat, error) {
			return regClient.HeartbeatFull(ctx, nodeID, body)
		},
		Body: cfg.heartbeatBody,
		Cannot: map[cluster.Carrier]func(CarrierView) string{
			cluster.CarrierNATS: NATSReasons(routable.Load(), natsLocal),
			cluster.CarrierTunnel: func(CarrierView) string {
				if !follower.CanAttach() {
					return "this worker has no frontend URL that a tunnel can use"
				}
				return ""
			},
		},
		Interval: heartbeatInterval,
		MaxDelay: cfg.followMaxDelay(),
	})
	defer carriers.Close()

	// Arm the readiness gate now that the worker can actually receive work.
	// The carrier is attached before the worker serves, so a worker that is up
	// but cut off from every carrier reports 503 instead of a meaningless 200
	// (#10987).
	//
	// Readiness also covers the data path: a worker whose link is fine but whose
	// backend processes have died is up and useless, and the scheduler cannot
	// tell the difference from the link alone.
	readiness.Set(nodes.CompositeReadiness(
		carriers.Ready,
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

	registerVerbs = func(control controlServer) error {
		if err := supervisor.registerLifecycleVerbs(control); err != nil {
			return fmt.Errorf("serving the worker lifecycle verbs: %w", err)
		}
		// Serve the file staging verbs only when S3 is configured
		if cfg.StorageURL != "" {
			if err := cfg.registerFileStagingVerbs(control, ephemeralCapacity); err != nil {
				return fmt.Errorf("serving the file staging verbs: %w", err)
			}
		}
		return nil
	}
	follower.register = registerVerbs

	// Attach to the carrier that the frontend named, with the credential that the
	// registration returned, and follow it from here on.
	if _, err := carriers.Attach(shutdownCtx, bootCarrier, credMgr, res); err != nil {
		nodes.ShutdownFileTransferServer(httpServer)
		return fmt.Errorf("attaching to the %s carrier: %w", bootCarrier, err)
	}
	go carriers.Run(shutdownCtx)

	xlog.Info("Worker ready, waiting for backend.install events")
	<-sigCh

	xlog.Info("Shutting down worker")
	shutdownCancel() // stop heartbeat loop immediately
	regClient.GracefulDeregister(nodeID)
	supervisor.stopAllBackends(false)
	nodes.ShutdownFileTransferServer(httpServer)
	return nil
}

// carrierOf maps the carrier that the frontend named to the one this worker
// attaches to, and reports whether it is the tunnel.
//
// A frontend that predates carriers names none, and the cluster runs on NATS.
// Then the worker needs a NATS URL, and without one it stops with a message that
// says so. The check is here and not in the flag, because the flag cannot know
// the carrier: for a cluster on the tunnel the URL is not needed.
func carrierOf(named, handedNATSURL string, cfg *Config) (onTunnel bool, err error) {
	switch named {
	case "tunnel":
		if cfg.NatsURL != "" {
			xlog.Info("The cluster runs on the tunnel; the NATS URL of this worker is kept for a change to NATS")
		}
		return true, nil
	case "", "nats":
		if cfg.NatsURL == "" && handedNATSURL == "" {
			if named == "" {
				return false, errors.New("the frontend does not name a carrier, so the cluster runs on NATS, and this worker has no NATS URL: set LOCALAI_NATS_URL")
			}
			return false, errors.New("the cluster runs on NATS, this worker has no NATS URL and the frontend handed over none: set LOCALAI_NATS_URL, or store the NATS address in the cluster settings")
		}
		return false, nil
	default:
		return false, fmt.Errorf("the frontend names the carrier %q, which this worker does not know: upgrade the worker", named)
	}
}
