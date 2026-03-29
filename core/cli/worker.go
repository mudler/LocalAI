package cli

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
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	cliContext "github.com/mudler/LocalAI/core/cli/context"
	"github.com/mudler/LocalAI/core/cli/workerregistry"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/storage"
	grpc "github.com/mudler/LocalAI/pkg/grpc"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/sanitize"
	"github.com/mudler/LocalAI/pkg/system"
	"github.com/mudler/LocalAI/pkg/xsysinfo"
	process "github.com/mudler/go-processmanager"
	"github.com/mudler/xlog"
)

// isPathAllowed checks if path is within one of the allowed directories.
func isPathAllowed(path string, allowedDirs []string) bool {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	resolved, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		// Path may not exist yet; use the absolute path
		resolved = absPath
	}
	for _, dir := range allowedDirs {
		absDir, err := filepath.Abs(dir)
		if err != nil {
			continue
		}
		if strings.HasPrefix(resolved, absDir+string(filepath.Separator)) || resolved == absDir {
			return true
		}
	}
	return false
}

// WorkerCMD starts a generic worker process for distributed mode.
// Workers are backend-agnostic — they wait for backend.install NATS events
// from the SmartRouter to install and start the required backend.
//
// NATS is required. The worker acts as a process supervisor:
// - Receives backend.install → installs backend from gallery, starts gRPC process, replies success
// - Receives backend.stop → stops the gRPC process
// - Receives stop → full shutdown (deregister + exit)
//
// Model loading (LoadModel) is always via direct gRPC — no NATS needed for that.
type WorkerCMD struct {
	Addr               string `env:"LOCALAI_SERVE_ADDR" default:"0.0.0.0:50051" help:"Address to bind the gRPC server to" group:"server"`
	BackendsPath       string `env:"LOCALAI_BACKENDS_PATH,BACKENDS_PATH" type:"path" default:"${basepath}/backends" help:"Path containing backends" group:"server"`
	BackendsSystemPath string `env:"LOCALAI_BACKENDS_SYSTEM_PATH" type:"path" default:"/var/lib/local-ai/backends" help:"Path containing system backends" group:"server"`
	BackendGalleries   string `env:"LOCALAI_BACKEND_GALLERIES,BACKEND_GALLERIES" help:"JSON list of backend galleries" group:"server" default:"${backends}"`
	ModelsPath         string `env:"LOCALAI_MODELS_PATH,MODELS_PATH" type:"path" default:"${basepath}/models" help:"Path containing models" group:"server"`

	// HTTP file transfer
	HTTPAddr          string `env:"LOCALAI_HTTP_ADDR" default:"" help:"HTTP file transfer server address (default: gRPC port + 1)" group:"server"`
	AdvertiseHTTPAddr string `env:"LOCALAI_ADVERTISE_HTTP_ADDR" help:"HTTP address the frontend uses to reach this node for file transfer" group:"server"`

	// Registration (required)
	AdvertiseAddr     string `env:"LOCALAI_ADVERTISE_ADDR" help:"Address the frontend uses to reach this node (defaults to hostname:port from Addr)" group:"registration"`
	RegisterTo        string `env:"LOCALAI_REGISTER_TO" required:"" help:"Frontend URL for registration" group:"registration"`
	NodeName          string `env:"LOCALAI_NODE_NAME" help:"Node name for registration (defaults to hostname)" group:"registration"`
	RegistrationToken string `env:"LOCALAI_REGISTRATION_TOKEN" help:"Token for authenticating with the frontend" group:"registration"`
	HeartbeatInterval string `env:"LOCALAI_HEARTBEAT_INTERVAL" default:"10s" help:"Interval between heartbeats" group:"registration"`

	// NATS (required)
	NatsURL string `env:"LOCALAI_NATS_URL" required:"" help:"NATS server URL" group:"distributed"`

	// S3 storage for distributed file transfer
	StorageURL       string `env:"LOCALAI_STORAGE_URL" help:"S3 endpoint URL" group:"distributed"`
	StorageBucket    string `env:"LOCALAI_STORAGE_BUCKET" help:"S3 bucket name" group:"distributed"`
	StorageRegion    string `env:"LOCALAI_STORAGE_REGION" help:"S3 region" group:"distributed"`
	StorageAccessKey string `env:"LOCALAI_STORAGE_ACCESS_KEY" help:"S3 access key" group:"distributed"`
	StorageSecretKey string `env:"LOCALAI_STORAGE_SECRET_KEY" help:"S3 secret key" group:"distributed"`
}

func (cmd *WorkerCMD) Run(ctx *cliContext.Context) error {
	xlog.Info("Starting worker", "addr", cmd.Addr)

	systemState, err := system.GetSystemState(
		system.WithModelPath(cmd.ModelsPath),
		system.WithBackendPath(cmd.BackendsPath),
		system.WithBackendSystemPath(cmd.BackendsSystemPath),
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
	if err := json.Unmarshal([]byte(cmd.BackendGalleries), &galleries); err != nil {
		xlog.Warn("Failed to parse backend galleries", "error", err)
	}

	// Self-registration with frontend (with retry)
	regClient := &workerregistry.RegistrationClient{
		FrontendURL:       cmd.RegisterTo,
		RegistrationToken: cmd.RegistrationToken,
	}

	registrationBody := cmd.registrationBody()
	nodeID, _, err := regClient.RegisterWithRetry(context.Background(), registrationBody, 10)
	if err != nil {
		return fmt.Errorf("failed to register with frontend: %w", err)
	}

	xlog.Info("Registered with frontend", "nodeID", nodeID, "frontend", cmd.RegisterTo)
	heartbeatInterval, err := time.ParseDuration(cmd.HeartbeatInterval)
	if err != nil && cmd.HeartbeatInterval != "" {
		xlog.Warn("invalid heartbeat interval, using default 10s", "input", cmd.HeartbeatInterval, "error", err)
	}
	heartbeatInterval = cmp.Or(heartbeatInterval, 10*time.Second)
	// Context cancelled on shutdown — used by heartbeat and other background goroutines
	shutdownCtx, shutdownCancel := context.WithCancel(context.Background())
	defer shutdownCancel()

	// Start HTTP file transfer server
	httpAddr := cmd.resolveHTTPAddr()
	stagingDir := filepath.Join(cmd.ModelsPath, "..", "staging")
	dataDir := filepath.Join(cmd.ModelsPath, "..", "data")
	httpServer, err := nodes.StartFileTransferServer(httpAddr, stagingDir, cmd.ModelsPath, dataDir, cmd.RegistrationToken, config.DefaultMaxUploadSize, ml.BackendLogs())
	if err != nil {
		return fmt.Errorf("starting HTTP file transfer server: %w", err)
	}

	// Connect to NATS
	xlog.Info("Connecting to NATS", "url", sanitize.URL(cmd.NatsURL))
	natsClient, err := messaging.New(cmd.NatsURL)
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
				body := cmd.heartbeatBody()
				if err := regClient.Heartbeat(shutdownCtx, nodeID, body); err != nil {
					xlog.Warn("Heartbeat failed", "error", err)
				}
			}
		}
	}()

	// Process supervisor — manages multiple backend gRPC processes on different ports
	basePort := 50051
	if cmd.Addr != "" {
		// Extract port from addr (e.g., "0.0.0.0:50051" → 50051)
		if _, portStr, err := net.SplitHostPort(cmd.Addr); err == nil {
			if p, err := strconv.Atoi(portStr); err == nil {
				basePort = p
			}
		}
	}
	// Buffered so NATS stop handler can send without blocking
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Set the registration token once before any backends are started
	if cmd.RegistrationToken != "" {
		os.Setenv(grpc.AuthTokenEnvVar, cmd.RegistrationToken)
	}

	supervisor := &backendSupervisor{
		cmd:         cmd,
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
	if cmd.StorageURL != "" {
		if err := cmd.subscribeFileStaging(natsClient, nodeID); err != nil {
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

// subscribeFileStaging subscribes to NATS file staging subjects for this node.
func (cmd *WorkerCMD) subscribeFileStaging(natsClient messaging.MessagingClient, nodeID string) error {
	// Create FileManager with same S3 config as the frontend
	// TODO: propagate a caller-provided context once WorkerCMD carries one
	s3Store, err := storage.NewS3Store(context.Background(), storage.S3Config{
		Endpoint:        cmd.StorageURL,
		Region:          cmd.StorageRegion,
		Bucket:          cmd.StorageBucket,
		AccessKeyID:     cmd.StorageAccessKey,
		SecretAccessKey: cmd.StorageSecretKey,
		ForcePathStyle:  true,
	})
	if err != nil {
		return fmt.Errorf("initializing S3 store: %w", err)
	}

	cacheDir := filepath.Join(cmd.ModelsPath, "..", "cache")
	fm, err := storage.NewFileManager(s3Store, cacheDir)
	if err != nil {
		return fmt.Errorf("initializing file manager: %w", err)
	}

	// Subscribe: files.ensure — download S3 key to local, reply with local path
	natsClient.SubscribeReply(messaging.SubjectNodeFilesEnsure(nodeID), func(data []byte, reply func([]byte)) {
		var req struct {
			Key string `json:"key"`
		}
		if err := json.Unmarshal(data, &req); err != nil {
			replyJSON(reply, map[string]string{"error": "invalid request"})
			return
		}

		localPath, err := fm.Download(context.Background(), req.Key)
		if err != nil {
			xlog.Error("File ensure failed", "key", req.Key, "error", err)
			replyJSON(reply, map[string]string{"error": err.Error()})
			return
		}

		xlog.Debug("File ensured locally", "key", req.Key, "path", localPath)
		replyJSON(reply, map[string]string{"local_path": localPath})
	})

	// Subscribe: files.stage — upload local path to S3, reply with key
	natsClient.SubscribeReply(messaging.SubjectNodeFilesStage(nodeID), func(data []byte, reply func([]byte)) {
		var req struct {
			LocalPath string `json:"local_path"`
			Key       string `json:"key"`
		}
		if err := json.Unmarshal(data, &req); err != nil {
			replyJSON(reply, map[string]string{"error": "invalid request"})
			return
		}

		allowedDirs := []string{cacheDir}
		if cmd.ModelsPath != "" {
			allowedDirs = append(allowedDirs, cmd.ModelsPath)
		}
		if !isPathAllowed(req.LocalPath, allowedDirs) {
			replyJSON(reply, map[string]string{"error": "path outside allowed directories"})
			return
		}

		if err := fm.Upload(context.Background(), req.Key, req.LocalPath); err != nil {
			xlog.Error("File stage failed", "path", req.LocalPath, "key", req.Key, "error", err)
			replyJSON(reply, map[string]string{"error": err.Error()})
			return
		}

		xlog.Debug("File staged to S3", "path", req.LocalPath, "key", req.Key)
		replyJSON(reply, map[string]string{"key": req.Key})
	})

	// Subscribe: files.temp — allocate temp file, reply with local path
	natsClient.SubscribeReply(messaging.SubjectNodeFilesTemp(nodeID), func(data []byte, reply func([]byte)) {
		tmpDir := filepath.Join(cacheDir, "staging-tmp")
		if err := os.MkdirAll(tmpDir, 0750); err != nil {
			replyJSON(reply, map[string]string{"error": fmt.Sprintf("creating temp dir: %v", err)})
			return
		}

		f, err := os.CreateTemp(tmpDir, "localai-staging-*.tmp")
		if err != nil {
			replyJSON(reply, map[string]string{"error": fmt.Sprintf("creating temp file: %v", err)})
			return
		}
		localPath := f.Name()
		f.Close()

		xlog.Debug("Allocated temp file", "path", localPath)
		replyJSON(reply, map[string]string{"local_path": localPath})
	})

	// Subscribe: files.listdir — list files in a local directory, reply with relative paths
	natsClient.SubscribeReply(messaging.SubjectNodeFilesListDir(nodeID), func(data []byte, reply func([]byte)) {
		var req struct {
			KeyPrefix string `json:"key_prefix"`
		}
		if err := json.Unmarshal(data, &req); err != nil {
			replyJSON(reply, map[string]any{"error": "invalid request"})
			return
		}

		// Resolve key prefix to local directory
		dirPath := filepath.Join(cacheDir, req.KeyPrefix)
		if rel, ok := strings.CutPrefix(req.KeyPrefix, storage.ModelKeyPrefix); ok && cmd.ModelsPath != "" {
			dirPath = filepath.Join(cmd.ModelsPath, rel)
		} else if rel, ok := strings.CutPrefix(req.KeyPrefix, storage.DataKeyPrefix); ok {
			dirPath = filepath.Join(cacheDir, "..", "data", rel)
		}

		// Sanitize to prevent directory traversal via crafted key_prefix
		dirPath = filepath.Clean(dirPath)
		cleanCache := filepath.Clean(cacheDir)
		cleanModels := filepath.Clean(cmd.ModelsPath)
		cleanData := filepath.Clean(filepath.Join(cacheDir, "..", "data"))
		if !(strings.HasPrefix(dirPath, cleanCache+string(filepath.Separator)) ||
			dirPath == cleanCache ||
			(cleanModels != "." && strings.HasPrefix(dirPath, cleanModels+string(filepath.Separator))) ||
			dirPath == cleanModels ||
			strings.HasPrefix(dirPath, cleanData+string(filepath.Separator)) ||
			dirPath == cleanData) {
			replyJSON(reply, map[string]any{"error": "invalid key prefix"})
			return
		}

		var files []string
		filepath.WalkDir(dirPath, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if !d.IsDir() {
				rel, err := filepath.Rel(dirPath, path)
				if err == nil {
					files = append(files, rel)
				}
			}
			return nil
		})

		xlog.Debug("Listed remote dir", "keyPrefix", req.KeyPrefix, "dirPath", dirPath, "fileCount", len(files))
		replyJSON(reply, map[string]any{"files": files})
	})

	xlog.Info("Subscribed to file staging NATS subjects", "nodeID", nodeID)
	return nil
}

// replyJSON marshals v to JSON and calls the reply function.
func replyJSON(reply func([]byte), v any) {
	data, err := json.Marshal(v)
	if err != nil {
		xlog.Error("Failed to marshal NATS reply", "error", err)
		data = []byte(`{"error":"internal marshal error"}`)
	}
	reply(data)
}

// backendProcess represents a single gRPC backend process.
type backendProcess struct {
	proc    *process.Process
	backend string
	addr    string // gRPC address (host:port)
}

// backendSupervisor manages multiple backend gRPC processes on different ports.
// Each backend type (e.g., llama-cpp, bert-embeddings) gets its own process and port.
type backendSupervisor struct {
	cmd         *WorkerCMD
	ml          *model.ModelLoader
	systemState *system.SystemState
	galleries   []config.Gallery
	nodeID      string
	nats        messaging.MessagingClient
	sigCh       chan<- os.Signal // send shutdown signal instead of os.Exit

	mu        sync.Mutex
	processes map[string]*backendProcess // key: backend name
	nextPort  int                        // next available port for new backends
	freePorts []int                      // ports freed by stopBackend, reused before nextPort
}

// startBackend starts a gRPC backend process on a dynamically allocated port.
// Returns the gRPC address.
func (s *backendSupervisor) startBackend(backend, backendPath string) (string, error) {
	s.mu.Lock()

	// Already running?
	if bp, ok := s.processes[backend]; ok {
		if bp.proc != nil && bp.proc.IsAlive() {
			s.mu.Unlock()
			return bp.addr, nil
		}
		// Process died — clean up and restart
		xlog.Warn("Backend process died unexpectedly, restarting", "backend", backend)
		delete(s.processes, backend)
	}

	// Allocate port — recycle freed ports first, then grow upward from basePort
	var port int
	if len(s.freePorts) > 0 {
		port = s.freePorts[len(s.freePorts)-1]
		s.freePorts = s.freePorts[:len(s.freePorts)-1]
	} else {
		port = s.nextPort
		s.nextPort++
	}
	bindAddr := fmt.Sprintf("0.0.0.0:%d", port)
	clientAddr := fmt.Sprintf("127.0.0.1:%d", port)

	proc, err := s.ml.StartProcess(backendPath, backend, bindAddr)
	if err != nil {
		s.mu.Unlock()
		return "", fmt.Errorf("starting backend process: %w", err)
	}

	s.processes[backend] = &backendProcess{
		proc:    proc,
		backend: backend,
		addr:    clientAddr,
	}
	xlog.Info("Backend process started", "backend", backend, "addr", clientAddr)

	// Capture reference before unlocking for race-safe health check.
	// Another goroutine could stopBackend and recycle the port while we poll.
	bp := s.processes[backend]
	s.mu.Unlock()

	// Wait for the gRPC server to be ready
	client := grpc.NewClientWithToken(clientAddr, false, nil, false, s.cmd.RegistrationToken)
	for range 20 {
		time.Sleep(200 * time.Millisecond)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		if ok, _ := client.HealthCheck(ctx); ok {
			cancel()
			// Verify the process wasn't stopped/replaced while health-checking
			s.mu.Lock()
			currentBP, exists := s.processes[backend]
			s.mu.Unlock()
			if !exists || currentBP != bp {
				return "", fmt.Errorf("backend %s was stopped during startup", backend)
			}
			xlog.Debug("Backend gRPC server is ready", "backend", backend, "addr", clientAddr)
			return clientAddr, nil
		}
		cancel()
	}

	xlog.Warn("Backend gRPC server not ready after waiting, proceeding anyway", "backend", backend, "addr", clientAddr)
	return clientAddr, nil
}

// stopBackend stops a specific backend's gRPC process.
func (s *backendSupervisor) stopBackend(backend string) {
	s.mu.Lock()
	bp, ok := s.processes[backend]
	if !ok || bp.proc == nil {
		s.mu.Unlock()
		return
	}
	// Clean up map and recycle port while holding lock
	delete(s.processes, backend)
	if _, portStr, err := net.SplitHostPort(bp.addr); err == nil {
		if p, err := strconv.Atoi(portStr); err == nil {
			s.freePorts = append(s.freePorts, p)
		}
	}
	s.mu.Unlock()

	// Network I/O outside the lock
	client := grpc.NewClientWithToken(bp.addr, false, nil, false, s.cmd.RegistrationToken)
	if freeFunc, ok := client.(interface{ Free(context.Context) error }); ok {
		xlog.Debug("Calling Free() before stopping backend", "backend", backend)
		if err := freeFunc.Free(context.Background()); err != nil {
			xlog.Warn("Free() failed (best-effort)", "backend", backend, "error", err)
		}
	}

	xlog.Info("Stopping backend process", "backend", backend, "addr", bp.addr)
	if err := bp.proc.Stop(); err != nil {
		xlog.Error("Error stopping backend process", "backend", backend, "error", err)
	}
}

// stopAllBackends stops all running backend processes.
func (s *backendSupervisor) stopAllBackends() {
	s.mu.Lock()
	backends := slices.Collect(maps.Keys(s.processes))
	s.mu.Unlock()

	for _, b := range backends {
		s.stopBackend(b)
	}
}

// isRunning returns whether a specific backend process is currently running.
func (s *backendSupervisor) isRunning(backend string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	bp, ok := s.processes[backend]
	return ok && bp.proc != nil && bp.proc.IsAlive()
}

// getAddr returns the gRPC address for a running backend, or empty string.
func (s *backendSupervisor) getAddr(backend string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if bp, ok := s.processes[backend]; ok {
		return bp.addr
	}
	return ""
}

// installBackend handles the backend.install flow:
// 1. If already running for this model, return existing address
// 2. Install backend from gallery (if not already installed)
// 3. Find backend binary
// 4. Start gRPC process on a new port
// Returns the gRPC address of the backend process.
func (s *backendSupervisor) installBackend(req messaging.BackendInstallRequest) (string, error) {
	// Process key: use ModelID if provided (per-model process), else backend name
	processKey := req.ModelID
	if processKey == "" {
		processKey = req.Backend
	}

	// If already running for this model, return its address
	if addr := s.getAddr(processKey); addr != "" {
		xlog.Info("Backend already running for model", "backend", req.Backend, "model", req.ModelID, "addr", addr)
		return addr, nil
	}

	// Parse galleries from request (override local config if provided)
	galleries := s.galleries
	if req.BackendGalleries != "" {
		var reqGalleries []config.Gallery
		if err := json.Unmarshal([]byte(req.BackendGalleries), &reqGalleries); err == nil {
			galleries = reqGalleries
		}
	}

	// Try to find the backend binary
	backendPath := s.findBackend(req.Backend)
	if backendPath == "" {
		// Backend not found locally — try auto-installing from gallery
		xlog.Info("Backend not found locally, attempting gallery install", "backend", req.Backend)
		if err := gallery.InstallBackendFromGallery(
			context.Background(), galleries, s.systemState, s.ml, req.Backend, nil, false,
		); err != nil {
			return "", fmt.Errorf("installing backend from gallery: %w", err)
		}
		// Re-register after install and retry
		gallery.RegisterBackends(s.systemState, s.ml)
		backendPath = s.findBackend(req.Backend)
	}

	if backendPath == "" {
		return "", fmt.Errorf("backend %q not found after install attempt", req.Backend)
	}

	xlog.Info("Found backend binary", "path", backendPath, "processKey", processKey)

	// Start the gRPC process on a new port (keyed by model, not just backend)
	return s.startBackend(processKey, backendPath)
}

// findBackend looks for the backend binary in the backends path and system path.
func (s *backendSupervisor) findBackend(backend string) string {
	candidates := []string{
		filepath.Join(s.cmd.BackendsPath, backend),
		filepath.Join(s.cmd.BackendsPath, backend, backend),
		filepath.Join(s.cmd.BackendsSystemPath, backend),
		filepath.Join(s.cmd.BackendsSystemPath, backend, backend),
	}
	if uri := s.ml.GetExternalBackend(backend); uri != "" {
		if fi, err := os.Stat(uri); err == nil && !fi.IsDir() {
			return uri
		}
	}
	for _, path := range candidates {
		fi, err := os.Stat(path)
		if err == nil && !fi.IsDir() {
			return path
		}
	}
	return ""
}

// subscribeLifecycleEvents subscribes to NATS backend lifecycle events.
func (s *backendSupervisor) subscribeLifecycleEvents() {
	// backend.install — install backend + start gRPC process (request-reply)
	s.nats.SubscribeReply(messaging.SubjectNodeBackendInstall(s.nodeID), func(data []byte, reply func([]byte)) {
		xlog.Info("Received NATS backend.install event")
		var req messaging.BackendInstallRequest
		if err := json.Unmarshal(data, &req); err != nil {
			resp := messaging.BackendInstallReply{Success: false, Error: fmt.Sprintf("invalid request: %v", err)}
			replyJSON(reply, resp)
			return
		}

		addr, err := s.installBackend(req)
		if err != nil {
			xlog.Error("Failed to install backend via NATS", "error", err)
			resp := messaging.BackendInstallReply{Success: false, Error: err.Error()}
			replyJSON(reply, resp)
			return
		}

		// Return the gRPC address so the router knows which port to use
		advertiseAddr := addr
		if s.cmd.AdvertiseAddr != "" {
			// Replace 0.0.0.0 with the advertised host but keep the dynamic port
			_, port, _ := net.SplitHostPort(addr)
			advertiseHost, _, _ := net.SplitHostPort(s.cmd.AdvertiseAddr)
			advertiseAddr = net.JoinHostPort(advertiseHost, port)
		}
		resp := messaging.BackendInstallReply{Success: true, Address: advertiseAddr}
		replyJSON(reply, resp)
	})

	// backend.stop — stop a specific backend process
	s.nats.Subscribe(messaging.SubjectNodeBackendStop(s.nodeID), func(data []byte) {
		// Try to parse backend name from payload; if empty, stop all
		var req struct {
			Backend string `json:"backend"`
		}
		if json.Unmarshal(data, &req) == nil && req.Backend != "" {
			xlog.Info("Received NATS backend.stop event", "backend", req.Backend)
			s.stopBackend(req.Backend)
		} else {
			xlog.Info("Received NATS backend.stop event (all)")
			s.stopAllBackends()
		}
	})

	// backend.delete — stop backend + delete files (request-reply)
	s.nats.SubscribeReply(messaging.SubjectNodeBackendDelete(s.nodeID), func(data []byte, reply func([]byte)) {
		xlog.Info("Received NATS backend.delete event")
		var req messaging.BackendDeleteRequest
		if err := json.Unmarshal(data, &req); err != nil {
			resp := messaging.BackendDeleteReply{Success: false, Error: fmt.Sprintf("invalid request: %v", err)}
			replyJSON(reply, resp)
			return
		}

		// Stop if running this backend
		if s.isRunning(req.Backend) {
			s.stopBackend(req.Backend)
		}

		// Delete the backend files
		if err := gallery.DeleteBackendFromSystem(s.systemState, req.Backend); err != nil {
			xlog.Warn("Failed to delete backend files", "backend", req.Backend, "error", err)
			resp := messaging.BackendDeleteReply{Success: false, Error: err.Error()}
			replyJSON(reply, resp)
			return
		}

		// Re-register backends after deletion
		gallery.RegisterBackends(s.systemState, s.ml)

		resp := messaging.BackendDeleteReply{Success: true}
		replyJSON(reply, resp)
	})

	// backend.list — list installed backends (request-reply)
	s.nats.SubscribeReply(messaging.SubjectNodeBackendList(s.nodeID), func(data []byte, reply func([]byte)) {
		xlog.Info("Received NATS backend.list event")
		backends, err := gallery.ListSystemBackends(s.systemState)
		if err != nil {
			resp := messaging.BackendListReply{Error: err.Error()}
			replyJSON(reply, resp)
			return
		}

		var infos []messaging.NodeBackendInfo
		for name, b := range backends {
			info := messaging.NodeBackendInfo{
				Name:     name,
				IsSystem: b.IsSystem,
				IsMeta:   b.IsMeta,
			}
			if b.Metadata != nil {
				info.InstalledAt = b.Metadata.InstalledAt
				info.GalleryURL = b.Metadata.GalleryURL
			}
			infos = append(infos, info)
		}

		resp := messaging.BackendListReply{Backends: infos}
		replyJSON(reply, resp)
	})

	// model.unload — call gRPC Free() to release GPU memory (request-reply)
	s.nats.SubscribeReply(messaging.SubjectNodeModelUnload(s.nodeID), func(data []byte, reply func([]byte)) {
		xlog.Info("Received NATS model.unload event")
		var req messaging.ModelUnloadRequest
		if err := json.Unmarshal(data, &req); err != nil {
			resp := messaging.ModelUnloadReply{Success: false, Error: fmt.Sprintf("invalid request: %v", err)}
			replyJSON(reply, resp)
			return
		}

		// Find the backend address for this model's backend type
		// The request includes an Address field if the router knows which process to target
		targetAddr := req.Address
		if targetAddr == "" {
			// Fallback: try all running backends
			s.mu.Lock()
			for _, bp := range s.processes {
				targetAddr = bp.addr
				break
			}
			s.mu.Unlock()
		}

		if targetAddr != "" {
			// Best-effort gRPC Free()
			client := grpc.NewClientWithToken(targetAddr, false, nil, false, s.cmd.RegistrationToken)
			if freeFunc, ok := client.(interface{ Free(context.Context) error }); ok {
				if err := freeFunc.Free(context.Background()); err != nil {
					xlog.Warn("Free() failed during model.unload", "error", err, "addr", targetAddr)
				}
			}
		}

		resp := messaging.ModelUnloadReply{Success: true}
		replyJSON(reply, resp)
	})

	// model.delete — remove model files from disk (request-reply)
	s.nats.SubscribeReply(messaging.SubjectNodeModelDelete(s.nodeID), func(data []byte, reply func([]byte)) {
		xlog.Info("Received NATS model.delete event")
		var req messaging.ModelDeleteRequest
		if err := json.Unmarshal(data, &req); err != nil {
			replyJSON(reply, messaging.ModelDeleteReply{Success: false, Error: "invalid request"})
			return
		}

		if err := gallery.DeleteStagedModelFiles(s.cmd.ModelsPath, req.ModelName); err != nil {
			xlog.Warn("Failed to delete model files", "model", req.ModelName, "error", err)
			replyJSON(reply, messaging.ModelDeleteReply{Success: false, Error: err.Error()})
			return
		}

		replyJSON(reply, messaging.ModelDeleteReply{Success: true})
	})

	// stop — trigger the normal shutdown path via sigCh so deferred cleanup runs
	s.nats.Subscribe(messaging.SubjectNodeStop(s.nodeID), func(data []byte) {
		xlog.Info("Received NATS stop event — signaling shutdown")
		select {
		case s.sigCh <- syscall.SIGTERM:
		default:
			xlog.Debug("Shutdown already signaled, ignoring duplicate stop")
		}
	})
}

// advertiseAddr returns the address the frontend should use to reach this node.
func (cmd *WorkerCMD) advertiseAddr() string {
	if cmd.AdvertiseAddr != "" {
		return cmd.AdvertiseAddr
	}
	host, port, ok := strings.Cut(cmd.Addr, ":")
	if ok && (host == "0.0.0.0" || host == "") {
		if hostname, err := os.Hostname(); err == nil {
			return hostname + ":" + port
		}
	}
	return cmd.Addr
}

// resolveHTTPAddr returns the address to bind the HTTP file transfer server to.
// Uses basePort-1 so it doesn't conflict with dynamically allocated gRPC ports
// which grow upward from basePort.
func (cmd *WorkerCMD) resolveHTTPAddr() string {
	if cmd.HTTPAddr != "" {
		return cmd.HTTPAddr
	}
	host, port, ok := strings.Cut(cmd.Addr, ":")
	if !ok {
		return "0.0.0.0:50050"
	}
	portNum, _ := strconv.Atoi(port)
	return fmt.Sprintf("%s:%d", host, portNum-1)
}

// advertiseHTTPAddr returns the HTTP address the frontend should use to reach
// this node for file transfer.
func (cmd *WorkerCMD) advertiseHTTPAddr() string {
	if cmd.AdvertiseHTTPAddr != "" {
		return cmd.AdvertiseHTTPAddr
	}
	httpAddr := cmd.resolveHTTPAddr()
	host, port, ok := strings.Cut(httpAddr, ":")
	if ok && (host == "0.0.0.0" || host == "") {
		if hostname, err := os.Hostname(); err == nil {
			return hostname + ":" + port
		}
	}
	return httpAddr
}

// registrationBody builds the JSON body for node registration.
func (cmd *WorkerCMD) registrationBody() map[string]any {
	nodeName := cmd.NodeName
	if nodeName == "" {
		hostname, err := os.Hostname()
		if err != nil {
			nodeName = fmt.Sprintf("node-%d", os.Getpid())
		} else {
			nodeName = hostname
		}
	}

	// Detect GPU info for VRAM-aware scheduling
	totalVRAM, _ := xsysinfo.TotalAvailableVRAM()
	gpuVendor, _ := xsysinfo.DetectGPUVendor()

	body := map[string]any{
		"name":           nodeName,
		"address":        cmd.advertiseAddr(),
		"http_address":   cmd.advertiseHTTPAddr(),
		"total_vram":     totalVRAM,
		"available_vram": totalVRAM, // initially all VRAM is available
		"gpu_vendor":     gpuVendor,
	}

	// If no GPU detected, report system RAM so the scheduler/UI has capacity info
	if totalVRAM == 0 {
		if ramInfo, err := xsysinfo.GetSystemRAMInfo(); err == nil {
			body["total_ram"] = ramInfo.Total
			body["available_ram"] = ramInfo.Available
		}
	}
	if cmd.RegistrationToken != "" {
		body["token"] = cmd.RegistrationToken
	}
	return body
}

// heartbeatBody returns the current VRAM/RAM stats for heartbeat payloads.
func (cmd *WorkerCMD) heartbeatBody() map[string]any {
	var availVRAM uint64
	aggregate := xsysinfo.GetGPUAggregateInfo()
	if aggregate.TotalVRAM > 0 {
		availVRAM = aggregate.FreeVRAM
	} else {
		// Fallback: report total as available (no usage tracking possible)
		availVRAM, _ = xsysinfo.TotalAvailableVRAM()
	}

	body := map[string]any{
		"available_vram": availVRAM,
	}

	// If no GPU, report system RAM usage instead
	if aggregate.TotalVRAM == 0 {
		if ramInfo, err := xsysinfo.GetSystemRAMInfo(); err == nil {
			body["available_ram"] = ramInfo.Available
		}
	}
	return body
}
