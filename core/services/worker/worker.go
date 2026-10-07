package worker

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	cliContext "github.com/mudler/LocalAI/core/cli/context"
	"github.com/mudler/LocalAI/core/cli/workerregistry"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/nodes"
	grpc "github.com/mudler/LocalAI/pkg/grpc"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/sanitize"
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

	registrationBody := cfg.registrationBody()
	natsTLS := messaging.TLSFiles{CA: cfg.NatsTLSCA, Cert: cfg.NatsTLSCert, Key: cfg.NatsTLSKey}

	// Register, and learn from the answer which carrier the cluster runs on.
	//
	// Static NATS credentials from the environment cannot be minted again, so
	// the worker registers once and uses them as they are. Otherwise the
	// credential manager registers, waits through admin approval, and for NATS
	// it refreshes the credentials before the minted JWT expires, so that the
	// connection survives the expiry through a reconnect that the client does by
	// itself. For the tunnel it holds the token of the node.
	var (
		res     *workerregistry.RegisterResponse
		credMgr = workerregistry.NewCredentialManager(
			func(ctx context.Context) (*workerregistry.RegisterResponse, error) {
				return regClient.RegisterFull(ctx, registrationBody)
			},
			cfg.NatsAuthRequired(),
		)
		staticNATS = cfg.NatsJWT != "" || cfg.NatsUserSeed != ""
		regErr     error
	)
	if staticNATS {
		res, regErr = regClient.RegisterFullWithRetry(shutdownCtx, registrationBody, 10)
	} else {
		res, regErr = credMgr.Acquire(shutdownCtx)
	}
	if regErr != nil {
		return fmt.Errorf("failed to register with frontend: %w", regErr)
	}
	nodeID := res.ID
	onTunnel, err := carrierOf(res.Carrier, cfg)
	if err != nil {
		return err
	}
	// The token comes from the credential manager, which is updated by every
	// registration. With static NATS credentials there is no manager, and a
	// frontend that answers with the tunnel gives the token once.
	tunnelToken := credMgr.TunnelToken
	if staticNATS {
		tunnelToken = func() string { return res.TunnelToken }
	}

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
	// The readiness gate is created here but only armed once NATS is up and the
	// backend supervisor exists, below, because the gate probes both.
	// Until then /readyz reports ready, which is correct: reaching this line
	// means the worker has already registered with the frontend, so it is
	// mid-startup rather than broken.
	readiness := &nodes.WorkerReadiness{}
	httpServer, err := nodes.StartFileTransferServerWithCapacity(httpAddr, stagingDir, cfg.ModelsPath, dataDir, cfg.RegistrationToken, config.DefaultMaxUploadSize, readiness, ephemeralCapacity, ml.BackendLogs())
	if err != nil {
		return fmt.Errorf("starting HTTP file transfer server: %w", err)
	}

	// Attach to the carrier that the frontend named. The link is what the
	// heartbeat and the readiness probe look at: a worker whose link is down
	// still sends its HTTP heartbeat, and the registry would show a node that
	// looks healthy and cannot be reached.
	var (
		link        carrierLink
		natsClient  *messaging.Client
		linkProbe   func() error
		linkDownMsg string
	)
	if onTunnel {
		t, terr := StartTunnel(shutdownCtx, TunnelConfig{
			FrontendURL: cfg.RegisterTo,
			NodeID:      nodeID,
			Token:       tunnelToken,
			// Built by tunnelServices and not inline, so that the routing table,
			// which is the security boundary of the tunnel, can be reached from a
			// spec without starting a worker.
			Services: tunnelServices(cfg, httpAddr),
		})
		if terr != nil {
			nodes.ShutdownFileTransferServer(httpServer)
			return fmt.Errorf("starting the worker tunnel: %w", terr)
		}
		defer func() {
			if err := t.Close(); err != nil {
				xlog.Warn("Closing the worker tunnel failed", "error", err)
			}
		}()
		link, linkProbe, linkDownMsg = t, nodes.TunnelReadiness(t), "tunnel disconnected"
	} else {
		xlog.Info("Connecting to NATS", "url", sanitize.URL(cfg.NatsURL))
		var cerr error
		natsClient, cerr = connectNatsFor(shutdownCtx, shutdownCancel, cfg, credMgr, staticNATS, natsTLS)
		if cerr != nil {
			nodes.ShutdownFileTransferServer(httpServer)
			return fmt.Errorf("connecting to NATS: %w", cerr)
		}
		defer natsClient.Close()
		link, linkProbe, linkDownMsg = natsLink{natsClient}, nodes.NATSReadiness(natsClient), "NATS disconnected"
	}

	// Start heartbeat goroutine (after the carrier is attached so the check works)
	go func() {
		ticker := time.NewTicker(heartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-shutdownCtx.Done():
				return
			case <-ticker.C:
				if !link.Connected() {
					xlog.Warn("Skipping heartbeat: " + linkDownMsg)
					continue
				}
				body := cfg.heartbeatBody()
				if err := regClient.Heartbeat(shutdownCtx, nodeID, body); err != nil {
					xlog.Warn("Heartbeat failed", "error", err)
				}
			}
		}
	}()

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

	// Arm the readiness gate now that the worker can actually receive work.
	// The carrier is already attached at this point, so a worker that is up but
	// cut off from it reports 503 instead of a meaningless 200 (#10987).
	//
	// Readiness also covers the data path: a worker whose link is fine but whose
	// backend processes have died is up and useless, and the scheduler cannot
	// tell the difference from the link alone.
	readiness.Set(nodes.CompositeReadiness(
		linkProbe,
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

	if natsClient != nil {
		control := newNATSControlServer(natsClient, nodeID)
		if err := supervisor.registerLifecycleVerbs(control); err != nil {
			nodes.ShutdownFileTransferServer(httpServer)
			return fmt.Errorf("subscribing to worker lifecycle events: %w", err)
		}

		// Serve the file staging verbs only when S3 is configured
		if cfg.StorageURL != "" {
			if err := cfg.registerFileStagingVerbs(control, ephemeralCapacity); err != nil {
				nodes.ShutdownFileTransferServer(httpServer)
				return fmt.Errorf("subscribing to file staging subjects: %w", err)
			}
		}
	} else {
		// The tunnel carries streams to the backends and to the file-transfer
		// server of this worker. The lifecycle verbs have no route on it yet.
		xlog.Warn("This worker is attached to the tunnel and serves no lifecycle verbs on it: backends are not installed or stopped by the frontend")
	}

	xlog.Info("Worker ready, waiting for backend.install events")
	// Exit on an OS signal or on an internal fatal condition (e.g. NATS
	// credentials became unrenewable), so the worker restarts and re-acquires
	// rather than lingering unable to serve.
	var runErr error
	select {
	case <-sigCh:
	case <-shutdownCtx.Done():
		runErr = fmt.Errorf("worker shutting down: NATS credentials unavailable")
		xlog.Error("Internal shutdown requested", "error", runErr)
	}

	xlog.Info("Shutting down worker")
	shutdownCancel() // stop heartbeat loop immediately
	regClient.GracefulDeregister(nodeID)
	supervisor.stopAllBackends(false)
	nodes.ShutdownFileTransferServer(httpServer)
	return runErr
}

// carrierLink is the connection of a worker to the carrier it is attached to.
type carrierLink interface {
	Connected() bool
}

// natsLink adapts a NATS client to carrierLink.
type natsLink struct{ c *messaging.Client }

func (l natsLink) Connected() bool { return l.c.IsConnected() }

// carrierOf maps the carrier that the frontend named to the one this worker
// attaches to, and reports whether it is the tunnel.
//
// A frontend that predates carriers names none, and the cluster runs on NATS.
// Then the worker needs a NATS URL, and without one it stops with a message that
// says so. The check is here and not in the flag, because the flag cannot know
// the carrier: for a cluster on the tunnel the URL is not needed.
func carrierOf(named string, cfg *Config) (onTunnel bool, err error) {
	switch named {
	case "tunnel":
		if cfg.NatsURL != "" {
			xlog.Info("The cluster runs on the tunnel, so the NATS URL of this worker is not used")
		}
		return true, nil
	case "", "nats":
		if cfg.NatsURL == "" {
			if named == "" {
				return false, errors.New("the frontend does not name a carrier, so the cluster runs on NATS, and this worker has no NATS URL: set LOCALAI_NATS_URL")
			}
			return false, errors.New("the cluster runs on NATS and this worker has no NATS URL: set LOCALAI_NATS_URL")
		}
		return false, nil
	default:
		return false, fmt.Errorf("the frontend names the carrier %q, which this worker does not know: upgrade the worker", named)
	}
}
