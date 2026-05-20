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

	// Self-registration with frontend (with retry)
	regClient := &workerregistry.RegistrationClient{
		FrontendURL:       cfg.RegisterTo,
		RegistrationToken: cfg.RegistrationToken,
	}

	registrationBody := cfg.registrationBody()
	nodeID, _, err := regClient.RegisterWithRetry(context.Background(), registrationBody, 10)
	if err != nil {
		return fmt.Errorf("failed to register with frontend: %w", err)
	}

	xlog.Info("Registered with frontend", "nodeID", nodeID, "frontend", cfg.RegisterTo)
	heartbeatInterval, err := time.ParseDuration(cfg.HeartbeatInterval)
	if err != nil && cfg.HeartbeatInterval != "" {
		xlog.Warn("invalid heartbeat interval, using default 10s", "input", cfg.HeartbeatInterval, "error", err)
	}
	heartbeatInterval = cmp.Or(heartbeatInterval, 10*time.Second)
	// Context cancelled on shutdown — used by heartbeat and other background goroutines
	shutdownCtx, shutdownCancel := context.WithCancel(context.Background())
	defer shutdownCancel()

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
	natsClient, err := messaging.New(cfg.NatsURL)
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
	<-sigCh

	xlog.Info("Shutting down worker")
	shutdownCancel() // stop heartbeat loop immediately
	regClient.GracefulDeregister(nodeID)
	supervisor.stopAllBackends()
	nodes.ShutdownFileTransferServer(httpServer)
	return nil
}
