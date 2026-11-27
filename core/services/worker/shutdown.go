package worker

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/core/services/workerctl"
	"github.com/mudler/xlog"
)

// The shutdown of a worker releases its loads fast. On SIGTERM the worker:
//
//  1. refuses new installs and upgrades (refuseShuttingDown), so the frontend
//     places new loads on another node;
//  2. aborts the load operations it runs: it cancels their downloads, kills
//     their processes, and waits, up to the abort wait, until the processes
//     exited and the installs returned (abortLoadOperations);
//  3. tells the frontend it drains, and that its loads ended when they all
//     did, so the frontend releases the load jobs at once and the next
//     request or reconciler pass loads the model on another node
//     (workerregistry.GracefulShutdown). A load that did not end in the abort
//     wait is released by the frontend after its stop window, as for a
//     worker that died.
//
// The replicas that serve keep serving through the graceful window, then the
// worker deregisters and stops its backends.

// defaultStoppingWait bounds how long a model.stop request waits for a
// process that another stop is already ending. A forced stop ends a process
// group within seconds. The wait stays at half the time the frontend waits for
// the reply (workerctl.ModelStopTimeout), so the reply arrives before the
// frontend gives up; a process that takes longer gets "already stopping", and
// the frontend tries again later.
const defaultStoppingWait = workerctl.ModelStopTimeout / 2

func (s *backendSupervisor) stoppingWaitOrDefault() time.Duration {
	if s.stoppingWait > 0 {
		return s.stoppingWait
	}
	return defaultStoppingWait
}

// defaultShutdownAbortWait bounds the abort phase of a shutdown as a whole:
// the wait for the processes of the aborted loads to exit and for the
// installs in progress to return. LOCALAI_SHUTDOWN_ABORT_WAIT sets it.
const defaultShutdownAbortWait = 30 * time.Second

// errWorkerShuttingDown is the error of a backend process that does not start
// because the worker shuts down.
var errWorkerShuttingDown = errors.New(workerctl.ErrWorkerShuttingDownMsg)

// refuseShuttingDown is the reply to an install that arrives while the worker
// shuts down.
func refuseShuttingDown() workerctl.BackendInstallReply {
	return workerctl.BackendInstallReply{Success: false, Error: workerctl.ErrWorkerShuttingDownMsg, ShuttingDown: true}
}

// beginShutdown makes the worker refuse new installs and upgrades, and
// cancels the downloads of the loads in progress. The flag is set under s.mu,
// the lock beginLoad registers a load under, so every load either registered
// before it (and abortLoadOperations sees it) or is refused.
func (s *backendSupervisor) beginShutdown() {
	s.mu.Lock()
	s.shuttingDown.Store(true)
	s.mu.Unlock()
	s.loadContext()
	s.cancelLoads()
}

// loadContext is the context of the downloads and installs of loads. A
// shutdown cancels it.
func (s *backendSupervisor) loadContext() context.Context {
	s.loadCtxOnce.Do(func() {
		s.loadCtx, s.cancelLoads = context.WithCancel(context.Background())
	})
	return s.loadCtx
}

// beginLoad registers the load of a backend.install request: its operation
// (nil for an install with no model) and the install itself, which
// abortLoadOperations waits for. It returns false, and registers nothing,
// once the shutdown started. The caller calls endLoad when the install
// returns.
func (s *backendSupervisor) beginLoad(req workerctl.BackendInstallRequest) (*loadOperation, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.shuttingDown.Load() {
		return nil, false
	}
	s.loads.Add(1)
	return s.beginOperationLocked(req), true
}

// endLoad marks the end of an install that beginLoad registered.
func (s *backendSupervisor) endLoad() {
	s.loads.Done()
}

// abortLoadOperations ends every load the worker runs: the loads the
// controller has not completed yet, and the installs still in progress. It
// kills the processes of the loads and waits until they exited and every
// install returned, up to wait for the whole phase. It returns the operation
// ids it aborted, and whether every load really ended: only then may the
// worker tell the controller that its loads ended. The caller has called
// beginShutdown, so no load starts after the ones it sees.
func (s *backendSupervisor) abortLoadOperations(wait time.Duration) (ids []string, ended bool) {
	deadline := time.Now().Add(wait)
	type victim struct {
		id  string
		key string
		bp  *backendProcess
	}
	var victims []victim
	s.mu.Lock()
	for id, op := range s.operations {
		delete(s.operations, id)
		op.expired = true
		v := victim{id: id, key: op.processKey}
		if bp, ok := s.processes[op.processKey]; ok && (op.instance == "" || bp.instance == op.instance) {
			bp.operationID = ""
			v.bp = bp
		}
		victims = append(victims, v)
	}
	s.mu.Unlock()

	ids = make([]string, 0, len(victims))
	var wg sync.WaitGroup
	var allExited atomic.Bool
	allExited.Store(true)
	for _, v := range victims {
		ids = append(ids, v.id)
		if v.bp == nil {
			continue
		}
		wg.Go(func() {
			xlog.Info("Shutting down: aborting a load operation", "operation", v.id, "processKey", v.key)
			if err := s.stopBackendExactBP(v.key, v.bp, true); err != nil {
				xlog.Warn("Failed to abort a load operation", "operation", v.id, "processKey", v.key, "error", err)
			}
			// stopBackendExactBP does nothing for a process another stop is
			// already ending: wait for that stop too.
			if !waitStoppedUntil(v.bp, deadline) {
				xlog.Warn("Shutting down: the process of an aborted load did not exit in the abort wait",
					"operation", v.id, "processKey", v.key)
				allExited.Store(false)
			}
		})
	}
	if !waitUntil(wg.Wait, deadline) || !allExited.Load() {
		return ids, false
	}
	// An install still in progress may have no process yet (it downloads);
	// its download was cancelled and the start of its process is refused, so
	// it returns soon. The loads ended once it did.
	if !waitUntil(s.loads.Wait, deadline) {
		xlog.Warn("Shutting down: an install in progress did not return in the abort wait")
		return ids, false
	}
	return ids, true
}

// waitUntil runs wait and reports whether it returned before deadline. A wait
// that does not return in time is left to finish on its own.
func waitUntil(wait func(), deadline time.Time) bool {
	done := make(chan struct{})
	go func() {
		wait()
		close(done)
	}()
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-timer.C:
		return false
	}
}

// waitStopped waits until the process of bp exited, up to the stopping wait.
// It reports whether the process is gone.
func (s *backendSupervisor) waitStopped(bp *backendProcess) bool {
	return waitStoppedUntil(bp, time.Now().Add(s.stoppingWaitOrDefault()))
}

// waitStoppedUntil waits until the process of bp exited, up to deadline. It
// reports whether the process is gone.
func waitStoppedUntil(bp *backendProcess, deadline time.Time) bool {
	if bp == nil || bp.proc == nil {
		return true
	}
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	select {
	case <-bp.proc.Done():
		return true
	case <-timer.C:
		return false
	}
}

// parseShutdownWindow reads a shutdown window setting, or returns def when it
// is empty or invalid.
func parseShutdownWindow(name, value string, def time.Duration) time.Duration {
	if value == "" {
		return def
	}
	d, err := time.ParseDuration(value)
	if err != nil || d <= 0 {
		xlog.Warn("Invalid shutdown window, using the default", "setting", name, "value", value, "default", def)
		return def
	}
	return d
}

// forgetOperation drops a finished operation. It is a no-op for an empty or
// unknown id.
func (s *backendSupervisor) forgetOperation(id string) {
	if id == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if op, known := s.operations[id]; known {
		op.expired = true
		delete(s.operations, id)
	}
}
