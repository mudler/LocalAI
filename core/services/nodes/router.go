package nodes

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	grpc "github.com/mudler/LocalAI/pkg/grpc"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/vram"
	"github.com/mudler/LocalAI/core/services/storage"
	"github.com/mudler/xlog"
)

// companionSuffixes maps a file extension to additional suffixes that should
// be staged alongside it. For example, piper TTS loads ".onnx.json" implicitly
// when given an ".onnx" model file.
var companionSuffixes = map[string][]string{
	".onnx": {".onnx.json"},
}

// SmartRouter routes inference requests to the best available backend node.
// It uses the NodeRegistry (PostgreSQL) for routing decisions.
type SmartRouter struct {
	registry      *NodeRegistry
	unloader      *RemoteUnloaderAdapter // optional, for NATS-driven load/unload
	fileStager    FileStager             // optional, for distributed file transfer
	galleriesJSON string                 // backend gallery config for dynamic installation
	authToken     string                 // gRPC bearer token for distributed auth
}

// NewSmartRouter creates a new SmartRouter backed by the given NodeRegistry.
func NewSmartRouter(registry *NodeRegistry) *SmartRouter {
	return &SmartRouter{registry: registry}
}

// SetUnloader sets the remote unloader for NATS-driven model lifecycle.
func (r *SmartRouter) SetUnloader(u *RemoteUnloaderAdapter) {
	r.unloader = u
}

// SetFileStager sets the file stager for distributed file transfer.
func (r *SmartRouter) SetFileStager(fs FileStager) {
	r.fileStager = fs
}

// SetGalleriesJSON sets the backend gallery config for dynamic backend installation.
func (r *SmartRouter) SetGalleriesJSON(g string) { r.galleriesJSON = g }

// SetAuthToken sets the gRPC bearer token for authenticating with remote backends.
func (r *SmartRouter) SetAuthToken(t string) { r.authToken = t }

// Unloader returns the remote unloader adapter for external use.
func (r *SmartRouter) Unloader() *RemoteUnloaderAdapter { return r.unloader }

// RouteResult contains the routing decision.
type RouteResult struct {
	Node    *BackendNode
	Client  grpc.Backend
	Release func() // Must be called when the request is done (decrements in-flight)
}

// Route finds the best node for the given model and backend type.
// It tries:
// 1. Nodes that already have the model loaded (least loaded first) — verified via gRPC health check
// 2. Idle-first scheduling: pick an idle node, then fall back to least-loaded.
//    Sends backend.install via NATS to ensure the right backend is running.
// Returns a RouteResult with a release function that must be called when done.
//
// modelID is the logical model identifier used for DB tracking (e.g. "qwen_qwen3.5-0.8b").
// modelName is the model file path used for gRPC LoadModel (e.g. "llama-cpp/models/Qwen_...gguf").
// When modelID is empty, modelName is used for both purposes (backward compat).
func (r *SmartRouter) Route(ctx context.Context, modelID, modelName, backendType string, modelOpts *pb.ModelOptions, parallel bool) (*RouteResult, error) {
	// Use modelID for DB tracking; fall back to modelName if empty
	trackingKey := modelID
	if trackingKey == "" {
		trackingKey = modelName
	}

	// Step 1: Find nodes that already have this model loaded
	nodes, err := r.registry.FindNodesWithModel(trackingKey)
	if err == nil && len(nodes) > 0 {
		// Pick the least loaded among them
		node := r.pickLeastLoaded(nodes)
		if node != nil {
			// Get the per-model address (the gRPC port for this model's backend process)
			nm, nmErr := r.registry.GetNodeModel(node.ID, trackingKey)
			modelAddr := node.Address // fallback to node base address
			if nmErr == nil && nm.Address != "" {
				modelAddr = nm.Address
			}

			// Verify the backend process is actually running via gRPC health check
			healthClient := r.buildClientForAddr(node, modelAddr, false)
			checkCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			if ok, _ := healthClient.HealthCheck(checkCtx); !ok {
				// Stale — clear and fall through to Step 2
				xlog.Warn("Backend not reachable for cached model, falling through to reload",
					"node", node.Name, "model", modelName)
				r.registry.RemoveNodeModel(node.ID, trackingKey)
			} else {
				// Node is alive, use it
				r.registry.TouchNodeModel(node.ID, trackingKey)
				grpcClient := r.buildClientForAddr(node, modelAddr, parallel)
				tracked := NewInFlightTrackingClient(grpcClient, r.registry, node.ID, trackingKey)
				return &RouteResult{
					Node:    node,
					Client:  tracked,
					Release: func() {},
				}, nil
			}
		}
	}

	// Step 2: Model not loaded anywhere — find a node and install the backend
	node, backendAddr, err := r.scheduleNewModel(ctx, backendType, trackingKey, modelOpts)
	if err != nil {
		return nil, fmt.Errorf("no available nodes: %w", err)
	}

	// Pre-stage model files via FileStager before loading
	if r.fileStager != nil && modelOpts != nil {
		stagedOpts, err := r.stageModelFiles(ctx, node, modelOpts)
		if err != nil {
			return nil, fmt.Errorf("staging model files for node %s: %w", node.Name, err)
		}
		modelOpts = stagedOpts
	}

	client := r.buildClientForAddr(node, backendAddr, parallel)

	// Load the model on this node
	if modelOpts != nil {
		xlog.Info("Loading model on remote node", "node", node.Name, "model", modelName, "addr", backendAddr)

		loadCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		defer cancel()

		res, err := client.LoadModel(loadCtx, modelOpts)
		if err != nil {
			return nil, fmt.Errorf("loading model %s on node %s: %w", modelName, node.Name, err)
		}
		if !res.Success {
			return nil, fmt.Errorf("loading model %s on node %s: %s", modelName, node.Name, res.Message)
		}
	}

	// Record the model as loaded on this node with its per-process address
	if err := r.registry.SetNodeModel(node.ID, trackingKey, "loaded", backendAddr); err != nil {
		xlog.Warn("Failed to record model on node", "node", node.Name, "model", trackingKey, "error", err)
	}

	tracked := NewInFlightTrackingClient(client, r.registry, node.ID, trackingKey)
	return &RouteResult{
		Node:   node,
		Client: tracked,
		Release: func() {},
	}, nil
}

// scheduleNewModel picks the best node for loading a new model.
// Strategy: VRAM-aware → idle-first → least-loaded.
// Sends backend.install via NATS so the chosen node has the right backend running.
func (r *SmartRouter) scheduleNewModel(ctx context.Context, backendType, modelID string, modelOpts *pb.ModelOptions) (*BackendNode, string, error) {
	// Estimate VRAM required for the model
	var estimatedVRAM uint64
	if modelOpts != nil {
		estimatedVRAM = r.estimateModelVRAM(ctx, modelOpts)
	}

	var node *BackendNode
	var err error

	if estimatedVRAM > 0 {
		// 1. Prefer nodes with enough VRAM (idle-first, then least-loaded)
		node, err = r.registry.FindNodeWithVRAM(estimatedVRAM)
		if err != nil {
			xlog.Warn("No nodes with enough VRAM, falling back to standard scheduling",
				"required_vram", vram.FormatBytes(estimatedVRAM), "error", err)
		}
	}

	if node == nil {
		// 2. Prefer truly idle nodes (no loaded models, no in-flight)
		node, err = r.registry.FindIdleNode()
		if err != nil {
			// 3. Fall back to least-loaded node (can run an additional backend process)
			node, err = r.registry.FindLeastLoadedNode()
		}
	}

	// 4. Preemptive eviction: if no suitable node found, evict the LRU model with zero in-flight
	if node == nil {
		evictedNode, evictErr := r.evictLRUAndFreeNode(ctx)
		if evictErr != nil {
			return nil, "", fmt.Errorf("no healthy nodes available and eviction failed: %w", evictErr)
		}
		node = evictedNode
	}

	// Send backend.install — the worker installs the backend if needed and starts the gRPC process
	addr, err := r.installBackendOnNode(ctx, node, backendType, modelID)
	if err != nil {
		return nil, "", fmt.Errorf("installing backend on node %s: %w", node.Name, err)
	}

	return node, addr, nil
}

// estimateModelVRAM estimates the VRAM required for a model using the unified estimator.
func (r *SmartRouter) estimateModelVRAM(ctx context.Context, opts *pb.ModelOptions) uint64 {
	estCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	input := vram.ModelEstimateInput{
		Options: vram.EstimateOptions{
			ContextLength: uint32(opts.ContextSize),
			GPULayers:     int(opts.NGPULayers),
		},
	}

	// Try model file as a local file for GGUF metadata estimation
	if opts.ModelFile != "" {
		if _, err := os.Stat(opts.ModelFile); err == nil {
			input.Files = append(input.Files, vram.FileInput{URI: opts.ModelFile, Size: 0})
		}
	}

	// Try HF repo from model name (e.g. "org/model")
	if opts.Model != "" {
		if repoID, ok := vram.ExtractHFRepoID(opts.Model); ok {
			input.HFRepo = repoID
		}
	}

	// If model file exists, get its size as fallback
	if opts.ModelFile != "" && len(input.Files) == 0 {
		if info, err := os.Stat(opts.ModelFile); err == nil {
			return vram.EstimateFromSize(uint64(info.Size())).VRAMBytes
		}
	}

	if len(input.Files) == 0 && input.HFRepo == "" && input.Size == "" {
		return 0
	}

	result, err := vram.EstimateModel(estCtx, input)
	if err != nil || result.VRAMBytes == 0 {
		// Last resort: try model file size
		if opts.ModelFile != "" {
			if info, statErr := os.Stat(opts.ModelFile); statErr == nil {
				return vram.EstimateFromSize(uint64(info.Size())).VRAMBytes
			}
		}
		return 0
	}
	return result.VRAMBytes
}

// installBackendOnNode sends a NATS backend.install request-reply to the node.
// The worker installs the backend from gallery (if not already installed),
// starts the gRPC process, and replies when ready.
// installBackendOnNode installs a backend on a node and returns the gRPC address.
func (r *SmartRouter) installBackendOnNode(ctx context.Context, node *BackendNode, backendType, modelID string) (string, error) {
	if r.unloader == nil {
		return "", fmt.Errorf("no NATS connection for backend installation")
	}

	reply, err := r.unloader.InstallBackend(node.ID, backendType, modelID, r.galleriesJSON)
	if err != nil {
		return "", err
	}
	if !reply.Success {
		return "", fmt.Errorf("worker replied with error: %s", reply.Error)
	}
	// Return the backend's gRPC address (new: per-process port from worker)
	addr := reply.Address
	if addr == "" {
		addr = node.Address // fallback to node base address
	}
	return addr, nil
}

// buildClient creates a gRPC client for a node, optionally wrapping it with
// a FileStagingClient for transparent file transfer.
func (r *SmartRouter) buildClient(node *BackendNode, parallel bool) grpc.Backend {
	return r.buildClientForAddr(node, node.Address, parallel)
}

func (r *SmartRouter) buildClientForAddr(node *BackendNode, addr string, parallel bool) grpc.Backend {
	var client grpc.Backend
	if r.authToken != "" {
		client = grpc.NewClientWithToken(addr, parallel, nil, false, r.authToken)
	} else {
		client = grpc.NewClient(addr, parallel, nil, false)
	}

	// Wrap with file staging if configured
	if r.fileStager != nil {
		return NewFileStagingClient(client, r.fileStager, node.ID)
	}
	return client
}

// stageModelFiles uploads model files to the backend node via the FileStager.
// Returns the ModelOptions with ModelFile and similar direct-path fields rewritten
// to absolute remote paths. Generic options (vae_path, etc.) are left as relative
// paths — backends resolve them via ModelPath.
func (r *SmartRouter) stageModelFiles(ctx context.Context, node *BackendNode, opts *pb.ModelOptions) (*pb.ModelOptions, error) {
	xlog.Debug("Staging model files for remote node", "node", node.Name, "modelFile", opts.ModelFile)

	// Derive the frontend models directory from ModelFile and Model.
	// Example: ModelFile="/models/sd-cpp/models/flux.gguf", Model="sd-cpp/models/flux.gguf"
	// → frontendModelsDir="/models"
	frontendModelsDir := ""
	if opts.ModelFile != "" && opts.Model != "" {
		frontendModelsDir = filepath.Clean(strings.TrimSuffix(opts.ModelFile, opts.Model))
	}

	// relKey computes a storage key preserving subdirectory structure relative
	// to the frontend models dir. Falls back to basename if Rel fails.
	relKey := func(localPath string) string {
		if frontendModelsDir != "" {
			if rel, err := filepath.Rel(frontendModelsDir, localPath); err == nil && !strings.HasPrefix(rel, "..") && rel != "." {
				return storage.ModelKey(rel)
			}
		}
		return storage.ModelKey(filepath.Base(localPath))
	}

	// Stage each model file path field. These fields are used directly by the
	// gRPC LoadModel call, so they must be rewritten to the absolute remote path.
	type pathField struct {
		name string
		val  *string
	}
	fields := []pathField{
		{"ModelFile", &opts.ModelFile},
		{"MMProj", &opts.MMProj},
		{"LoraAdapter", &opts.LoraAdapter},
		{"DraftModel", &opts.DraftModel},
		{"CLIPModel", &opts.CLIPModel},
		{"Tokenizer", &opts.Tokenizer},
		{"AudioPath", &opts.AudioPath},
	}

	for _, f := range fields {
		if *f.val == "" {
			continue
		}
		// Skip non-existent files
		if _, err := os.Stat(*f.val); os.IsNotExist(err) {
			xlog.Debug("Skipping staging for non-existent path", "field", f.name, "path", *f.val)
			*f.val = ""
			continue
		}
		localPath := *f.val
		key := relKey(localPath)
		remotePath, err := r.fileStager.EnsureRemote(ctx, node.ID, localPath, key)
		if err != nil {
			// ModelFile is required — fail the whole operation
			if f.name == "ModelFile" {
				return nil, fmt.Errorf("staging model file: %w", err)
			}
			// Optional files: clear the path so the backend doesn't try a non-existent frontend path
			xlog.Warn("Failed to stage model file, clearing field", "field", f.name, "path", localPath, "error", err)
			*f.val = ""
			continue
		}
		xlog.Debug("Staged model field", "field", f.name, "remotePath", remotePath)
		*f.val = remotePath

		// Derive ModelPath from the first staged file (ModelFile).
		// remotePath = "/models/sd-cpp/models/flux.gguf", relFromModels = "sd-cpp/models/flux.gguf"
		// → ModelPath = "/models" (the worker's equivalent of frontendModelsDir)
		if f.name == "ModelFile" && opts.Model != "" {
			opts.ModelPath = filepath.Clean(strings.TrimSuffix(remotePath, opts.Model))
			xlog.Debug("Derived remote ModelPath", "modelPath", opts.ModelPath)
		}

		r.stageCompanionFiles(ctx, node, localPath, frontendModelsDir)
	}

	// Handle LoraAdapters (array) — rewritten to absolute remote paths
	staged := opts.LoraAdapters[:0]
	for _, adapter := range opts.LoraAdapters {
		if adapter == "" {
			continue
		}
		if _, err := os.Stat(adapter); os.IsNotExist(err) {
			xlog.Debug("Skipping staging for non-existent lora adapter", "path", adapter)
			continue
		}
		key := relKey(adapter)
		remotePath, err := r.fileStager.EnsureRemote(ctx, node.ID, adapter, key)
		if err != nil {
			xlog.Warn("Failed to stage lora adapter, skipping", "path", adapter, "error", err)
			continue
		}
		staged = append(staged, remotePath)
	}
	opts.LoraAdapters = staged

	// Handle LoraBase field — rewritten to absolute remote path
	if opts.LoraBase != "" {
		if _, err := os.Stat(opts.LoraBase); err == nil {
			key := relKey(opts.LoraBase)
			if remotePath, err := r.fileStager.EnsureRemote(ctx, node.ID, opts.LoraBase, key); err == nil {
				opts.LoraBase = remotePath
			} else {
				xlog.Warn("Failed to stage LoraBase, clearing field", "path", opts.LoraBase, "error", err)
				opts.LoraBase = ""
			}
		}
	}

	// Stage file paths referenced in generic Options (key:value pairs where values
	// are file paths). Options stay as relative paths — backends resolve them via ModelPath.
	r.stageGenericOptions(ctx, node, opts.Options, frontendModelsDir)
	r.stageGenericOptions(ctx, node, opts.Overrides, frontendModelsDir)

	return opts, nil
}

// stageCompanionFiles stages known companion files that exist alongside
// localPath. For example, piper TTS implicitly loads ".onnx.json" next to
// the ".onnx" model file. Errors are logged but not propagated.
func (r *SmartRouter) stageCompanionFiles(ctx context.Context, node *BackendNode, localPath, frontendModelsDir string) {
	ext := filepath.Ext(localPath)
	suffixes, ok := companionSuffixes[ext]
	if !ok {
		return
	}
	base := strings.TrimSuffix(localPath, ext)
	for _, suffix := range suffixes {
		companion := base + suffix
		if _, err := os.Stat(companion); err != nil {
			continue
		}
		// Preserve subdirectory structure in the key
		key := storage.ModelKey(filepath.Base(companion))
		if frontendModelsDir != "" {
			if rel, err := filepath.Rel(frontendModelsDir, companion); err == nil && !strings.HasPrefix(rel, "..") {
				key = storage.ModelKey(rel)
			}
		}
		if _, err := r.fileStager.EnsureRemote(ctx, node.ID, companion, key); err != nil {
			xlog.Warn("Failed to stage companion file", "path", companion, "error", err)
		} else {
			xlog.Debug("Staged companion file", "path", companion)
		}
	}
}

// stageGenericOptions iterates key:value option strings and stages any values
// that resolve to existing files relative to the frontend models directory.
// Option values are NOT rewritten — backends resolve them via ModelPath.
func (r *SmartRouter) stageGenericOptions(ctx context.Context, node *BackendNode, options []string, frontendModelsDir string) {
	for _, opt := range options {
		parts := strings.SplitN(opt, ":", 2)
		if len(parts) != 2 || parts[1] == "" {
			continue
		}
		val := parts[1]

		// Check if value is an existing file path (absolute or relative to frontend models dir)
		absPath := val
		if !filepath.IsAbs(val) && frontendModelsDir != "" {
			absPath = filepath.Join(frontendModelsDir, val)
		}
		if _, err := os.Stat(absPath); os.IsNotExist(err) {
			continue
		}

		// Stage the file to the worker, preserving subdirectory structure
		key := storage.ModelKey(filepath.Base(absPath))
		if frontendModelsDir != "" {
			if rel, err := filepath.Rel(frontendModelsDir, absPath); err == nil && !strings.HasPrefix(rel, "..") {
				key = storage.ModelKey(rel)
			}
		}
		if _, err := r.fileStager.EnsureRemote(ctx, node.ID, absPath, key); err != nil {
			xlog.Warn("Failed to stage option file, skipping", "option", opt, "path", absPath, "error", err)
			continue
		}
		// Leave option value unchanged — backend resolves relative paths via ModelPath
		xlog.Debug("Staged option file", "option", parts[0], "localPath", absPath)
	}
}

// UnloadModel sends a NATS unload event to the node hosting the model.
// The worker process handles Free() + kill + deregister.
func (r *SmartRouter) UnloadModel(nodeID, modelName string) error {
	if r.unloader == nil {
		return fmt.Errorf("no remote unloader configured")
	}
	return r.unloader.UnloadRemoteModel(modelName)
}

// EvictLRU evicts the least-recently-used model from a node to make room.
// Returns the name of the evicted model, or empty string if nothing could be evicted.
func (r *SmartRouter) EvictLRU(nodeID string) (string, error) {
	lru, err := r.registry.FindLRUModel(nodeID)
	if err != nil {
		return "", fmt.Errorf("finding LRU model on node %s: %w", nodeID, err)
	}

	if err := r.UnloadModel(nodeID, lru.ModelName); err != nil {
		return "", err
	}
	return lru.ModelName, nil
}

// evictLRUAndFreeNode finds the globally least-recently-used model with zero in-flight,
// unloads it, and returns its node for reuse. If all models are busy, waits with timeout.
func (r *SmartRouter) evictLRUAndFreeNode(ctx context.Context) (*BackendNode, error) {
	const maxRetries = 30
	const retryInterval = time.Second

	for attempt := 0; attempt < maxRetries; attempt++ {
		lru, err := r.registry.FindGlobalLRUModelWithZeroInFlight()
		if err == nil {
			xlog.Info("Evicting LRU model to free capacity",
				"node", lru.NodeID, "model", lru.ModelName, "lastUsed", lru.LastUsed)

			// Unload the model (gRPC Free to release VRAM)
			if r.unloader != nil {
				// Send unload with the model's backend address
				r.unloader.UnloadModelOnNode(lru.NodeID, lru.ModelName)
			}
			r.registry.RemoveNodeModel(lru.NodeID, lru.ModelName)

			// Return the node that was freed
			node, nodeErr := r.registry.Get(lru.NodeID)
			if nodeErr != nil {
				return nil, fmt.Errorf("node %s not found after eviction: %w", lru.NodeID, nodeErr)
			}
			return node, nil
		}

		// All models have in-flight requests — wait and retry
		if attempt == 0 {
			xlog.Info("All models have in-flight requests, waiting for capacity")
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("context cancelled while waiting for eviction")
		case <-time.After(retryInterval):
			// retry
		}
	}

	return nil, fmt.Errorf("no evictable models after %d retries (all models busy)", maxRetries)
}

// pickLeastLoaded selects the node with the fewest in-flight requests from a list.
func (r *SmartRouter) pickLeastLoaded(nodes []BackendNode) *BackendNode {
	if len(nodes) == 0 {
		return nil
	}
	// Simple: just pick the first one (they come from the DB query which can be ordered)
	return &nodes[0]
}
