package nodes

import (
	"cmp"
	"context"
	"io"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/advisorylock"
	"github.com/mudler/xlog"
	"gorm.io/gorm"
)

// HealthMonitor periodically checks the health of registered backend nodes.
type HealthMonitor struct {
	registry            NodeHealthStore
	db                  *gorm.DB // if non-nil, use advisory lock so only one frontend runs checks
	checkInterval       time.Duration
	staleThreshold      time.Duration
	autoOffline         bool                 // mark stale nodes as offline (preserves approval status)
	clientFactory       BackendClientFactory // creates gRPC backend clients
	perModelHealthCheck bool                 // check each model's backend process individually
	cancel              context.CancelFunc
	cancelMu            sync.Mutex
}

// NewHealthMonitor creates a new HealthMonitor.
// If db is non-nil (PostgreSQL), an advisory lock is used so that only one
// frontend instance runs health checks at a time in distributed mode.
// If clientFactory is nil, a default factory using the given authToken is used.
func NewHealthMonitor(registry NodeHealthStore, db *gorm.DB, checkInterval, staleThreshold time.Duration, authToken string, perModelHealthCheck bool, clientFactory ...BackendClientFactory) *HealthMonitor {
	checkInterval = cmp.Or(checkInterval, 15*time.Second)
	staleThreshold = cmp.Or(staleThreshold, 60*time.Second)
	var factory BackendClientFactory
	if len(clientFactory) > 0 && clientFactory[0] != nil {
		factory = clientFactory[0]
	} else {
		factory = &tokenClientFactory{token: authToken}
	}
	return &HealthMonitor{
		registry:            registry,
		db:                  db,
		checkInterval:       checkInterval,
		staleThreshold:      staleThreshold,
		autoOffline:         true,
		clientFactory:       factory,
		perModelHealthCheck: perModelHealthCheck,
	}
}

// Start begins the health monitoring loop in a background goroutine.
// If a previous instance is running, it is stopped first.
func (hm *HealthMonitor) Start(ctx context.Context) {
	hm.cancelMu.Lock()
	if hm.cancel != nil {
		hm.cancel() // stop previous instance
	}
	ctx, hm.cancel = context.WithCancel(ctx)
	hm.cancelMu.Unlock()
	go hm.run(ctx)
}

// Stop stops the health monitoring loop.
func (hm *HealthMonitor) Stop() {
	hm.cancelMu.Lock()
	defer hm.cancelMu.Unlock()
	if hm.cancel != nil {
		hm.cancel()
		hm.cancel = nil
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
		acquired, err := advisorylock.TryWithLockCtx(ctx, hm.db, advisorylock.KeyHealthCheck, func() error {
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
	nodes, err := hm.registry.List(ctx)
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
				if err := hm.registry.MarkOffline(ctx, node.ID); err != nil {
					xlog.Error("Failed to mark stale node offline", "node", node.Name, "error", err)
				}
			} else {
				hm.registry.MarkUnhealthy(ctx, node.ID)
			}
			continue
		}

		// Only gRPC health-check nodes that have models loaded.
		// Idle nodes (no models) haven't started their gRPC process yet
		// in NATS mode — connection refused is expected, not a failure.
		// Heartbeats alone are sufficient to prove the supervisor is alive.
		models, _ := hm.registry.GetNodeModels(ctx, node.ID)
		if len(models) == 0 {
			continue
		}

		client := hm.clientFactory.NewClient(node.Address, false)
		checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		alive, err := client.HealthCheck(checkCtx)
		cancel()

		if !alive || err != nil {
			xlog.Warn("Node health check failed", "node", node.Name, "address", node.Address, "error", err)
			hm.registry.MarkUnhealthy(ctx, node.ID)
			if closer, ok := client.(io.Closer); ok {
				closer.Close()
			}
			continue
		}

		// Close the node-level gRPC client now that we're done with it
		if closer, ok := client.(io.Closer); ok {
			closer.Close()
		}

		if node.Status == StatusUnhealthy {
			// Node recovered
			xlog.Info("Node recovered", "node", node.Name)
			if err := hm.registry.MarkHealthy(ctx, node.ID); err != nil {
				xlog.Error("Failed to mark node healthy", "node", node.Name, "error", err)
			}
		}

		// Per-model backend health check: probe each model's distinct gRPC address
		if hm.perModelHealthCheck {
			for _, m := range models {
				if m.Address == "" || m.Address == node.Address {
					continue
				}
				mClient := hm.clientFactory.NewClient(m.Address, false)
				mCheckCtx, mCancel := context.WithTimeout(ctx, 5*time.Second)
				if ok, _ := mClient.HealthCheck(mCheckCtx); !ok {
					xlog.Warn("Model backend unhealthy, removing from registry",
						"node", node.ID, "model", m.ModelName, "address", m.Address)
					hm.registry.RemoveNodeModel(ctx, node.ID, m.ModelName)
				}
				mCancel()
				if closer, ok := mClient.(io.Closer); ok {
					closer.Close()
				}
			}
		}
	}
}
