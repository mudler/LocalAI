package worker

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	cliContext "github.com/mudler/LocalAI/core/cli/context"
	"github.com/mudler/LocalAI/core/cli/workerregistry"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"

	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/storage"
	"github.com/mudler/LocalAI/core/services/workerctl"
	grpc "github.com/mudler/LocalAI/pkg/grpc"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	"github.com/mudler/xlog"
)

// Run starts the distributed agent worker: registers with the frontend, opens
// its tunnel, serves its control plane on that tunnel, and blocks on signals.
func Run(ctx *cliContext.Context, cfg *Config) error {
	xlog.Info("Starting worker", "basePort", cfg.effectiveBasePort())

	// Fail fast, before prefetch and registration, on any configuration
	// that would produce a worker the cluster believes in and cannot use. See
	// validateStartup for what those are and why each is fatal rather than
	// degraded.
	if err := cfg.validateStartup(); err != nil {
		return err
	}

	systemState, err := system.GetSystemState(
		system.WithModelPath(cfg.ModelsPath),
		system.WithBackendPath(cfg.BackendsPath),
		system.WithBackendSystemPath(cfg.BackendsSystemPath),
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
	// serve backend installs. Non-fatal on every failure path:
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

	// One registration, and the tunnel credential it returns is the only
	// credential a backend worker holds. There is no second acquisition path:
	// the bus this worker used to also authenticate against is gone from its
	// startup entirely.
	//
	// This path registers exactly once and never again, so the credential it
	// holds cannot go stale by rotation from its own side.
	//
	// It CAN be superseded from outside: Register upserts by NAME, so a second
	// worker registering under this node's name rotates the row's credential,
	// and this worker then fails every tunnel dial with 401 for the life of the
	// process. It logs that once per backoff and never recovers on its own; a
	// restart fixes it only until the other worker registers again.
	//
	// Still deliberately not auto-re-registered, and now for a concrete reason
	// rather than a deferral. Register CLEARS this node's NodeModel rows, on
	// the assumption that a re-registering worker restarted with nothing
	// loaded, so re-registering on a 401 would delete a live worker's replica
	// rows on every retry, and under the name collision that produces the 401
	// the two workers would take turns doing it forever. That is a credential
	// failure causing model reclamation, which is the one outcome this whole
	// design exists to prevent. It is also why the NATS credential manager,
	// whose refresh loop re-registered on a timer, is no longer on this path:
	// it was wiping a live worker's rows every time it renewed a JWT.
	//
	// The fix belongs to whichever comes first: a re-auth path that mints a
	// tunnel credential WITHOUT the rest of registration's side effects, or a
	// worker identity that is not the operator-chosen name, which is what would
	// make a collision detectable instead of silent. Until then the 401 is
	// loud, names both causes, and the operator acts on it.
	res, err := regClient.RegisterFullWithRetry(shutdownCtx, registrationBody, 10)
	if err != nil {
		return fmt.Errorf("failed to register with frontend: %w", err)
	}
	nodeID := res.ID
	// Read through a function because StartTunnel presents the credential at
	// DIAL time, not at start time; there is one value behind it today and the
	// indirection is what keeps a future rotation from needing a new dial path.
	tunnelToken := func() string { return res.TunnelToken }

	xlog.Info("Registered with frontend", "nodeID", nodeID, "frontend", cfg.RegisterTo)
	heartbeatInterval, err := time.ParseDuration(cfg.HeartbeatInterval)
	if err != nil && cfg.HeartbeatInterval != "" {
		xlog.Warn("invalid heartbeat interval, using default 10s", "input", cfg.HeartbeatInterval, "error", err)
	}
	heartbeatInterval = cmp.Or(heartbeatInterval, 10*time.Second)

	// Start HTTP file transfer server. (Empty-token enforcement is handled at
	// the top of Run so the worker fails before registering.)
	httpAddr := cfg.resolveHTTPAddr()
	stagingDir := filepath.Join(cfg.ModelsPath, "..", "staging")
	// Derived through the same helper the listdir verb resolves `data/` keys
	// against, and not a second time here. Two independent joins that agreed
	// today would each stay self consistent if one moved, and the symptom would
	// be a verb that lists files the file server does not serve.
	dataDir := cfg.stagingDataDir()
	// The readiness gate is created here but only armed once the tunnel exists,
	// below. Until then /readyz reports ready, which is correct: reaching this
	// line means the worker has already registered with the frontend, so it is
	// mid-startup rather than broken.
	readiness := &nodes.WorkerReadiness{}

	// The supervisor is built BEFORE the HTTP server, not after, because the
	// server is what serves its control plane. Ten NATS subscriptions used to
	// be attached to it later, and could be, because the bus buffered nothing
	// the worker had not subscribed to; a control route that is not mounted
	// when the tunnel comes up is instead a 404 the frontend reads as a worker
	// that does not implement the verb.
	basePort := cfg.effectiveBasePort()
	// Buffered so the node.stop verb can signal without blocking its response.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Set the registration token once before any backends are started.
	if cfg.RegistrationToken != "" {
		if err := os.Setenv(grpc.AuthTokenEnvVar, cfg.RegistrationToken); err != nil {
			return fmt.Errorf("setting backend authentication token: %w", err)
		}
	}

	// Process supervisor — manages multiple backend gRPC processes on different ports
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
	}

	// The file-staging FileManager is built BEFORE the server, for the same
	// reason the supervisor is: the server is what serves those four verbs, and
	// a route that is not mounted when the tunnel comes up is a 404 the
	// frontend reads as a worker that does not implement the verb. It used to
	// be built after NATS connected, which is a window that no longer exists.
	//
	// A worker with no object store configured mounts NO file verbs. That is
	// the honest answer rather than a degraded one: it has nowhere to fetch
	// from or stage to, and the frontend that reaches such a deployment uses
	// the HTTP file stager, which does not call these paths at all.
	var stagingFM *storage.FileManager
	if cfg.StorageURL != "" {
		stagingFM, err = cfg.NewStagingFileManager(shutdownCtx)
		if err != nil {
			return fmt.Errorf("initializing file staging: %w", err)
		}
	}

	httpServer, err := startWorkerHTTPServer(httpAddr, stagingDir, cfg.ModelsPath, dataDir,
		cfg.RegistrationToken, readiness, supervisor, cfg, stagingFM, ml.BackendLogs())
	if err != nil {
		return fmt.Errorf("starting HTTP file transfer server: %w", err)
	}

	// Per-request input files land in stagingDir over that server and nothing
	// used to remove them, so a long-lived worker filled its own disk.
	StartEphemeralStagingCleanup(shutdownCtx, stagingDir, 0, 0)

	// The tunnel is started here, after the HTTP server it fronts is listening
	// and before any backend process exists. Both orders are deliberate: a
	// stream tagged for HTTP that arrived before the server bound would be
	// refused as unavailable, while a stream tagged for gRPC resolves its
	// backend at dial time, so nothing has to exist yet for the tunnel to be
	// useful.
	//
	// A failure to START it is fatal, unlike a failure to CONNECT: it means the
	// frontend URL or this node's identity is unusable, and a worker that
	// silently ran without its tunnel would look healthy while being
	// unreachable to everything that dials through it.
	//
	// Unconditional: LOCALAI_WORKER_TUNNEL=false is refused by validateStartup
	// before this point, so there is no configuration that reaches here without
	// one. A guard here would be a branch nothing can take, which reads as a
	// supported no-tunnel mode that does not exist.
	tunnel, terr := startTunnelAndArmReadiness(shutdownCtx, readiness, TunnelConfig{
		FrontendURL: cfg.RegisterTo,
		NodeID:      nodeID,
		Token:       tunnelToken,
		// Built by tunnelServices rather than inline, so the routing
		// table, which is this feature's security boundary, is reachable
		// from a spec without starting a worker.
		Services: tunnelServices(cfg, httpAddr),
	})
	if terr != nil {
		nodes.ShutdownFileTransferServer(httpServer)
		return fmt.Errorf("starting the worker tunnel: %w", terr)
	}
	defer func() {
		if err := tunnel.Close(); err != nil {
			xlog.Warn("Closing the worker tunnel failed", "error", err)
		}
	}()

	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()
	go heartbeatLoop(shutdownCtx, ticker.C, func(ctx context.Context) error {
		return regClient.Heartbeat(ctx, nodeID, cfg.heartbeatBody())
	})

	xlog.Info("Worker ready, serving its control plane over the tunnel")
	<-sigCh

	xlog.Info("Shutting down worker")
	shutdownCancel() // stop heartbeat loop immediately
	regClient.GracefulDeregister(nodeID)
	supervisor.stopAllBackends(false)
	nodes.ShutdownFileTransferServer(httpServer)
	return nil
}

// heartbeatLoop posts this worker's heartbeat on every tick until ctx ends.
//
// It is given no view of the tunnel, and that absence is the point rather than
// an omission. The heartbeat is the WORKER'S OWN ANSWER that its process is
// alive; whether the frontend can REACH this worker is a separate fact, which
// the frontend reads from the tunnel session it holds and ages against
// LOCALAI_WORKER_RECONNECT_GRACE. Withholding the heartbeat while the tunnel
// re-homes would report an unreachable worker as an absent one, on the one path
// that has no grace at all: the health monitor marks a silent node offline or
// unhealthy and its pending backend ops are deleted behind it.
//
// A failed post is likewise not a reason to stop. The frontend being briefly
// unreachable is the exact moment a worker must keep trying, and a loop that
// returned here would silence a healthy worker for the rest of its life after
// one frontend restart.
func heartbeatLoop(ctx context.Context, tick <-chan time.Time, send func(context.Context) error) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick:
			if err := send(ctx); err != nil {
				xlog.Warn("Heartbeat failed", "error", err)
			}
		}
	}
}

// startTunnelAndArmReadiness starts the worker's tunnel and points the
// readiness gate at it.
//
// One call rather than two lines at the call site, because the gate and the
// tunnel are one fact. /readyz means "the frontend can reach me", and a live
// tunnel session is the only thing that makes that true: the worker binds
// loopback, advertises no address, and every request the frontend makes of it
// arrives as a stream inside that session. Armed as a separate statement, the
// arming is a line whose loss has no symptom - the gate fails open, so the
// worker answers 200 forever with no session, which is issue #10987 back
// again and nothing else in the process would say a word.
//
// A tunnel that fails to START leaves the gate as it found it. There is no
// worker to report on: Run turns that into a fatal error before anything else
// happens.
func startTunnelAndArmReadiness(ctx context.Context, readiness *nodes.WorkerReadiness, cfg TunnelConfig) (*Tunnel, error) {
	t, err := StartTunnel(ctx, cfg)
	if err != nil {
		return nil, err
	}
	readiness.Set(nodes.TunnelReadiness(t))
	return t, nil
}

// startWorkerHTTPServer starts the worker's loopback HTTP server with sup's
// control plane mounted on it.
//
// It takes the supervisor rather than an optional route set on purpose: the
// control plane and the file routes are served by one listener behind one
// bearer check, and there is no worker that wants the second without the first.
// Making the supervisor a parameter is what stops a future edit from starting
// the server without the control plane and producing a worker that looks
// healthy while answering 404 to every command.
func startWorkerHTTPServer(addr, stagingDir, modelsDir, dataDir, token string,
	readiness *nodes.WorkerReadiness, sup *backendSupervisor, cfg *Config,
	stagingFM *storage.FileManager, logStore *model.BackendLogStore) (*http.Server, error) {
	return nodes.StartFileTransferServer(addr, stagingDir, modelsDir, dataDir, token,
		config.DefaultMaxUploadSize, readiness, &nodes.AuthenticatedRoutes{
			Prefix: workerctl.Prefix,
			// One registrar for both route sets, because there is ONE control
			// prefix and AuthenticatedRoutes mounts one mux behind one bearer
			// check. A second route set would be a second check to forget.
			Register: func(mux *http.ServeMux) {
				sup.RegisterControlRoutes(mux)
				if stagingFM != nil {
					cfg.RegisterFileControlRoutes(mux, stagingFM)
				}
			},
		}, logStore)
}
