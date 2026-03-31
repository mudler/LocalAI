package nodes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/advisorylock"
	grpc "github.com/mudler/LocalAI/pkg/grpc"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/vram"
	"github.com/mudler/xlog"
	"google.golang.org/protobuf/proto"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// companionSuffixes maps a file extension to additional suffixes that should
// be staged alongside it. For example, piper TTS loads ".onnx.json" implicitly
// when given an ".onnx" model file.
var companionSuffixes = map[string][]string{
	".onnx": {".onnx.json"},
}

// SmartRouterOptions holds all dependencies for constructing a SmartRouter.
// Passing them at construction time eliminates data races from post-creation setters.
type SmartRouterOptions struct {
	Unloader      NodeCommandSender
	FileStager    FileStager
	GalleriesJSON string
	AuthToken     string
	ClientFactory BackendClientFactory // optional; defaults to tokenClientFactory
	DB            *gorm.DB             // for advisory locks during routing
}

// SmartRouter routes inference requests to the best available backend node.
// It uses the ModelRouter interface (backed by NodeRegistry in production) for routing decisions.
type SmartRouter struct {
	registry      ModelRouter
	unloader      NodeCommandSender    // optional, for NATS-driven load/unload
	fileStager    FileStager           // optional, for distributed file transfer
	galleriesJSON string               // backend gallery config for dynamic installation
	clientFactory BackendClientFactory // creates gRPC backend clients
	db            *gorm.DB             // for advisory locks during routing
}

// NewSmartRouter creates a new SmartRouter backed by the given ModelRouter.
// All optional dependencies are passed via SmartRouterOptions to avoid post-creation races.
func NewSmartRouter(registry ModelRouter, opts SmartRouterOptions) *SmartRouter {
	factory := opts.ClientFactory
	if factory == nil {
		factory = &tokenClientFactory{token: opts.AuthToken}
	}
	return &SmartRouter{
		registry:      registry,
		unloader:      opts.Unloader,
		fileStager:    opts.FileStager,
		galleriesJSON: opts.GalleriesJSON,
		clientFactory: factory,
		db:            opts.DB,
	}
}

// Unloader returns the remote unloader adapter for external use.
func (r *SmartRouter) Unloader() NodeCommandSender { return r.unloader }

// ScheduleAndLoadModel implements ModelScheduler for the reconciler.
// It schedules a model on a suitable node (optionally from candidates) and loads it.
func (r *SmartRouter) ScheduleAndLoadModel(ctx context.Context, modelName string, candidateNodeIDs []string) (*BackendNode, error) {
	// Use scheduleNewModel with empty backend type and nil model options.
	// The reconciler doesn't know the backend type — it will be determined by the model config.
	node, _, err := r.scheduleNewModel(ctx, "", modelName, nil)
	if err != nil {
		return nil, err
	}
	return node, nil
}

// RouteResult contains the routing decision.
type RouteResult struct {
	Node    *BackendNode
	Client  grpc.Backend
	Release func() // Must be called when the request is done (decrements in-flight)
}

// Route finds the best node for the given model and backend type.
// It tries:
//  1. Nodes that already have the model loaded (least loaded first) — verified via gRPC health check
//  2. Idle-first scheduling: pick an idle node, then fall back to least-loaded.
//     Sends backend.install via NATS to ensure the right backend is running.
//
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

	// Step 1: Find and atomically lock a node with this model loaded
	node, nm, err := r.registry.FindAndLockNodeWithModel(ctx, trackingKey)
	if err == nil && node != nil {
		modelAddr := node.Address
		if nm.Address != "" {
			modelAddr = nm.Address
		}

		// Verify the backend process is still alive via gRPC health check
		if !r.probeHealth(ctx, node, modelAddr) {
			// Stale — roll back the increment, remove the model record, fall through
			r.registry.DecrementInFlight(ctx, node.ID, trackingKey)
			r.registry.RemoveNodeModel(ctx, node.ID, trackingKey)
			xlog.Warn("Backend not reachable for cached model, falling through to reload",
				"node", node.Name, "model", modelName)
		} else {
			// Verify node still matches scheduling constraints
			if !r.nodeMatchesScheduling(ctx, node, trackingKey) {
				r.registry.DecrementInFlight(ctx, node.ID, trackingKey)
				xlog.Info("Cached model on node that no longer matches selector, falling through",
					"node", node.Name, "model", trackingKey)
				// Fall through to step 2 (scheduleNewModel)
			} else {
				// Node is alive — FindAndLockNodeWithModel already incremented in-flight as a
				// reservation. InFlightTrackingClient handles per-inference tracking, and its
				// onFirstComplete callback releases the reservation after the first inference
				// call finishes, so in-flight returns to 0 when idle.
				r.registry.TouchNodeModel(ctx, node.ID, trackingKey)
				grpcClient := r.buildClientForAddr(node, modelAddr, parallel)
				tracked := NewInFlightTrackingClient(grpcClient, r.registry, node.ID, trackingKey)
				tracked.OnFirstComplete(func() {
					r.registry.DecrementInFlight(context.Background(), node.ID, trackingKey)
				})
				return &RouteResult{
					Node:   node,
					Client: tracked,
					Release: func() {
						closeClient(grpcClient)
					},
				}, nil
			}
		}
	}

	// Step 2: Model not loaded — schedule loading with distributed lock to prevent duplicates
	loadModel := func() (*RouteResult, error) {
		// Re-check after acquiring lock — another request may have loaded it
		node, nm, err := r.registry.FindAndLockNodeWithModel(ctx, trackingKey)
		if err == nil && node != nil {
			modelAddr := node.Address
			if nm.Address != "" {
				modelAddr = nm.Address
			}

			// Verify the backend process is still alive via gRPC health check
			if !r.probeHealth(ctx, node, modelAddr) {
				// Stale — roll back the increment, remove the model record, continue loading
				r.registry.DecrementInFlight(ctx, node.ID, trackingKey)
				r.registry.RemoveNodeModel(ctx, node.ID, trackingKey)
				xlog.Warn("Backend not reachable for cached model inside lock, proceeding to load",
					"node", node.Name, "model", modelName)
			} else {
				// Verify node still matches scheduling constraints
				if !r.nodeMatchesScheduling(ctx, node, trackingKey) {
					r.registry.DecrementInFlight(ctx, node.ID, trackingKey)
					xlog.Info("Cached model on node that no longer matches selector, falling through",
						"node", node.Name, "model", trackingKey)
					// Fall through to scheduling below
				} else {
					// Model loaded while we waited — FindAndLockNodeWithModel already incremented
					// in-flight as a reservation. Release it after the first inference completes.
					r.registry.TouchNodeModel(ctx, node.ID, trackingKey)
					grpcClient := r.buildClientForAddr(node, modelAddr, parallel)
					tracked := NewInFlightTrackingClient(grpcClient, r.registry, node.ID, trackingKey)
					tracked.OnFirstComplete(func() {
						r.registry.DecrementInFlight(context.Background(), node.ID, trackingKey)
					})
					return &RouteResult{
						Node:   node,
						Client: tracked,
						Release: func() {
							closeClient(grpcClient)
						},
					}, nil
				}
			}
		}

		// Still not loaded — proceed with scheduling
		node, backendAddr, err := r.scheduleNewModel(ctx, backendType, trackingKey, modelOpts)
		if err != nil {
			return nil, fmt.Errorf("no available nodes: %w", err)
		}

		// Pre-stage model files via FileStager before loading
		if r.fileStager != nil && modelOpts != nil {
			stagedOpts, err := r.stageModelFiles(ctx, node, modelOpts, trackingKey)
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
		if err := r.registry.SetNodeModel(ctx, node.ID, trackingKey, "loaded", backendAddr, 1); err != nil {
			xlog.Warn("Failed to record model on node", "node", node.Name, "model", trackingKey, "error", err)
		}

		tracked := NewInFlightTrackingClient(client, r.registry, node.ID, trackingKey)
		tracked.OnFirstComplete(func() {
			r.registry.DecrementInFlight(context.Background(), node.ID, trackingKey)
		})
		return &RouteResult{
			Node:   node,
			Client: tracked,
			Release: func() {
				closeClient(client)
			},
		}, nil
	}

	if r.db != nil {
		lockKey := advisorylock.KeyFromString("model-load:" + trackingKey)
		var result *RouteResult
		lockErr := advisorylock.WithLockCtx(ctx, r.db, lockKey, func() error {
			var err error
			result, err = loadModel()
			return err
		})
		if lockErr != nil {
			return nil, fmt.Errorf("loading model %s: %w", trackingKey, lockErr)
		}
		return result, nil
	}
	// No DB (non-distributed) — proceed without lock
	return loadModel()
}

// parseSelectorJSON decodes a JSON node selector string into a map.
func parseSelectorJSON(selectorJSON string) map[string]string {
	if selectorJSON == "" {
		return nil
	}
	var selector map[string]string
	if err := json.Unmarshal([]byte(selectorJSON), &selector); err != nil {
		xlog.Warn("Failed to parse node selector", "selector", selectorJSON, "error", err)
		return nil
	}
	return selector
}

func extractNodeIDs(nodes []BackendNode) []string {
	ids := make([]string, len(nodes))
	for i, n := range nodes {
		ids[i] = n.ID
	}
	return ids
}

// nodeMatchesScheduling checks if a node satisfies the scheduling constraints for a model.
// Returns true if no constraints exist or the node matches all selector labels.
func (r *SmartRouter) nodeMatchesScheduling(ctx context.Context, node *BackendNode, modelName string) bool {
	sched, err := r.registry.GetModelScheduling(ctx, modelName)
	if err != nil || sched == nil || sched.NodeSelector == "" {
		return true // no constraints
	}

	selector := parseSelectorJSON(sched.NodeSelector)
	if len(selector) == 0 {
		return true
	}

	labels, err := r.registry.GetNodeLabels(ctx, node.ID)
	if err != nil {
		xlog.Warn("Failed to get node labels for selector check", "node", node.ID, "error", err)
		return true // fail open
	}

	labelMap := make(map[string]string)
	for _, l := range labels {
		labelMap[l.Key] = l.Value
	}

	for k, v := range selector {
		if labelMap[k] != v {
			return false
		}
	}
	return true
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

	// Check for scheduling constraints (node selector)
	sched, _ := r.registry.GetModelScheduling(ctx, modelID)
	var candidateNodeIDs []string // nil = all nodes eligible

	if sched != nil && sched.NodeSelector != "" {
		selector := parseSelectorJSON(sched.NodeSelector)
		if len(selector) > 0 {
			candidates, err := r.registry.FindNodesBySelector(ctx, selector)
			if err != nil || len(candidates) == 0 {
				return nil, "", fmt.Errorf("no healthy nodes match selector for model %s: %v", modelID, sched.NodeSelector)
			}
			candidateNodeIDs = extractNodeIDs(candidates)
		}
	}

	var node *BackendNode
	var err error

	if estimatedVRAM > 0 {
		if candidateNodeIDs != nil {
			node, err = r.registry.FindNodeWithVRAMFromSet(ctx, estimatedVRAM, candidateNodeIDs)
		} else {
			node, err = r.registry.FindNodeWithVRAM(ctx, estimatedVRAM)
		}
		if err != nil {
			xlog.Warn("No nodes with enough VRAM, falling back to standard scheduling",
				"required_vram", vram.FormatBytes(estimatedVRAM), "error", err)
		}
	}

	if node == nil {
		if candidateNodeIDs != nil {
			node, err = r.registry.FindIdleNodeFromSet(ctx, candidateNodeIDs)
			if err != nil {
				node, err = r.registry.FindLeastLoadedNodeFromSet(ctx, candidateNodeIDs)
			}
		} else {
			node, err = r.registry.FindIdleNode(ctx)
			if err != nil {
				node, err = r.registry.FindLeastLoadedNode(ctx)
			}
		}
	}

	// 4. Preemptive eviction: if no suitable node found, evict the LRU model with zero in-flight
	if node == nil {
		evictedNode, evictErr := r.evictLRUAndFreeNode(ctx)
		if evictErr != nil {
			if errors.Is(evictErr, ErrEvictionBusy) {
				return nil, "", fmt.Errorf("no healthy nodes available: %w", evictErr)
			}
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

func (r *SmartRouter) buildClientForAddr(node *BackendNode, addr string, parallel bool) grpc.Backend {
	client := r.clientFactory.NewClient(addr, parallel)

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
//
// All files are namespaced under trackingKey so that worker-side deletion can
// simply remove the {ModelsPath}/{trackingKey}/ directory.
func (r *SmartRouter) stageModelFiles(ctx context.Context, node *BackendNode, opts *pb.ModelOptions, trackingKey string) (*pb.ModelOptions, error) {
	opts = proto.Clone(opts).(*pb.ModelOptions)
	xlog.Info("Staging model files for remote node", "node", node.Name, "modelFile", opts.ModelFile, "trackingKey", trackingKey)

	// Derive the frontend models directory from ModelFile and Model.
	// Example: ModelFile="/models/sd-cpp/models/flux.gguf", Model="sd-cpp/models/flux.gguf"
	// → frontendModelsDir="/models"
	frontendModelsDir := ""
	if opts.ModelFile != "" && opts.Model != "" {
		frontendModelsDir = filepath.Clean(strings.TrimSuffix(opts.ModelFile, opts.Model))
	}

	// keyMapper generates storage keys namespaced under trackingKey, preserving
	// subdirectory structure relative to frontendModelsDir. This ensures:
	// 1. All files for a model land in one directory on the worker for clean deletion
	// 2. Relative option paths (vae_path, etc.) resolve correctly via ModelPath
	keyMapper := &StagingKeyMapper{
		TrackingKey:       trackingKey,
		FrontendModelsDir: frontendModelsDir,
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
		key := keyMapper.Key(localPath)
		remotePath, err := r.fileStager.EnsureRemote(ctx, node.ID, localPath, key)
		if err != nil {
			// ModelFile is required — fail the whole operation
			if f.name == "ModelFile" {
				xlog.Error("Failed to stage model file for remote node", "node", node.Name, "field", f.name, "path", localPath, "error", err)
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
		// With tracking key namespacing:
		// remotePath = "/worker/models/{trackingKey}/sd-cpp/models/flux.gguf"
		// Model = "sd-cpp/models/flux.gguf"
		// → ModelPath = "/worker/models/{trackingKey}"
		if f.name == "ModelFile" && opts.Model != "" {
			opts.ModelPath = DeriveRemoteModelPath(remotePath, opts.Model)
			xlog.Debug("Derived remote ModelPath", "modelPath", opts.ModelPath)
		}

		r.stageCompanionFiles(ctx, node, localPath, keyMapper.Key)
	}

	// Handle LoraAdapters (array) — rewritten to absolute remote paths
	staged := make([]string, 0, len(opts.LoraAdapters))
	for _, adapter := range opts.LoraAdapters {
		if adapter == "" {
			continue
		}
		if _, err := os.Stat(adapter); os.IsNotExist(err) {
			xlog.Debug("Skipping staging for non-existent lora adapter", "path", adapter)
			continue
		}
		key := keyMapper.Key(adapter)
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
			key := keyMapper.Key(opts.LoraBase)
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
	r.stageGenericOptions(ctx, node, opts.Options, frontendModelsDir, keyMapper.Key)
	r.stageGenericOptions(ctx, node, opts.Overrides, frontendModelsDir, keyMapper.Key)

	return opts, nil
}

// stageCompanionFiles stages known companion files that exist alongside
// localPath. For example, piper TTS implicitly loads ".onnx.json" next to
// the ".onnx" model file. Errors are logged but not propagated.
// keyFn generates the namespaced storage key for each file path.
func (r *SmartRouter) stageCompanionFiles(ctx context.Context, node *BackendNode, localPath string, keyFn func(string) string) {
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
		key := keyFn(companion)
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
// keyFn generates the namespaced storage key for each file path.
func (r *SmartRouter) stageGenericOptions(ctx context.Context, node *BackendNode, options []string, frontendModelsDir string, keyFn func(string) string) {
	for _, opt := range options {
		optKey, val, ok := strings.Cut(opt, ":")
		if !ok || val == "" {
			continue
		}

		// Check if value is an existing file path (absolute or relative to frontend models dir)
		absPath := val
		if !filepath.IsAbs(val) && frontendModelsDir != "" {
			absPath = filepath.Join(frontendModelsDir, val)
		}
		if _, err := os.Stat(absPath); os.IsNotExist(err) {
			continue
		}

		// Stage the file to the worker using the namespaced key
		key := keyFn(absPath)
		if _, err := r.fileStager.EnsureRemote(ctx, node.ID, absPath, key); err != nil {
			xlog.Warn("Failed to stage option file, skipping", "option", opt, "path", absPath, "error", err)
			continue
		}
		// Leave option value unchanged — backend resolves relative paths via ModelPath
		xlog.Debug("Staged option file", "option", optKey, "localPath", absPath)
	}
}

// probeHealth checks whether a backend process on the given node/addr is alive
// via a gRPC health check with a 2-second timeout. The client is closed after the check.
func (r *SmartRouter) probeHealth(ctx context.Context, node *BackendNode, addr string) bool {
	client := r.buildClientForAddr(node, addr, false)
	defer closeClient(client)
	checkCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	ok, _ := client.HealthCheck(checkCtx)
	return ok
}

// closeClient closes a gRPC backend client if it implements io.Closer.
func closeClient(client grpc.Backend) {
	if closer, ok := client.(io.Closer); ok {
		closer.Close()
	}
}

// UnloadModel sends a NATS unload event to a specific node for the given model.
// The worker process handles Free() + kill + deregister.
func (r *SmartRouter) UnloadModel(ctx context.Context, nodeID, modelName string) error {
	if r.unloader == nil {
		return fmt.Errorf("no remote unloader configured")
	}
	// Target the specific node, not all nodes hosting this model
	if err := r.unloader.StopBackend(nodeID, modelName); err != nil {
		return fmt.Errorf("failed to stop backend on node %s: %w", nodeID, err)
	}
	r.registry.RemoveNodeModel(ctx, nodeID, modelName)
	return nil
}

// EvictLRU evicts the least-recently-used model from a node to make room.
// Returns the name of the evicted model, or empty string if nothing could be evicted.
func (r *SmartRouter) EvictLRU(ctx context.Context, nodeID string) (string, error) {
	lru, err := r.registry.FindLRUModel(ctx, nodeID)
	if err != nil {
		return "", fmt.Errorf("finding LRU model on node %s: %w", nodeID, err)
	}

	if err := r.UnloadModel(ctx, nodeID, lru.ModelName); err != nil {
		return "", err
	}
	return lru.ModelName, nil
}

// ErrEvictionBusy is returned when all loaded models have in-flight requests
// and none can be evicted to make room.
var ErrEvictionBusy = errors.New("all models busy, cannot evict")

// evictLRUAndFreeNode finds the globally least-recently-used model with zero in-flight,
// unloads it, and returns its node for reuse. If all models are busy, retries briefly.
//
// Uses SELECT FOR UPDATE inside a transaction to prevent two frontends from
// simultaneously picking the same eviction target. The NodeModel row is deleted
// inside the transaction; the NATS unload command is sent after commit.
func (r *SmartRouter) evictLRUAndFreeNode(ctx context.Context) (*BackendNode, error) {
	const maxEvictionRetries = 5
	const evictionRetryInterval = 500 * time.Millisecond

	if r.db == nil {
		return nil, ErrEvictionBusy // no DB means no row-level locking for safe eviction
	}

	for attempt := range maxEvictionRetries {
		var lru NodeModel
		err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			// Lock the row so no other frontend can evict the same model
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
				Joins("JOIN backend_nodes ON backend_nodes.id = node_models.node_id").
				Where(`node_models.in_flight = 0 AND node_models.state = ? AND backend_nodes.status = ?
  AND (
    NOT EXISTS (SELECT 1 FROM model_scheduling_configs sc WHERE sc.model_name = node_models.model_name AND (sc.min_replicas > 0 OR sc.max_replicas > 0))
    OR (SELECT COUNT(*) FROM node_models nm2 WHERE nm2.model_name = node_models.model_name AND nm2.state = 'loaded')
       > COALESCE((SELECT sc2.min_replicas FROM model_scheduling_configs sc2 WHERE sc2.model_name = node_models.model_name), 1)
  )`, "loaded", StatusHealthy).
				Order("node_models.last_used ASC").
				First(&lru).Error; err != nil {
				return err
			}
			// Remove inside the same transaction
			return tx.Where("node_id = ? AND model_name = ?", lru.NodeID, lru.ModelName).
				Delete(&NodeModel{}).Error
		})

		if err == nil {
			xlog.Info("Evicted LRU model to free capacity",
				"node", lru.NodeID, "model", lru.ModelName, "lastUsed", lru.LastUsed)

			// Unload outside the transaction (NATS call)
			if r.unloader != nil {
				if uerr := r.unloader.UnloadModelOnNode(lru.NodeID, lru.ModelName); uerr != nil {
					xlog.Warn("eviction unload failed (model already removed from registry)", "error", uerr)
				}
			}

			node, nodeErr := r.registry.Get(ctx, lru.NodeID)
			if nodeErr != nil {
				return nil, fmt.Errorf("node %s not found after eviction: %w", lru.NodeID, nodeErr)
			}
			return node, nil
		}

		// gorm.ErrRecordNotFound means all models have in-flight requests
		if attempt == 0 {
			xlog.Info("All models have in-flight requests, waiting for capacity")
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("context cancelled while waiting for eviction")
		case <-time.After(evictionRetryInterval):
			// retry
		}
	}

	return nil, ErrEvictionBusy
}
