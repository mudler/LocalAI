package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	cliContext "github.com/mudler/LocalAI/core/cli/context"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/storage"
	grpc "github.com/mudler/LocalAI/pkg/grpc"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	"github.com/mudler/LocalAI/pkg/xsysinfo"
	process "github.com/mudler/go-processmanager"
	"github.com/mudler/xlog"
)

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
	var nodeID string
	const maxRetries = 10
	backoff := 2 * time.Second
	maxBackoff := 30 * time.Second

	for attempt := 1; attempt <= maxRetries; attempt++ {
		nodeID, err = cmd.registerWithFrontend()
		if err == nil {
			break
		}
		if attempt == maxRetries {
			return fmt.Errorf("failed to register with frontend after %d attempts: %w", maxRetries, err)
		}
		xlog.Warn("Registration failed, retrying", "attempt", attempt, "next_retry", backoff, "error", err)
		time.Sleep(backoff)
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}

	xlog.Info("Registered with frontend", "nodeID", nodeID, "frontend", cmd.RegisterTo)
	heartbeatInterval, _ := time.ParseDuration(cmd.HeartbeatInterval)
	if heartbeatInterval == 0 {
		heartbeatInterval = 10 * time.Second
	}
	go cmd.heartbeatLoop(nodeID, heartbeatInterval)

	// Start HTTP file transfer server
	httpAddr := cmd.resolveHTTPAddr()
	stagingDir := filepath.Join(cmd.ModelsPath, "..", "staging")
	dataDir := filepath.Join(cmd.ModelsPath, "..", "data")
	httpServer, err := nodes.StartFileTransferServer(httpAddr, stagingDir, cmd.ModelsPath, dataDir, cmd.RegistrationToken, ml.BackendLogs())
	if err != nil {
		return fmt.Errorf("starting HTTP file transfer server: %w", err)
	}

	// Connect to NATS
	xlog.Info("Connecting to NATS", "url", cmd.NatsURL)
	natsClient, err := messaging.New(cmd.NatsURL)
	if err != nil {
		return fmt.Errorf("connecting to NATS: %w", err)
	}
	defer natsClient.Close()

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
	supervisor := &backendSupervisor{
		cmd:         cmd,
		ml:          ml,
		systemState: systemState,
		galleries:   galleries,
		nodeID:      nodeID,
		nats:        natsClient,
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

	// Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	xlog.Info("Shutting down worker")
	supervisor.stopAllBackends()
	nodes.ShutdownFileTransferServer(httpServer)
	cmd.gracefulDeregister(nodeID)
	return nil
}

// subscribeFileStaging subscribes to NATS file staging subjects for this node.
func (cmd *WorkerCMD) subscribeFileStaging(natsClient *messaging.Client, nodeID string) error {
	// Create FileManager with same S3 config as the frontend
	s3Store, err := storage.NewS3Store(storage.S3Config{
		Endpoint:       cmd.StorageURL,
		Region:         cmd.StorageRegion,
		Bucket:         cmd.StorageBucket,
		AccessKeyID:    cmd.StorageAccessKey,
		SecretAccessKey: cmd.StorageSecretKey,
		ForcePathStyle: true,
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
		if strings.HasPrefix(req.KeyPrefix, storage.ModelKeyPrefix) && cmd.ModelsPath != "" {
			dirPath = filepath.Join(cmd.ModelsPath, strings.TrimPrefix(req.KeyPrefix, storage.ModelKeyPrefix))
		} else if strings.HasPrefix(req.KeyPrefix, storage.DataKeyPrefix) {
			dirPath = filepath.Join(cacheDir, "..", "data", strings.TrimPrefix(req.KeyPrefix, storage.DataKeyPrefix))
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
	data, _ := json.Marshal(v)
	reply(data)
}

// backendProcess represents a single gRPC backend process.
type backendProcess struct {
	proc    *process.Process
	backend string
	path    string
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
	nats        *messaging.Client

	mu        sync.Mutex
	processes map[string]*backendProcess // key: backend name
	nextPort  int                        // next available port for new backends
}

// getProcess returns the process for a backend, or nil if not running.
func (s *backendSupervisor) getProcess(backend string) *backendProcess {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.processes[backend]
}

// startBackend starts a gRPC backend process on a dynamically allocated port.
// Returns the gRPC address.
func (s *backendSupervisor) startBackend(backend, backendPath string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Already running?
	if bp, ok := s.processes[backend]; ok {
		if bp.proc != nil && bp.proc.IsAlive() {
			return bp.addr, nil
		}
		// Process died — clean up and restart
		xlog.Warn("Backend process died unexpectedly, restarting", "backend", backend)
		delete(s.processes, backend)
	}

	// Allocate port — gRPC ports grow upward from basePort; HTTP file server is below
	addr := fmt.Sprintf("0.0.0.0:%d", s.nextPort)
	s.nextPort++

	// Pass the registration token to the backend process for gRPC auth
	if s.cmd.RegistrationToken != "" {
		os.Setenv(grpc.AuthTokenEnvVar, s.cmd.RegistrationToken)
	}

	proc, err := s.ml.StartProcess(backendPath, backend, addr)
	if err != nil {
		return "", fmt.Errorf("starting backend process: %w", err)
	}

	s.processes[backend] = &backendProcess{
		proc:    proc,
		backend: backend,
		path:    backendPath,
		addr:    addr,
	}
	xlog.Info("Backend process started", "backend", backend, "addr", addr)

	// Wait for the gRPC server to be ready
	client := grpc.NewClientWithToken(addr, false, nil, false, s.cmd.RegistrationToken)
	for i := 0; i < 20; i++ {
		time.Sleep(200 * time.Millisecond)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		if ok, _ := client.HealthCheck(ctx); ok {
			cancel()
			xlog.Debug("Backend gRPC server is ready", "backend", backend, "addr", addr)
			return addr, nil
		}
		cancel()
	}

	xlog.Warn("Backend gRPC server not ready after waiting, proceeding anyway", "backend", backend, "addr", addr)
	return addr, nil
}

// stopBackend stops a specific backend's gRPC process.
func (s *backendSupervisor) stopBackend(backend string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	bp, ok := s.processes[backend]
	if !ok || bp.proc == nil {
		return
	}

	// Best-effort Free() to release GPU memory
	client := grpc.NewClientWithToken(bp.addr, false, nil, false, s.cmd.RegistrationToken)
	if freeFunc, ok := client.(interface{ Free() error }); ok {
		xlog.Debug("Calling Free() before stopping backend", "backend", backend)
		if err := freeFunc.Free(); err != nil {
			xlog.Warn("Free() failed (best-effort)", "backend", backend, "error", err)
		}
	}

	xlog.Info("Stopping backend process", "backend", backend, "addr", bp.addr)
	if err := bp.proc.Stop(); err != nil {
		xlog.Error("Error stopping backend process", "backend", backend, "error", err)
	}
	delete(s.processes, backend)
}

// stopAllBackends stops all running backend processes.
func (s *backendSupervisor) stopAllBackends() {
	s.mu.Lock()
	backends := make([]string, 0, len(s.processes))
	for name := range s.processes {
		backends = append(backends, name)
	}
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
			respData, _ := json.Marshal(resp)
			reply(respData)
			return
		}

		addr, err := s.installBackend(req)
		if err != nil {
			xlog.Error("Failed to install backend via NATS", "error", err)
			resp := messaging.BackendInstallReply{Success: false, Error: err.Error()}
			respData, _ := json.Marshal(resp)
			reply(respData)
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
		respData, _ := json.Marshal(resp)
		reply(respData)
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
			respData, _ := json.Marshal(resp)
			reply(respData)
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
			respData, _ := json.Marshal(resp)
			reply(respData)
			return
		}

		// Re-register backends after deletion
		gallery.RegisterBackends(s.systemState, s.ml)

		resp := messaging.BackendDeleteReply{Success: true}
		respData, _ := json.Marshal(resp)
		reply(respData)
	})

	// backend.list — list installed backends (request-reply)
	s.nats.SubscribeReply(messaging.SubjectNodeBackendList(s.nodeID), func(data []byte, reply func([]byte)) {
		xlog.Info("Received NATS backend.list event")
		backends, err := gallery.ListSystemBackends(s.systemState)
		if err != nil {
			resp := messaging.BackendListReply{Error: err.Error()}
			respData, _ := json.Marshal(resp)
			reply(respData)
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
		respData, _ := json.Marshal(resp)
		reply(respData)
	})

	// model.unload — call gRPC Free() to release GPU memory (request-reply)
	s.nats.SubscribeReply(messaging.SubjectNodeModelUnload(s.nodeID), func(data []byte, reply func([]byte)) {
		xlog.Info("Received NATS model.unload event")
		var req messaging.ModelUnloadRequest
		if err := json.Unmarshal(data, &req); err != nil {
			resp := messaging.ModelUnloadReply{Success: false, Error: fmt.Sprintf("invalid request: %v", err)}
			respData, _ := json.Marshal(resp)
			reply(respData)
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
			if freeFunc, ok := client.(interface{ Free() error }); ok {
				if err := freeFunc.Free(); err != nil {
					xlog.Warn("Free() failed during model.unload", "error", err, "addr", targetAddr)
				}
			}
		}

		resp := messaging.ModelUnloadReply{Success: true}
		respData, _ := json.Marshal(resp)
		reply(respData)
	})

	// model.delete — remove model files from disk (request-reply)
	s.nats.SubscribeReply(messaging.SubjectNodeModelDelete(s.nodeID), func(data []byte, reply func([]byte)) {
		xlog.Info("Received NATS model.delete event")
		var req messaging.ModelDeleteRequest
		if err := json.Unmarshal(data, &req); err != nil {
			resp := messaging.ModelDeleteReply{Success: false, Error: fmt.Sprintf("invalid request: %v", err)}
			respData, _ := json.Marshal(resp)
			reply(respData)
			return
		}

		// Remove model files from models path
		modelPath := filepath.Join(s.cmd.ModelsPath, req.ModelName)
		if _, err := os.Stat(modelPath); err == nil {
			if err := os.RemoveAll(modelPath); err != nil {
				xlog.Warn("Failed to remove model directory", "path", modelPath, "error", err)
			}
		}
		// Also try removing as a single file (model_name.gguf etc.)
		matches, _ := filepath.Glob(filepath.Join(s.cmd.ModelsPath, req.ModelName+"*"))
		for _, m := range matches {
			os.Remove(m)
		}

		resp := messaging.ModelDeleteReply{Success: true}
		respData, _ := json.Marshal(resp)
		reply(respData)
	})

	// stop — full shutdown (deregister + exit)
	s.nats.Subscribe(messaging.SubjectNodeStop(s.nodeID), func(data []byte) {
		xlog.Info("Received NATS stop event — shutting down entirely")
		s.stopAllBackends()
		s.cmd.gracefulDeregister(s.nodeID)
		os.Exit(0)
	})
}

// gracefulDeregister handles drain → deregister.
func (cmd *WorkerCMD) gracefulDeregister(nodeID string) {
	if cmd.RegisterTo == "" || nodeID == "" {
		return
	}

	if err := cmd.drainNode(nodeID); err != nil {
		xlog.Warn("Failed to set drain status", "error", err)
	} else {
		cmd.waitForDrain(nodeID)
	}

	if err := cmd.deregisterFromFrontend(nodeID); err != nil {
		xlog.Error("Failed to deregister", "error", err)
	} else {
		xlog.Info("Deregistered from frontend")
	}
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
	var portNum int
	fmt.Sscanf(port, "%d", &portNum)
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

// registerWithFrontend calls POST /api/node/register on the frontend to self-register.
func (cmd *WorkerCMD) registerWithFrontend() (string, error) {
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

	jsonBody, _ := json.Marshal(body)
	url := strings.TrimRight(cmd.RegisterTo, "/") + "/api/node/register"
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, bytes.NewReader(jsonBody))
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if cmd.RegistrationToken != "" {
		req.Header.Set("Authorization", "Bearer "+cmd.RegistrationToken)
	}
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return "", fmt.Errorf("posting to %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("registration failed with status %d", resp.StatusCode)
	}

	var result struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decoding response: %w", err)
	}
	return result.ID, nil
}

// heartbeatLoop sends periodic heartbeats to the frontend with VRAM updates.
func (cmd *WorkerCMD) heartbeatLoop(nodeID string, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	url := strings.TrimRight(cmd.RegisterTo, "/") + "/api/node/" + nodeID + "/heartbeat"
	client := &http.Client{Timeout: 5 * time.Second}

	for range ticker.C {
		// Report current VRAM usage
		var availVRAM uint64
		aggregate := xsysinfo.GetGPUAggregateInfo()
		if aggregate.TotalVRAM > 0 {
			availVRAM = aggregate.FreeVRAM
		} else {
			// Fallback: report total as available (no usage tracking possible)
			availVRAM, _ = xsysinfo.TotalAvailableVRAM()
		}

		heartbeatBody := map[string]any{
			"available_vram": availVRAM,
		}

		// If no GPU, report system RAM usage instead
		if aggregate.TotalVRAM == 0 {
			if ramInfo, err := xsysinfo.GetSystemRAMInfo(); err == nil {
				heartbeatBody["available_ram"] = ramInfo.Available
			}
		}
		jsonBody, _ := json.Marshal(heartbeatBody)

		req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, url, bytes.NewReader(jsonBody))
		req.Header.Set("Content-Type", "application/json")
		if cmd.RegistrationToken != "" {
			req.Header.Set("Authorization", "Bearer "+cmd.RegistrationToken)
		}
		resp, err := client.Do(req)
		if err != nil {
			xlog.Warn("Heartbeat failed", "error", err)
			continue
		}
		resp.Body.Close()
	}
}

// drainNode sets the node to draining status via the frontend API.
func (cmd *WorkerCMD) drainNode(nodeID string) error {
	url := strings.TrimRight(cmd.RegisterTo, "/") + "/api/node/" + nodeID + "/drain"
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, url, nil)
	if cmd.RegistrationToken != "" {
		req.Header.Set("Authorization", "Bearer "+cmd.RegistrationToken)
	}
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("drain failed with status %d", resp.StatusCode)
	}
	return nil
}

// waitForDrain polls until no in-flight requests remain or timeout.
func (cmd *WorkerCMD) waitForDrain(nodeID string) {
	url := strings.TrimRight(cmd.RegisterTo, "/") + "/api/node/" + nodeID + "/models"
	client := &http.Client{Timeout: 5 * time.Second}

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
		if cmd.RegistrationToken != "" {
			req.Header.Set("Authorization", "Bearer "+cmd.RegistrationToken)
		}
		resp, err := client.Do(req)
		if err != nil {
			break
		}
		var models []struct {
			InFlight int `json:"in_flight"`
		}
		json.NewDecoder(resp.Body).Decode(&models)
		resp.Body.Close()

		total := 0
		for _, m := range models {
			total += m.InFlight
		}
		if total == 0 {
			xlog.Info("All in-flight requests drained")
			return
		}
		xlog.Info("Waiting for in-flight requests", "count", total)
		time.Sleep(1 * time.Second)
	}
	xlog.Warn("Drain timeout reached, proceeding with shutdown")
}

// deregisterFromFrontend marks the node as offline via POST /api/node/:id/deregister.
// The node row is preserved in the database so re-registration restores approval status.
func (cmd *WorkerCMD) deregisterFromFrontend(nodeID string) error {
	url := strings.TrimRight(cmd.RegisterTo, "/") + "/api/node/" + nodeID + "/deregister"
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, url, nil)
	if cmd.RegistrationToken != "" {
		req.Header.Set("Authorization", "Bearer "+cmd.RegistrationToken)
	}
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("deregistration failed with status %d", resp.StatusCode)
	}
	return nil
}
