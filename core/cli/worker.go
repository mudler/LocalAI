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
	"github.com/mudler/LocalAI/core/services/galleryop"
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
	// Primary address — the reachable address of this worker.
	// Host is used for advertise, port is the base for gRPC backends.
	// HTTP file transfer runs on port-1.
	Addr      string `env:"LOCALAI_ADDR" help:"Address where this worker is reachable (host:port). Port is base for gRPC backends, port-1 for HTTP." group:"server"`
	ServeAddr string `env:"LOCALAI_SERVE_ADDR" default:"0.0.0.0:50051" help:"(Advanced) gRPC base port bind address" group:"server" hidden:""`

	BackendsPath       string `env:"LOCALAI_BACKENDS_PATH,BACKENDS_PATH" type:"path" default:"${basepath}/backends" help:"Path containing backends" group:"server"`
	BackendsSystemPath string `env:"LOCALAI_BACKENDS_SYSTEM_PATH" type:"path" default:"/var/lib/local-ai/backends" help:"Path containing system backends" group:"server"`
	BackendGalleries   string `env:"LOCALAI_BACKEND_GALLERIES,BACKEND_GALLERIES" help:"JSON list of backend galleries" group:"server" default:"${backends}"`
	ModelsPath         string `env:"LOCALAI_MODELS_PATH,MODELS_PATH" type:"path" default:"${basepath}/models" help:"Path containing models" group:"server"`

	// HTTP file transfer
	HTTPAddr          string `env:"LOCALAI_HTTP_ADDR" default:"" help:"HTTP file transfer server address (default: gRPC port + 1)" group:"server" hidden:""`
	AdvertiseHTTPAddr string `env:"LOCALAI_ADVERTISE_HTTP_ADDR" help:"HTTP address the frontend uses to reach this node for file transfer" group:"server" hidden:""`

	// Registration (required)
	AdvertiseAddr     string `env:"LOCALAI_ADVERTISE_ADDR" help:"Address the frontend uses to reach this node (defaults to hostname:port from Addr)" group:"registration" hidden:""`
	RegisterTo        string `env:"LOCALAI_REGISTER_TO" required:"" help:"Frontend URL for registration" group:"registration"`
	NodeName          string `env:"LOCALAI_NODE_NAME" help:"Node name for registration (defaults to hostname)" group:"registration"`
	RegistrationToken string `env:"LOCALAI_REGISTRATION_TOKEN" help:"Token for authenticating with the frontend" group:"registration"`
	HeartbeatInterval string `env:"LOCALAI_HEARTBEAT_INTERVAL" default:"10s" help:"Interval between heartbeats" group:"registration"`
	NodeLabels        string `env:"LOCALAI_NODE_LABELS" help:"Comma-separated key=value labels for this node (e.g. tier=fast,gpu=a100)" group:"registration"`
	// MaxReplicasPerModel caps how many replicas of any one model can run on
	// this worker concurrently. Default 1 = historical single-replica
	// behavior. Set higher when a node has enough VRAM to host multiple
	// copies of the same model (e.g. a fat 128 GiB box running 4× of a
	// 24 GiB model for throughput). The auto-label `node.replica-slots=N`
	// is published so model schedulers can target high-capacity nodes via
	// the existing label selector.
	MaxReplicasPerModel int `env:"LOCALAI_MAX_REPLICAS_PER_MODEL" default:"1" help:"Max replicas of any single model on this worker. Default 1 preserves single-replica behavior; set higher to allow stacking replicas on a fat node." group:"registration"`

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
	xlog.Info("Starting worker", "advertise", cmd.advertiseAddr(), "basePort", cmd.effectiveBasePort())

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
	basePort := cmd.effectiveBasePort()
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

	// Wait for the gRPC server to be ready before reporting success.
	// Slow nodes (Jetson Orin doing first-boot CUDA init, large CGO libs)
	// can take 10-15s before the gRPC port accepts connections; the previous
	// 4s window made the worker reply Success on a not-yet-listening port,
	// which manifested upstream as "connect: connection refused" on the
	// frontend's first LoadModel dial.
	client := grpc.NewClientWithToken(clientAddr, false, nil, false, s.cmd.RegistrationToken)
	const (
		readinessPollInterval = 200 * time.Millisecond
		readinessTimeout      = 30 * time.Second
	)
	deadline := time.Now().Add(readinessTimeout)
	for time.Now().Before(deadline) {
		time.Sleep(readinessPollInterval)
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

		// Check if the process died (e.g. OOM, CUDA error, missing libs)
		if !proc.IsAlive() {
			stderrTail := readLastLinesFromFile(proc.StderrPath(), 20)
			xlog.Warn("Backend process died during startup", "backend", backend, "stderr", stderrTail)
			s.mu.Lock()
			delete(s.processes, backend)
			s.freePorts = append(s.freePorts, port)
			s.mu.Unlock()
			return "", fmt.Errorf("backend process %s died during startup. Last stderr:\n%s", backend, stderrTail)
		}
	}

	// Readiness deadline exceeded. Returning success here would leave the
	// frontend with an unbound address (it dials, gets ECONNREFUSED, and
	// the operator sees a misleading "connection refused" instead of the
	// real cause). Stop the half-started process, recycle the port, and
	// surface the failure to the caller with the backend's stderr tail.
	stderrTail := readLastLinesFromFile(proc.StderrPath(), 20)
	xlog.Error("Backend gRPC server not ready before deadline; aborting install", "backend", backend, "addr", clientAddr, "timeout", readinessTimeout, "stderr", stderrTail)
	if killErr := proc.Stop(); killErr != nil {
		xlog.Warn("Failed to stop unready backend process", "backend", backend, "error", killErr)
	}
	s.mu.Lock()
	if cur, ok := s.processes[backend]; ok && cur == bp {
		delete(s.processes, backend)
		s.freePorts = append(s.freePorts, port)
	}
	s.mu.Unlock()
	return "", fmt.Errorf("backend %s did not become ready within %s. Last stderr:\n%s", backend, readinessTimeout, stderrTail)
}

// resolveProcessKeys turns a caller-supplied identifier into the set of
// process map keys it refers to. PR #9583 changed s.processes to be keyed by
// `modelID#replicaIndex`, but external NATS handlers still pass the bare
// model ID — without this resolver, those lookups silently no-op'd, so
// admin "Unload model" / "Delete backend" left the worker process alive.
//
//   - Exact match wins. Callers that already know the full processKey
//     (stopAllBackends iterating its own map) get exactly that entry.
//   - Else, an identifier without `#` is treated as a model prefix and
//     every `id#N` replica is returned.
//   - An identifier that contains `#` but doesn't match anything returns
//     nothing — no spurious prefix fallback when the caller was explicit.
func (s *backendSupervisor) resolveProcessKeys(id string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.processes[id]; ok {
		return []string{id}
	}
	if strings.Contains(id, "#") {
		return nil
	}
	prefix := id + "#"
	var keys []string
	for k := range s.processes {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	return keys
}

// stopBackend stops the backend process(es) matching the given identifier.
// Accepts a bare modelID (stops every replica) or a full processKey
// (stops just that replica).
func (s *backendSupervisor) stopBackend(id string) {
	for _, key := range s.resolveProcessKeys(id) {
		s.stopBackendExact(key)
	}
}

// stopBackendExact stops the process under exactly this key. Locking and
// network I/O are split: the map mutation runs under the lock, the gRPC
// Free() and proc.Stop() calls run after release so they don't block
// other supervisor operations.
func (s *backendSupervisor) stopBackendExact(key string) {
	s.mu.Lock()
	bp, ok := s.processes[key]
	if !ok || bp.proc == nil {
		s.mu.Unlock()
		return
	}
	delete(s.processes, key)
	if _, portStr, err := net.SplitHostPort(bp.addr); err == nil {
		if p, err := strconv.Atoi(portStr); err == nil {
			s.freePorts = append(s.freePorts, p)
		}
	}
	s.mu.Unlock()

	client := grpc.NewClientWithToken(bp.addr, false, nil, false, s.cmd.RegistrationToken)
	xlog.Debug("Calling Free() before stopping backend", "backend", key)
	if err := client.Free(context.Background()); err != nil {
		xlog.Warn("Free() failed (best-effort)", "backend", key, "error", err)
	}

	xlog.Info("Stopping backend process", "backend", key, "addr", bp.addr)
	if err := bp.proc.Stop(); err != nil {
		xlog.Error("Error stopping backend process", "backend", key, "error", err)
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

// readLastLinesFromFile reads the last n lines from a file.
// Returns an empty string if the file cannot be read.
func readLastLinesFromFile(path string, n int) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// isRunning returns whether at least one backend process matching the given
// identifier is currently running. Accepts a bare modelID (matches any
// replica) or a full processKey (exact match). Callers like the
// backend.delete pre-check rely on the bare-name path.
func (s *backendSupervisor) isRunning(id string) bool {
	keys := s.resolveProcessKeys(id)
	if len(keys) == 0 {
		// Same lock-free zero-process check the caller would have done.
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, key := range keys {
		if bp, ok := s.processes[key]; ok && bp.proc != nil && bp.proc.IsAlive() {
			return true
		}
	}
	return false
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

// buildProcessKey is the supervisor's stable identifier for a backend gRPC
// process. It includes the replica index so the same model can run multiple
// processes on a worker simultaneously without colliding on the same map slot
// or port. The "#N" suffix is purely internal — the controller never reads it.
func buildProcessKey(modelID, backend string, replicaIndex int) string {
	base := modelID
	if base == "" {
		base = backend
	}
	return fmt.Sprintf("%s#%d", base, replicaIndex)
}

// installBackend handles the backend.install flow:
// 1. If already running for this (model, replica) slot, return existing address
// 2. Install backend from gallery (if not already installed)
// 3. Find backend binary
// 4. Start gRPC process on a new port
// Returns the gRPC address of the backend process.
//
// ProcessKey includes the replica index so a worker with MaxReplicasPerModel>1
// can host multiple processes for the same model on distinct ports. Old
// controllers (no replica_index in the request) implicitly target replica 0,
// which preserves single-replica behavior.
func (s *backendSupervisor) installBackend(req messaging.BackendInstallRequest) (string, error) {
	processKey := buildProcessKey(req.ModelID, req.Backend, int(req.ReplicaIndex))

	// If already running for this model+replica, return its address
	if addr := s.getAddr(processKey); addr != "" {
		xlog.Info("Backend already running for model replica", "backend", req.Backend, "model", req.ModelID, "replica", req.ReplicaIndex, "addr", addr)
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
		if req.URI != "" {
			xlog.Info("Backend not found locally, attempting external install", "backend", req.Backend, "uri", req.URI)
			if err := galleryop.InstallExternalBackend(
				context.Background(), galleries, s.systemState, s.ml, nil, req.URI, req.Name, req.Alias,
			); err != nil {
				return "", fmt.Errorf("installing backend from gallery: %w", err)
			}
		} else {
			xlog.Info("Backend not found locally, attempting gallery install", "backend", req.Backend)
			if err := gallery.InstallBackendFromGallery(
				context.Background(), galleries, s.systemState, s.ml, req.Backend, nil, false,
			); err != nil {
				return "", fmt.Errorf("installing backend from gallery: %w", err)
			}
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
		advAddr := s.cmd.advertiseAddr()
		if advAddr != addr { // only remap if advertise differs from bind
			_, port, _ := net.SplitHostPort(addr)
			advertiseHost, _, _ := net.SplitHostPort(advAddr)
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
		var req messaging.BackendDeleteRequest
		if err := json.Unmarshal(data, &req); err != nil {
			resp := messaging.BackendDeleteReply{Success: false, Error: fmt.Sprintf("invalid request: %v", err)}
			replyJSON(reply, resp)
			return
		}
		xlog.Info("Received NATS backend.delete event", "backend", req.Backend)

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
				info.Version = b.Metadata.Version
				info.URI = b.Metadata.URI
				info.Digest = b.Metadata.Digest
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
			if err := client.Free(context.Background()); err != nil {
				xlog.Warn("Free() failed during model.unload", "error", err, "addr", targetAddr)
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

// effectiveBasePort returns the port used as base for gRPC backend processes.
// Priority: Addr port → ServeAddr port → 50051
func (cmd *WorkerCMD) effectiveBasePort() int {
	for _, addr := range []string{cmd.Addr, cmd.ServeAddr} {
		if addr != "" {
			if _, portStr, ok := strings.Cut(addr, ":"); ok {
				if p, _ := strconv.Atoi(portStr); p > 0 {
					return p
				}
			}
		}
	}
	return 50051
}

// advertiseAddr returns the address the frontend should use to reach this node.
func (cmd *WorkerCMD) advertiseAddr() string {
	if cmd.AdvertiseAddr != "" {
		return cmd.AdvertiseAddr
	}
	if cmd.Addr != "" {
		return cmd.Addr
	}
	hostname, _ := os.Hostname()
	return fmt.Sprintf("%s:%d", cmp.Or(hostname, "localhost"), cmd.effectiveBasePort())
}

// resolveHTTPAddr returns the address to bind the HTTP file transfer server to.
// Uses basePort-1 so it doesn't conflict with dynamically allocated gRPC ports
// which grow upward from basePort.
func (cmd *WorkerCMD) resolveHTTPAddr() string {
	if cmd.HTTPAddr != "" {
		return cmd.HTTPAddr
	}
	return fmt.Sprintf("0.0.0.0:%d", cmd.effectiveBasePort()-1)
}

// advertiseHTTPAddr returns the HTTP address the frontend should use to reach
// this node for file transfer.
func (cmd *WorkerCMD) advertiseHTTPAddr() string {
	if cmd.AdvertiseHTTPAddr != "" {
		return cmd.AdvertiseHTTPAddr
	}
	advHost, _, _ := strings.Cut(cmd.advertiseAddr(), ":")
	httpPort := cmd.effectiveBasePort() - 1
	return fmt.Sprintf("%s:%d", advHost, httpPort)
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

	maxReplicas := cmd.MaxReplicasPerModel
	if maxReplicas < 1 {
		maxReplicas = 1
	}
	body := map[string]any{
		"name":                   nodeName,
		"address":                cmd.advertiseAddr(),
		"http_address":           cmd.advertiseHTTPAddr(),
		"total_vram":             totalVRAM,
		"available_vram":         totalVRAM, // initially all VRAM is available
		"gpu_vendor":             gpuVendor,
		"max_replicas_per_model": maxReplicas,
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

	// Parse and add static node labels. Always include the auto-label
	// `node.replica-slots=N` so AND-selectors in ModelSchedulingConfig can
	// target high-capacity nodes (e.g. {"node.replica-slots":"4"}).
	labels := make(map[string]string)
	if cmd.NodeLabels != "" {
		for _, pair := range strings.Split(cmd.NodeLabels, ",") {
			pair = strings.TrimSpace(pair)
			if k, v, ok := strings.Cut(pair, "="); ok {
				labels[strings.TrimSpace(k)] = strings.TrimSpace(v)
			}
		}
	}
	labels["node.replica-slots"] = strconv.Itoa(maxReplicas)
	body["labels"] = labels

	return body
}

// heartbeatBody returns the current VRAM/RAM stats for heartbeat payloads.
//
// When aggregate VRAM usage is unknown (no GPU, or temporary detection
// failure), we deliberately OMIT available_vram so the frontend keeps its
// last good value — overwriting with 0 makes the UI show the node as "fully
// used", while reporting total-as-available lies to the scheduler about
// free capacity.
func (cmd *WorkerCMD) heartbeatBody() map[string]any {
	body := map[string]any{}
	aggregate := xsysinfo.GetGPUAggregateInfo()
	if aggregate.TotalVRAM > 0 {
		body["available_vram"] = aggregate.FreeVRAM
	}

	// CPU-only workers (or workers that lost GPU visibility momentarily):
	// report system RAM so the scheduler still has capacity info.
	if aggregate.TotalVRAM == 0 {
		if ramInfo, err := xsysinfo.GetSystemRAMInfo(); err == nil {
			body["available_ram"] = ramInfo.Available
		}
	}
	return body
}
