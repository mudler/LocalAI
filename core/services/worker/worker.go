package worker

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
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
	gallery.RegisterBackends(systemState, ml)

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

	// Resolve how to connect to NATS. Static env credentials cannot be re-minted,
	// so register once and use them directly. Otherwise the credential manager
	// (re)registers to obtain credentials — waiting through admin approval — and
	// refreshes them before the minted JWT expires, so the connection survives
	// expiry via a transparent reconnect.
	var (
		nodeID      string
		connectNats func() (*messaging.Client, error)
	)
	if cfg.NatsJWT != "" || cfg.NatsUserSeed != "" {
		nid, _, _, _, regErr := regClient.RegisterWithRetry(shutdownCtx, registrationBody, 10)
		if regErr != nil {
			return fmt.Errorf("failed to register with frontend: %w", regErr)
		}
		nodeID = nid
		connectNats = func() (*messaging.Client, error) {
			return connectNATS(cfg.NatsURL, cfg.NatsJWT, cfg.NatsUserSeed, "", "", cfg.NatsRequireAuth, natsTLS)
		}
	} else {
		credMgr := workerregistry.NewNATSCredentialManager(
			func(ctx context.Context) (*workerregistry.RegisterResponse, error) {
				return regClient.RegisterFull(ctx, registrationBody)
			},
			cfg.NatsRequireAuth,
		)
		res, regErr := credMgr.Acquire(shutdownCtx)
		if regErr != nil {
			return fmt.Errorf("failed to register with frontend: %w", regErr)
		}
		nodeID = res.ID
		connectNats = func() (*messaging.Client, error) {
			var opts []messaging.Option
			if credMgr.HasCredentials() {
				opts = append(opts, messaging.WithUserJWTProvider(credMgr.Provider()))
			}
			if natsTLS.Enabled() {
				opts = append(opts, messaging.WithTLS(natsTLS))
			}
			client, cerr := messaging.New(cfg.NatsURL, opts...)
			if cerr == nil && credMgr.HasCredentials() {
				go func() {
					if err := credMgr.RefreshLoop(shutdownCtx); err != nil {
						xlog.Error("NATS credential refresh permanently failed; shutting down worker", "error", err)
						shutdownCancel()
					}
				}()
			}
			return client, cerr
		}
	}

	xlog.Info("Registered with frontend", "nodeID", nodeID, "frontend", cfg.RegisterTo)
	heartbeatInterval, err := time.ParseDuration(cfg.HeartbeatInterval)
	if err != nil && cfg.HeartbeatInterval != "" {
		xlog.Warn("invalid heartbeat interval, using default 10s", "input", cfg.HeartbeatInterval, "error", err)
	}
	heartbeatInterval = cmp.Or(heartbeatInterval, 10*time.Second)

	// Start HTTP file transfer server
	httpAddr := cfg.resolveHTTPAddr()
	stagingDir := filepath.Join(cfg.ModelsPath, "..", "staging")
	dataDir := filepath.Join(cfg.ModelsPath, "..", "data")
	httpServer, err := nodes.StartFileTransferServer(httpAddr, stagingDir, cfg.ModelsPath, dataDir, cfg.RegistrationToken, config.DefaultMaxUploadSize, ml.BackendLogs())
	if err != nil {
		return fmt.Errorf("starting HTTP file transfer server: %w", err)
	}

	// Connect to NATS
	xlog.Info("Connecting to NATS", "url", sanitize.URL(cfg.NatsURL))
	natsClient, err := connectNats()
	if err != nil {
		nodes.ShutdownFileTransferServer(httpServer)
		return fmt.Errorf("connecting to NATS: %w", err)
	}
	defer natsClient.Close()

	// Start heartbeat goroutine (after NATS is connected so IsConnected check works)
	go func() {
		ticker := time.NewTicker(heartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-shutdownCtx.Done():
				return
			case <-ticker.C:
				if !natsClient.IsConnected() {
					xlog.Warn("Skipping heartbeat: NATS disconnected")
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
		os.Setenv(grpc.AuthTokenEnvVar, cfg.RegistrationToken)
	}

	supervisor := &backendSupervisor{
		cfg:         cfg,
		ml:          ml,
		systemState: systemState,
		galleries:   galleries,
		nodeID:      nodeID,
		nats:        natsClient,
		sigCh:       sigCh,
		processes:   make(map[string]*backendProcess),
		nextPort:    basePort,
	}
	supervisor.subscribeLifecycleEvents()

	// Subscribe to file staging NATS subjects if S3 is configured
	if cfg.StorageURL != "" {
		if err := cfg.subscribeFileStaging(natsClient, nodeID); err != nil {
			xlog.Error("Failed to subscribe to file staging subjects", "error", err)
		}
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
	supervisor.stopAllBackends()
	nodes.ShutdownFileTransferServer(httpServer)
	return runErr
}
