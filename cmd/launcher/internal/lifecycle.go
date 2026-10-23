// SPDX-License-Identifier: MIT
package launcher

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mudler/LocalAI/pkg/workerledger"
)

type lifecycleState string

const (
	lifecycleStopped    lifecycleState = "stopped"
	lifecycleStarting   lifecycleState = "starting"
	lifecycleRunning    lifecycleState = "running"
	lifecycleStopping   lifecycleState = "stopping"
	lifecycleIncomplete lifecycleState = "shutdown incomplete"
)

var (
	errLifecycleBusy      = errors.New("a runtime operation is already in progress or a runtime is running")
	errNotRunning         = errors.New("no runtime is running")
	errShutdownIncomplete = errors.New("shutdown incomplete: a process owned by the earlier run may still be alive")
)

// Spec section "Lifecycle and ownership": 30 s to drain, then force, then 5 s to
// reap and verify.
const (
	defaultTermGrace = 30 * time.Second
	defaultKillGrace = 5 * time.Second
)

// launchSpec is everything the controller needs to run one child. It carries
// no policy: validation happened when the launch description was built.
type launchSpec struct {
	Binary string
	Args   []string
	Env    []string
	Dir    string
	// LedgerPath is the backend process ledger the runtime writes. Empty for a
	// role that records no backend ownership.
	LedgerPath string
	// Attach runs before the child starts, to connect its output.
	Attach func(*exec.Cmd) error
	// OnExit is called once per run, with the run's generation, after the
	// runtime and every owned group are confirmed gone (or cleanup failed).
	OnExit func(generation uint64, err error)
}

// lifecycle serializes the single child the launcher may own. All state changes
// happen under mu; blocking waits happen outside it, behind the Stopping and
// Starting states, which reject every other operation.
type lifecycle struct {
	mu         sync.Mutex
	state      lifecycleState
	generation uint64
	cmd        *exec.Cmd
	exited     chan struct{}
	spec       launchSpec
	lastGen    uint64
	lastErr    error

	termGrace time.Duration
	killGrace time.Duration
}

func newLifecycle() *lifecycle {
	return &lifecycle{state: lifecycleStopped, termGrace: defaultTermGrace, killGrace: defaultKillGrace}
}

func (l *lifecycle) State() lifecycleState {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.state
}

// Running reports whether generation is the current, live run. Asynchronous
// work tagged with an older generation must be discarded.
func (l *lifecycle) Running(generation uint64) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.state == lifecycleRunning && l.generation == generation
}

// PID is the runtime's process id, or 0 when none runs.
func (l *lifecycle) PID() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.cmd == nil || l.cmd.Process == nil {
		return 0
	}
	return l.cmd.Process.Pid
}

// LastExit is the generation and result of the most recent finished run.
func (l *lifecycle) LastExit() (uint64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.lastGen, l.lastErr
}

// busy returns the error that explains why no new operation may begin.
func (l *lifecycle) busyLocked() error {
	if l.state == lifecycleIncomplete {
		return errShutdownIncomplete
	}
	return errLifecycleBusy
}

// Start reconciles ownership left by earlier runs, then runs spec.
func (l *lifecycle) Start(spec launchSpec) (uint64, error) {
	l.mu.Lock()
	if l.state != lifecycleStopped {
		err := l.busyLocked()
		l.mu.Unlock()
		return 0, err
	}
	l.state = lifecycleStarting
	l.mu.Unlock()

	fail := func(state lifecycleState, err error) (uint64, error) {
		l.mu.Lock()
		l.state = state
		l.mu.Unlock()
		return 0, err
	}

	// Starting requires confirmed absence of an owned process tree. Uncertain
	// ownership blocks the start and nothing unverified is signalled.
	if spec.LedgerPath != "" {
		if _, err := sweepLedger(spec.LedgerPath); err != nil {
			return fail(lifecycleStopped, err)
		}
	}

	cmd := exec.Command(spec.Binary, spec.Args...)
	cmd.Env = spec.Env
	cmd.Dir = spec.Dir
	// Own process group: the runtime can be signalled as a unit, and a
	// terminal interrupt aimed at the launcher does not reach it.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if spec.Attach != nil {
		if err := spec.Attach(cmd); err != nil {
			return fail(lifecycleStopped, err)
		}
	}
	if err := cmd.Start(); err != nil {
		return fail(lifecycleStopped, fmt.Errorf("failed to start the runtime: %w", err))
	}

	exited := make(chan struct{})
	l.mu.Lock()
	l.generation++
	gen := l.generation
	l.cmd, l.exited, l.spec = cmd, exited, spec
	l.state = lifecycleRunning
	l.mu.Unlock()

	go l.watch(gen, cmd, exited)
	return gen, nil
}

// watch notices an exit nobody asked for. Everything the runtime owned must
// still be cleaned up: a parent exit does not prove its descendants stopped.
func (l *lifecycle) watch(gen uint64, cmd *exec.Cmd, exited chan struct{}) {
	waitErr := cmd.Wait()
	close(exited)

	l.mu.Lock()
	if l.generation != gen || l.state != lifecycleRunning {
		l.mu.Unlock() // Stop owns the finish
		return
	}
	l.state = lifecycleStopping
	spec := l.spec
	l.mu.Unlock()

	cleanupErr := l.reapOwned(cmd.Process.Pid, spec)
	if waitErr == nil {
		waitErr = errors.New("the runtime exited without being asked to stop")
	}
	l.finish(gen, spec, waitErr, cleanupErr)
}

// finish records the end of a run. A failed cleanup leaves the controller
// blocked in the incomplete state.
func (l *lifecycle) finish(gen uint64, spec launchSpec, runErr, cleanupErr error) {
	l.mu.Lock()
	if cleanupErr != nil {
		l.state = lifecycleIncomplete
		l.lastErr = fmt.Errorf("%w: %v", errShutdownIncomplete, cleanupErr)
	} else {
		l.state = lifecycleStopped
		l.lastErr = runErr
		l.cmd, l.exited = nil, nil
	}
	l.lastGen = gen
	reported := l.lastErr
	l.mu.Unlock()
	if spec.OnExit != nil {
		spec.OnExit(gen, reported)
	}
}

// Stop runs the bounded stop sequence and returns only once the runtime and
// every owned group are confirmed gone. Calling it again after
// errShutdownIncomplete retries the cleanup.
func (l *lifecycle) Stop() error {
	l.mu.Lock()
	switch l.state {
	case lifecycleStopped:
		l.mu.Unlock()
		return errNotRunning
	case lifecycleStarting, lifecycleStopping:
		l.mu.Unlock()
		return errLifecycleBusy
	}
	retry := l.state == lifecycleIncomplete
	l.state = lifecycleStopping
	cmd, exited, spec, gen := l.cmd, l.exited, l.spec, l.generation
	l.mu.Unlock()

	var stopErr error
	if !retry {
		stopErr = l.stopRuntime(cmd, exited)
	}
	pid := 0
	if cmd != nil && cmd.Process != nil {
		pid = cmd.Process.Pid
	}
	cleanupErr := l.reapOwned(pid, spec)
	if stopErr != nil && cleanupErr == nil {
		cleanupErr = stopErr
	}
	l.finish(gen, spec, nil, cleanupErr)
	if cleanupErr != nil {
		return fmt.Errorf("%w: %v", errShutdownIncomplete, cleanupErr)
	}
	return nil
}

// stopRuntime asks for a graceful exit, then forces the runtime's group.
func (l *lifecycle) stopRuntime(cmd *exec.Cmd, exited chan struct{}) error {
	select {
	case <-exited:
		return nil
	default:
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
	timer := time.NewTimer(l.termGrace)
	defer timer.Stop()
	select {
	case <-exited:
		return nil
	case <-timer.C:
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	_ = cmd.Process.Kill()
	reap := time.NewTimer(l.killGrace)
	defer reap.Stop()
	select {
	case <-exited:
		return nil
	case <-reap.C:
		return errors.New("the runtime did not exit after being killed")
	}
}

// reapOwned empties the runtime's own process group and every recorded backend
// group. It reports failure when any group cannot be proven empty.
func (l *lifecycle) reapOwned(runtimePID int, spec launchSpec) error {
	var problems []string
	if runtimePID > 0 && workerledger.GroupAlive(runtimePID) {
		_ = syscall.Kill(-runtimePID, syscall.SIGKILL)
		deadline := time.Now().Add(l.killGrace)
		for workerledger.GroupAlive(runtimePID) {
			if time.Now().After(deadline) {
				problems = append(problems, fmt.Sprintf("runtime process group %d still has live members", runtimePID))
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	if spec.LedgerPath != "" {
		if _, err := sweepLedger(spec.LedgerPath); err != nil {
			problems = append(problems, err.Error())
		}
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

// sweepLedger kills the backend groups a ledger records and drops records whose
// group is verified gone. A record it cannot resolve (identity unverifiable,
// group that survives a kill, unreadable ledger) is kept, nothing unverified is
// signalled, and an error is returned: the caller must not start another role.
// The identity rules are those the worker uses to write the ledger.
func sweepLedger(path string) (int, error) {
	entries, err := workerledger.ReadFile(path)
	if err != nil {
		return 0, err
	}
	if len(entries) == 0 {
		return 0, nil
	}
	killed := 0
	var remaining []workerledger.Entry
	var problems []string
	for _, e := range entries {
		switch workerledger.Classify(e) {
		case workerledger.Gone, workerledger.Foreign:
		case workerledger.Uncertain:
			remaining = append(remaining, e)
			problems = append(problems, fmt.Sprintf("%s (group %d: ownership cannot be verified)", e.Key, e.PID))
		case workerledger.Owned:
			if err := workerledger.Reap(e); err != nil {
				remaining = append(remaining, e)
				problems = append(problems, fmt.Sprintf("%s (%v)", e.Key, err))
				continue
			}
			killed++
		}
	}
	if err := workerledger.WriteFile(path, remaining); err != nil {
		problems = append(problems, fmt.Sprintf("cannot update the process ledger: %v", err))
	}
	if len(problems) > 0 {
		return killed, fmt.Errorf("backend ownership is unresolved: %s", strings.Join(problems, "; "))
	}
	return killed, nil
}
