package nodes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/mudler/LocalAI/core/services/advisorylock"
	grpcclient "github.com/mudler/LocalAI/pkg/grpc"
	"github.com/mudler/xlog"
	"github.com/nats-io/nats.go"
	"gorm.io/gorm"
)

// ModelProber checks whether a model's backend process is still reachable.
// Defaulted to a gRPC health probe but overridable for tests so we don't
// need to stand up a real server. Returning false without an error means the
// process is reachable but unhealthy (same as a timeout for our purposes).
type ModelProber interface {
	IsAlive(ctx context.Context, address string) bool
}

// grpcModelProber does a 1s HealthCheck on the model's stored gRPC address.
type grpcModelProber struct{ token string }

func (g grpcModelProber) IsAlive(ctx context.Context, address string) bool {
	client := grpcclient.NewClientWithToken(address, false, nil, false, g.token)
	probeCtx, cancel := context.WithTimeout(ctx, 1*time.Second)
	defer cancel()
	ok, _ := client.HealthCheck(probeCtx)
	return ok
}

// ReplicaReconciler periodically ensures model replica counts match their
// scheduling configs. It scales up replicas when below MinReplicas or when
// all replicas are busy (up to MaxReplicas), and scales down idle replicas
// above MinReplicas.
//
// Alongside replica scaling it runs two state-reconciliation passes — draining
// the pending_backend_ops queue and probing loaded models' gRPC addresses to
// orphan ghosts. Both passes are wrapped in the KeyStateReconciler advisory
// lock so N frontends don't stampede.
//
// Only processes models with auto-scaling enabled (MinReplicas > 0 or MaxReplicas > 0).
type ReplicaReconciler struct {
	registry       *NodeRegistry
	scheduler      ModelScheduler // interface for scheduling new models
	unloader       NodeCommandSender
	adapter        *RemoteUnloaderAdapter // NATS sender for pending-op drain
	prober         ModelProber            // health probe for model gRPC addrs
	db             *gorm.DB
	interval       time.Duration
	scaleDownDelay time.Duration
	// probeStaleAfter: only probe node_models rows older than this so we
	// don't hammer every worker every tick for models we just heard from.
	probeStaleAfter time.Duration
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
	Registry *NodeRegistry
	Scheduler ModelScheduler
	Unloader NodeCommandSender
	// Adapter is the NATS sender used to retry pending backend ops. When nil,
	// the state-reconciler pending-drain pass is a no-op (single-node mode).
	Adapter *RemoteUnloaderAdapter
	// RegistrationToken is used by the default gRPC prober when probing model
	// addresses. Matches the worker's token so HealthCheck auth succeeds.
	RegistrationToken string
	// Prober overrides the default gRPC health probe (used by tests).
	Prober ModelProber
	DB              *gorm.DB
	Interval        time.Duration // default 30s
	ScaleDownDelay  time.Duration // default 5m
	ProbeStaleAfter time.Duration // default 2m
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
	probeStaleAfter := opts.ProbeStaleAfter
	if probeStaleAfter == 0 {
		probeStaleAfter = 2 * time.Minute
	}
	prober := opts.Prober
	if prober == nil {
		prober = grpcModelProber{token: opts.RegistrationToken}
	}
	return &ReplicaReconciler{
		registry:        opts.Registry,
		scheduler:       opts.Scheduler,
		unloader:        opts.Unloader,
		adapter:         opts.Adapter,
		prober:          prober,
		db:              opts.DB,
		interval:        interval,
		scaleDownDelay:  scaleDownDelay,
		probeStaleAfter: probeStaleAfter,
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

// reconcileOnce performs a single reconciliation pass. Replica work and
// state-reconciliation work run under *different* advisory locks so multiple
// frontends can share load across passes, and one long-running pass doesn't
// block the other forever if a frontend wedges.
func (rc *ReplicaReconciler) reconcileOnce(ctx context.Context) {
	if rc.db != nil {
		replicaKey := advisorylock.KeyFromString("replica-reconciler")
		_ = advisorylock.WithLockCtx(ctx, rc.db, replicaKey, func() error {
			rc.reconcile(ctx)
			return nil
		})
		// Try, don't block: if another frontend is already running the state
		// pass, this tick is a no-op. Matches the health monitor pattern.
		_, _ = advisorylock.TryWithLockCtx(ctx, rc.db, advisorylock.KeyStateReconciler, func() error {
			rc.reconcileState(ctx)
			return nil
		})
	} else {
		rc.reconcile(ctx)
		rc.reconcileState(ctx)
	}
}

// reconcileState runs the state-reconciliation passes: drain pending backend
// ops for freshly-healthy nodes, then probe model gRPC addresses to orphan
// ghosts. Both passes are best-effort: a failure on one node doesn't stop
// the rest.
func (rc *ReplicaReconciler) reconcileState(ctx context.Context) {
	if rc.adapter != nil {
		rc.drainPendingBackendOps(ctx)
	}
	rc.probeLoadedModels(ctx)
}

// drainPendingBackendOps retries queued backend ops whose next_retry_at has
// passed on nodes that are currently healthy. On success the row is deleted;
// on failure attempts++ and next_retry_at moves out via exponential backoff.
func (rc *ReplicaReconciler) drainPendingBackendOps(ctx context.Context) {
	ops, err := rc.registry.ListDuePendingBackendOps(ctx)
	if err != nil {
		xlog.Warn("Reconciler: failed to list pending backend ops", "error", err)
		return
	}
	if len(ops) == 0 {
		return
	}
	xlog.Debug("Reconciler: draining pending backend ops", "count", len(ops))

	for _, op := range ops {
		if err := ctx.Err(); err != nil {
			return
		}
		var applyErr error
		switch op.Op {
		case OpBackendDelete:
			_, applyErr = rc.adapter.DeleteBackend(op.NodeID, op.Backend)
		case OpBackendInstall, OpBackendUpgrade:
			reply, err := rc.adapter.InstallBackend(op.NodeID, op.Backend, "", string(op.Galleries), "", "", "")
			if err != nil {
				applyErr = err
			} else if !reply.Success {
				applyErr = fmt.Errorf("%s failed: %s", op.Op, reply.Error)
			}
		default:
			xlog.Warn("Reconciler: unknown pending op", "op", op.Op, "id", op.ID)
			continue
		}

		if applyErr == nil {
			if err := rc.registry.DeletePendingBackendOp(ctx, op.ID); err != nil {
				xlog.Warn("Reconciler: failed to delete drained op row", "id", op.ID, "error", err)
			} else {
				xlog.Info("Reconciler: pending backend op applied",
					"op", op.Op, "backend", op.Backend, "node", op.NodeID, "attempts", op.Attempts+1)
			}
			continue
		}

		// ErrNoResponders means the node has no active NATS subscription for
		// this subject. Either its connection dropped, or it's the wrong
		// node type entirely. Mark unhealthy so the health monitor's
		// heartbeat-only pass doesn't immediately flip it back — and so
		// ListDuePendingBackendOps (which filters by status=healthy) stops
		// picking the row until the node genuinely recovers.
		if errors.Is(applyErr, nats.ErrNoResponders) {
			xlog.Warn("Reconciler: no NATS responders — marking node unhealthy",
				"op", op.Op, "backend", op.Backend, "node", op.NodeID)
			_ = rc.registry.MarkUnhealthy(ctx, op.NodeID)
		}

		// Dead-letter cap: after maxAttempts the row is the reconciler
		// equivalent of a poison message. Delete it loudly so the queue
		// doesn't churn NATS every tick forever — operators can re-issue
		// the op from the UI if they still want it applied.
		if op.Attempts+1 >= maxPendingBackendOpAttempts {
			xlog.Error("Reconciler: abandoning pending backend op after max attempts",
				"op", op.Op, "backend", op.Backend, "node", op.NodeID,
				"attempts", op.Attempts+1, "last_error", applyErr)
			if err := rc.registry.DeletePendingBackendOp(ctx, op.ID); err != nil {
				xlog.Warn("Reconciler: failed to delete abandoned op row", "id", op.ID, "error", err)
			}
			continue
		}

		_ = rc.registry.RecordPendingBackendOpFailure(ctx, op.ID, applyErr.Error())
		xlog.Warn("Reconciler: pending backend op retry failed",
			"op", op.Op, "backend", op.Backend, "node", op.NodeID, "attempts", op.Attempts+1, "error", applyErr)
	}
}

// maxPendingBackendOpAttempts caps how many times the reconciler retries a
// failing row before dead-lettering it. Ten attempts at exponential backoff
// (30s → 15m cap) is >1h of wall-clock patience — well past any transient
// worker restart or network blip. Poisoned rows beyond that are almost
// certainly structural (wrong node type, non-existent gallery entry) and no
// amount of further retrying will help.
const maxPendingBackendOpAttempts = 10

// probeLoadedModels gRPC-health-checks model addresses that the DB says are
// loaded. If a model's backend process is gone (OOM, crash, manual restart)
// we remove the row so ghosts don't linger. Only probes rows older than
// probeStaleAfter so we don't hammer every worker every tick for models we
// just heard from.
func (rc *ReplicaReconciler) probeLoadedModels(ctx context.Context) {
	var stale []NodeModel
	cutoff := time.Now().Add(-rc.probeStaleAfter)
	err := rc.registry.db.WithContext(ctx).
		Joins("JOIN backend_nodes ON backend_nodes.id = node_models.node_id").
		Where("node_models.state = ? AND backend_nodes.status = ? AND node_models.updated_at < ? AND node_models.address != ''",
			"loaded", StatusHealthy, cutoff).
		Find(&stale).Error
	if err != nil {
		xlog.Warn("Reconciler: failed to list loaded models for probe", "error", err)
		return
	}
	for _, m := range stale {
		if err := ctx.Err(); err != nil {
			return
		}
		if rc.prober.IsAlive(ctx, m.Address) {
			// Bump updated_at so we don't probe this row again immediately.
			_ = rc.registry.db.WithContext(ctx).Model(&NodeModel{}).
				Where("id = ?", m.ID).Update("updated_at", time.Now()).Error
			continue
		}
		if err := rc.registry.RemoveNodeModel(ctx, m.NodeID, m.ModelName); err != nil {
			xlog.Warn("Reconciler: failed to remove unreachable model", "node", m.NodeID, "model", m.ModelName, "error", err)
			continue
		}
		xlog.Warn("Reconciler: model unreachable, removed from registry",
			"node", m.NodeID, "model", m.ModelName, "address", m.Address)
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
