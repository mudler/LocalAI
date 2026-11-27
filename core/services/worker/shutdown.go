package worker

import (
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/workerctl"
	"github.com/mudler/xlog"
)

// The shutdown of a worker releases its loads fast. On SIGTERM the worker:
//
//  1. refuses new installs and upgrades (refuseShuttingDown), so the frontend
//     places new loads on another node;
//  2. aborts the load operations it runs and waits for their processes to
//     exit (abortLoadOperations);
//  3. tells the frontend it drains and that its loads ended, so the frontend
//     releases the load jobs at once and the next request or reconciler pass
//     loads the model on another node (workerregistry.GracefulDeregister).
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

// refuseShuttingDown is the reply to an install that arrives while the worker
// shuts down.
func refuseShuttingDown() workerctl.BackendInstallReply {
	return workerctl.BackendInstallReply{Success: false, Error: workerctl.ErrWorkerShuttingDownMsg, ShuttingDown: true}
}

// beginShutdown makes the worker refuse new installs and upgrades.
func (s *backendSupervisor) beginShutdown() {
	s.shuttingDown.Store(true)
}

// abortLoadOperations ends every load operation the worker runs: the loads the
// controller has not completed yet. It kills their processes and waits until
// they exited, so the worker can tell the controller that its loads ended. It
// returns the operation ids it aborted.
func (s *backendSupervisor) abortLoadOperations() []string {
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

	ids := make([]string, 0, len(victims))
	var wg sync.WaitGroup
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
			s.waitStopped(v.bp)
		})
	}
	wg.Wait()
	return ids
}

// waitStopped waits until the process of bp exited, up to the stopping wait.
// It reports whether the process is gone.
func (s *backendSupervisor) waitStopped(bp *backendProcess) bool {
	if bp == nil || bp.proc == nil {
		return true
	}
	select {
	case <-bp.proc.Done():
		return true
	case <-time.After(s.stoppingWaitOrDefault()):
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
