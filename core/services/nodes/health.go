package nodes

import (
	"cmp"
	"context"
	"io"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/advisorylock"
	"github.com/mudler/LocalAI/core/services/cluster"
	grpc "github.com/mudler/LocalAI/pkg/grpc"
	"github.com/mudler/xlog"
	"gorm.io/gorm"
)

// perModelMissThreshold is the number of consecutive failed gRPC probes
// against a model's backend before the model is removed from the registry.
// A single failure can be transient (network blip, brief GC pause on the
// worker, a long-running request hogging the gRPC server thread); requiring
// N consecutive misses avoids deleting healthy rows over noise. At the
// default 15s tick this means a model has to be unreachable for ~45s before
// it gets reaped.
const perModelMissThreshold = 3

// modelKey identifies a specific (node, model, replica) tuple. We track miss
// counts per tuple because the same model name can be loaded on multiple
// replicas on the same node.
type modelKey struct {
	NodeID       string
	ModelName    string
	ReplicaIndex int
}

// NodePresenceReader says what a deployment knows about the tunnel of a worker.
// Satisfied by *cluster.Registry.
type NodePresenceReader interface {
	Presence(ctx context.Context, nodeID string, grace time.Duration) (cluster.Presence, error)
}

// HealthMonitor periodically checks the health of registered backend nodes.
type HealthMonitor struct {
	registry            NodeHealthStore
	db                  *gorm.DB // if non-nil, use advisory lock so only one frontend runs checks
	checkInterval       time.Duration
	staleThreshold      time.Duration
	autoOffline         bool                 // mark stale nodes as offline (preserves approval status)
	clientFactory       BackendClientFactory // creates gRPC backend clients
	perModelHealthCheck bool                 // check each model's backend process individually
	missesMu            sync.Mutex
	misses              map[modelKey]int // consecutive failed-probe counts; reset on success or model removal
	cancel              context.CancelFunc
	cancelMu            sync.Mutex

	// presence and tunnelActive are set by UsePresence. They are read by the
	// loop and written before it starts.
	presence       NodePresenceReader
	reconnectGrace time.Duration
	tunnelActive   func() bool
}

// UsePresence makes the monitor read the tunnel of each node while the tunnel is
// the active carrier. A node that heartbeats over HTTP and holds no tunnel is up
// and cannot be reached, and nothing else shows it: the heartbeat is fine, and
// the verbs that fail with no route are a fact of one request. Without this
// read, a scheduler that marks such a node unhealthy on a failed request sees
// the monitor promote it again at the next tick, and the node flaps.
//
// tunnelActive is asked at each tick. When NATS is the active carrier no node
// holds a tunnel and nothing is read. A grace of zero or less takes
// cluster.DefaultReconnectGrace. Call it before Start.
func (hm *HealthMonitor) UsePresence(reader NodePresenceReader, grace time.Duration, tunnelActive func() bool) {
	hm.presence = reader
	hm.reconnectGrace = cmp.Or(grace, cluster.DefaultReconnectGrace)
	hm.tunnelActive = tunnelActive
}

// tunnelDeparted reports whether this deployment has decided that the tunnel of a
// node is gone: no live replica holds it, and the departure is older than the
// reconnect grace.
//
// Only cluster.PresenceGone answers true. Reconnecting is a worker that is
// dialling again, unknown is a worker that never dialled or whose departure aged
// out, and a read that fails is no answer at all. Acting on any of them would
// demote a fleet for a reason that has nothing to do with a worker. It applies to
// every node type, because an agent worker is reached through its tunnel and
// nothing else.
func (hm *HealthMonitor) tunnelDeparted(ctx context.Context, node *BackendNode) bool {
	if hm.presence == nil || hm.tunnelActive == nil || !hm.tunnelActive() || node == nil {
		return false
	}
	p, err := hm.presence.Presence(ctx, node.ID, hm.reconnectGrace)
	if err != nil {
		xlog.Warn("Health monitor could not read node presence; leaving the node's status alone",
			"node", node.Name, "nodeID", node.ID, "error", err)
		return false
	}
	return p == cluster.PresenceGone
}

// NewHealthMonitor creates a new HealthMonitor.
// If db is non-nil (PostgreSQL), an advisory lock is used so that only one
// frontend instance runs health checks at a time in distributed mode.
// If clientFactory is nil, a default factory using the given authToken is used.
func NewHealthMonitor(registry NodeHealthStore, db *gorm.DB, checkInterval, staleThreshold time.Duration, authToken string, perModelHealthCheck bool, clientFactory ...BackendClientFactory) *HealthMonitor {
	checkInterval = cmp.Or(checkInterval, 15*time.Second)
	// Heartbeat checkpointing lets last_heartbeat sit up to one checkpoint
	// interval behind by design, so a hardcoded 60s fallback here would mark
	// every healthy, beating node offline. Track the shared default instead,
	// which is derived from that interval.
	staleThreshold = cmp.Or(staleThreshold, config.DefaultStaleNodeThreshold)
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
		misses:              make(map[modelKey]int),
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
// Node liveness is determined by heartbeat freshness — both backend and agent
// workers send periodic HTTP heartbeats to the frontend, so a stale heartbeat
// means the worker supervisor is down. This is simpler and more reliable than
// probing individual gRPC backend processes (which can crash independently).
//
// Per-model health checks (opt-in) separately probe each model's gRPC address
// and remove stale model records without affecting the node's overall status.
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

		// Node liveness: heartbeat staleness check.
		// Workers (both backend and agent) send HTTP heartbeats to the frontend.
		// If the heartbeat is stale, the worker is presumed down.
		if time.Since(node.LastHeartbeat) > hm.staleThreshold {
			// Skip nodes already marked offline/unhealthy — re-marking them
			// every cycle floods the log with the same WARN+INFO pair for
			// nodes the operator has intentionally taken down.
			if node.Status == StatusOffline || node.Status == StatusUnhealthy {
				continue
			}
			xlog.Warn("Node heartbeat stale", "node", node.Name, "lastHeartbeat", node.LastHeartbeat)
			if hm.autoOffline {
				xlog.Info("Marking stale node offline", "node", node.Name)
				if err := hm.registry.MarkOffline(ctx, node.ID); err != nil {
					xlog.Error("Failed to mark stale node offline", "node", node.Name, "error", err)
				}
			} else {
				hm.registry.MarkUnhealthy(ctx, node.ID)
			}
			continue
		}

		// The heartbeat is fresh, so the supervisor of the worker is alive. That is
		// not the same as the deployment being able to reach it: the heartbeat is
		// an HTTP call that does not use the tunnel, and a worker can send it for
		// ever with no tunnel (a proxy that stopped upgrading websockets, a
		// credential that was rotated, a reconnect loop longer than the grace).
		//
		// The node is demoted and not marked offline. MarkUnhealthy is status
		// only, and status is enough: routing selects on status=healthy, so the
		// loaded rows stop being chosen and the model is placed somewhere that can
		// be reached. MarkOffline deletes the rows of the node, and deleting rows
		// on a presence read would give any defect in that read the largest blast
		// radius in the system.
		//
		// No re-promotion and no per-model probes follow: the probes would dial a
		// worker that has no route and count none of it, and the promotion below
		// would undo the demotion at the next tick.
		if hm.tunnelDeparted(ctx, &node) {
			if node.Status != StatusUnhealthy && node.Status != StatusOffline {
				xlog.Warn("Node is heartbeating but its tunnel has been gone longer than the reconnect grace; marking unhealthy",
					"node", node.Name, "nodeID", node.ID, "type", node.NodeType, "grace", hm.reconnectGrace)
				if err := hm.registry.MarkUnhealthy(ctx, node.ID); err != nil {
					xlog.Error("Failed to mark a departed node unhealthy", "node", node.Name, "error", err)
				}
			}
			continue
		}

		// Heartbeat is fresh — node is alive
		if node.Status == StatusUnhealthy || node.Status == StatusOffline {
			xlog.Info("Node recovered", "node", node.Name)
			if err := hm.registry.MarkHealthy(ctx, node.ID); err != nil {
				xlog.Error("Failed to mark node healthy", "node", node.Name, "error", err)
			}
		}

		// Per-model backend health check: probe each model's gRPC address and
		// remove stale model records. This does NOT affect the node's status —
		// a crashed backend process is a model-level issue, not a node-level
		// one. A model is only removed after perModelMissThreshold consecutive
		// failed probes so a single network/GC blip doesn't force a reload.
		if hm.perModelHealthCheck {
			models, _ := hm.registry.GetNodeModels(ctx, node.ID)
			for _, m := range models {
				if m.Address == "" || m.Address == node.Address {
					continue
				}
				mClient := hm.clientFactory.NewClient(node.ID, m.Address, false)
				mCheckCtx, mCancel := context.WithTimeout(ctx, 5*time.Second)
				ok, _ := mClient.HealthCheck(mCheckCtx)
				mCancel()
				// A dial that failed in the transport says nothing about the
				// backend. It is not a miss, and it does not clear a streak either.
				transportFailed := !ok && grpc.TransportFailureOf(mClient) != nil
				if closer, ok := mClient.(io.Closer); ok {
					closer.Close()
				}
				if transportFailed {
					xlog.Debug("Model backend probe could not reach the backend, not counting it",
						"node", node.ID, "model", m.ModelName, "replica", m.ReplicaIndex, "address", m.Address)
					continue
				}

				key := modelKey{NodeID: node.ID, ModelName: m.ModelName, ReplicaIndex: m.ReplicaIndex}
				hm.missesMu.Lock()
				if ok {
					// Probe succeeded — wipe any previous miss streak.
					delete(hm.misses, key)
					hm.missesMu.Unlock()
					continue
				}
				hm.misses[key]++
				misses := hm.misses[key]
				hm.missesMu.Unlock()

				if misses < perModelMissThreshold {
					xlog.Debug("Model backend probe failed, awaiting threshold before removal",
						"node", node.ID, "model", m.ModelName, "replica", m.ReplicaIndex,
						"address", m.Address, "misses", misses, "threshold", perModelMissThreshold)
					continue
				}
				xlog.Warn("Model backend unhealthy after consecutive misses, removing from registry",
					"node", node.ID, "model", m.ModelName, "replica", m.ReplicaIndex,
					"address", m.Address, "misses", misses)
				if err := hm.registry.RemoveNodeModel(ctx, node.ID, m.ModelName, m.ReplicaIndex); err != nil {
					xlog.Warn("Failed to remove unhealthy model from registry",
						"node", node.ID, "model", m.ModelName, "replica", m.ReplicaIndex, "error", err)
					// Leave the miss counter in place so the next tick retries
					// the removal rather than starting the streak over.
					continue
				}
				hm.missesMu.Lock()
				delete(hm.misses, key)
				hm.missesMu.Unlock()
			}
		}
	}
}
