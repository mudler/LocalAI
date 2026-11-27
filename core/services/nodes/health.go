package nodes

import (
	"cmp"
	"context"
	"io"
	"slices"
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

	// judge says which routes of a node are live. UsePresence gives it the
	// tunnel rows; without them it reads the report of the worker only.
	judge RouteJudge
	// now is the clock of the route grace; time.Now when nil.
	now func() time.Time

	// routeMu guards noRouteSince and downLogged. Both live in this replica
	// only: the advisory lock gives each tick to one frontend, and a frontend
	// that has not looked at a node for a while starts its grace again, which
	// only delays a demotion.
	routeMu sync.Mutex
	// noRouteSince is when a node with a fresh heartbeat was first seen with no
	// live route that carries work, and when it was last seen so.
	noRouteSince map[string]noRoute
	// downLogged is the set of routes of each node last logged as down, so a
	// lost route is logged once and not at every tick.
	downLogged map[string]string
}

// UsePresence makes the monitor read the tunnel rows that every frontend shares
// for each node that holds a tunnel, whatever routes the other workers of the
// cluster use. A grace of zero or less takes cluster.DefaultReconnectGrace. The
// same grace applies to a NATS route that a worker reports lost, so a route
// that reconnects inside it does not flap. Call it before Start.
func (hm *HealthMonitor) UsePresence(reader NodePresenceReader, grace time.Duration) {
	hm.judge.Presence = reader
	hm.judge.Grace = grace
}

func (hm *HealthMonitor) grace() time.Duration {
	if hm.judge.Grace > 0 {
		return hm.judge.Grace
	}
	return cluster.DefaultReconnectGrace
}

func (hm *HealthMonitor) clock() time.Time {
	if hm.now != nil {
		return hm.now()
	}
	return time.Now()
}

// RouteJudge returns the judge the monitor uses, so that the node list shows
// the route state that the monitor acts on.
func (hm *HealthMonitor) RouteJudge() RouteJudge {
	j := hm.judge
	j.StaleThreshold = hm.staleThreshold
	j.Grace = hm.grace()
	return j
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

// CheckNow runs one round of checks at once. A replica calls it when its
// broadcast bus reconnects, so that route state that changed meanwhile is read
// without waiting for the next tick.
func (hm *HealthMonitor) CheckNow(ctx context.Context) { hm.checkAll(ctx) }

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
// Node liveness is judged per route (RouteJudge): the HTTP heartbeat that both
// backend and agent workers send, NATS as the worker reports it, and the tunnel
// as the connection rows show it. A node is reachable while NATS or its tunnel
// is live. This is simpler and more reliable than probing individual gRPC
// backend processes (which can crash independently).
//
// Per-model health checks (opt-in) separately probe each model's gRPC address
// and remove stale model records without affecting the node's overall status.
func (hm *HealthMonitor) doCheckAll(ctx context.Context) {
	nodes, err := hm.registry.List(ctx)
	if err != nil {
		xlog.Error("Health monitor: failed to list nodes", "error", err)
		return
	}

	now := hm.clock()
	judge := hm.RouteJudge()
	seen := make(map[string]struct{}, len(nodes))
	defer hm.forgetRouteState(seen)

	for _, node := range nodes {
		// Draining nodes are operator-managed; pending nodes have never been
		// approved. Turning a pending node offline would let a later
		// re-registration or recovery promote it to healthy without approval.
		if node.Status == StatusDraining {
			hm.checkDrainingNode(ctx, &node, judge, now)
			continue
		}
		if node.Status == StatusPending {
			continue
		}

		// Node liveness is judged per route. The heartbeat says that the worker
		// process runs and carries the report of its routes; NATS and the
		// tunnel carry the work. A node is reachable while any route that
		// carries work is live, whatever routes the other nodes hold.
		states := judge.States(ctx, &node, now)
		fresh := states[0].Live()
		carrying := carryingRoutes(states)
		judged := len(carrying) > 0
		live := slices.ContainsFunc(carrying, RouteState.Live)
		hm.logRouteChanges(&node, states)
		seen[node.ID] = struct{}{}

		if !live && !fresh {
			hm.clearNoRoute(node.ID)
			// No route at all: the stale rules apply as before. Skip nodes
			// already marked offline/unhealthy — re-marking them every cycle
			// floods the log with the same WARN+INFO pair for nodes the
			// operator has intentionally taken down.
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
				if err := hm.registry.MarkUnhealthy(ctx, node.ID); err != nil {
					xlog.Error("Failed to mark unreachable node unhealthy", "node", node.Name, "error", err)
				}
			}
			continue
		}

		// The heartbeat is fresh and no route that carries work is live: the
		// worker runs and cannot be reached (a proxy that stopped upgrading
		// websockets, a rotated credential, a NATS server that is away). The
		// node is demoted, not marked offline: MarkUnhealthy is status only,
		// routing selects on status=healthy, and nothing of the node is
		// deleted on a read of its routes. A route that is down for less than
		// the reconnect grace is waited for, so a route that reconnects does
		// not flap the node. No promotion and no per-model probes follow: the
		// probes would dial a worker that has no route.
		if judged && !live {
			if !hm.noRouteFor(node.ID, carrying, now) {
				continue
			}
			if node.Status != StatusUnhealthy && node.Status != StatusOffline {
				xlog.Warn("Node is heartbeating but none of its routes is live; marking unhealthy",
					"node", node.Name, "nodeID", node.ID, "type", node.NodeType, "routes", node.Follow,
					"routeError", node.FollowError, "grace", hm.grace())
				if err := hm.registry.MarkUnhealthy(ctx, node.ID); err != nil {
					xlog.Error("Failed to mark a node with no live route unhealthy", "node", node.Name, "error", err)
				}
			}
			continue
		}
		hm.clearNoRoute(node.ID)

		// A route is live: the node is reachable, even when another of its
		// routes, or its heartbeat, is lost.
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
					_ = closer.Close() // probe connection is discarded; a close error changes nothing
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

// noRoute is how long this replica has seen a node with no live route.
type noRoute struct{ since, last time.Time }

// noRouteFor reports whether a node with a fresh heartbeat has had no live route
// that carries work for at least the reconnect grace. A route that is already
// down for longer than the grace (a tunnel that presence reports gone) needs no
// further wait.
func (hm *HealthMonitor) noRouteFor(nodeID string, carrying []RouteState, now time.Time) bool {
	if !slices.ContainsFunc(carrying, func(s RouteState) bool { return !s.settled }) {
		return true
	}
	hm.routeMu.Lock()
	defer hm.routeMu.Unlock()
	if hm.noRouteSince == nil {
		hm.noRouteSince = map[string]noRoute{}
	}
	nr, ok := hm.noRouteSince[nodeID]
	// Another frontend ran the ticks in between, and a route may have come
	// back and gone again meanwhile: the grace starts again.
	if !ok || now.Sub(nr.last) > 3*hm.checkInterval {
		hm.noRouteSince[nodeID] = noRoute{since: now, last: now}
		return false
	}
	nr.last = now
	hm.noRouteSince[nodeID] = nr
	return now.Sub(nr.since) >= hm.grace()
}

func (hm *HealthMonitor) clearNoRoute(nodeID string) {
	hm.routeMu.Lock()
	defer hm.routeMu.Unlock()
	delete(hm.noRouteSince, nodeID)
}

// logRouteChanges logs a route that a node lost, or got back, once per change.
// The node list shows the same states at any time.
func (hm *HealthMonitor) logRouteChanges(node *BackendNode, states []RouteState) {
	down := downRoutes(states)
	hm.routeMu.Lock()
	if hm.downLogged == nil {
		hm.downLogged = map[string]string{}
	}
	prev, seen := hm.downLogged[node.ID]
	hm.downLogged[node.ID] = down
	hm.routeMu.Unlock()
	if down == prev || (!seen && down == "") {
		return
	}
	if down == "" {
		xlog.Info("Every route of the node is live again", "node", node.Name, "nodeID", node.ID)
		return
	}
	xlog.Warn("A route of the node is not live", "node", node.Name, "nodeID", node.ID, "down", down, "routeError", node.FollowError)
}

// forgetRouteState drops what the monitor keeps about nodes that are no longer
// listed or that it skipped.
func (hm *HealthMonitor) forgetRouteState(seen map[string]struct{}) {
	hm.routeMu.Lock()
	defer hm.routeMu.Unlock()
	for id := range hm.noRouteSince {
		if _, ok := seen[id]; !ok {
			delete(hm.noRouteSince, id)
		}
	}
	for id := range hm.downLogged {
		if _, ok := seen[id]; !ok {
			delete(hm.downLogged, id)
		}
	}
}

// drainingReleaser removes the replicas of a draining node. NodeRegistry
// implements it; the monitor's test fakes need not.
type drainingReleaser interface {
	ReleaseDrainingNode(ctx context.Context, nodeID string) ([]NodeModel, error)
}

// checkDrainingNode removes the replicas of a draining node that stopped
// answering: no route that carries work is live and its heartbeat is stale.
// A draining node keeps its replicas routable while it serves them, so a
// node that died during its drain would otherwise be routed to until the
// drain deadline. The node stays draining; its status is the operator's.
func (hm *HealthMonitor) checkDrainingNode(ctx context.Context, node *BackendNode, judge RouteJudge, now time.Time) {
	rel, ok := hm.registry.(drainingReleaser)
	if !ok {
		return
	}
	states := judge.States(ctx, node, now)
	fresh := states[0].Live()
	if fresh || slices.ContainsFunc(carryingRoutes(states), RouteState.Live) {
		return
	}
	removed, err := rel.ReleaseDrainingNode(ctx, node.ID)
	if err != nil {
		xlog.Warn("Failed to release the replicas of a draining node that stopped answering", "node", node.Name, "error", err)
		return
	}
	if len(removed) > 0 {
		xlog.Warn("Draining node stopped answering; removed its replicas", "node", node.Name, "replicas", len(removed))
	}
}
