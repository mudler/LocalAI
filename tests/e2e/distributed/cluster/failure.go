package cluster

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// KillFrontend SIGKILLs frontend i. This is the "replica died" case: no drain,
// no graceful deregistration, sockets drop without a FIN from the application.
//
// The signal is delivered but not waited on, because a spec that asserts on the
// cluster's reaction wants to observe the window between the death and the
// survivors noticing it. Poll FrontendAlive with Eventually to join the exit.
func (c *Cluster) KillFrontend(i int) error {
	if err := c.checkFrontendIndex(i); err != nil {
		return err
	}
	return signalProcess(c.frontends[i], syscall.SIGKILL)
}

// StopFrontendGracefully SIGTERMs frontend i. This is the rolling-update case:
// the process gets a chance to drain and deregister. Like KillFrontend it does
// not wait; the point of the distinction between the two is what the process
// does with the time between the signal and its exit.
func (c *Cluster) StopFrontendGracefully(i int) error {
	if err := c.checkFrontendIndex(i); err != nil {
		return err
	}
	return signalProcess(c.frontends[i], syscall.SIGTERM)
}

// KillWorker SIGKILLs worker i.
func (c *Cluster) KillWorker(i int) error {
	if err := c.checkWorkerIndex(i); err != nil {
		return err
	}
	return signalProcess(c.workers[i], syscall.SIGKILL)
}

// RestartFrontend brings frontend i back on its original port with an empty
// data directory, modelling a replaced pod rather than a resumed one.
//
// The port is pinned rather than reallocated: workers read LOCALAI_REGISTER_TO
// once at boot and never re-resolve it, so a replica that returns on a new port
// is unreachable by exactly the workers that registered with it, and the
// failover the spec means to observe never happens. Rebinding is safe because
// the previous listener is fully closed before the new process starts (see the
// terminate below) and Go's listeners set SO_REUSEADDR, so a lingering
// TIME_WAIT on an accepted connection does not block the bind.
//
// The data directory is wiped so the replica must rehydrate node, session and
// job state from the shared Postgres and NATS. Keeping it would model a pod
// with a persistent volume and would hide the very class of bug these tests
// exist to find. This is only safe because startFrontend pins
// LOCALAI_AUTH_HMAC_SECRET: the secret otherwise lives at
// {DataPath}/.hmac_secret, and wiping it would make every session minted before
// the restart hash to a row the restarted replica cannot find, turning a
// failover assertion into an unexplained 401.
//
// The wipe also destroys state that nothing can rebuild. This harness sets no
// LOCALAI_STORAGE_URL, so the distributed object store is a directory under
// {DataPath} (core/application/distributed.go:146), and quantization and
// fine-tune jobs write their outputs to {DataPath}/quantization and
// {DataPath}/fine-tune (core/services/{quantization,finetune}/service.go:95);
// agent state, router-corpus, the voiceprofile store and {DataPath}/traces go
// the same way. Postgres keeps the job row, the artifact it points at is gone.
// So a spec that finishes a quantization or fine-tune on a replica, restarts
// it, and then asserts the artifact is retrievable fails for a storage reason
// dressed up as a failover one. No spec does that today; this note is here so
// the first one that tries does not spend a day on it.
//
// After StopFrontendGracefully, wait for the process to actually go before
// restarting:
//
//	Eventually(func() bool { return c.FrontendAlive(i) }, "20s", "500ms").
//		Should(BeFalse())
//
// FrontendAlive takes an index, so it has to be wrapped in a closure; handing
// Gomega the method value directly fails immediately: Eventually reports that
// the function it was given takes one argument and none were provided, and
// points at Eventually().WithArguments(). Restart
// terminates whatever is still running with SIGKILL, so restarting straight
// after a SIGTERM cuts the drain short and quietly turns the rolling-update
// case into the crash case, which is the opposite of what pairing those two
// calls is meant to express.
func (c *Cluster) RestartFrontend(i int) error {
	if err := c.checkFrontendIndex(i); err != nil {
		return err
	}
	old := c.frontends[i]
	if old == nil {
		return fmt.Errorf("frontend %d was never started, nothing to restart", i)
	}
	// frontendDataDir is relative when baseDir is empty, and this deletes it:
	// a Cluster assembled by a future test helper without a work dir would have
	// RemoveAll walking "frontend-N/data" under the package source directory.
	if c.baseDir == "" {
		return fmt.Errorf("refusing to wipe the data dir of frontend %d: cluster has no work dir", i)
	}
	// The old process may still be running (a restart with no preceding kill) or
	// already dead but unreaped. terminate is idempotent, bounds its wait, and
	// releases the log handle the replacement is about to reopen; without it the
	// replacement races the old listener for the port and leaks a file
	// descriptor per restart.
	old.terminate()
	if err := os.RemoveAll(c.frontendDataDir(i)); err != nil {
		return fmt.Errorf("wiping data dir of frontend %d: %w", i, err)
	}

	p, err := c.startFrontend(i, old.Port)
	if err != nil {
		return fmt.Errorf("restarting frontend %d: %w", i, err)
	}
	c.frontends[i] = p
	return nil
}

// FrontendAlive reports whether frontend i's process is still running.
func (c *Cluster) FrontendAlive(i int) bool {
	if i < 0 || i >= len(c.frontends) {
		return false
	}
	return c.frontends[i].alive()
}

// alive reports whether the process is still running.
//
// The exited check is cheap hygiene, not a fix for the zombie window. The
// reaper closes exited only after Cmd.Wait returns, and Wait marks the
// os.Process done before it returns (runtime/os pidfd path), so by the time
// exited is closed signal 0 already errors: this branch cannot fire earlier
// than the one it precedes. The window that stays open is the other one,
// between the child exiting and waitid collecting it: there the child is a
// zombie, signal 0 to a zombie succeeds, and alive reports true for a process
// that is already dead. There is no local fix; the caller's is to poll rather
// than assert once, wrapping the index-taking FrontendAlive in a closure:
//
//	Eventually(func() bool { return c.FrontendAlive(i) }, "20s", "500ms").
//		Should(BeFalse())
func (p *Process) alive() bool {
	if p == nil || p.Cmd == nil || p.Cmd.Process == nil {
		return false
	}
	select {
	case <-p.exited:
		return false
	default:
	}
	// Signal 0 tests for existence without delivering anything.
	return p.Cmd.Process.Signal(syscall.Signal(0)) == nil
}

func signalProcess(p *Process, sig syscall.Signal) error {
	if p == nil || p.Cmd == nil || p.Cmd.Process == nil {
		return fmt.Errorf("process is not running")
	}
	if err := p.Cmd.Process.Signal(sig); err != nil {
		return fmt.Errorf("signalling %s with %v: %w", p.Name, sig, err)
	}
	return nil
}

// checkFrontendIndex keeps the out-of-range wording identical across every
// primitive, so a failing spec reads the same whichever one tripped.
func (c *Cluster) checkFrontendIndex(i int) error {
	if i < 0 || i >= len(c.frontends) {
		return fmt.Errorf("frontend %d out of range (cluster has %d)", i, len(c.frontends))
	}
	return nil
}

// checkWorkerIndex is checkFrontendIndex for workers, and exists for the same
// reason: one wording, whichever primitive tripped.
func (c *Cluster) checkWorkerIndex(i int) error {
	if i < 0 || i >= len(c.workers) {
		return fmt.Errorf("worker %d out of range (cluster has %d)", i, len(c.workers))
	}
	return nil
}

func frontendName(i int) string {
	return fmt.Sprintf("frontend-%d", i)
}

func (c *Cluster) frontendDir(i int) string {
	return filepath.Join(c.baseDir, frontendName(i))
}

// frontendDataDir is LOCALAI_DATA_PATH for frontend i. RestartFrontend wipes it,
// so it must be the exact path startFrontend hands the child.
func (c *Cluster) frontendDataDir(i int) string {
	return filepath.Join(c.frontendDir(i), "data")
}
