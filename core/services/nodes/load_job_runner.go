package nodes

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/workerctl"
	"github.com/mudler/xlog"
)

// maxColdLoadRounds bounds how many times a request may claim-or-wait before
// giving up. A round ends when the job reaches a terminal state; a second round
// only happens when the model was evicted between the job finishing and the
// waiter re-checking, which is rare and must not become a spin.
const maxColdLoadRounds = 3

// routeViaLoadJob serves a request whose model is not loaded, in distributed
// mode. A cold load is a durable job of the model on one node, owned by
// whichever replica claimed it; every other request for the same model, on this
// replica or any other, attaches as a waiter and is served the moment a
// replica is ready.
//
// A job is keyed by (model, node), so a slow load on one node never holds the
// model. A request waits on the most advanced live load on a node that matches
// the model's rule. It starts a new load, on a node that has no load of the
// model, only when there is no such load or when the best one made no progress
// for the stall time (LOCALAI_MODEL_LOAD_STALL).
//
// The advisory lock of the job is held only for the claim. Before this split it
// wrapped the whole load, so a 35.7 GB staging run pinned it for ~20 minutes and
// every concurrent request died at the role's 60s statement_timeout with
// SQLSTATE 57014.
func (r *SmartRouter) routeViaLoadJob(ctx context.Context, att *routeAttempt) (*RouteResult, error) {
	// A held HTTP request cannot survive real infrastructure: an ingress or LB
	// idle timeout kills a twenty-minute request regardless of what LocalAI
	// does. So the wait is bounded, and expiry produces a structured answer
	// carrying live progress rather than letting the connection die anonymously.
	budget := r.loadWaitBudget()
	waitCtx := ctx
	if budget > 0 {
		var cancelWait context.CancelFunc
		waitCtx, cancelWait = context.WithTimeout(ctx, budget)
		defer cancelWait()
	}

	for range maxColdLoadRounds {
		target, served, err := r.joinOrStartLoad(ctx, att)
		if served != nil || err != nil {
			return served, err
		}

		// The waiter is keyed by the generation it waits for, so the end of an
		// older attempt can never wake it. A job that ended before this
		// registration is caught by the authority check inside waitForLoadJob.
		waiter := r.loadWaiterChan(loadWaiterKey(target.ref))
		served, err = r.waitForLoadJob(waitCtx, att, target, waiter)
		if served != nil {
			return served, nil
		}
		if err != nil {
			// The caller's own context is still live, so it was the wait budget
			// that ran out, not the client giving up: answer with progress.
			if ctx.Err() == nil && waitCtx.Err() != nil {
				return nil, r.loadingAnswer(ctx, att.trackingKey, budget)
			}
			return nil, err
		}

		// The signal is not the authority — the model may have been evicted
		// between ready and wake, so re-run the warm path.
		if result := r.tryWarmPath(ctx, att); result != nil {
			return result, nil
		}
	}
	return nil, fmt.Errorf("loading model %s: the load finished but the model is not available", att.trackingKey)
}

// loadWaitTarget is the attempt a request waits on, and its node. The node
// finds a row that has no generation yet (see jobOfWait).
type loadWaitTarget struct {
	ref    LoadJobRef
	nodeID string
}

func waitTargetOf(job *ModelLoadJob) loadWaitTarget {
	return loadWaitTarget{ref: job.Ref(), nodeID: job.NodeID}
}

// joinOrStartLoad picks the load this request waits on: a live load of the
// model on a matching node, or a new one it claims and starts. It returns the
// attempt to wait on, a route when the model turned out to be loaded already,
// or the error to answer with.
func (r *SmartRouter) joinOrStartLoad(ctx context.Context, att *routeAttempt) (loadWaitTarget, *RouteResult, error) {
	jobs, err := r.registry.ListLoadJobs(ctx, att.trackingKey)
	if err != nil {
		// Without the job rows nothing fences this load, so a load without
		// them would publish replicas nobody can cancel. Fail the request.
		// Models that are already loaded keep serving: the warm path does not
		// read the job table.
		return loadWaitTarget{}, nil, fmt.Errorf("reading the loads of model %s: %w", att.trackingKey, err)
	}
	now := time.Now()
	live := matchingLiveJobs(att, jobs, now)
	if target := MostAdvancedLoadJob(r.progressingJobs(live, now)); target != nil {
		xlog.Info("Model is already loading; waiting for it",
			"model", att.trackingKey, "state", target.State, "node", target.NodeName, "owner", target.OwnerReplica)
		return waitTargetOf(target), nil, nil
	}

	// No live load that makes progress on a matching node: start one on a
	// node that has no load of this model.
	placed, perr := r.placeNewReplica(ctx, att.trackingKey, att.modelOpts, nil, busyLoadNodes(jobs, now))
	if perr != nil {
		// Nowhere to start another load. A live load, even a stalled one, is
		// still the best chance; then a recent failure explains the wait.
		if target := MostAdvancedLoadJob(live); target != nil {
			xlog.Info("No other node can take the model; waiting for the running load",
				"model", att.trackingKey, "state", target.State, "node", target.NodeName, "reason", perr)
			return waitTargetOf(target), nil, nil
		}
		if held := heldLoadJob(jobs, now); held != nil {
			return loadWaitTarget{}, nil, NewLoadHeldError(held)
		}
		if errors.Is(perr, ErrLoadInProgress) {
			// Every node the model may use loads it already, but none of
			// those loads is one this request can wait on (the rule changed
			// under it, for example). Nothing failed: the HTTP layer answers
			// 503 with Retry-After for ErrNoAvailableNodes, not 500.
			return loadWaitTarget{}, nil, fmt.Errorf("%w: %w", perr, ErrNoAvailableNodes)
		}
		return loadWaitTarget{}, nil, perr
	}

	job, claimed, err := r.registry.ClaimLoadJobOn(ctx, att.trackingKey, placed.node.ID, ReplicaID())
	if err != nil {
		return loadWaitTarget{}, nil, fmt.Errorf("claiming the load of model %s on node %s: %w", att.trackingKey, placed.node.Name, err)
	}
	switch {
	case claimed:
		// The model may have been loaded between this request's warm-path
		// check and the claim. Without this check the claim would schedule a
		// second copy of a model that is already up.
		if result := r.tryWarmPath(ctx, att); result != nil {
			_ = r.finishLoadJob(ctx, job.Ref()) // already logged; the warm result stands
			return loadWaitTarget{}, result, nil
		}
		// The placement evicts only now, when this load is ours and still
		// needed. A failure releases the claim instead of failing the job:
		// a node that has no room this second is not a failed load, and a
		// failed row would hold the node for its report window.
		if err := r.makeRoom(ctx, att.trackingKey, placed); err != nil {
			_ = r.finishLoadJob(ctx, job.Ref())
			return loadWaitTarget{}, nil, err
		}
		r.startLoadJob(ctx, att, job.Ref(), placed)
	case job.State == LoadJobStateFailed:
		// Inside the failure grace window of that node: report the real cause
		// rather than silently starting a fresh load of a model that just
		// failed there.
		return loadWaitTarget{}, nil, NewLoadHeldError(job)
	default:
		xlog.Info("Model is already loading on another replica; waiting for it",
			"model", att.trackingKey, "state", job.State, "node", job.NodeName, "owner", job.OwnerReplica)
	}
	return waitTargetOf(job), nil, nil
}

// liveLoadJob reports whether job is a running attempt whose owner still holds
// its lease, by this frontend's clock. The claim judges leases by the database
// clock; this only decides what to wait on.
func liveLoadJob(job *ModelLoadJob, now time.Time) bool {
	return job.State != LoadJobStateFailed && job.LeaseUntil != nil && job.LeaseUntil.After(now)
}

// holdsNode reports whether job is a failed load whose stop window has not
// passed yet, so its node is still held for it.
func holdsNode(job *ModelLoadJob, now time.Time) bool {
	return job.State == LoadJobStateFailed && (job.StopDeadline == nil || job.StopDeadline.After(now))
}

// heldLoadJob returns the failed job that still holds its node the longest, or
// nil.
func heldLoadJob(jobs []ModelLoadJob, now time.Time) *ModelLoadJob {
	var held []ModelLoadJob
	for i := range jobs {
		if holdsNode(&jobs[i], now) {
			held = append(held, jobs[i])
		}
	}
	return MostAdvancedLoadJob(held)
}

// busyLoadNodes returns the nodes that a new load of the model must not use:
// those with a live load of it, and those whose failed load still holds them
// for its stop window. A row whose lease or window has passed is left to the
// claim, which releases it.
func busyLoadNodes(jobs []ModelLoadJob, now time.Time) []string {
	var out []string
	for i := range jobs {
		j := &jobs[i]
		if j.NodeID == "" {
			continue
		}
		if liveLoadJob(j, now) || holdsNode(j, now) {
			out = append(out, j.NodeID)
		}
	}
	return out
}

// matchingLiveJobs returns the live jobs that load on a node the request may
// use. A load on a node the rule excludes cannot serve this request, so it is
// not waited on. A job with no node yet (an older binary) is kept.
func matchingLiveJobs(att *routeAttempt, jobs []ModelLoadJob, now time.Time) []ModelLoadJob {
	var out []ModelLoadJob
	for i := range jobs {
		j := &jobs[i]
		if !liveLoadJob(j, now) {
			continue
		}
		if j.NodeID != "" && att.candidateNodeIDs != nil && !slices.Contains(att.candidateNodeIDs, j.NodeID) {
			continue
		}
		out = append(out, *j)
	}
	return out
}

// progressingJobs drops the jobs that made no progress for the stall time.
func (r *SmartRouter) progressingJobs(jobs []ModelLoadJob, now time.Time) []ModelLoadJob {
	stall := r.loadStall()
	if stall <= 0 {
		return jobs
	}
	var out []ModelLoadJob
	for _, j := range jobs {
		moved := j.CreatedAt
		if j.ProgressAt != nil {
			moved = *j.ProgressAt
		}
		if now.Sub(moved) < stall {
			out = append(out, j)
		}
	}
	return out
}

// loadStall resolves the configured stall time, where 0 means "never".
func (r *SmartRouter) loadStall() time.Duration {
	switch {
	case r.modelLoadStall < 0:
		return 0
	case r.modelLoadStall == 0:
		return config.DefaultModelLoadStall
	default:
		return r.modelLoadStall
	}
}

// loadWaitBudget resolves the configured wait into a duration, where 0 means
// "no timer — wait as long as the load takes".
func (r *SmartRouter) loadWaitBudget() time.Duration {
	switch {
	case r.modelLoadWait < 0: // LOCALAI_MODEL_LOAD_WAIT=0
		return 0
	case r.modelLoadWait == 0: // unset
		return config.DefaultModelLoadWait
	default:
		return r.modelLoadWait
	}
}

// loadingAnswer builds the 503 payload for a caller whose wait budget expired,
// reading the job row for live progress. A job that finished in the meantime
// leaves nothing to report, so the caller is told to retry against a plain
// deadline instead.
func (r *SmartRouter) loadingAnswer(ctx context.Context, trackingKey string, budget time.Duration) error {
	// The wait context is spent; read on a fresh, short-lived one.
	readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()

	// The model may be loading on several nodes; report the one furthest along.
	jobs, err := r.registry.ListLoadJobs(readCtx, trackingKey)
	job := MostAdvancedLoadJob(jobs)
	if err != nil || job == nil {
		return fmt.Errorf("timed out waiting for model %s to load", trackingKey)
	}
	if job.State == LoadJobStateFailed {
		return NewLoadHeldError(job)
	}
	return newModelLoadingError(job, budget)
}

// startLoadJob runs the claimed cold load in the background, detached from the
// request that triggered it. The job is owned by its record, not by that
// request: the client may disconnect, be retried onto another replica, or time
// out, and the transfer keeps going.
func (r *SmartRouter) startLoadJob(ctx context.Context, att *routeAttempt, ref LoadJobRef, placed *replicaPlacement) {
	// Keep the request's context VALUES (prefix chain and friends) but none of
	// its cancellation — see newColdLoadContext.
	parent := context.WithoutCancel(ctx)

	go func() {
		// runLoadOwner books the outcome and logs it; there is no caller left
		// to return the error to.
		_ = r.runLoadOwner(parent, ref, func(ownerCtx context.Context) error {
			loadCtx, cancelLoad := r.newColdLoadContext(ownerCtx)
			defer cancelLoad()
			_, err := r.coldLoad(loadCtx, att, 0, placed)
			return err
		})
	}()
}

// runLoadOwner is the one loop that owns a claimed load, for the request path
// and the reconciler path alike. It runs work under the job's generation,
// heartbeats the row, and ends with a conditional fail or delete.
//
// The heartbeat doubles as the ownership check: when it finds the job gone or
// held by another generation, the work context is cancelled. An owner that lost
// its job stops, and its late writes find zero rows. The stale error is
// returned to the caller instead of being swallowed, so the caller can tell a
// lost job from a failed load.
func (r *SmartRouter) runLoadOwner(ctx context.Context, ref LoadJobRef, work func(context.Context) error) error {
	ownerCtx, cancel := context.WithCancelCause(withLoadOwnership(ctx, ref))
	defer cancel(nil)

	phase := newLoadPhaseReporter()
	ownerCtx = withLoadPhaseReporter(ownerCtx, phase)

	stopHeartbeat := r.startLoadJobHeartbeat(ownerCtx, ref, phase, cancel)
	err := work(ownerCtx)
	stopHeartbeat()
	nodeID, replica, legacy := phase.placement()
	// Record the placement now. The heartbeat writes it once a second, and a
	// failure that comes sooner would leave the job with no node, so no stop
	// could find the work.
	if nodeID != "" {
		flushCtx, cancelFlush := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		if ferr := r.registry.UpdateLoadJob(flushCtx, ref, phase.snapshot()); ferr != nil && !errors.Is(ferr, ErrStaleLoadJob) {
			xlog.Debug("Failed to record the load placement", "model", ref.TrackingKey, "error", ferr)
		}
		cancelFlush()
	}

	cause := context.Cause(ownerCtx)
	lost := errors.Is(cause, ErrStaleLoadJob)
	leaseExpired := errors.Is(cause, ErrLoadLeaseExpired)
	opLost := errors.Is(cause, ErrLoadOperationLost)
	if opLost {
		err = fmt.Errorf("loading model %s: %w", ref.TrackingKey, ErrLoadOperationLost)
	}
	if leaseExpired {
		err = fmt.Errorf("loading model %s: %w", ref.TrackingKey, ErrLoadLeaseExpired)
	}
	// Bookkeeping must survive the owner context, which may be exactly what
	// just ended.
	bookCtx, cancelBook := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancelBook()

	switch {
	case lost:
		xlog.Warn("Cold load stopped: its job now belongs to another attempt", "model", ref.TrackingKey)
		r.closeLoadWaiters(loadWaiterKey(ref))
		// The job was replaced or cancelled, but this attempt's remote work may
		// still run. Stop it by its operation id. The confirmation shortens the
		// stop window of a cancelled job.
		r.stopLoadWork(bookCtx, ref, nodeID, replica, phase.backendAddress(), legacy)
		return fmt.Errorf("loading model %s: %w", ref.TrackingKey, ErrStaleLoadJob)
	case err != nil:
		xlog.Error("Cold load job failed", "model", ref.TrackingKey, "error", err)
		// Work may outlive the failure when we gave up on it (deadline, cancel,
		// lost lease) once a node was chosen. An answer from the backend, or a
		// failure before any node was chosen, means nothing is left running.
		mayRun := phase.snapshot().State != LoadJobStatePending && (leaseExpired || loadAbandonedOnWorker(err))
		if ferr := r.registry.FailLoadJob(bookCtx, ref, err.Error(), mayRun); ferr != nil {
			if errors.Is(ferr, ErrStaleLoadJob) {
				r.closeLoadWaiters(loadWaiterKey(ref))
				return fmt.Errorf("loading model %s: %w (load error: %v)", ref.TrackingKey, ErrStaleLoadJob, err)
			}
			xlog.Warn("Failed to record cold load failure", "model", ref.TrackingKey, "error", ferr)
		}
		r.closeLoadWaiters(loadWaiterKey(ref))
		switch {
		case !mayRun:
			// The backend answered, so the work ended. Stop watching it.
			r.completeLoadOperation(bookCtx, ref, nodeID)
		default:
			r.stopLoadWork(bookCtx, ref, nodeID, replica, phase.backendAddress(), legacy)
		}
		// The row stays until its stop deadline so a request arriving right
		// now reports this failure instead of starting a duplicate load. The
		// timer only tidies the table: a restart loses it, and the next claim
		// replaces a failed row past its deadline without it.
		time.AfterFunc(r.failedJobTidyDelay(mayRun), func() {
			delCtx, cancelDel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancelDel()
			if derr := r.registry.DeleteFailedLoadJob(delCtx, ref); derr != nil && !errors.Is(derr, ErrStaleLoadJob) {
				xlog.Warn("Failed to clear failed cold load job", "model", ref.TrackingKey, "error", derr)
			}
		})
		return err
	}
	// The load succeeded: end the operation on the worker before the job goes,
	// so the watchdog stops watching a backend that now serves.
	r.completeLoadOperation(bookCtx, ref, nodeID)
	return r.finishLoadJob(bookCtx, ref)
}

// completeLoadOperation tells the worker the load finished. It retries a few
// times, and a loss is not fatal: the worker leaves a backend that already
// answers READY running when an operation expires.
func (r *SmartRouter) completeLoadOperation(ctx context.Context, ref LoadJobRef, nodeID string) {
	if r.unloader == nil || nodeID == "" {
		return
	}
	var lastErr error
	for range 3 {
		if _, lastErr = r.unloader.OperationControl(nodeID, workerctl.OperationRequest{Complete: []string{ref.Generation}}); lastErr == nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(500 * time.Millisecond):
		}
	}
	xlog.Warn("Could not complete the load operation on the worker", "node", nodeID, "model", ref.TrackingKey, "error", lastErr)
}

// stopLoadWork stops the remote work of one attempt through the single stop
// path, and records the outcome on the job. Without a node there is nothing to
// stop yet; the worker's own watchdog bounds any install already in flight.
func (r *SmartRouter) stopLoadWork(ctx context.Context, ref LoadJobRef, nodeID string, replica int, addr string, legacy bool) {
	if r.unloader == nil || nodeID == "" {
		return
	}
	reg, ok := r.registry.(loadAttemptRegistry)
	if !ok {
		return
	}
	StopLoadAttempt(ctx, reg, r.unloader, ref, nodeID, replica, addr, legacy)
}

// finishLoadJob ends a job that succeeded. The NodeModel row (state `loaded`)
// is the record from here, so the job row is dropped BEFORE waiters are woken:
// they re-run the warm path and must not find a job that is really done. A
// stale ref deletes nothing and wakes only its own generation's waiters.
func (r *SmartRouter) finishLoadJob(ctx context.Context, ref LoadJobRef) error {
	err := r.registry.DeleteLoadJob(ctx, ref)
	if err != nil {
		xlog.Warn("Failed to clear completed cold load job", "model", ref.TrackingKey, "error", err)
	}
	r.closeLoadWaiters(loadWaiterKey(ref))
	if errors.Is(err, ErrStaleLoadJob) {
		return fmt.Errorf("loading model %s: %w", ref.TrackingKey, err)
	}
	return nil
}

// startLoadJobHeartbeat keeps the job row's liveness and progress fresh while
// the load runs, and returns a function that stops it.
//
// The heartbeat is deliberately time-driven rather than byte-driven: a
// checkpoint load moves no bytes for many minutes, and a job that only wrote a
// row when bytes moved would look orphaned and be reclaimed mid-load. Byte
// progress is copied in from the staging tracker, which already debounces the
// per-chunk callbacks, so the row is written at most once per interval.
func (r *SmartRouter) startLoadJobHeartbeat(parent context.Context, ref LoadJobRef, phase *loadPhaseReporter, abort context.CancelCauseFunc) func() {
	var renewing atomic.Bool
	var unknown atomic.Int32
	ticks := 0
	every := r.opRenewEvery
	if every <= 0 {
		every = loadOpRenewEvery
	}
	trackingKey := ref.TrackingKey
	done := make(chan struct{})
	stopped := make(chan struct{})

	go func() {
		defer close(stopped)
		ticker := time.NewTicker(loadJobHeartbeatInterval)
		defer ticker.Stop()
		var startedAt time.Time
		// lastRenewed is monotonic, so the owner's own deadline does not move
		// with the wall clock.
		lastRenewed := time.Now()
		for {
			select {
			case <-done:
				return
			case <-parent.Done():
				return
			case <-ticker.C:
				u := phase.snapshot()
				ticks++
				if ticks%every == 1 || every == 1 {
					r.renewLoadOperation(parent, ref, phase, &renewing, &unknown, abort)
				}
				// The tracker is keyed by model. Several loads of one model
				// may run on several nodes, so only the one on this job's
				// node is this job's progress. A job with no node yet has
				// none: another node's bytes would move its progress time and
				// hide a stall.
				if st := r.stagingTracker.Get(trackingKey); st != nil && u.NodeName != "" && st.NodeName == u.NodeName {
					u.BytesSent, u.TotalBytes = st.BytesSent, st.TotalBytes
					u.FileIndex, u.TotalFiles = st.FileIndex, st.TotalFiles
					if u.BytesSent > 0 && startedAt.IsZero() {
						startedAt = time.Now()
					}
					u.StartedAt = startedAt
				}
				ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), loadJobHeartbeatInterval*5)
				err := r.registry.UpdateLoadJob(ctx, ref, u)
				cancel()
				if errors.Is(err, ErrStaleLoadJob) {
					// Zero rows: the job is gone or another generation holds
					// it. Stop the work instead of finishing a load nobody
					// owns.
					abort(ErrStaleLoadJob)
					return
				}
				if err == nil {
					lastRenewed = time.Now()
					continue
				}
				xlog.Debug("Failed to heartbeat cold load job", "model", trackingKey, "error", err)
				// A failed renewal is not fatal by itself. The owner keeps
				// working until the lease it last extended has run out, then
				// stops: it must not outlive a lease it cannot extend.
				if time.Since(lastRenewed) >= r.loadLeaseTTL() {
					abort(ErrLoadLeaseExpired)
					return
				}
			}
		}
	}()

	return func() {
		close(done)
		<-stopped
	}
}

// waitForLoadJob blocks until the cold load of ref reaches a terminal state,
// the job's failure is known, or the caller gives up.
//
// Waiters share one broadcast rather than an ordered queue: they all want the
// identical outcome — the model loaded — so ordering them would add fairness
// machinery that changes no result. The local channel is only a hint that wakes
// same-replica waiters early; the DB is the authority, because a waiter on
// another replica has no channel to close and broadcasts are
// fire-and-forget, so a missed terminal event must not strand it.
//
// The waiter is bound to one generation. When the job row is gone, or belongs to
// another generation, the attempt it waited for is over: the caller re-runs the
// warm path and, if the model is still missing, claims again.
//
// On every poll the waiter also tries the warm path. A replica of the model may
// become ready on another node that matches the rule while this job is still
// running, for example one the reconciler loaded, or a faster node. The request
// is served from it at once and the job keeps running. Without this a request
// waited for the slowest load in the cluster, not the first ready replica.
func (r *SmartRouter) waitForLoadJob(ctx context.Context, att *routeAttempt, target loadWaitTarget, waiter <-chan struct{}) (*RouteResult, error) {
	ref := target.ref
	defer r.releaseLoadWaiter(loadWaiterKey(ref), waiter)

	// The registration may have come after the job ended; ask the authority
	// before sleeping on the hint.
	if done, err := r.checkLoadWait(ctx, target); done {
		return nil, err
	}

	ticker := time.NewTicker(loadJobPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-waiter:
			waiter = nil // closed: do not spin on it
			if done, err := r.checkLoadWait(ctx, target); done {
				return nil, err
			}
		case <-ctx.Done():
			// The client gave up. The job is unaffected: it is owned by the job
			// record, not by this request.
			return nil, ctx.Err()
		case <-ticker.C:
			if result := r.tryWarmPath(ctx, att); result != nil {
				xlog.Info("Served a waiting request from a replica that became ready; the load job keeps running",
					"model", ref.TrackingKey, "node", result.Node.Name)
				return result, nil
			}
			if done, err := r.checkLoadWait(ctx, target); done {
				return nil, err
			}
		}
	}
}

// checkLoadWait reads the job row and reports whether the wait is over.
func (r *SmartRouter) checkLoadWait(ctx context.Context, target loadWaitTarget) (bool, error) {
	job, err := r.jobOfWait(ctx, target)
	if err != nil {
		xlog.Debug("Polling the model load job failed", "model", target.ref.TrackingKey, "error", err)
		return false, nil
	}
	if job == nil {
		// Terminal: it succeeded, was reaped, or was replaced. Either way the
		// caller re-checks the warm path.
		return true, nil
	}
	if job.State == LoadJobStateFailed {
		return true, NewLoadHeldError(job)
	}
	return false, nil
}

// jobOfWait reads the row of the attempt a request waits on, or nil when it is
// over. A row with no generation (written before a frontend backfilled it)
// cannot be read by its generation, so it is read by its model and node; once
// the row has a generation, it is another attempt, and this one is over.
func (r *SmartRouter) jobOfWait(ctx context.Context, target loadWaitTarget) (*ModelLoadJob, error) {
	if target.ref.owned() {
		return r.registry.GetLoadJobByRef(ctx, target.ref)
	}
	jobs, err := r.registry.ListLoadJobs(ctx, target.ref.TrackingKey)
	if err != nil {
		return nil, err
	}
	for i := range jobs {
		if jobs[i].NodeID == target.nodeID && !jobs[i].Ref().owned() {
			return &jobs[i], nil
		}
	}
	return nil, nil
}

// loadWaiter is the shared wake-up channel for one generation, with a count of
// the requests registered on it.
type loadWaiter struct {
	ch   chan struct{}
	refs int
}

// loadWaiterKey keys waiters by generation, not by model, so a late finish of
// one attempt cannot wake the waiters of the next.
func loadWaiterKey(ref LoadJobRef) string { return ref.TrackingKey + "\x00" + ref.Generation }

// loadWaiterChan registers a waiter and returns the broadcast channel for key,
// creating it on first use. Same shape as advisorylock.localLocks: N local
// requests share one wait and wake together.
func (r *SmartRouter) loadWaiterChan(key string) <-chan struct{} {
	r.loadWaitersMu.Lock()
	defer r.loadWaitersMu.Unlock()
	if r.loadWaiters == nil {
		r.loadWaiters = map[string]*loadWaiter{}
	}
	w, ok := r.loadWaiters[key]
	if !ok {
		w = &loadWaiter{ch: make(chan struct{})}
		r.loadWaiters[key] = w
	}
	w.refs++
	return w.ch
}

// releaseLoadWaiter drops one registration. Without it a waiter that gives up
// before the job ends would leave its entry in the map for ever. The channel
// identity check keeps a late release from deleting a newer registration.
func (r *SmartRouter) releaseLoadWaiter(key string, ch <-chan struct{}) {
	r.loadWaitersMu.Lock()
	defer r.loadWaitersMu.Unlock()
	if w := r.loadWaiters[key]; w != nil && w.ch == ch {
		w.refs--
		if w.refs <= 0 {
			delete(r.loadWaiters, key)
		}
	}
}

// closeLoadWaiters wakes every local waiter on key. A waiter that registers
// after this sees a fresh channel and falls back to the DB check.
func (r *SmartRouter) closeLoadWaiters(key string) {
	r.loadWaitersMu.Lock()
	w, ok := r.loadWaiters[key]
	delete(r.loadWaiters, key)
	r.loadWaitersMu.Unlock()
	if ok {
		close(w.ch)
	}
}

// loadOpRenewEvery is how many heartbeat ticks pass between operation renewals
// on the worker. Renewing every few seconds is far inside the worker's kill TTL
// and spares the bus a request per second per load.
const loadOpRenewEvery = 5

// loadOpLostAfter is how many renewals in a row the worker must answer with
// "unknown" before the owner gives up on the operation.
const loadOpLostAfter = 3

// renewLoadOperation extends the worker's lease on the load. It runs off the
// heartbeat goroutine, so a slow worker cannot delay the database lease, and at
// most one is in flight. A failure costs nothing until the worker's kill TTL.
func (r *SmartRouter) renewLoadOperation(ctx context.Context, ref LoadJobRef, phase *loadPhaseReporter, busy *atomic.Bool, unknown *atomic.Int32, abort context.CancelCauseFunc) {
	nodeID, _, _ := phase.placement()
	if r.unloader == nil || nodeID == "" || !busy.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer busy.Store(false)
		reply, err := r.unloader.OperationControl(nodeID, workerctl.OperationRequest{Renew: []string{ref.Generation}})
		switch {
		case err != nil:
			xlog.Debug("Failed to renew the load operation", "node", nodeID, "model", ref.TrackingKey, "error", err)
		case len(reply.Unknown) > 0:
			// The worker answered and does not know the operation: it
			// restarted, or its watchdog already ended it. Either way the work
			// is gone, and nothing will ever finish this load. One miss can be an
			// install still in flight, so it takes several in a row.
			if n := unknown.Add(1); n >= loadOpLostAfter {
				xlog.Warn("The worker no longer knows this load operation; failing the load", "node", nodeID, "model", ref.TrackingKey)
				abort(ErrLoadOperationLost)
			}
		default:
			unknown.Store(0)
		}
	}()
}

func (r *SmartRouter) loadLeaseTTL() time.Duration {
	if r.leaseTTL > 0 {
		return r.leaseTTL
	}
	return loadJobLeaseTTL
}

// failedJobTidyDelay is when the in-process timer tries to remove a failed row.
// It only has to be after the stop deadline the row was given.
func (r *SmartRouter) failedJobTidyDelay(mayRun bool) time.Duration {
	if mayRun {
		return loadJobStopWindow + time.Second
	}
	return loadJobFailureReport + time.Second
}
