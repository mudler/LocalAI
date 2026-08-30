package nodes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/advisorylock"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/nodes/prefixcache"
	grpcclient "github.com/mudler/LocalAI/pkg/grpc"
	"github.com/mudler/xlog"
	"github.com/nats-io/nats.go"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

// ProbeOutcome is the result of a model liveness probe. The distinction that
// matters is between silence caused by a BUSY backend and silence caused by a
// MISSING one: only the latter justifies deleting the registry row.
type ProbeOutcome int

const (
	// ProbeAlive: the backend answered and reported healthy.
	ProbeAlive ProbeOutcome = iota
	// ProbeBusy: the backend was reachable but did not answer within the
	// probe deadline. Single-threaded backends block here for the whole
	// duration of a long generation, so this is evidence of life, not death.
	ProbeBusy
	// ProbeUnreachable: nothing is listening (connection refused), or the
	// backend answered and affirmatively reported itself unhealthy.
	ProbeUnreachable
)

// ModelProber checks the state of a model's backend process.
// Defaulted to a gRPC health probe but overridable for tests so we don't
// need to stand up a real server.
type ModelProber interface {
	Probe(ctx context.Context, address string) ProbeOutcome
}

// NodeProcessLister asks a worker which model backend processes it currently
// has running. Implemented by RemoteUnloaderAdapter over NATS.
//
// This is the sounder liveness signal: the worker owns the process table, so
// its answer does not depend on whether a backend is busy. A health probe
// against the backend's own serving port cannot make that distinction, which
// is why it is only the fallback for workers that do not answer.
type NodeProcessLister interface {
	ListRunningModels(nodeID string) (*messaging.ModelsRunningReply, error)
}

// probeTimeout bounds a single liveness probe. Kept short because a healthy
// idle backend answers immediately; a backend that needs longer is by
// definition busy, which classifyProbeOutcome reports as ProbeBusy rather than
// as death.
const probeTimeout = 1 * time.Second

// grpcModelProber does a short HealthCheck on the model's stored gRPC address.
type grpcModelProber struct{ token string }

func (g grpcModelProber) Probe(ctx context.Context, address string) ProbeOutcome {
	client := grpcclient.NewClientWithToken(address, false, nil, false, g.token)
	probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	ok, err := client.HealthCheck(probeCtx)
	return classifyProbeOutcome(ok, err)
}

// classifyProbeOutcome maps a HealthCheck result onto a ProbeOutcome.
//
// The gRPC client is lazy, so connection failures surface on the RPC rather
// than at dial time, and the status code tells the two cases apart:
//
//   - DeadlineExceeded: the transport was fine but nothing serviced the RPC in
//     time. That is a backend stuck inside a long synchronous request.
//   - Unavailable: nothing is listening. The process is gone.
//
// A blackholed network also yields DeadlineExceeded and is therefore treated as
// busy. That is deliberate: whole-node failures are the health monitor's job
// (it marks the node offline and reaps its models), and mistaking a network
// blip for a dead backend is the failure this classification exists to prevent.
func classifyProbeOutcome(ok bool, err error) ProbeOutcome {
	if err == nil {
		if ok {
			return ProbeAlive
		}
		// Answered, but reported unhealthy. An affirmative signal, unlike silence.
		return ProbeUnreachable
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return ProbeBusy
	}
	switch status.Code(err) {
	case codes.DeadlineExceeded:
		return ProbeBusy
	case codes.Unavailable:
		return ProbeUnreachable
	default:
		// Any other status means something on the other end produced a
		// response, so the process exists. Not healthy, but not a ghost.
		return ProbeBusy
	}
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
	// pressure is the shared forced-disturb counter written by the router. When
	// a model's count within the Pressure's rolling window reaches pressureThreshold the
	// reconciler treats its cache-warm replica as saturated and scales up,
	// subject to the same MaxReplicas/capacity/UnsatisfiableUntil machinery as
	// the other scale-up paths. nil disables this signal (a true no-op).
	pressure          *prefixcache.Pressure
	pressureThreshold int
	// processLister asks workers which model processes they are running. nil
	// disables the worker-authoritative pass, leaving only the port probe.
	processLister NodeProcessLister
	// probeFailures counts CONSECUTIVE failed liveness probes per node_models
	// row id, so a single miss against a busy-but-alive backend cannot delete
	// its registry row. Reset on any successful probe and on reap.
	probeFailuresMu sync.Mutex
	probeFailures   map[string]int
	// workerMisses counts CONSECUTIVE passes in which a worker did not report
	// a row's model as running. Kept separate from probeFailures so the two
	// independent signals never add into one another's budget.
	workerMissesMu sync.Mutex
	workerMisses   map[string]int
	// inFlightIdle counts CONSECUTIVE passes in which a row with a positive
	// in_flight counter was observed answering health probes promptly, which is
	// what a backend inside a request cannot do.
	inFlightIdleMu sync.Mutex
	inFlightIdle   map[string]int
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
	Registry  *NodeRegistry
	Scheduler ModelScheduler
	Unloader  NodeCommandSender
	// Adapter is the NATS sender used to retry pending backend ops. When nil,
	// the state-reconciler pending-drain pass is a no-op (single-node mode).
	Adapter *RemoteUnloaderAdapter
	// RegistrationToken is used by the default gRPC prober when probing model
	// addresses. Matches the worker's token so HealthCheck auth succeeds.
	RegistrationToken string
	// Prober overrides the default gRPC health probe (used by tests).
	Prober ModelProber
	// ProcessLister overrides the default worker process query. When nil and
	// no Adapter is set, the worker-authoritative pass is skipped entirely and
	// only the port probe runs.
	ProcessLister   NodeProcessLister
	DB              *gorm.DB
	Interval        time.Duration // default 30s
	ScaleDownDelay  time.Duration // default 5m
	ProbeStaleAfter time.Duration // default 2m
	// Pressure is the shared forced-disturb counter written by the router. nil
	// disables the cache-saturation autoscale signal (a true no-op).
	Pressure *prefixcache.Pressure
	// PressureThreshold is the forced-disturb count within PressureWindow that
	// triggers a scale-up. Default prefixcache.DefaultConfig().PressureScaleThreshold (1).
	PressureThreshold int
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
	pressureThreshold := opts.PressureThreshold
	if pressureThreshold == 0 {
		pressureThreshold = prefixcache.DefaultConfig().PressureScaleThreshold
	}
	// The adapter already speaks the models.running request, so it is the
	// natural default; an explicit ProcessLister (tests) wins over it.
	processLister := opts.ProcessLister
	if processLister == nil && opts.Adapter != nil {
		processLister = opts.Adapter
	}
	return &ReplicaReconciler{
		registry:          opts.Registry,
		scheduler:         opts.Scheduler,
		unloader:          opts.Unloader,
		adapter:           opts.Adapter,
		prober:            prober,
		processLister:     processLister,
		db:                opts.DB,
		interval:          interval,
		scaleDownDelay:    scaleDownDelay,
		probeStaleAfter:   probeStaleAfter,
		pressure:          opts.Pressure,
		pressureThreshold: pressureThreshold,
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
// ops for freshly-healthy nodes, reconcile registry rows against what workers
// report they are running, port-probe whatever is left, then reclaim replica
// slots held by loads nobody is driving. All passes are best-effort: a failure
// on one node doesn't stop the rest.
//
// Order matters. The worker pass runs first and refreshes updated_at for every
// model a worker vouches for, which takes those rows out of the port prober's
// stale set. The port probe is therefore only the fallback for nodes whose
// worker did not answer.
func (rc *ReplicaReconciler) reconcileState(ctx context.Context) {
	if rc.adapter != nil {
		rc.drainPendingBackendOps(ctx)
	}
	rc.reconcileNodeProcesses(ctx)
	rc.probeLoadedModels(ctx)
	rc.sweepLeakedInFlight(ctx)
	// Runs last: the passes above can move a row into a serving state, and a
	// row that just became loaded is no longer this sweeper's business.
	rc.reclaimAbandonedLoads(ctx)
}

// drainPendingBackendOps retries queued backend ops whose next_retry_at has
// passed on nodes that are currently healthy. On success the row is deleted;
// on failure attempts++ and next_retry_at moves out via exponential backoff.
func (rc *ReplicaReconciler) drainPendingBackendOps(ctx context.Context) {
	// Garbage-collect ops behind nodes that went offline/draining. These are
	// invisible to ListDuePendingBackendOps (which filters status=healthy), so
	// without this sweep they leak forever and keep the UI operation spinning.
	if _, err := rc.registry.DeleteStalePendingBackendOps(ctx, stalePendingBackendOpGrace); err != nil {
		xlog.Warn("Reconciler: failed to clear stale pending backend ops", "error", err)
	}

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
		case OpBackendInstall:
			// Pending-op drain for admin install — not a per-replica load.
			// Replica 0 is the conventional admin slot. Install is idempotent:
			// the worker short-circuits if the backend is already running.
			reply, err := rc.adapter.InstallBackend(op.NodeID, op.Backend, "", string(op.Galleries), "", "", "", 0, "", nil)
			if err != nil {
				applyErr = err
			} else if !reply.Success {
				applyErr = fmt.Errorf("install failed: %s", reply.Error)
			}
		case OpBackendUpgrade:
			// Pending-op drain for admin upgrade — fires backend.upgrade so
			// the slow re-pull doesn't head-of-line-block install traffic on
			// the same worker. Falls back to the legacy backend.install
			// Force=true path on nats.ErrNoResponders for old workers that
			// don't subscribe to backend.upgrade yet (rolling-update window).
			// Reconciler retries are background reconciliation with no live
			// admin watching a progress bar, so opID/onProgress are empty —
			// the adapter skips the progress subscription entirely.
			reply, err := rc.adapter.UpgradeBackend(op.NodeID, op.Backend, string(op.Galleries), "", "", "", 0, "", nil)
			if err != nil {
				if errors.Is(err, nats.ErrNoResponders) {
					instReply, instErr := rc.adapter.installWithForceFallback(op.NodeID, op.Backend, string(op.Galleries), "", "", "", 0, "", nil)
					if instErr != nil {
						applyErr = instErr
					} else if !instReply.Success {
						applyErr = fmt.Errorf("upgrade (legacy fallback) failed: %s", instReply.Error)
					}
				} else {
					applyErr = err
				}
			} else if !reply.Success {
				applyErr = fmt.Errorf("upgrade failed: %s", reply.Error)
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

// stalePendingBackendOpGrace is how long a node may be offline before its
// pending backend ops are garbage-collected. Draining nodes are cleared
// immediately regardless of this window (see DeleteStalePendingBackendOps).
// ListDuePendingBackendOps never surfaces ops behind non-healthy nodes, so
// without this sweep they would leak forever and keep the UI op spinning.
const stalePendingBackendOpGrace = 15 * time.Minute

// probeFailuresBeforeReap is how many CONSECUTIVE failed liveness probes a
// replica must accumulate before its row is deleted. The probe is a 1s gRPC
// HealthCheck, and a backend that is merely busy cannot answer it: single
// threaded Python backends (video/avatar generation, long diffusion loops)
// block for minutes at a time inside one request. Treating the first miss as
// death deleted rows for backends that were alive and working, which made the
// model vanish from the nodes page while it was still generating. Three misses
// across three ticks is ~1.5 minutes of silence at the default interval, well
// past any GC pause or scheduling hiccup, while a genuinely dead process is
// still reaped promptly.
const probeFailuresBeforeReap = 3

// probeLoadedModels gRPC-health-checks model addresses that the DB says are
// loaded. If a model's backend process is gone (OOM, crash, manual restart)
// we remove the row so ghosts don't linger. Only probes rows older than
// probeStaleAfter so we don't hammer every worker every tick for models we
// just heard from.
//
// Two guards keep this from reaping healthy backends. A probe that times out is
// classified as ProbeBusy rather than as death, because a backend blocked
// inside a long generation cannot answer while it works. And a replica must
// then look unreachable on probeFailuresBeforeReap CONSECUTIVE passes, so a
// transient blip cannot orphan a live replica.
//
// Deliberately NOT gated on in_flight. That counter has no decrement guarantee
// (a frontend that dies mid-request leaves the increment behind), so using it
// to shield rows from reaping would make a leaked counter produce a replica
// that can never be cleaned up.
func (rc *ReplicaReconciler) probeLoadedModels(ctx context.Context) {
	var stale []NodeModel
	cutoff := time.Now().Add(-rc.probeStaleAfter)
	err := currentModelRevision(rc.registry.db.WithContext(ctx)).
		Joins("JOIN backend_nodes ON backend_nodes.id = node_models.node_id").
		Where("node_models.state = ? AND backend_nodes.status = ? AND node_models.updated_at < ? AND node_models.address != ''",
			"loaded", StatusHealthy, cutoff).
		Find(&stale).Error
	if err != nil {
		xlog.Warn("Reconciler: failed to list loaded models for probe", "error", err)
		return
	}
	seen := make(map[string]struct{}, len(stale))
	for _, m := range stale {
		if err := ctx.Err(); err != nil {
			return
		}
		seen[m.ID] = struct{}{}
		switch rc.prober.Probe(ctx, m.Address) {
		case ProbeAlive:
			rc.clearProbeFailures(m.ID)
			// Bump updated_at so we don't probe this row again immediately.
			_ = rc.registry.db.WithContext(ctx).Model(&NodeModel{}).
				Where("id = ?", m.ID).Update("updated_at", time.Now()).Error
			continue
		case ProbeBusy:
			// Reachable but mid-request. Proof of life, so clear the streak.
			rc.clearProbeFailures(m.ID)
			xlog.Debug("Reconciler: model busy, skipping liveness reap",
				"node", m.NodeID, "model", m.ModelName, "replica", m.ReplicaIndex, "address", m.Address)
			continue
		}

		failures := rc.recordProbeFailure(m.ID)
		if failures < probeFailuresBeforeReap {
			xlog.Debug("Reconciler: model unreachable, waiting for more misses before reaping",
				"node", m.NodeID, "model", m.ModelName, "replica", m.ReplicaIndex, "address", m.Address,
				"failures", failures, "threshold", probeFailuresBeforeReap)
			continue
		}
		if err := rc.registry.RemoveNodeModel(ctx, m.NodeID, m.ModelName, m.ReplicaIndex); err != nil {
			xlog.Warn("Reconciler: failed to remove unreachable model", "node", m.NodeID, "model", m.ModelName, "replica", m.ReplicaIndex, "error", err)
			continue
		}
		rc.clearProbeFailures(m.ID)
		xlog.Warn("Reconciler: model unreachable, removed from registry",
			"node", m.NodeID, "model", m.ModelName, "replica", m.ReplicaIndex, "address", m.Address,
			"failures", failures)
	}
	rc.pruneProbeFailures(seen)
}

// inFlightLeakIdleAfter is how long a positive in_flight counter must sit
// without its last_used moving before the sweeper will even consider it a leak.
// Generous on purpose: it only bounds how quickly a genuine leak is corrected,
// whereas being too eager risks overruling a real request.
const inFlightLeakIdleAfter = 30 * time.Minute

// inFlightLeakConfirmations is how many CONSECUTIVE passes must observe the
// backend answering promptly before the counter is overruled.
const inFlightLeakConfirmations = 2

// sweepLeakedInFlight corrects in_flight counters that no longer reflect any
// real request.
//
// The counter has no decrement guarantee. `track()` balances its increment with
// a defer, but that only holds while the process lives: a frontend killed
// mid-request (rolling restart, OOM) never runs the defer, and the load-time
// reservation is released only when the FIRST inference completes, so a request
// that dies before reaching the backend strands it. The result is not cosmetic —
// FindLRUModel, FindGlobalLRUModelWithZeroInFlight and the router's eviction
// query all require in_flight = 0, so a leaked counter pins that replica's VRAM
// forever.
//
// Elapsed time alone cannot identify a leak: IncrementInFlight stamps last_used
// at request START and nothing moves it while the request runs, so a long
// generation is indistinguishable from a leak by age. The probe supplies the
// missing bit — a backend that answers a health check promptly is not inside a
// request, because that is exactly what a busy backend cannot do. Requiring the
// row to ALSO be idle for inFlightLeakIdleAfter covers backends that serve
// requests in parallel and can answer while working: those keep last_used fresh
// through each new request's increment.
func (rc *ReplicaReconciler) sweepLeakedInFlight(ctx context.Context) {
	var suspects []NodeModel
	cutoff := time.Now().Add(-inFlightLeakIdleAfter)
	err := currentModelRevision(rc.registry.db.WithContext(ctx)).
		Joins("JOIN backend_nodes ON backend_nodes.id = node_models.node_id").
		Where("node_models.state = ? AND backend_nodes.status = ? AND node_models.in_flight > 0 AND node_models.last_used < ? AND node_models.address != ''",
			"loaded", StatusHealthy, cutoff).
		Find(&suspects).Error
	if err != nil {
		xlog.Warn("Reconciler: failed to list rows for in-flight leak sweep", "error", err)
		return
	}

	seen := make(map[string]struct{}, len(suspects))
	for _, m := range suspects {
		if err := ctx.Err(); err != nil {
			return
		}
		seen[m.ID] = struct{}{}
		if rc.prober.Probe(ctx, m.Address) != ProbeAlive {
			// Busy or unreachable. Busy means the counter may well be real;
			// unreachable is the reaper's business, not the sweeper's.
			rc.clearInFlightIdle(m.ID)
			continue
		}
		confirmations := rc.recordInFlightIdle(m.ID)
		if confirmations < inFlightLeakConfirmations {
			continue
		}
		res := rc.registry.db.WithContext(ctx).Model(&NodeModel{}).
			Where("id = ? AND in_flight > 0", m.ID).
			UpdateColumn("in_flight", 0)
		if res.Error != nil {
			xlog.Warn("Reconciler: failed to reset leaked in-flight counter",
				"node", m.NodeID, "model", m.ModelName, "replica", m.ReplicaIndex, "error", res.Error)
			continue
		}
		rc.clearInFlightIdle(m.ID)
		if res.RowsAffected > 0 {
			xlog.Warn("Reconciler: reset leaked in-flight counter on an idle replica",
				"node", m.NodeID, "model", m.ModelName, "replica", m.ReplicaIndex,
				"was", m.InFlight, "idleFor", time.Since(m.LastUsed).String())
		}
	}
	rc.pruneInFlightIdle(seen)
}

func (rc *ReplicaReconciler) recordInFlightIdle(rowID string) int {
	rc.inFlightIdleMu.Lock()
	defer rc.inFlightIdleMu.Unlock()
	if rc.inFlightIdle == nil {
		rc.inFlightIdle = make(map[string]int)
	}
	rc.inFlightIdle[rowID]++
	return rc.inFlightIdle[rowID]
}

func (rc *ReplicaReconciler) clearInFlightIdle(rowID string) {
	rc.inFlightIdleMu.Lock()
	defer rc.inFlightIdleMu.Unlock()
	delete(rc.inFlightIdle, rowID)
}

// pruneInFlightIdle bounds the streak map. Dropping a streak only ever resets
// it, so this can never cause a premature correction.
func (rc *ReplicaReconciler) pruneInFlightIdle(seen map[string]struct{}) {
	rc.inFlightIdleMu.Lock()
	defer rc.inFlightIdleMu.Unlock()
	for id := range rc.inFlightIdle {
		if _, ok := seen[id]; !ok {
			delete(rc.inFlightIdle, id)
		}
	}
}

// workerMissesBeforeReap is how many CONSECUTIVE passes a worker must fail to
// report a model before its row is deleted. The worker's answer is
// authoritative, so this only needs to absorb the narrow window where a row
// exists but the process table has not caught up; two passes is ample given
// rows are only considered once they are probeStaleAfter old.
const workerMissesBeforeReap = 2

// reconcileNodeProcesses reconciles registry rows against what each worker says
// it is actually running.
//
// This is the primary liveness pass, and it is sounder than probing the
// backend's own serving port: the worker owns the process table and answers
// immediately whether or not the backend is mid-request. A model confirmed here
// also gets its updated_at bumped, which keeps it out of the port prober's
// stale set entirely — that is what stops a backend deep in a long generation
// from ever being mistaken for a dead one.
//
// A worker that cannot be reached is skipped rather than treated as empty. A
// messaging failure says nothing about the processes, and assuming the worst
// would delete a whole node's rows on a transient NATS blip; the port probe
// remains as the fallback for those nodes.
func (rc *ReplicaReconciler) reconcileNodeProcesses(ctx context.Context) {
	if rc.processLister == nil {
		return
	}

	var stale []NodeModel
	cutoff := time.Now().Add(-rc.probeStaleAfter)
	err := currentModelRevision(rc.registry.db.WithContext(ctx)).
		Joins("JOIN backend_nodes ON backend_nodes.id = node_models.node_id").
		Where("node_models.state = ? AND backend_nodes.status = ? AND backend_nodes.node_type = ? AND node_models.updated_at < ?",
			"loaded", StatusHealthy, NodeTypeBackend, cutoff).
		Find(&stale).Error
	if err != nil {
		xlog.Warn("Reconciler: failed to list loaded models for worker reconciliation", "error", err)
		return
	}
	if len(stale) == 0 {
		return
	}

	byNode := make(map[string][]NodeModel)
	for _, m := range stale {
		byNode[m.NodeID] = append(byNode[m.NodeID], m)
	}

	seen := make(map[string]struct{}, len(stale))
	for nodeID, rows := range byNode {
		if err := ctx.Err(); err != nil {
			return
		}
		reply, err := rc.processLister.ListRunningModels(nodeID)
		if err != nil {
			xlog.Debug("Reconciler: worker did not answer models.running, leaving its replicas to the port probe",
				"node", nodeID, "error", err)
			continue
		}
		if reply == nil || reply.Error != "" {
			xlog.Debug("Reconciler: worker reported an error for models.running, skipping node",
				"node", nodeID, "error", replyError(reply))
			continue
		}

		running := make(map[replicaKey]struct{}, len(reply.Models))
		for _, m := range reply.Models {
			running[replicaKey{model: m.ModelID, replica: m.ReplicaIndex}] = struct{}{}
		}

		for _, row := range rows {
			seen[row.ID] = struct{}{}
			if _, ok := running[replicaKey{model: row.ModelName, replica: row.ReplicaIndex}]; ok {
				rc.clearWorkerMisses(row.ID)
				// Confirmed alive by the process owner. Bump updated_at so the
				// port prober does not also go poking at a busy backend.
				_ = rc.registry.db.WithContext(ctx).Model(&NodeModel{}).
					Where("id = ?", row.ID).Update("updated_at", time.Now()).Error
				continue
			}

			misses := rc.recordWorkerMiss(row.ID)
			if misses < workerMissesBeforeReap {
				xlog.Debug("Reconciler: worker does not report model as running, waiting for confirmation",
					"node", nodeID, "model", row.ModelName, "replica", row.ReplicaIndex,
					"misses", misses, "threshold", workerMissesBeforeReap)
				continue
			}
			if err := rc.registry.RemoveNodeModel(ctx, row.NodeID, row.ModelName, row.ReplicaIndex); err != nil {
				xlog.Warn("Reconciler: failed to remove model the worker is not running",
					"node", nodeID, "model", row.ModelName, "replica", row.ReplicaIndex, "error", err)
				continue
			}
			rc.clearWorkerMisses(row.ID)
			xlog.Warn("Reconciler: worker is not running this model, removed from registry",
				"node", nodeID, "model", row.ModelName, "replica", row.ReplicaIndex, "misses", misses)
		}
	}
	rc.pruneWorkerMisses(seen)
}

// replicaKey identifies one replica of one model on a node.
type replicaKey struct {
	model   string
	replica int
}

// replyError safely extracts the error text from a possibly-nil reply.
func replyError(reply *messaging.ModelsRunningReply) string {
	if reply == nil {
		return "nil reply"
	}
	return reply.Error
}

func (rc *ReplicaReconciler) recordWorkerMiss(rowID string) int {
	rc.workerMissesMu.Lock()
	defer rc.workerMissesMu.Unlock()
	if rc.workerMisses == nil {
		rc.workerMisses = make(map[string]int)
	}
	rc.workerMisses[rowID]++
	return rc.workerMisses[rowID]
}

func (rc *ReplicaReconciler) clearWorkerMisses(rowID string) {
	rc.workerMissesMu.Lock()
	defer rc.workerMissesMu.Unlock()
	delete(rc.workerMisses, rowID)
}

// pruneWorkerMisses drops streaks for rows that were not candidates this pass,
// bounding the map. Dropping a streak only ever resets it, so this can never
// cause a premature reap.
func (rc *ReplicaReconciler) pruneWorkerMisses(seen map[string]struct{}) {
	rc.workerMissesMu.Lock()
	defer rc.workerMissesMu.Unlock()
	for id := range rc.workerMisses {
		if _, ok := seen[id]; !ok {
			delete(rc.workerMisses, id)
		}
	}
}

// recordProbeFailure increments and returns the consecutive-failure count for a
// replica row.
func (rc *ReplicaReconciler) recordProbeFailure(rowID string) int {
	rc.probeFailuresMu.Lock()
	defer rc.probeFailuresMu.Unlock()
	if rc.probeFailures == nil {
		rc.probeFailures = make(map[string]int)
	}
	rc.probeFailures[rowID]++
	return rc.probeFailures[rowID]
}

// clearProbeFailures resets a replica's streak after it answers or is reaped.
func (rc *ReplicaReconciler) clearProbeFailures(rowID string) {
	rc.probeFailuresMu.Lock()
	defer rc.probeFailuresMu.Unlock()
	delete(rc.probeFailures, rowID)
}

// pruneProbeFailures drops streaks for rows that were not probe candidates this
// pass, so the map cannot grow without bound as replicas come and go. Dropping
// a streak only ever resets it, so this can never cause a premature reap.
func (rc *ReplicaReconciler) pruneProbeFailures(seen map[string]struct{}) {
	rc.probeFailuresMu.Lock()
	defer rc.probeFailuresMu.Unlock()
	for id := range rc.probeFailures {
		if _, ok := seen[id]; !ok {
			delete(rc.probeFailures, id)
		}
	}
}

func (rc *ReplicaReconciler) reconcile(ctx context.Context) {
	// Keep each rule's stored target in step with the alias mapping. Only the
	// eviction guard reads that column, and it cannot resolve aliases itself.
	if err := rc.registry.RefreshSchedulingTargets(ctx); err != nil {
		xlog.Warn("Reconciler: failed to refresh scheduling targets", "error", err)
	}

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

// unsatisfiableTickThreshold is how many consecutive ticks of "capacity == 0
// && need > 0" must elapse before the reconciler stops trying. Three ticks at
// the default 30s interval gives ~90s of grace before logging a warning and
// entering cooldown — enough to ride out a transient race between Register
// and the next tick, but short enough that a misconfig (MinReplicas above
// cluster capacity) doesn't churn the worker forever like it did pre-PR4.
const unsatisfiableTickThreshold = 3

// unsatisfiableCooldown is the duration the reconciler waits before retrying
// after the threshold trips. ClearAllUnsatisfiable on cluster events shortens
// this in practice — the cooldown is the worst-case, not the steady-state.
const unsatisfiableCooldown = 5 * time.Minute

// candidateNodeIDsForSelector resolves the model's NodeSelector to a slice
// of node IDs, or returns nil if no selector is configured (meaning "any
// healthy node" — registry helpers interpret nil as no candidate filter).
// Returns ok=false if a non-empty selector matched zero nodes, in which case
// the caller should skip — there's nothing to schedule on.
func (rc *ReplicaReconciler) candidateNodeIDsForSelector(ctx context.Context, cfg ModelSchedulingConfig) (ids []string, ok bool) {
	if cfg.NodeSelector == "" {
		return nil, true
	}
	sel := parseSelector(cfg.NodeSelector)
	if len(sel) == 0 {
		return nil, true
	}
	nodes, err := rc.registry.FindNodesBySelector(ctx, sel)
	if err != nil || len(nodes) == 0 {
		return nil, false
	}
	ids = make([]string, len(nodes))
	for i, n := range nodes {
		ids[i] = n.ID
	}
	return ids, true
}

// reconcileModel brings one scheduling rule's replica count in line with what
// the rule asks for.
//
// A rule is keyed by the name the operator chose, which may be an alias, while
// the model it governs is cfg.Target(). The two are used deliberately: anything
// that touches a loaded replica (counting, capacity, scheduling, eviction,
// cache pressure) goes through the target, and the rule's own bookkeeping
// columns (the unsatisfiable counter and cooldown) stay keyed by cfg.ModelName.
func (rc *ReplicaReconciler) reconcileModel(ctx context.Context, cfg ModelSchedulingConfig) {
	// An alias that resolves to itself is one that no longer resolves at all:
	// its target was removed, or it was pointed at another alias. Scheduling it
	// would ask a worker to load a pure redirect, which has no backend and no
	// model file behind it, so leave the rule alone until the alias is fixed.
	if cfg.ModelIsAlias && cfg.Target() == cfg.ModelName {
		xlog.Warn("Reconciler: scheduling rule is keyed by an alias that does not resolve; skipping",
			"rule", cfg.ModelName)
		return
	}
	target := cfg.Target()

	// spread_all: derive a dynamic replica target equal to the number of nodes
	// currently matching the selector (all healthy backend nodes when the
	// selector is empty). Feeding it through Min==Max==target reuses every
	// existing path: the floor scales up toward target (capped at capacity),
	// Max==target stops busy-burst/pressure overshooting, and idle scale-down
	// trims above target. The target re-tracks node join/leave each tick. cfg is
	// a by-value copy, so mutating it here is local to this tick.
	if cfg.SpreadAll {
		matched, err := rc.registry.FindNodesBySelector(ctx, parseSelector(cfg.NodeSelector))
		if err != nil {
			xlog.Warn("Reconciler: spread_all failed to resolve matching nodes", "model", cfg.ModelName, "error", err)
			return
		}
		if len(matched) == 0 {
			xlog.Info("Reconciler: spread_all has no matching nodes; nothing to schedule",
				"model", cfg.ModelName, "selector", cfg.NodeSelector)
			return
		}
		cfg.MinReplicas = len(matched)
		cfg.MaxReplicas = len(matched)
	}

	// Cooldown gate: if we previously decided this config is unsatisfiable,
	// don't even bother checking until the cooldown expires. ClearAllUnsatisfiable
	// (fired by node lifecycle events) bypasses this by zeroing the column.
	if cfg.UnsatisfiableUntil != nil && cfg.UnsatisfiableUntil.After(time.Now()) {
		return
	}

	current, err := rc.registry.CountLoadedReplicas(ctx, target)
	if err != nil {
		xlog.Warn("Reconciler: failed to count replicas", "model", target, "error", err)
		return
	}

	// 1. Ensure minimum replicas, but only up to what the cluster can host.
	//    Without this cap, a MinReplicas above cluster capacity would loop
	//    forever (the original flap: every tick "scaling up", but the registry
	//    never grows because all nodes are full).
	if cfg.MinReplicas > 0 && int(current) < cfg.MinReplicas {
		candidateNodeIDs, selectorMatched := rc.candidateNodeIDsForSelector(ctx, cfg)
		if !selectorMatched {
			xlog.Warn("Reconciler: no nodes match selector", "model", target, "selector", cfg.NodeSelector)
			rc.markCapacityProblem(ctx, cfg.ModelName, "no nodes match selector")
			return
		}

		capacity, capErr := rc.registry.ClusterCapacityForModel(ctx, target, candidateNodeIDs)
		if capErr != nil {
			xlog.Warn("Reconciler: failed to compute cluster capacity", "model", target, "error", capErr)
			return
		}

		needed := cfg.MinReplicas - int(current)
		if capacity == 0 {
			// No capacity right now. Bump hysteresis; trip cooldown if it
			// crosses the threshold. ClearAllUnsatisfiable resets this on
			// any plausible capacity-changing event.
			rc.markCapacityProblem(ctx, cfg.ModelName, "cluster capacity exhausted")
			return
		}
		// Cap to actual capacity so we don't try harder than possible.
		if needed > capacity {
			xlog.Info("Reconciler: capping scale-up at cluster capacity", "model", target,
				"need", needed, "capacity", capacity)
			needed = capacity
		}
		xlog.Info("Reconciler: scaling up to meet minimum", "model", target,
			"current", current, "min", cfg.MinReplicas, "adding", needed)
		if rc.scaleUp(ctx, cfg, needed) {
			// A real (or partial) scale-up clears the hysteresis so a future
			// dip starts fresh. If scaleUp added nothing (scheduler errored or
			// no node could be loaded) we leave the hysteresis intact so the
			// next tick retries from where it left off rather than resetting
			// the unsatisfiable counter on a failed attempt.
			_ = rc.registry.ClearUnsatisfiable(ctx, cfg.ModelName)
		}
		return
	}

	// scaledUp tracks whether a scale-up already fired in this tick. The two
	// scale-up paths below (busy-burst and pressure) share the single `current`
	// value read once above; scaleUp does not re-check it. So at most one of
	// them may fire per tick, otherwise a model that is both busy AND over the
	// pressure threshold would scale +2 and could overshoot MaxReplicas by one.
	// Scale-down is also skipped in a tick that scaled up.
	scaledUp := false

	// 2. Auto-scale up if all replicas are busy
	if current > 0 && (cfg.MaxReplicas == 0 || int(current) < cfg.MaxReplicas) {
		if rc.allReplicasBusy(ctx, target) {
			candidateNodeIDs, selectorMatched := rc.candidateNodeIDsForSelector(ctx, cfg)
			if !selectorMatched {
				return
			}
			capacity, capErr := rc.registry.ClusterCapacityForModel(ctx, target, candidateNodeIDs)
			if capErr != nil || capacity == 0 {
				// All busy AND no slot available — burst load above capacity.
				// Don't enter cooldown for this case (it's transient demand,
				// not a misconfig); the next tick will retry naturally.
				return
			}
			xlog.Info("Reconciler: all replicas busy, scaling up", "model", target,
				"current", current)
			// Only mark the tick as having scaled up if a replica was actually
			// added. On a failed scaleUp, leave scaledUp false so the pressure
			// path below and the scale-down logic still apply as they would
			// have if the busy-burst path had not run.
			scaledUp = rc.scaleUp(ctx, cfg, 1)
		}
	}

	// 2b. Auto-scale up on prefix-cache forced-disturb pressure. A forced-disturb
	//     is recorded by the router when a request had a usable hot prefix match
	//     but the load guard forced it off the warm node: the cache-warm replica
	//     is saturated. We reuse the same MaxReplicas + capacity guards as the
	//     busy-burst path, and the same UnsatisfiableUntil cooldown gates this
	//     block at the top of reconcileModel, so a no-capacity model will not
	//     spin. Pressure never overrides MaxReplicas or force-evicts.
	//
	//     Skipped when the busy-burst path already scaled up this tick: at most
	//     one scaleUp(+1) per tick (see scaledUp above).
	if !scaledUp && rc.pressure != nil && current > 0 && (cfg.MaxReplicas == 0 || int(current) < cfg.MaxReplicas) {
		if pressureCount := rc.pressure.Count(target, time.Now()); pressureCount >= rc.pressureThreshold {
			candidateNodeIDs, selectorMatched := rc.candidateNodeIDsForSelector(ctx, cfg)
			if selectorMatched {
				capacity, capErr := rc.registry.ClusterCapacityForModel(ctx, target, candidateNodeIDs)
				if capErr == nil && capacity > 0 {
					xlog.Info("Reconciler: prefix-cache forced-disturb pressure, scaling up",
						"model", target, "current", current,
						"pressure", pressureCount,
						"threshold", rc.pressureThreshold)
					if rc.scaleUp(ctx, cfg, 1) {
						scaledUp = true
						// Consume the signal only on a real scale-up:
						// Pressure.Count is non-draining (it prunes only by
						// age), so a single burst stays in-window for the whole
						// window and would re-fire scaleUp on every tick. Reset
						// clears the model's events so a fresh scale-up needs
						// fresh forced-disturbs to accumulate. If scaleUp added
						// nothing (scheduler errored or no node could be loaded)
						// we preserve the signal so the next tick retries off
						// the same accumulated pressure instead of having to
						// re-accumulate a full window from scratch.
						rc.pressure.Reset(target)
					}
				}
				// No capacity: transient demand, not a misconfig - let the next
				// tick retry naturally (mirrors the busy-burst path's choice not
				// to enter cooldown for burst load).
			}
		}
	}

	// 3. Scale down idle replicas above minimum. Skipped in a tick that already
	//    scaled up so we never scale up and down in the same pass.
	if !scaledUp {
		floor := max(cfg.MinReplicas, 1)
		if int(current) > floor {
			rc.scaleDownIdle(ctx, cfg, int(current), floor)
		}
	}
}

// markCapacityProblem advances the hysteresis counter and sets the cooldown
// timestamp once it crosses the threshold. Centralized so the two scale-up
// paths (MinReplicas and busy-burst) report capacity exhaustion the same way.
func (rc *ReplicaReconciler) markCapacityProblem(ctx context.Context, modelName, reason string) {
	ticks, err := rc.registry.BumpUnsatisfiableTicks(ctx, modelName)
	if err != nil {
		xlog.Warn("Reconciler: failed to bump unsatisfiable counter", "model", modelName, "error", err)
		return
	}
	if ticks >= unsatisfiableTickThreshold {
		until := time.Now().Add(unsatisfiableCooldown)
		if err := rc.registry.MarkUnsatisfiable(ctx, modelName, until); err != nil {
			xlog.Warn("Reconciler: failed to mark unsatisfiable", "model", modelName, "error", err)
			return
		}
		xlog.Warn("Reconciler: scheduling unsatisfiable, entering cooldown",
			"model", modelName, "reason", reason,
			"cooldown", unsatisfiableCooldown, "retry_after", until.Format(time.RFC3339))
	}
}

// scaleUp schedules additional replicas of the model. Callers in
// reconcileModel are expected to have already capped `count` against
// ClusterCapacityForModel so this function never tries to overshoot.
//
// Returns true if at least one replica was actually scheduled. Callers use
// this to gate signal-consuming side effects (Pressure.Reset,
// ClearUnsatisfiable) on a real scale-up: a failed/no-op scaleUp must not
// discard the accumulated forced-disturb pressure or clear the unsatisfiable
// hysteresis, otherwise the signal has to re-accumulate from scratch and the
// next tick can't simply retry.
func (rc *ReplicaReconciler) scaleUp(ctx context.Context, cfg ModelSchedulingConfig, count int) bool {
	if rc.scheduler == nil {
		xlog.Warn("Reconciler: no scheduler available, cannot scale up")
		return false
	}

	// Resolve selector → candidate node IDs (nil when no selector → "any
	// healthy node"). The selector mismatch case is handled upstream in
	// reconcileModel, but defensively short-circuit here too.
	candidateNodeIDs, ok := rc.candidateNodeIDsForSelector(ctx, cfg)
	if !ok {
		return false
	}

	scheduled := 0
	for i := 0; i < count; i++ {
		node, err := rc.scheduler.ScheduleAndLoadModel(ctx, cfg.Target(), candidateNodeIDs)
		if err != nil {
			xlog.Warn("Reconciler: failed to scale up replica", "model", cfg.Target(),
				"attempt", i+1, "error", err)
			break // stop trying on first failure
		}
		scheduled++
		xlog.Info("Reconciler: scaled up replica", "model", cfg.Target(), "node", node.Name)
	}
	return scheduled > 0
}

// scaleDownIdle removes idle replicas above the floor.
func (rc *ReplicaReconciler) scaleDownIdle(ctx context.Context, cfg ModelSchedulingConfig, current, floor int) {
	if rc.unloader == nil {
		return
	}

	// Find idle replicas that have been unused for longer than scaleDownDelay.
	// Order by replica_index DESC first, then last_used ASC: trim the
	// highest-indexed replicas first so subsequent scale-ups can reuse the
	// low indexes via NextFreeReplicaIndex, keeping slot allocation compact
	// and matching the worker supervisor's port-recycling behavior.
	cutoff := time.Now().Add(-rc.scaleDownDelay)
	var idleModels []NodeModel
	currentModelRevision(rc.registry.db.WithContext(ctx)).
		Where("node_models.model_name = ? AND node_models.state = ? AND node_models.in_flight = 0 AND node_models.last_used < ?",
			cfg.Target(), "loaded", cutoff).
		Order("replica_index DESC, last_used ASC").
		Find(&idleModels)

	toRemove := current - floor
	removed := 0
	for _, nm := range idleModels {
		if removed >= toRemove {
			break
		}
		// Remove this specific replica row from registry (sibling replicas of
		// the same model on the same node, if any, are unaffected).
		if err := rc.registry.RemoveNodeModel(ctx, nm.NodeID, nm.ModelName, nm.ReplicaIndex); err != nil {
			xlog.Warn("Reconciler: failed to remove model record", "node", nm.NodeID, "model", nm.ModelName, "replica", nm.ReplicaIndex, "error", err)
			continue
		}
		// Unload from worker
		if err := rc.unloader.UnloadModelOnNode(nm.NodeID, nm.ModelName); err != nil {
			xlog.Warn("Reconciler: unload failed (model already removed from registry)", "error", err)
		}
		xlog.Info("Reconciler: scaled down idle replica", "model", cfg.Target(), "node", nm.NodeID, "replica", nm.ReplicaIndex)
		removed++
	}
}

// allReplicasBusy returns true if all loaded replicas of a model have in-flight requests.
func (rc *ReplicaReconciler) allReplicasBusy(ctx context.Context, modelName string) bool {
	var idleCount int64
	currentModelRevision(rc.registry.db.WithContext(ctx).Model(&NodeModel{})).
		Where("node_models.model_name = ? AND node_models.state = ? AND node_models.in_flight = 0", modelName, "loaded").
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
