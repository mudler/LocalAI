package nodes

import (
	"context"
	"encoding/json"
	"time"

	"github.com/mudler/LocalAI/core/services/advisorylock"
	"github.com/mudler/xlog"
	"gorm.io/gorm"
)

// ReplicaReconciler periodically ensures model replica counts match their
// scheduling configs. It scales up replicas when below MinReplicas or when
// all replicas are busy (up to MaxReplicas), and scales down idle replicas
// above MinReplicas.
//
// Only processes models with auto-scaling enabled (MinReplicas > 0 or MaxReplicas > 0).
type ReplicaReconciler struct {
	registry       *NodeRegistry
	scheduler      ModelScheduler // interface for scheduling new models
	unloader       NodeCommandSender
	db             *gorm.DB
	interval       time.Duration
	scaleDownDelay time.Duration
}

// ModelScheduler abstracts the scheduling logic needed by the reconciler.
// SmartRouter implements this interface.
type ModelScheduler interface {
	// ScheduleAndLoadModel picks a node (optionally from candidateNodeIDs),
	// installs the backend, and loads the model. Returns the node used.
	ScheduleAndLoadModel(ctx context.Context, modelName string, candidateNodeIDs []string) (*BackendNode, error)
}

// ReplicaReconcilerOptions holds configuration for creating a ReplicaReconciler.
type ReplicaReconcilerOptions struct {
	Registry       *NodeRegistry
	Scheduler      ModelScheduler
	Unloader       NodeCommandSender
	DB             *gorm.DB
	Interval       time.Duration // default 30s
	ScaleDownDelay time.Duration // default 5m
}

// NewReplicaReconciler creates a new ReplicaReconciler.
func NewReplicaReconciler(opts ReplicaReconcilerOptions) *ReplicaReconciler {
	interval := opts.Interval
	if interval == 0 {
		interval = 30 * time.Second
	}
	scaleDownDelay := opts.ScaleDownDelay
	if scaleDownDelay == 0 {
		scaleDownDelay = 5 * time.Minute
	}
	return &ReplicaReconciler{
		registry:       opts.Registry,
		scheduler:      opts.Scheduler,
		unloader:       opts.Unloader,
		db:             opts.DB,
		interval:       interval,
		scaleDownDelay: scaleDownDelay,
	}
}

// Run starts the reconciliation loop. It blocks until ctx is cancelled.
func (rc *ReplicaReconciler) Run(ctx context.Context) {
	ticker := time.NewTicker(rc.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			rc.reconcileOnce(ctx)
		}
	}
}

// reconcileOnce performs a single reconciliation pass.
// Uses an advisory lock so only one frontend instance reconciles at a time.
func (rc *ReplicaReconciler) reconcileOnce(ctx context.Context) {
	if rc.db != nil {
		lockKey := advisorylock.KeyFromString("replica-reconciler")
		_ = advisorylock.WithLockCtx(ctx, rc.db, lockKey, func() error {
			rc.reconcile(ctx)
			return nil
		})
	} else {
		rc.reconcile(ctx)
	}
}

func (rc *ReplicaReconciler) reconcile(ctx context.Context) {
	configs, err := rc.registry.ListAutoScalingConfigs(ctx)
	if err != nil {
		xlog.Warn("Reconciler: failed to list auto-scaling configs", "error", err)
		return
	}

	for _, cfg := range configs {
		if err := ctx.Err(); err != nil {
			return // context cancelled
		}
		rc.reconcileModel(ctx, cfg)
	}
}

func (rc *ReplicaReconciler) reconcileModel(ctx context.Context, cfg ModelSchedulingConfig) {
	current, err := rc.registry.CountLoadedReplicas(ctx, cfg.ModelName)
	if err != nil {
		xlog.Warn("Reconciler: failed to count replicas", "model", cfg.ModelName, "error", err)
		return
	}

	// 1. Ensure minimum replicas
	if cfg.MinReplicas > 0 && int(current) < cfg.MinReplicas {
		needed := cfg.MinReplicas - int(current)
		xlog.Info("Reconciler: scaling up to meet minimum", "model", cfg.ModelName,
			"current", current, "min", cfg.MinReplicas, "adding", needed)
		rc.scaleUp(ctx, cfg, needed)
		return
	}

	// 2. Auto-scale up if all replicas are busy
	if current > 0 && (cfg.MaxReplicas == 0 || int(current) < cfg.MaxReplicas) {
		if rc.allReplicasBusy(ctx, cfg.ModelName) {
			xlog.Info("Reconciler: all replicas busy, scaling up", "model", cfg.ModelName,
				"current", current)
			rc.scaleUp(ctx, cfg, 1)
		}
	}

	// 3. Scale down idle replicas above minimum
	floor := cfg.MinReplicas
	if floor < 1 {
		floor = 1
	}
	if int(current) > floor {
		rc.scaleDownIdle(ctx, cfg, int(current), floor)
	}
}

// scaleUp schedules additional replicas of the model.
func (rc *ReplicaReconciler) scaleUp(ctx context.Context, cfg ModelSchedulingConfig, count int) {
	if rc.scheduler == nil {
		xlog.Warn("Reconciler: no scheduler available, cannot scale up")
		return
	}

	// Determine candidate nodes from selector
	var candidateNodeIDs []string
	if cfg.NodeSelector != "" {
		selector := parseSelector(cfg.NodeSelector)
		if len(selector) > 0 {
			candidates, err := rc.registry.FindNodesBySelector(ctx, selector)
			if err != nil || len(candidates) == 0 {
				xlog.Warn("Reconciler: no nodes match selector", "model", cfg.ModelName,
					"selector", cfg.NodeSelector)
				return
			}
			candidateNodeIDs = make([]string, len(candidates))
			for i, n := range candidates {
				candidateNodeIDs[i] = n.ID
			}
		}
	}

	for i := 0; i < count; i++ {
		node, err := rc.scheduler.ScheduleAndLoadModel(ctx, cfg.ModelName, candidateNodeIDs)
		if err != nil {
			xlog.Warn("Reconciler: failed to scale up replica", "model", cfg.ModelName,
				"attempt", i+1, "error", err)
			return // stop trying on first failure
		}
		xlog.Info("Reconciler: scaled up replica", "model", cfg.ModelName, "node", node.Name)
	}
}

// scaleDownIdle removes idle replicas above the floor.
func (rc *ReplicaReconciler) scaleDownIdle(ctx context.Context, cfg ModelSchedulingConfig, current, floor int) {
	if rc.unloader == nil {
		return
	}

	// Find idle replicas that have been unused for longer than scaleDownDelay
	cutoff := time.Now().Add(-rc.scaleDownDelay)
	var idleModels []NodeModel
	rc.registry.db.WithContext(ctx).
		Where("model_name = ? AND state = ? AND in_flight = 0 AND last_used < ?",
			cfg.ModelName, "loaded", cutoff).
		Order("last_used ASC").
		Find(&idleModels)

	toRemove := current - floor
	removed := 0
	for _, nm := range idleModels {
		if removed >= toRemove {
			break
		}
		// Remove from registry
		if err := rc.registry.RemoveNodeModel(ctx, nm.NodeID, nm.ModelName); err != nil {
			xlog.Warn("Reconciler: failed to remove model record", "error", err)
			continue
		}
		// Unload from worker
		if err := rc.unloader.UnloadModelOnNode(nm.NodeID, nm.ModelName); err != nil {
			xlog.Warn("Reconciler: unload failed (model already removed from registry)", "error", err)
		}
		xlog.Info("Reconciler: scaled down idle replica", "model", cfg.ModelName, "node", nm.NodeID)
		removed++
	}
}

// allReplicasBusy returns true if all loaded replicas of a model have in-flight requests.
func (rc *ReplicaReconciler) allReplicasBusy(ctx context.Context, modelName string) bool {
	var idleCount int64
	rc.registry.db.WithContext(ctx).Model(&NodeModel{}).
		Where("model_name = ? AND state = ? AND in_flight = 0", modelName, "loaded").
		Count(&idleCount)
	return idleCount == 0
}

// parseSelector decodes a JSON node selector string into a map.
func parseSelector(selectorJSON string) map[string]string {
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
