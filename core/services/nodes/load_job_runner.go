package nodes

import (
	"context"
	"fmt"
	"time"

	"github.com/mudler/xlog"
)

// maxColdLoadRounds bounds how many times a request may claim-or-wait before
// giving up. A round ends when the job reaches a terminal state; a second round
// only happens when the model was evicted between the job finishing and the
// waiter re-checking, which is rare and must not become a spin.
const maxColdLoadRounds = 3

// routeViaLoadJob serves a request whose model is not loaded, in distributed
// mode. The cold load itself becomes a durable job owned by whichever replica
// claims it; every other request for the same model — on this replica or any
// other — attaches as a waiter and is served the moment the model is ready.
//
// The per-model advisory lock still de-duplicates loaders, but it is held only
// for the claim. Before this split it wrapped the whole load, so a 35.7 GB
// staging run pinned it for ~20 minutes and every concurrent request died at
// the role's 60s statement_timeout with SQLSTATE 57014.
func (r *SmartRouter) routeViaLoadJob(ctx context.Context, att *routeAttempt) (*RouteResult, error) {
	for range maxColdLoadRounds {
		// Register interest BEFORE claiming, so a job that finishes immediately
		// cannot close the channel before this waiter exists.
		waiter := r.loadWaiterChan(att.trackingKey)

		job, claimed, err := r.registry.ClaimLoadJob(ctx, att.trackingKey, ReplicaID())
		if err != nil {
			// A broken job table must not make the model unroutable: fall back
			// to loading inline, which is what every release before this did.
			xlog.Warn("Claiming the model load job failed; loading inline instead",
				"model", att.trackingKey, "error", err)
			loadCtx, cancelLoad := r.newColdLoadContext(context.WithoutCancel(ctx))
			defer cancelLoad()
			return r.coldLoad(loadCtx, att, 1)
		}

		switch {
		case claimed:
			// The model may have been loaded between this request's warm-path
			// check and the claim — the check the old code did after acquiring
			// the lock. Without it the claim would schedule a second copy of a
			// model that is already up.
			if result := r.tryWarmPath(ctx, att); result != nil {
				r.finishLoadJob(ctx, att.trackingKey)
				return result, nil
			}
			r.startLoadJob(ctx, att)
		case job != nil && job.State == LoadJobStateFailed:
			// Inside the failure grace window: report the real cause rather
			// than silently starting a fresh load of a model that just failed.
			return nil, fmt.Errorf("loading model %s: %s", att.trackingKey, job.LastError)
		default:
			xlog.Info("Model is already loading on another replica; waiting for it",
				"model", att.trackingKey, "state", job.State, "node", job.NodeName, "owner", job.OwnerReplica)
		}

		if err := r.waitForLoadJob(ctx, att.trackingKey, waiter); err != nil {
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

// startLoadJob runs the claimed cold load in the background, detached from the
// request that triggered it. The job is owned by its record, not by that
// request: the client may disconnect, be retried onto another replica, or time
// out, and the transfer keeps going.
func (r *SmartRouter) startLoadJob(ctx context.Context, att *routeAttempt) {
	trackingKey := att.trackingKey
	// Keep the request's context VALUES (prefix chain and friends) but none of
	// its cancellation — see newColdLoadContext.
	parent := context.WithoutCancel(ctx)

	go func() {
		loadCtx, cancelLoad := r.newColdLoadContext(parent)
		defer cancelLoad()

		phase := newLoadPhaseReporter()
		loadCtx = withLoadPhaseReporter(loadCtx, phase)

		stopHeartbeat := r.startLoadJobHeartbeat(parent, trackingKey, phase)

		_, err := r.coldLoad(loadCtx, att, 0)

		stopHeartbeat()

		// Bookkeeping must survive the load context, which may be exactly what
		// just expired.
		bookCtx, cancelBook := context.WithTimeout(context.WithoutCancel(parent), 30*time.Second)
		defer cancelBook()

		if err != nil {
			xlog.Error("Cold load job failed", "model", trackingKey, "error", err)
			if ferr := r.registry.FailLoadJob(bookCtx, trackingKey, err.Error()); ferr != nil {
				xlog.Warn("Failed to record cold load failure", "model", trackingKey, "error", ferr)
			}
			r.closeLoadWaiters(trackingKey)
			// Keep the row briefly so a request arriving right now reports this
			// failure instead of starting a duplicate load. Deleting it
			// immediately turns a failure into a retry storm.
			time.AfterFunc(loadJobFailureGrace, func() {
				delCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				if derr := r.registry.DeleteLoadJob(delCtx, trackingKey); derr != nil {
					xlog.Warn("Failed to clear failed cold load job", "model", trackingKey, "error", derr)
				}
			})
			return
		}

		r.finishLoadJob(bookCtx, trackingKey)
	}()
}

// finishLoadJob ends a job that succeeded. The NodeModel row (state `loaded`)
// is the record from here, so the job row is dropped BEFORE waiters are woken:
// they re-run the warm path and must not find a job that is really done.
func (r *SmartRouter) finishLoadJob(ctx context.Context, trackingKey string) {
	if err := r.registry.DeleteLoadJob(ctx, trackingKey); err != nil {
		xlog.Warn("Failed to clear completed cold load job", "model", trackingKey, "error", err)
	}
	r.closeLoadWaiters(trackingKey)
}

// startLoadJobHeartbeat keeps the job row's liveness and progress fresh while
// the load runs, and returns a function that stops it.
//
// The heartbeat is deliberately time-driven rather than byte-driven: a
// checkpoint load moves no bytes for many minutes, and a job that only wrote a
// row when bytes moved would look orphaned and be reclaimed mid-load. Byte
// progress is copied in from the staging tracker, which already debounces the
// per-chunk callbacks, so the row is written at most once per interval.
func (r *SmartRouter) startLoadJobHeartbeat(parent context.Context, trackingKey string, phase *loadPhaseReporter) func() {
	done := make(chan struct{})
	stopped := make(chan struct{})

	go func() {
		defer close(stopped)
		ticker := time.NewTicker(loadJobHeartbeatInterval)
		defer ticker.Stop()
		var startedAt time.Time
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				u := phase.snapshot()
				if st := r.stagingTracker.Get(trackingKey); st != nil {
					u.BytesSent, u.TotalBytes = st.BytesSent, st.TotalBytes
					u.FileIndex, u.TotalFiles = st.FileIndex, st.TotalFiles
					if u.BytesSent > 0 && startedAt.IsZero() {
						startedAt = time.Now()
					}
					u.StartedAt = startedAt
				}
				ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), loadJobHeartbeatInterval*5)
				if err := r.registry.UpdateLoadJob(ctx, trackingKey, u); err != nil {
					xlog.Debug("Failed to heartbeat cold load job", "model", trackingKey, "error", err)
				}
				cancel()
			}
		}
	}()

	return func() {
		close(done)
		<-stopped
	}
}

// waitForLoadJob blocks until the cold load of trackingKey reaches a terminal
// state, the job's failure is known, or the caller gives up.
//
// Waiters share one broadcast rather than an ordered queue: they all want the
// identical outcome — the model loaded — so ordering them would add fairness
// machinery that changes no result. The local channel wakes same-replica
// waiters instantly; the DB poll is the authority, because a waiter on another
// replica has no channel to close and NATS broadcasts are fire-and-forget, so a
// missed terminal event must not strand it.
func (r *SmartRouter) waitForLoadJob(ctx context.Context, trackingKey string, waiter <-chan struct{}) error {
	ticker := time.NewTicker(loadJobPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-waiter:
			return nil
		case <-ctx.Done():
			// The client gave up. The job is unaffected: it is owned by the job
			// record, not by this request.
			return ctx.Err()
		case <-ticker.C:
			job, err := r.registry.GetLoadJob(ctx, trackingKey)
			if err != nil {
				xlog.Debug("Polling the model load job failed", "model", trackingKey, "error", err)
				continue
			}
			if job == nil {
				// Terminal: either it succeeded, or it was reaped. Either way
				// the caller re-checks the warm path.
				return nil
			}
			if job.State == LoadJobStateFailed {
				return fmt.Errorf("loading model %s: %s", trackingKey, job.LastError)
			}
		}
	}
}

// loadWaiterChan returns the broadcast channel for trackingKey, creating it on
// first use. Same shape as advisorylock.localLocks: N local requests share one
// wait and wake together.
func (r *SmartRouter) loadWaiterChan(trackingKey string) <-chan struct{} {
	r.loadWaitersMu.Lock()
	defer r.loadWaitersMu.Unlock()
	if r.loadWaiters == nil {
		r.loadWaiters = map[string]chan struct{}{}
	}
	ch, ok := r.loadWaiters[trackingKey]
	if !ok {
		ch = make(chan struct{})
		r.loadWaiters[trackingKey] = ch
	}
	return ch
}

// closeLoadWaiters wakes every local waiter on trackingKey. A waiter that
// registers after this sees a fresh channel and falls back to the DB poll.
func (r *SmartRouter) closeLoadWaiters(trackingKey string) {
	r.loadWaitersMu.Lock()
	ch, ok := r.loadWaiters[trackingKey]
	delete(r.loadWaiters, trackingKey)
	r.loadWaitersMu.Unlock()
	if ok {
		close(ch)
	}
}
