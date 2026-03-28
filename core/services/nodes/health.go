package nodes

import (
	"cmp"
	"context"
	"time"

	"github.com/mudler/LocalAI/core/services/advisorylock"
	"github.com/mudler/LocalAI/core/services/messaging"
	grpc "github.com/mudler/LocalAI/pkg/grpc"
	"github.com/mudler/xlog"
	"gorm.io/gorm"
)

// HealthMonitor periodically checks the health of registered backend nodes.
type HealthMonitor struct {
	registry         *NodeRegistry
	db               *gorm.DB // if non-nil, use advisory lock so only one frontend runs checks
	checkInterval    time.Duration
	staleThreshold   time.Duration
	autoOffline      bool // mark stale nodes as offline (preserves approval status)
	cancel           context.CancelFunc
}

// NewHealthMonitor creates a new HealthMonitor.
// If db is non-nil (PostgreSQL), an advisory lock is used so that only one
// frontend instance runs health checks at a time in distributed mode.
func NewHealthMonitor(registry *NodeRegistry, db *gorm.DB, checkInterval, staleThreshold time.Duration) *HealthMonitor {
	checkInterval = cmp.Or(checkInterval, 15*time.Second)
	staleThreshold = cmp.Or(staleThreshold, 60*time.Second)
	return &HealthMonitor{
		registry:       registry,
		db:             db,
		checkInterval:  checkInterval,
		staleThreshold: staleThreshold,
		autoOffline:    true,
	}
}

// Start begins the health monitoring loop in a background goroutine.
func (hm *HealthMonitor) Start(ctx context.Context) {
	ctx, hm.cancel = context.WithCancel(ctx)
	go hm.run(ctx)
}

// Stop stops the health monitoring loop.
func (hm *HealthMonitor) Stop() {
	if hm.cancel != nil {
		hm.cancel()
	}
}

func (hm *HealthMonitor) run(ctx context.Context) {
	ticker := time.NewTicker(hm.checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			hm.checkAll(ctx)
		}
	}
}

func (hm *HealthMonitor) checkAll(ctx context.Context) {
	// In distributed mode, use an advisory lock so only one frontend runs checks
	if hm.db != nil {
		acquired, err := advisorylock.TryWithLock(hm.db, messaging.AdvisoryLockHealthCheck, func() error {
			hm.doCheckAll(ctx)
			return nil
		})
		if err != nil {
			xlog.Error("Health monitor advisory lock error", "error", err)
		}
		_ = acquired
		return
	}

	hm.doCheckAll(ctx)
}

// doCheckAll performs the actual health check logic for all nodes.
func (hm *HealthMonitor) doCheckAll(ctx context.Context) {
	nodes, err := hm.registry.List()
	if err != nil {
		xlog.Error("Health monitor: failed to list nodes", "error", err)
		return
	}

	for _, node := range nodes {
		if node.Status == StatusDraining {
			continue
		}

		// Check heartbeat staleness first
		if time.Since(node.LastHeartbeat) > hm.staleThreshold {
			xlog.Warn("Node heartbeat stale", "node", node.Name, "lastHeartbeat", node.LastHeartbeat)
			if hm.autoOffline {
				// Mark offline instead of deregistering — preserves the node row
				// so re-registration restores the previous approval status
				xlog.Info("Marking stale node offline", "node", node.Name)
				if err := hm.registry.MarkOffline(node.ID); err != nil {
					xlog.Error("Failed to mark stale node offline", "node", node.Name, "error", err)
				}
			} else {
				hm.registry.MarkUnhealthy(node.ID)
			}
			continue
		}

		// Only gRPC health-check nodes that have models loaded.
		// Idle nodes (no models) haven't started their gRPC process yet
		// in NATS mode — connection refused is expected, not a failure.
		// Heartbeats alone are sufficient to prove the supervisor is alive.
		models, _ := hm.registry.GetNodeModels(node.ID)
		if len(models) == 0 {
			continue
		}

		client := grpc.NewClient(node.Address, false, nil, false)
		checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		alive, err := client.HealthCheck(checkCtx)
		cancel()

		if !alive || err != nil {
			xlog.Warn("Node health check failed", "node", node.Name, "address", node.Address, "error", err)
			hm.registry.MarkUnhealthy(node.ID)
		} else if node.Status == StatusUnhealthy {
			// Node recovered
			xlog.Info("Node recovered", "node", node.Name)
			hm.registry.Heartbeat(node.ID, nil)
		}
	}
}
