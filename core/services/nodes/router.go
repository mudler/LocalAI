package nodes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/advisorylock"
	"github.com/mudler/LocalAI/core/services/nodes/prefixcache"
	"github.com/mudler/LocalAI/pkg/distributedhdr"
	grpc "github.com/mudler/LocalAI/pkg/grpc"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/vram"
	"github.com/mudler/xlog"
	"golang.org/x/sync/singleflight"
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
	// ConflictResolver, when set, lets the scheduler narrow placement
	// candidates by per-model concurrency_groups (#9659). When nil, group
	// anti-affinity is disabled at the scheduler layer; the per-node
	// watchdog still enforces the rule on arrival.
	ConflictResolver ConcurrencyConflictResolver
	// PrefixProvider, when set, enables prefix-cache-aware routing: requests
	// carrying a prompt prefix chain (distributedhdr.PrefixChain) are biased
	// toward the node that already holds the longest matching prefix, subject
	// to the load guard in prefixcache.Select. nil disables it entirely and
	// routing is byte-for-byte the round-robin floor. At runtime this is the
	// *prefixcache.Sync so Observe/Invalidate broadcast to peers.
	PrefixProvider prefixcache.Provider
	// PrefixConfig holds the global policy + thresholds. Per-model overrides on
	// ModelSchedulingConfig refine it per request. Unused when PrefixProvider
	// is nil.
	PrefixConfig prefixcache.Config
	// Pressure, when set, records a forced-disturb each time a request had a
	// usable hot prefix match but the load guard forced it off the warm node.
	// The reconciler reads the same instance to autoscale a saturated cache-warm
	// replica. nil disables recording (the disabled path stays a no-op).
	Pressure *prefixcache.Pressure
}

// SmartRouter routes inference requests to the best available backend node.
// It uses the ModelRouter interface (backed by NodeRegistry in production) for routing decisions.
type SmartRouter struct {
	registry         ModelRouter
	unloader         NodeCommandSender    // optional, for NATS-driven load/unload
	fileStager       FileStager           // optional, for distributed file transfer
	galleriesJSON    string               // backend gallery config for dynamic installation
	clientFactory    BackendClientFactory // creates gRPC backend clients
	db               *gorm.DB             // for advisory locks during routing
	stagingTracker   *StagingTracker      // tracks file staging progress for UI visibility
	conflictResolver ConcurrencyConflictResolver
	// prefixProvider is the prefix-cache routing seam (nil disables it; see
	// SmartRouterOptions.PrefixProvider). prefixConfig holds the global policy
	// and thresholds.
	prefixProvider prefixcache.Provider
	prefixConfig   prefixcache.Config
	// pressure records forced-disturb events (hot match forced off the warm
	// node by the load guard). nil disables recording. See SmartRouterOptions.
	pressure *prefixcache.Pressure
	// installFlight coalesces concurrent identical NATS install requests
	// (same nodeID + backend + modelID + replica) so 6 simultaneous chat
	// completions for one not-yet-loaded model produce ONE round-trip, not
	// six. Avoids amplifying head-of-line blocking on the worker side.
	installFlight singleflight.Group
	// probeCache memoizes recent successful gRPC HealthCheck results so
	// per-request routing doesn't stall behind a busy backend's serialized
	// HealthCheck/Predict. See probe_cache.go for the rationale.
	probeCache *probeCache
}

// probeCacheTTL is how long a successful gRPC HealthCheck on a backend is
// trusted before the next request re-probes. Matches healthCheckTTL in
// pkg/model/model.go so the single-process and distributed paths share a
// staleness budget. The background HealthMonitor still reaps dead backends
// independently within ~45s (see perModelMissThreshold).
const probeCacheTTL = 30 * time.Second

// NewSmartRouter creates a new SmartRouter backed by the given ModelRouter.
// All optional dependencies are passed via SmartRouterOptions to avoid post-creation races.
func NewSmartRouter(registry ModelRouter, opts SmartRouterOptions) *SmartRouter {
	factory := opts.ClientFactory
	if factory == nil {
		factory = &tokenClientFactory{token: opts.AuthToken}
	}
	return &SmartRouter{
		registry:         registry,
		unloader:         opts.Unloader,
		fileStager:       opts.FileStager,
		galleriesJSON:    opts.GalleriesJSON,
		clientFactory:    factory,
		db:               opts.DB,
		stagingTracker:   NewStagingTracker(),
		conflictResolver: opts.ConflictResolver,
		probeCache:       newProbeCache(probeCacheTTL),
		prefixProvider:   opts.PrefixProvider,
		prefixConfig:     opts.PrefixConfig,
		pressure:         opts.Pressure,
	}
}

// Unloader returns the remote unloader adapter for external use.
func (r *SmartRouter) Unloader() NodeCommandSender { return r.unloader }

// StagingTracker returns the staging progress tracker for UI visibility.
func (r *SmartRouter) StagingTracker() *StagingTracker { return r.stagingTracker }

// scheduleLoadResult holds the result of scheduling and loading a model on a node.
type scheduleLoadResult struct {
	Node         *BackendNode
	Client       grpc.Backend
	BackendAddr  string
	ReplicaIndex int
}

// scheduleAndLoad is the shared core for loading a model on a new node.
// Used by both Route() (for first-time loads) and ScheduleAndLoadModel() (for reconciler scale-ups).
//
// Steps: pick node + replica slot → install backend → stage files → LoadModel → SetNodeModel.
//
// scheduleNewModel allocates the replica index internally so the worker's
// processKey, port, and the registry row all agree.
func (r *SmartRouter) scheduleAndLoad(ctx context.Context, backendType, trackingKey, modelName string,
	modelOpts *pb.ModelOptions, parallel bool, initialInFlight int) (*scheduleLoadResult, error) {

	node, backendAddr, replicaIndex, err := r.scheduleNewModel(ctx, backendType, trackingKey, modelOpts)
	if err != nil {
		return nil, fmt.Errorf("no available nodes: %w", err)
	}

	// Pre-stage model files via FileStager before loading
	loadOpts := modelOpts
	if r.fileStager != nil && modelOpts != nil {
		staged, err := r.stageModelFiles(ctx, node, modelOpts, trackingKey)
		if err != nil {
			return nil, fmt.Errorf("staging model files for node %s: %w", node.Name, err)
		}
		loadOpts = staged
	}

	client := r.buildClientForAddr(node, backendAddr, parallel)

	// Load the model on the remote node
	if loadOpts != nil {
		xlog.Info("Loading model on remote node", "node", node.Name, "model", modelName, "addr", backendAddr)

		loadCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		defer cancel()

		res, err := client.LoadModel(loadCtx, loadOpts)
		if err != nil {
			return nil, fmt.Errorf("loading model %s on node %s: %w", modelName, node.Name, err)
		}
		if !res.Success {
			return nil, fmt.Errorf("loading model %s on node %s: %s", modelName, node.Name, res.Message)
		}
	}

	// Record the model as loaded on this node (specific replica slot).
	if err := r.registry.SetNodeModel(ctx, node.ID, trackingKey, replicaIndex, "loaded", backendAddr, initialInFlight); err != nil {
		xlog.Warn("Failed to record model on node", "node", node.Name, "model", trackingKey, "replica", replicaIndex, "error", err)
	}

	// Store load metadata for future replica scale-ups by the reconciler.
	// Writes both per-replica (NodeModel.model_opts_blob) for backward compat
	// and per-model (ModelLoadInfo table) so the reconciler can recover after
	// every replica row has been removed (Bug-1).
	if modelOpts != nil {
		if optsBlob, marshalErr := proto.Marshal(modelOpts); marshalErr == nil {
			if storeErr := r.registry.SetNodeModelLoadInfo(ctx, node.ID, trackingKey, replicaIndex, backendType, optsBlob); storeErr != nil {
				xlog.Warn("Failed to store model load info", "node", node.Name, "model", trackingKey, "replica", replicaIndex, "error", storeErr)
			}
			if storeErr := r.registry.UpsertModelLoadInfo(ctx, trackingKey, backendType, optsBlob); storeErr != nil {
				xlog.Warn("Failed to upsert per-model load info", "model", trackingKey, "error", storeErr)
			}
		}
	}

	return &scheduleLoadResult{Node: node, Client: client, BackendAddr: backendAddr, ReplicaIndex: replicaIndex}, nil
}

// ScheduleAndLoadModel implements ModelScheduler for the reconciler.
// It retrieves stored model options from an existing replica and performs the
// full load sequence (stage files, LoadModel, SetNodeModel) on a new node.
func (r *SmartRouter) ScheduleAndLoadModel(ctx context.Context, modelName string, candidateNodeIDs []string) (*BackendNode, error) {
	// Get load info from an existing replica (stored when Route() first loaded the model)
	backendType, optsBlob, err := r.registry.GetModelLoadInfo(ctx, modelName)
	if err != nil {
		// No replica has ever been loaded for this model, so we have no
		// backend type or model options to replicate. The previous fallback
		// fired backend.install with backend="" every reconciler tick, which
		// the worker rejected ("backend name is empty"). Skip cleanly: the
		// model needs to be served at least once via Route() so its load
		// info is stored — then the reconciler can replicate it.
		return nil, fmt.Errorf("no load info for model %s: serve at least one request for it before the reconciler can replicate (cause: %w)", modelName, err)
	}

	// Deserialize the stored model options
	var modelOpts pb.ModelOptions
	if err := proto.Unmarshal(optsBlob, &modelOpts); err != nil {
		return nil, fmt.Errorf("unmarshalling stored model options for %s: %w", modelName, err)
	}

	// initialInFlight=0: reconciler is pre-loading, not serving a request.
	// scheduleAndLoad picks both the node and the replica slot internally.
	result, err := r.scheduleAndLoad(ctx, backendType, modelName, modelName, &modelOpts, false, 0)
	if err != nil {
		return nil, err
	}
	return result.Node, nil
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

	// Fetch the model's scheduling config once: it is immutable for the life of
	// this request, and resolveSelectorCandidates, buildPreference, and
	// nodeMatchesScheduling all read it. Fetching once gives a consistent
	// snapshot and avoids three DB round-trips for one row. nil sched means
	// "no scheduling constraints", same as before.
	sched, _ := r.registry.GetModelScheduling(ctx, trackingKey)

	// Resolve the model's NodeSelector once so cached-replica lookup and the
	// new-load scheduler agree on the candidate set. Without this, a cached
	// replica on a node the selector now excludes was picked over a matching
	// replica elsewhere, and the fall-through then tried to load on the
	// matching node where the model was already at capacity (eviction-busy).
	candidateNodeIDs, err := r.resolveSelectorCandidates(ctx, trackingKey, sched)
	if err != nil {
		return nil, err
	}

	// Compute the prefix-cache preference once for this request. pref biases
	// FindAndLockNodeWithModel toward the warm-cache node; observeChain is
	// non-nil only when this model uses prefix_cache, gating the Observe calls
	// below. Both are nil (no-op) when prefix-cache routing is disabled.
	pref, observeChain := r.buildPreference(ctx, trackingKey, candidateNodeIDs, sched)

	// Step 1: Find and atomically lock a node with this model loaded
	node, nm, err := r.registry.FindAndLockNodeWithModel(ctx, trackingKey, candidateNodeIDs, pref)
	if err == nil && node != nil {
		modelAddr := node.Address
		if nm.Address != "" {
			modelAddr = nm.Address
		}
		replicaIdx := nm.ReplicaIndex

		// Verify the backend process is still alive via gRPC health check
		if !r.probeHealth(ctx, node, modelAddr) {
			// Stale — roll back the increment, remove the specific replica row, fall through
			r.registry.DecrementInFlight(ctx, node.ID, trackingKey, replicaIdx)
			r.registry.RemoveNodeModel(ctx, node.ID, trackingKey, replicaIdx)
			xlog.Warn("Backend not reachable for cached model, falling through to reload",
				"node", node.Name, "model", modelName, "replica", replicaIdx)
		} else {
			// Verify node still matches scheduling constraints
			if !r.nodeMatchesScheduling(ctx, node, sched) {
				r.registry.DecrementInFlight(ctx, node.ID, trackingKey, replicaIdx)
				xlog.Info("Cached model on node that no longer matches selector, falling through",
					"node", node.Name, "model", trackingKey, "replica", replicaIdx)
				// Fall through to step 2 (scheduleNewModel)
			} else {
				// Node is alive — FindAndLockNodeWithModel already incremented in-flight as a
				// reservation. InFlightTrackingClient handles per-inference tracking, and its
				// onFirstComplete callback releases the reservation after the first inference
				// call finishes, so in-flight returns to 0 when idle.
				r.registry.TouchNodeModel(ctx, node.ID, trackingKey, replicaIdx)
				r.observePrefix(trackingKey, observeChain, prefixcache.ReplicaKey{NodeID: node.ID, Replica: replicaIdx})
				grpcClient := r.buildClientForAddr(node, modelAddr, parallel)
				tracked := NewInFlightTrackingClient(grpcClient, r.registry, node.ID, trackingKey, replicaIdx)
				tracked.OnFirstComplete(func() {
					r.registry.DecrementInFlight(context.Background(), node.ID, trackingKey, replicaIdx)
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
		node, nm, err := r.registry.FindAndLockNodeWithModel(ctx, trackingKey, candidateNodeIDs, pref)
		if err == nil && node != nil {
			modelAddr := node.Address
			if nm.Address != "" {
				modelAddr = nm.Address
			}
			replicaIdx := nm.ReplicaIndex

			// Verify the backend process is still alive via gRPC health check
			if !r.probeHealth(ctx, node, modelAddr) {
				// Stale — roll back the increment, remove the specific replica row, continue loading
				r.registry.DecrementInFlight(ctx, node.ID, trackingKey, replicaIdx)
				r.registry.RemoveNodeModel(ctx, node.ID, trackingKey, replicaIdx)
				xlog.Warn("Backend not reachable for cached model inside lock, proceeding to load",
					"node", node.Name, "model", modelName, "replica", replicaIdx)
			} else {
				// Verify node still matches scheduling constraints
				if !r.nodeMatchesScheduling(ctx, node, sched) {
					r.registry.DecrementInFlight(ctx, node.ID, trackingKey, replicaIdx)
					xlog.Info("Cached model on node that no longer matches selector, falling through",
						"node", node.Name, "model", trackingKey, "replica", replicaIdx)
					// Fall through to scheduling below
				} else {
					// Model loaded while we waited — FindAndLockNodeWithModel already incremented
					// in-flight as a reservation. Release it after the first inference completes.
					r.registry.TouchNodeModel(ctx, node.ID, trackingKey, replicaIdx)
					r.observePrefix(trackingKey, observeChain, prefixcache.ReplicaKey{NodeID: node.ID, Replica: replicaIdx})
					grpcClient := r.buildClientForAddr(node, modelAddr, parallel)
					tracked := NewInFlightTrackingClient(grpcClient, r.registry, node.ID, trackingKey, replicaIdx)
					tracked.OnFirstComplete(func() {
						r.registry.DecrementInFlight(context.Background(), node.ID, trackingKey, replicaIdx)
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

		// Still not loaded — use shared schedule-and-load logic, which picks
		// both the node and the replica slot.
		result, err := r.scheduleAndLoad(ctx, backendType, trackingKey, modelName, modelOpts, parallel, 1)
		if err != nil {
			return nil, err
		}

		// Cold load landed on result.Node replica result.ReplicaIndex: record the
		// assignment so subsequent requests with the same prefix prefer it.
		r.observePrefix(trackingKey, observeChain, prefixcache.ReplicaKey{NodeID: result.Node.ID, Replica: result.ReplicaIndex})

		replicaIdx := result.ReplicaIndex
		tracked := NewInFlightTrackingClient(result.Client, r.registry, result.Node.ID, trackingKey, replicaIdx)
		tracked.OnFirstComplete(func() {
			r.registry.DecrementInFlight(context.Background(), result.Node.ID, trackingKey, replicaIdx)
		})
		return &RouteResult{
			Node:   result.Node,
			Client: tracked,
			Release: func() {
				closeClient(result.Client)
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

// buildPreference computes the per-request route preference from the prefix
// chain on ctx and the model's resolved policy. The returned observeChain is
// non-nil only when the resolved policy is prefix_cache, signalling Route to
// record the assignment after a successful pick; for round-robin models it is
// nil so the tree is never polluted. The *RoutePreference is non-nil only when
// a load-eligible preferred node was chosen.
//
// When prefix-cache routing is disabled (nil provider), no chain is present,
// or the policy resolves to round-robin, both returns are nil and routing is
// the unchanged round-robin floor.
func (r *SmartRouter) buildPreference(ctx context.Context, modelID string, candidateNodeIDs []string, sched *ModelSchedulingConfig) (*RoutePreference, []uint64) {
	if r.prefixProvider == nil {
		return nil, nil
	}
	chain := distributedhdr.PrefixChain(ctx)
	if len(chain) == 0 {
		return nil, nil
	}

	// Resolve per-model policy + thresholds over the global config.
	policy := r.prefixConfig.GlobalPolicy
	cfg := r.prefixConfig
	if sched != nil {
		policy = prefixcache.ParsePolicy(sched.RoutePolicy).Resolve(r.prefixConfig.GlobalPolicy)
		if sched.BalanceAbsThreshold > 0 {
			cfg.BalanceAbsThreshold = sched.BalanceAbsThreshold
		}
		if sched.BalanceRelThreshold > 0 {
			cfg.BalanceRelThreshold = sched.BalanceRelThreshold
		}
		if sched.MinPrefixMatch > 0 {
			cfg.MinPrefixMatch = sched.MinPrefixMatch
		}
	}
	if policy != prefixcache.RoutePolicyPrefixCache {
		return nil, nil
	}

	// Load the candidate replicas PER REPLICA. Affinity is tracked per replica
	// (each replica is a separate process with its own KV cache), so two
	// replicas of the same model on the same node are two distinct candidates.
	// FindAndLockNodeWithModel then locks the EXACT (node, replica) the policy
	// chose.
	stats, err := r.registry.LoadedReplicaStats(ctx, modelID, candidateNodeIDs)
	if err != nil {
		xlog.Debug("prefixcache: loading replica stats failed, skipping preference", "model", modelID, "error", err)
		return nil, chain
	}
	if len(stats) == 0 {
		return nil, chain
	}
	cands := make([]prefixcache.Candidate, 0, len(stats))
	keys := make([]prefixcache.ReplicaKey, 0, len(stats))
	for _, s := range stats {
		key := prefixcache.ReplicaKey{NodeID: s.NodeID, Replica: s.ReplicaIndex}
		cands = append(cands, prefixcache.Candidate{Key: key, InFlight: s.InFlight})
		keys = append(keys, key)
	}

	d := r.prefixProvider.Decide(modelID, chain, keys, time.Now())
	chosen, ok := prefixcache.Select(cands, d, cfg)

	// Observability for the prefix-cache routing decision. One line per request
	// at Debug: enable with DEBUG=true on the frontend to assess cache-aware
	// routing. hotMatchHonored=true means we routed to the cache-warm replica;
	// false with HasHot means the load guard forced a cold pick.
	xlog.Debug("prefix-cache routing decision",
		"model", modelID,
		"chainDepth", len(chain),
		"candidates", len(cands),
		"hotNode", d.Hot.NodeID,
		"hotReplica", d.Hot.Replica,
		"hasHot", d.HasHot,
		"matchRatio", d.MatchRatio,
		"minMatch", cfg.MinPrefixMatch,
		"chosen", fmt.Sprintf("%s/%d", chosen.NodeID, chosen.Replica),
		"hotMatchHonored", d.HasHot && chosen == d.Hot)

	// Forced-disturb: a usable hot prefix match existed but the load guard
	// forced us off the warm replica (Select picked a different replica). This
	// is the scale-worthy signal - the cache-warm replica is saturated. It
	// deliberately does not fire for all-unique workloads (no hot match),
	// avoiding false-positive scale-ups. nil pressure is a no-op.
	if r.pressure != nil && d.HasHot && d.MatchRatio >= cfg.MinPrefixMatch && chosen != d.Hot {
		r.pressure.Record(modelID, time.Now())
	}

	if !ok {
		return nil, chain
	}
	return &RoutePreference{PreferredNodeID: chosen.NodeID, PreferredReplica: chosen.Replica}, chain
}

// observePrefix records that the replica `key` served the request whose prompt
// prefix is chain. It is a no-op when prefix-cache routing is disabled or the
// chain is empty (round-robin models pass a nil chain so the tree is never
// polluted).
func (r *SmartRouter) observePrefix(modelID string, chain []uint64, key prefixcache.ReplicaKey) {
	if r.prefixProvider == nil || len(chain) == 0 {
		return
	}
	r.prefixProvider.Observe(modelID, chain, key, time.Now())
	xlog.Debug("prefix-cache observed assignment", "model", modelID, "node", key.NodeID, "replica", key.Replica, "chainDepth", len(chain))
}

// resolveSelectorCandidates returns the node IDs that match the model's
// NodeSelector. Returns nil when no selector is configured ("any healthy node"
// — registry helpers treat nil as no filter). Returns an error when a
// non-empty selector matches zero healthy nodes, since there is nothing to
// route or schedule on.
func (r *SmartRouter) resolveSelectorCandidates(ctx context.Context, modelID string, sched *ModelSchedulingConfig) ([]string, error) {
	if sched == nil || sched.NodeSelector == "" {
		return nil, nil
	}
	selector := parseSelectorJSON(sched.NodeSelector)
	if len(selector) == 0 {
		return nil, nil
	}
	candidates, err := r.registry.FindNodesBySelector(ctx, selector)
	if err != nil {
		return nil, fmt.Errorf("looking up nodes for selector %s: %w", sched.NodeSelector, err)
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("no healthy nodes match selector for model %s: %s", modelID, sched.NodeSelector)
	}
	return extractNodeIDs(candidates), nil
}

// narrowByGroupAntiAffinity removes candidate nodes that already host a model
// declared as concurrent-conflicting with modelID via concurrency_groups
// (#9659). This is a soft filter: when *every* candidate would be excluded,
// the original set is returned and the per-node watchdog evicts on arrival.
//
// candidates may be nil ("any healthy node" — registry helpers treat nil as
// no filter). nil is returned unchanged: hard-narrowing the implicit "all
// nodes" set would silently exclude every node we know nothing about.
func (r *SmartRouter) narrowByGroupAntiAffinity(ctx context.Context, modelID string, candidates []string) ([]string, error) {
	if r.conflictResolver == nil || candidates == nil {
		return candidates, nil
	}
	conflicts := r.conflictResolver.GetModelsConflictingWith(modelID)
	if len(conflicts) == 0 {
		return candidates, nil
	}

	excluded := make(map[string]struct{})
	for _, name := range conflicts {
		nodes, err := r.registry.FindNodesWithModel(ctx, name)
		if err != nil {
			// Best-effort: a single lookup failure shouldn't fail placement.
			// Log and move on — the watchdog still enforces the rule on arrival.
			xlog.Warn("Group anti-affinity: lookup failed, skipping", "model", name, "error", err)
			continue
		}
		for _, n := range nodes {
			excluded[n.ID] = struct{}{}
		}
	}
	if len(excluded) == 0 {
		return candidates, nil
	}

	narrowed := candidates[:0:0]
	for _, id := range candidates {
		if _, bad := excluded[id]; bad {
			continue
		}
		narrowed = append(narrowed, id)
	}
	if len(narrowed) == 0 {
		// Soft fallback: every candidate has a conflict. Return the original
		// set and let the per-node watchdog evict on arrival rather than
		// failing the request.
		xlog.Debug("Group anti-affinity: all candidates conflict, falling back to original set",
			"model", modelID, "conflicts", conflicts)
		return candidates, nil
	}
	xlog.Debug("Group anti-affinity narrowed candidates",
		"model", modelID, "before", len(candidates), "after", len(narrowed))
	return narrowed, nil
}

// nodeMatchesScheduling checks if a node satisfies the scheduling constraints for a model.
// Returns true if no constraints exist or the node matches all selector labels.
func (r *SmartRouter) nodeMatchesScheduling(ctx context.Context, node *BackendNode, sched *ModelSchedulingConfig) bool {
	if sched == nil || sched.NodeSelector == "" {
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

// scheduleNewModel picks the best node for loading a new model and allocates
// the replica slot.
// Strategy: filter to nodes with a free slot for this model → VRAM-aware →
// idle-first → least-loaded → eviction.
// Sends backend.install via NATS so the chosen node has the right backend running.
//
// Returns (node, gRPC address, replicaIndex, err). replicaIndex is the slot
// the worker has been told to use; the caller must pass the same index into
// SetNodeModel so the registry row matches the live process.
func (r *SmartRouter) scheduleNewModel(ctx context.Context, backendType, modelID string, modelOpts *pb.ModelOptions) (*BackendNode, string, int, error) {
	// Estimate VRAM required for the model
	var estimatedVRAM uint64
	if modelOpts != nil {
		estimatedVRAM = r.estimateModelVRAM(ctx, modelOpts)
	}

	// Check for scheduling constraints (node selector). If a selector is set,
	// we restrict the candidate pool to matching nodes; otherwise nil means
	// "any healthy node".
	sched, _ := r.registry.GetModelScheduling(ctx, modelID)
	candidateNodeIDs, err := r.resolveSelectorCandidates(ctx, modelID, sched)
	if err != nil {
		return nil, "", 0, err
	}

	// Apply concurrency-group anti-affinity (#9659): prefer nodes that don't
	// already host a model declared exclusive with this one. Soft filter — if
	// every candidate has a conflict, the original set is returned and the
	// per-node watchdog evicts on arrival.
	candidateNodeIDs, err = r.narrowByGroupAntiAffinity(ctx, modelID, candidateNodeIDs)
	if err != nil {
		return nil, "", 0, err
	}

	// Narrow candidates to nodes that still have a free replica slot for this
	// model. Without this filter, the scheduler would happily pick a node
	// already at capacity for this model (e.g. when MinReplicas > free
	// cluster capacity), which is what caused the original 30s flap loop.
	freeSlotNodes, err := r.registry.FindNodesWithFreeSlot(ctx, modelID, candidateNodeIDs)
	if err != nil {
		xlog.Warn("Failed to query nodes with free slot; falling back to selector-only filtering",
			"model", modelID, "error", err)
	} else if len(freeSlotNodes) > 0 {
		// Replace the candidate set with only those that have capacity.
		candidateNodeIDs = extractNodeIDs(freeSlotNodes)
	}
	// If freeSlotNodes is empty (everyone full), candidateNodeIDs is whatever
	// it was — we'll fall through to eviction below.

	var node *BackendNode

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
				return nil, "", 0, fmt.Errorf("no healthy nodes available: %w", evictErr)
			}
			return nil, "", 0, fmt.Errorf("no healthy nodes available and eviction failed: %w", evictErr)
		}
		node = evictedNode
	}

	// Allocate the replica slot before sending backend.install so the worker
	// uses the same slot for its processKey + port. Default to 0 when the
	// node's MaxReplicasPerModel is 1 (preserves single-replica behavior).
	maxSlots := node.MaxReplicasPerModel
	if maxSlots < 1 {
		maxSlots = 1
	}
	replicaIdx, slotErr := r.registry.NextFreeReplicaIndex(ctx, node.ID, modelID, maxSlots)
	if slotErr != nil {
		// All slots on this node are taken — fall back to eviction. This is
		// rare in practice because FindNodesWithFreeSlot already filtered;
		// it can race with another concurrent scheduler.
		xlog.Warn("Chosen node has no free replica slot, evicting LRU",
			"node", node.Name, "model", modelID, "max_slots", maxSlots)
		evictedNode, evictErr := r.evictLRUAndFreeNode(ctx)
		if evictErr != nil {
			return nil, "", 0, fmt.Errorf("no replica slot on %s and eviction failed: %w", node.Name, evictErr)
		}
		node = evictedNode
		replicaIdx, slotErr = r.registry.NextFreeReplicaIndex(ctx, node.ID, modelID, node.MaxReplicasPerModel)
		if slotErr != nil {
			return nil, "", 0, fmt.Errorf("no replica slot on %s after eviction: %w", node.Name, slotErr)
		}
	}

	// Soft-reserve VRAM up front so a second scheduling tick within the same
	// heartbeat window can't pick this node based on stale free-VRAM
	// numbers. The worker's next heartbeat resets reserved_vram to the
	// authoritative reading; explicit rollback below covers the failure
	// window between reservation and a successful install.
	reserved := false
	if estimatedVRAM > 0 {
		reserveErr := r.registry.ReserveVRAM(ctx, node.ID, estimatedVRAM)
		if reserveErr != nil {
			// ErrInsufficientVRAM races with another scheduler — log and
			// proceed without a reservation rather than failing the load.
			// FindNodeWithVRAM already accounted for reserved_vram, so this
			// is a tight race window; the worker will reconcile via heartbeat.
			xlog.Warn("Failed to reserve VRAM, proceeding without reservation",
				"node", node.Name, "bytes", estimatedVRAM, "error", reserveErr)
		} else {
			reserved = true
		}
	}

	// Send backend.install — the worker installs the backend if needed and
	// starts the gRPC process bound to a port for this (model, replica) slot.
	addr, installErr := r.installBackendOnNode(ctx, node, backendType, modelID, replicaIdx)
	if installErr != nil {
		// Roll back the reservation explicitly so the column is accurate
		// before the next heartbeat. Best-effort.
		if reserved {
			_ = r.registry.ReleaseVRAM(ctx, node.ID, estimatedVRAM)
		}
		return nil, "", 0, fmt.Errorf("installing backend on node %s: %w", node.Name, installErr)
	}

	return node, addr, replicaIdx, nil
}

// estimateModelVRAM estimates the VRAM required for a model using the unified estimator.
func (r *SmartRouter) estimateModelVRAM(ctx context.Context, opts *pb.ModelOptions) uint64 {
	estCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	ctxSize := uint32(opts.ContextSize)
	if ctxSize == 0 {
		ctxSize = 8192
	}

	input := vram.ModelEstimateInput{
		Options: vram.EstimateOptions{
			GPULayers: int(opts.NGPULayers),
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

	if len(input.Files) == 0 && input.HFRepo == "" && input.Size == "" {
		return 0
	}

	result, err := vram.EstimateModelMultiContext(estCtx, input, []uint32{ctxSize})
	if err != nil {
		return 0
	}
	return result.VRAMForContext(ctxSize)
}

// installBackendOnNode sends a NATS backend.install request-reply to the node
// and returns the gRPC address. Concurrent identical calls (same nodeID +
// backend + modelID + replica) coalesce via singleflight: 6 chat completions
// for the same not-yet-loaded model produce 1 NATS round-trip and 6 callers
// share the result. This kills the load-amplification we saw in the live
// cluster where 6× simultaneous BackendLoader logs sat behind one slow
// install in the worker's NATS callback queue.
//
// Routine load: the worker's fast-path "already running → return current
// address" is correct here. Upgrades go through
// DistributedBackendManager.UpgradeBackend on the backend.upgrade subject.
func (r *SmartRouter) installBackendOnNode(ctx context.Context, node *BackendNode, backendType, modelID string, replicaIndex int) (string, error) {
	if r.unloader == nil {
		return "", fmt.Errorf("no NATS connection for backend installation")
	}

	key := fmt.Sprintf("%s|%s|%s|%d", node.ID, backendType, modelID, replicaIndex)
	v, err, _ := r.installFlight.Do(key, func() (any, error) {
		reply, err := r.unloader.InstallBackend(node.ID, backendType, modelID, r.galleriesJSON, "", "", "", replicaIndex, "", nil)
		if err != nil {
			return "", err
		}
		if !reply.Success {
			return "", fmt.Errorf("worker replied with error: %s", reply.Error)
		}
		// Return the backend's gRPC address (per-replica port from worker)
		addr := reply.Address
		if addr == "" {
			addr = node.Address // fallback to node base address
		}
		return addr, nil
	})
	if err != nil {
		return "", err
	}
	return v.(string), nil
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

	// Local model directory, captured before the ModelFile field is rewritten to
	// its remote path below. Companion assets declared as option paths (e.g.
	// sherpa-onnx's tokens.txt / espeak-ng-data) live beside the model, so option
	// values are resolved relative to this dir as well as frontendModelsDir —
	// letting a shared config declare them with bare names regardless of whether
	// Model includes a subdirectory.
	localModelDir := ""
	if opts.ModelFile != "" {
		localModelDir = filepath.Dir(opts.ModelFile)
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

	// Count stageable files for progress tracking. Directory models expand to
	// the number of files they contain, matching what stageDirectory uploads.
	totalFiles := 0
	for _, f := range fields {
		if *f.val != "" {
			totalFiles += countStageableFiles(*f.val)
		}
	}
	for _, adapter := range opts.LoraAdapters {
		if adapter != "" {
			if _, err := os.Stat(adapter); err == nil {
				totalFiles++
			}
		}
	}
	if opts.LoraBase != "" {
		if _, err := os.Stat(opts.LoraBase); err == nil {
			totalFiles++
		}
	}

	// Start tracking staging progress
	r.stagingTracker.Start(trackingKey, node.Name, totalFiles)
	defer r.stagingTracker.Complete(trackingKey)

	fileIdx := 0
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

		// Directory models (e.g. qwen3-tts-cpp ships its weights and tokenizer
		// ggufs under one directory) can't be uploaded as a single file — the
		// stager would open the directory and read its fd, failing with
		// "is a directory" (EISDIR). Expand the directory and stage each
		// contained file, then rewrite the field to the remote directory.
		if fi, statErr := os.Stat(localPath); statErr == nil && fi.IsDir() {
			remoteDir, dirErr := r.stageDirectory(ctx, node, trackingKey, localPath, keyMapper, &fileIdx, totalFiles)
			if dirErr != nil {
				if f.name == "ModelFile" {
					xlog.Error("Failed to stage model directory for remote node", "node", node.Name, "field", f.name, "path", localPath, "error", dirErr)
					return nil, fmt.Errorf("staging model file: %w", dirErr)
				}
				xlog.Warn("Failed to stage model directory, clearing field", "field", f.name, "path", localPath, "error", dirErr)
				*f.val = ""
				continue
			}
			*f.val = remoteDir
			if f.name == "ModelFile" && opts.Model != "" {
				opts.ModelPath = DeriveRemoteModelPath(remoteDir, opts.Model)
				xlog.Debug("Derived remote ModelPath", "modelPath", opts.ModelPath)
			}
			continue
		}

		fileIdx++
		key := keyMapper.Key(localPath)

		// Attach progress callback to context for byte-level tracking
		fileName := filepath.Base(localPath)
		stageCtx := r.withStagingCallback(ctx, trackingKey, fileName, fileIdx, totalFiles)

		xlog.Info("Staging file", "model", trackingKey, "node", node.Name, "field", f.name, "file", fileName, "fileIndex", fileIdx, "totalFiles", totalFiles)

		remotePath, err := r.fileStager.EnsureRemote(stageCtx, node.ID, localPath, key)
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

		r.stagingTracker.FileComplete(trackingKey, fileIdx, totalFiles)
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
	stagedAdapters := make([]string, 0, len(opts.LoraAdapters))
	for _, adapter := range opts.LoraAdapters {
		if adapter == "" {
			continue
		}
		if _, err := os.Stat(adapter); os.IsNotExist(err) {
			xlog.Debug("Skipping staging for non-existent lora adapter", "path", adapter)
			continue
		}
		fileIdx++
		fileName := filepath.Base(adapter)
		stageCtx := r.withStagingCallback(ctx, trackingKey, fileName, fileIdx, totalFiles)

		key := keyMapper.Key(adapter)
		remotePath, err := r.fileStager.EnsureRemote(stageCtx, node.ID, adapter, key)
		if err != nil {
			xlog.Warn("Failed to stage lora adapter, skipping", "path", adapter, "error", err)
			continue
		}
		r.stagingTracker.FileComplete(trackingKey, fileIdx, totalFiles)
		stagedAdapters = append(stagedAdapters, remotePath)
	}
	opts.LoraAdapters = stagedAdapters

	// Handle LoraBase field — rewritten to absolute remote path
	if opts.LoraBase != "" {
		if _, err := os.Stat(opts.LoraBase); err == nil {
			fileIdx++
			fileName := filepath.Base(opts.LoraBase)
			stageCtx := r.withStagingCallback(ctx, trackingKey, fileName, fileIdx, totalFiles)

			key := keyMapper.Key(opts.LoraBase)
			if remotePath, err := r.fileStager.EnsureRemote(stageCtx, node.ID, opts.LoraBase, key); err == nil {
				r.stagingTracker.FileComplete(trackingKey, fileIdx, totalFiles)
				opts.LoraBase = remotePath
			} else {
				xlog.Warn("Failed to stage LoraBase, clearing field", "path", opts.LoraBase, "error", err)
				opts.LoraBase = ""
			}
		}
	}

	// Stage file paths referenced in generic Options (key:value pairs where values
	// are file paths). Options stay as relative paths — backends resolve them via ModelPath.
	r.stageGenericOptions(ctx, node, opts.Options, frontendModelsDir, localModelDir, keyMapper.Key)
	r.stageGenericOptions(ctx, node, opts.Overrides, frontendModelsDir, localModelDir, keyMapper.Key)

	return opts, nil
}

// withStagingCallback creates a context with a progress callback that updates the staging tracker.
func (r *SmartRouter) withStagingCallback(ctx context.Context, trackingKey, fileName string, fileIdx, totalFiles int) context.Context {
	start := time.Now()
	return WithStagingProgress(ctx, func(fn string, bytesSent, totalBytes int64) {
		var speed string
		elapsed := time.Since(start)
		if elapsed > 0 {
			bytesPerSec := float64(bytesSent) / elapsed.Seconds()
			speed = humanFileSize(int64(bytesPerSec)) + "/s"
		}
		r.stagingTracker.UpdateFile(trackingKey, fn, fileIdx, bytesSent, totalBytes, speed)
	})
}

// countStageableFiles returns the number of regular files a model path expands
// to for staging: 1 for a regular file, the contained file count for a
// directory, and 0 if the path does not exist.
func countStageableFiles(path string) int {
	fi, err := os.Stat(path)
	if err != nil {
		return 0
	}
	if !fi.IsDir() {
		return 1
	}
	n := 0
	_ = filepath.WalkDir(path, func(_ string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if !d.IsDir() {
			n++
		}
		return nil
	})
	return n
}

// stageDirectory stages every file under a directory-based model (e.g.
// qwen3-tts-cpp, whose weights and tokenizer ggufs live in one directory).
// Each file is uploaded individually with a structure-preserving key; the
// returned path is the remote directory that contained them, suitable for the
// backend's ModelFile/ModelPath. fileIdx is advanced per staged file so the
// staging progress tracker stays accurate.
func (r *SmartRouter) stageDirectory(ctx context.Context, node *BackendNode, trackingKey, dir string, keyMapper *StagingKeyMapper, fileIdx *int, totalFiles int) (string, error) {
	var remoteDir string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}

		*fileIdx++
		fileName := filepath.Base(path)
		stageCtx := r.withStagingCallback(ctx, trackingKey, fileName, *fileIdx, totalFiles)
		xlog.Info("Staging file", "model", trackingKey, "node", node.Name, "field", "ModelDir", "file", fileName, "fileIndex", *fileIdx, "totalFiles", totalFiles)

		remoteFile, err := r.fileStager.EnsureRemote(stageCtx, node.ID, path, keyMapper.Key(path))
		if err != nil {
			return fmt.Errorf("staging %s: %w", path, err)
		}
		r.stagingTracker.FileComplete(trackingKey, *fileIdx, totalFiles)

		// Every file under dir shares the same remote parent directory; derive
		// it from this file's staged path and its path relative to dir.
		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			return relErr
		}
		remoteDir = DeriveRemoteModelPath(remoteFile, rel)

		r.stageCompanionFiles(ctx, node, path, keyMapper.Key)
		return nil
	})
	if err != nil {
		return "", err
	}
	if remoteDir == "" {
		return "", fmt.Errorf("model directory %s contains no files", dir)
	}
	return remoteDir, nil
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
// that resolve to existing files relative to the frontend models directory or
// the model's own directory. Option values are NOT rewritten — backends resolve
// them via ModelPath. keyFn generates the namespaced storage key for each file.
func (r *SmartRouter) stageGenericOptions(ctx context.Context, node *BackendNode, options []string, frontendModelsDir, modelDir string, keyFn func(string) string) {
	for _, opt := range options {
		optKey, val, ok := strings.Cut(opt, ":")
		if !ok || val == "" {
			continue
		}

		// Resolve the value to an existing path: absolute as-is, otherwise
		// relative to frontendModelsDir first, then the model's own directory
		// (where backends like sherpa-onnx keep companion assets such as
		// tokens.txt and espeak-ng-data).
		absPath, ok := resolveOptionPath(val, frontendModelsDir, modelDir)
		if !ok {
			continue
		}
		info, err := os.Stat(absPath)
		if err != nil {
			continue
		}

		// A directory option value (e.g. sherpa-onnx's espeak-ng-data) is staged
		// file-by-file so the whole tree is recreated beside the model on the
		// worker; a single file is staged directly. Values are never rewritten —
		// backends resolve relative paths via ModelPath.
		if err == nil && info.IsDir() {
			r.stageOptionDir(ctx, node, absPath, keyFn)
			xlog.Debug("Staged option directory", "option", optKey, "localPath", absPath)
			continue
		}

		key := keyFn(absPath)
		if _, err := r.fileStager.EnsureRemote(ctx, node.ID, absPath, key); err != nil {
			xlog.Warn("Failed to stage option file, skipping", "option", opt, "path", absPath, "error", err)
			continue
		}
		xlog.Debug("Staged option file", "option", optKey, "localPath", absPath)
	}
}

// resolveOptionPath finds an existing local path for an option value: an
// absolute path as-is, otherwise relative to frontendModelsDir, then to the
// model's own directory. Returns false when none exists.
func resolveOptionPath(val, frontendModelsDir, modelDir string) (string, bool) {
	if filepath.IsAbs(val) {
		if _, err := os.Stat(val); err == nil {
			return val, true
		}
		return "", false
	}
	for _, base := range []string{frontendModelsDir, modelDir} {
		if base == "" {
			continue
		}
		p := filepath.Join(base, val)
		if _, err := os.Stat(p); err == nil {
			return p, true
		}
	}
	return "", false
}

// stageOptionDir stages every regular file under an option-declared directory
// (e.g. sherpa-onnx's espeak-ng-data) using the structure-preserving key, so the
// tree is recreated beside the model on the worker. Per-file errors are logged
// and skipped; the option value itself is not rewritten.
func (r *SmartRouter) stageOptionDir(ctx context.Context, node *BackendNode, dir string, keyFn func(string) string) {
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() {
			return nil
		}
		if _, err := r.fileStager.EnsureRemote(ctx, node.ID, path, keyFn(path)); err != nil {
			xlog.Warn("Failed to stage option directory file, skipping", "path", path, "error", err)
		}
		return nil
	})
}

// probeHealth checks whether a backend process on the given node/addr is alive
// via a gRPC health check with a 2-second timeout. The client is closed after
// the check.
//
// The result is memoized in r.probeCache for probeCacheTTL. With per-request
// routing every inference call lands here, and unbounded re-probing can stall
// behind a busy backend that serializes HealthCheck against active Predict.
// Concurrent probes for the same (node, addr) coalesce via singleflight so a
// burst of N requests for a cold cache costs at most one round-trip, not N.
// Failed probes invalidate the cache so the staleness recovery path
// (DecrementInFlight + RemoveNodeModel) still triggers on the next request.
func (r *SmartRouter) probeHealth(ctx context.Context, node *BackendNode, addr string) bool {
	key := node.ID + "|" + addr
	return r.probeCache.DoOrCached(key, func() bool {
		client := r.buildClientForAddr(node, addr, false)
		defer closeClient(client)
		checkCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		ok, _ := client.HealthCheck(checkCtx)
		return ok
	})
}

// closeClient closes a gRPC backend client if it implements io.Closer.
func closeClient(client grpc.Backend) {
	if closer, ok := client.(io.Closer); ok {
		closer.Close()
	}
}

// UnloadModel sends a NATS unload event to a specific node for the given model
// and removes every replica row for (nodeID, modelName).
// The worker process handles Free() + kill + deregister.
func (r *SmartRouter) UnloadModel(ctx context.Context, nodeID, modelName string) error {
	if r.unloader == nil {
		return fmt.Errorf("no remote unloader configured")
	}
	// Target the specific node, not all nodes hosting this model
	if err := r.unloader.StopBackend(nodeID, modelName); err != nil {
		return fmt.Errorf("failed to stop backend on node %s: %w", nodeID, err)
	}
	r.registry.RemoveAllNodeModelReplicas(ctx, nodeID, modelName)
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
			// Remove inside the same transaction. Target the specific replica row
			// by ID so we don't accidentally delete sibling replicas of the same
			// model on the same node.
			return tx.Where("id = ?", lru.ID).Delete(&NodeModel{}).Error
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
