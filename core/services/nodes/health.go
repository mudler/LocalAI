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

// HealthMonitor periodically checks the health of registered backend nodes.
type HealthMonitor struct {
	registry            NodeHealthStore
	db                  *gorm.DB // if non-nil, use advisory lock so only one frontend runs checks
	checkInterval       time.Duration
	staleThreshold      time.Duration
	autoOffline         bool                 // mark stale nodes as offline (preserves approval status)
	clientFactory       BackendClientFactory // creates gRPC backend clients
	perModelHealthCheck bool                 // check each model's backend process individually
	// presence and reconnectGrace are the second liveness mechanism. The
	// heartbeat says the worker's supervisor is alive; presence says whether
	// anything in this deployment can still reach its backends. See
	// tunnelDeparted. nil disables the second mechanism entirely.
	presence       NodePresenceReader
	reconnectGrace time.Duration
	// departures is the one place a node's departure evicts per-node state.
	// See DepartureNotifier: this monitor is its only caller.
	departures *DepartureNotifier
	missesMu   sync.Mutex
	// misses holds one consecutive-failed-probe count per (node, model,
	// replica). It is bounded to the rows the LAST completed pass actually
	// walked; see forgetUnseenMisses for why that, and not a departure
	// subscription, is what bounds it.
	misses   map[modelKey]int
	cancel   context.CancelFunc
	cancelMu sync.Mutex
}

// NewHealthMonitor creates a new HealthMonitor.
// If db is non-nil (PostgreSQL), an advisory lock is used so that only one
// frontend instance runs health checks at a time in distributed mode.
// clientFactory is what reaches a worker's backends, over that worker's tunnel.
// Omitting it (or passing nil) leaves the monitor with a factory that refuses
// every request, so per-model probes are skipped and logged rather than counted
// as misses; authToken is then only the credential a working factory would have
// carried. Production always passes one.
//
// presence and reconnectGrace are a REQUIRED positional pair rather than another
// optional tail, so a caller has to decide rather than inherit a nil. A nil
// reader means this deployment has nothing that can say a worker is gone, which
// is correct for a single-node install and wrong for a distributed one; a zero
// grace with a non-nil reader takes the documented default, because a zero
// window would make every departure a verdict the instant it was stamped.
//
// departures is positional and REQUIRED for the same reason, not a setter and
// not an option: a caller that does not pass one fails to COMPILE. A monitor
// with no notifier is a deployment where a departed node's per-node caches are
// never dropped, and there is no other symptom. Passing nil is still legal and
// still says something, namely that this deployment has no caches to drop; a
// distributed one always passes the notifier its subscribers were registered on.
func NewHealthMonitor(registry NodeHealthStore, db *gorm.DB, checkInterval, staleThreshold time.Duration, authToken string, perModelHealthCheck bool, presence NodePresenceReader, reconnectGrace time.Duration, departures *DepartureNotifier, clientFactory ...BackendClientFactory) *HealthMonitor {
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
		presence:            presence,
		reconnectGrace:      cmp.Or(reconnectGrace, config.DefaultWorkerReconnectGrace),
		departures:          departures,
		misses:              make(map[modelKey]int),
	}
}

// ReadsAbsence reports whether this monitor has a source for the second
// liveness mechanism.
//
// It exists to be asserted at wiring time (see core/application), and that is
// worth stating because it is the only symptom the wiring has. A monitor built
// without a presence reader does not fail, log, or behave oddly: it reports a
// worker whose tunnel died an hour ago as healthy, forever, which is exactly
// what a healthy fleet looks like.
func (hm *HealthMonitor) ReadsAbsence() bool { return hm != nil && hm.presence != nil }

// Departures returns the notifier this monitor fires departures on.
//
// Exposed so the wiring can be asserted on the object production built rather
// than on a notifier a spec assembled itself: the subscribers are registered by
// core/application on a notifier it then hands to this constructor, and a
// deployment that registered them on a DIFFERENT notifier evicts nothing while
// a listing of that other notifier's registrations still names all four.
func (hm *HealthMonitor) Departures() *DepartureNotifier {
	if hm == nil {
		return nil
	}
	return hm.departures
}

// tunnelDeparted reports whether this deployment has decided that a node's
// tunnel is gone: no live replica holds it and the departure has outlived the
// reconnect grace.
//
// Only cluster.PresenceGone answers true. PresenceReconnecting is a worker
// re-dialling right now, PresenceUnknown is a worker that has never dialled or
// whose departure aged out, and a query that FAILS is not an answer at all.
// Acting on any of those would demote a fleet for a reason that has nothing to
// do with any worker, which is the collapse this whole mechanism replaced.
//
// EVERY worker type, and that is the change this rule most recently absorbed.
// It used to skip agent nodes, because an agent worker dialled no tunnel and
// took its jobs and its verbs over the message bus instead, so a departure row
// for one said nothing about whether it could work. There is no bus left: an
// agent worker is reachable through its tunnel and through nothing else, so a
// departed agent tunnel means exactly what a departed backend tunnel means and
// the skip would now hide the only symptom an unreachable agent worker has.
//
// The rule itself did not widen, only the set of nodes it applies to. The three
// answers nobody may act on stay unacted on for both types.
func (hm *HealthMonitor) tunnelDeparted(ctx context.Context, node *BackendNode) bool {
	if hm.presence == nil || node == nil {
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
		// No prune. A pass that could not read the fleet observed nothing, and
		// pruning against nothing would wipe every streak in progress.
		return
	}

	// Every model row this pass walked, whether or not it managed to probe it.
	// See forgetUnseenMisses.
	seen := make(map[modelKey]struct{})

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

		// The heartbeat is fresh, so the worker's supervisor is alive. That is
		// NOT the same as this deployment being able to reach its backends:
		// those are reached over the worker's TUNNEL, and a worker can
		// heartbeat forever with no tunnel at all (a proxy that stopped
		// upgrading WebSockets, a rotated registration credential, a reconnect
		// loop longer than the grace).
		//
		// Before this check the two were conflated and the result was a node
		// wedged in plain sight: listed healthy, heartbeating, with every
		// request for a model already loaded on it failing "no route to that
		// worker" indefinitely. Nothing reaped it, because every reaper keys on
		// the heartbeat and the heartbeat was fine.
		//
		// Demoted and not marked offline, deliberately. MarkUnhealthy is
		// status-only, and status is enough: routing and eviction both select
		// on status=healthy, so the loaded rows stop being chosen and the model
		// is placed somewhere reachable on the next request. MarkOffline would
		// DELETE this node's rows, and deleting rows on a presence read would
		// give any future defect in that read the largest blast radius in the
		// system for no gain the demotion does not already deliver.
		if hm.tunnelDeparted(ctx, &node) {
			if node.Status != StatusUnhealthy && node.Status != StatusOffline {
				xlog.Warn("Node is heartbeating but its tunnel has been gone longer than the reconnect grace; marking unhealthy",
					"node", node.Name, "nodeID", node.ID, "type", node.NodeType, "grace", hm.reconnectGrace)
				if err := hm.registry.MarkUnhealthy(ctx, node.ID); err != nil {
					xlog.Error("Failed to mark a departed node unhealthy", "node", node.Name, "error", err)
				}
			}
			// The ONE place a departure evicts per-node state, and it is fired
			// from THIS branch only. The stale-heartbeat branch above already
			// marks the node offline, which deletes its rows and runs the
			// registry's replica-removed hooks; firing here as well would
			// double-evict and, worse, would make the notification mean two
			// different things at its subscribers.
			hm.departures.Departed(DepartedNode{ID: node.ID, Name: node.Name, Type: node.NodeType})
			// No re-promotion, and no per-model probes. The probes would dial a
			// worker there is no route to, once per model per tick, and decline
			// to count any of it; the re-promotion below is what used to undo
			// this demotion on the very next tick.
			continue
		}

		// Present, so the NEXT departure of this node is announced again. This
		// runs on every tick for every live node and must stay a map delete:
		// the notifier is edge triggered, and re-arming here is the edge.
		hm.departures.Present(node.ID)

		// Heartbeat is fresh and the tunnel is not gone: the node is alive
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
				// A row with no address names no backend process, so there is
				// nothing to probe. The old second arm of this test skipped a
				// replica whose address equalled the NODE's; a node has no
				// address any more, so that comparison could only ever be true
				// for two empty strings and has been dropped rather than left
				// to read as a live rule.
				if m.WorkerLocalAddress == "" {
					continue
				}
				// Marked seen BEFORE the probe, so that a row this pass could
				// not reach keeps the streak it already had. The unreachable
				// branch below leaves that streak exactly as it was, and a
				// prune that only counted PROBED rows would clear it instead,
				// forgiving a backend that really has died every time a peer
				// link blipped.
				key := modelKey{NodeID: node.ID, ModelName: m.ModelName, ReplicaIndex: m.ReplicaIndex}
				seen[key] = struct{}{}
				// Through the node's tunnel, never a direct dial to m.WorkerLocalAddress:
				// that address is a port inside the worker. A worker this
				// replica cannot reach is not evidence that its backend died,
				// so the miss counter is left alone and the row survives;
				// counting it as a miss would reap live models across the whole
				// fleet the moment the tunnel wiring was wrong.
				mClient, err := hm.clientFactory.NewClientForNode(node.ID, m.WorkerLocalAddress, false)
				if err != nil {
					xlog.Error("Skipping model health probe: no way to reach the worker",
						"node", node.ID, "model", m.ModelName, "replica", m.ReplicaIndex, "error", err)
					continue
				}
				mCheckCtx, mCancel := context.WithTimeout(ctx, 5*time.Second)
				ok, _ := mClient.HealthCheck(mCheckCtx)
				mCancel()
				// Asked BEFORE the client is closed, because closing is what
				// would discard the transport's record of why it failed.
				unreached := unroutable(mClient)
				if closer, ok := mClient.(io.Closer); ok {
					closer.Close()
				}
				if unreached != nil {
					// The probe never reached a backend, so it observed
					// nothing. The miss streak is left exactly as it was:
					// neither advanced, which after three passes would delete
					// this row and every other row in the fleet the moment a
					// peer link blipped, nor cleared, which would forgive a
					// backend that really has died.
					xlog.Warn("Could not probe a model backend: no route to the worker",
						"node", node.ID, "model", m.ModelName, "replica", m.ReplicaIndex, "error", unreached)
					continue
				}

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
						"address", m.WorkerLocalAddress, "misses", misses, "threshold", perModelMissThreshold)
					continue
				}
				xlog.Warn("Model backend unhealthy after consecutive misses, removing from registry",
					"node", node.ID, "model", m.ModelName, "replica", m.ReplicaIndex,
					"address", m.WorkerLocalAddress, "misses", misses)
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

	hm.forgetUnseenMisses(seen)
}

// forgetUnseenMisses drops every miss streak whose model row this pass did not
// walk, so the map is bounded by the fleet's live rows rather than by every
// (node, model, replica) the process has ever seen.
//
// It is level triggered and it is deliberately NOT a subscriber on the
// departure notifier, though the notifier is where the rest of the per-node
// caches are evicted from and it would have been the shorter change.
//
// A departure subscription would fix ONE of the four ways a streak is
// stranded. The others are a node marked offline or unhealthy on a stale
// heartbeat, a node the operator set draining, and a model row removed by
// anything other than this loop (an unload, a scale-down, an eviction). Each
// leaves a counter with no row behind it, and each would need its own hook.
// What all four have in common is exactly the thing this reads: the row was
// not there when the pass looked. So the notifier keeps its one meaning, "no
// live replica holds this node's tunnel", and this keeps its own, "there is no
// longer a row to count misses against".
//
// Forgetting is safe in one direction only, and it is the safe one: a cleared
// streak DELAYS a reap by up to perModelMissThreshold passes and can never
// cause one. That is what makes it sound to prune on a pass whose
// GetNodeModels call failed, which is otherwise indistinguishable here from a
// node with no models.
func (hm *HealthMonitor) forgetUnseenMisses(seen map[modelKey]struct{}) {
	hm.missesMu.Lock()
	defer hm.missesMu.Unlock()
	for key := range hm.misses {
		if _, ok := seen[key]; !ok {
			delete(hm.misses, key)
		}
	}
}

// missCount reports the streak recorded for one model row. It exists for the
// specs that assert this map is bounded, which is otherwise a property with no
// observable effect until the process runs out of memory.
func (hm *HealthMonitor) missCount(nodeID, modelName string, replicaIndex int) int {
	hm.missesMu.Lock()
	defer hm.missesMu.Unlock()
	return hm.misses[modelKey{NodeID: nodeID, ModelName: modelName, ReplicaIndex: replicaIndex}]
}

// trackedMisses reports how many model rows currently hold a miss streak.
func (hm *HealthMonitor) trackedMisses() int {
	hm.missesMu.Lock()
	defer hm.missesMu.Unlock()
	return len(hm.misses)
}
