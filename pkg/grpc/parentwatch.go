package grpc

import (
	"log"
	"os"
	"runtime"
	"strings"
	"time"
)

// Backend worker processes (the per-model gRPC servers LocalAI spawns) are
// deliberately placed in their own process group by the process manager so
// LocalAI's graceful shutdown can signal the whole group. That graceful path
// (SIGTERM -> grace -> SIGKILL, driven by pkg/signals + pkg/model) only runs
// when LocalAI itself receives a catchable signal and lives long enough to run
// its handlers. If LocalAI is SIGKILLed (e.g. a supervising process's
// graceful-shutdown grace period elapses first), that teardown never runs and
// this backend would be reparented to init and linger, holding VRAM and its
// listen port.
//
// The watcher below is a best-effort backstop for exactly that case: it does
// NOT replace the graceful teardown, it only covers the "parent vanished
// without cleaning up" path. It works by detecting reparenting: when the
// process that spawned this backend dies, the kernel reparents us to the
// nearest sub-reaper or to init (PID 1), so getppid() stops matching the value
// we captured at startup. This getppid() approach is portable across
// Linux/macOS (unlike Linux-only PR_SET_PDEATHSIG), which is why it's used
// here rather than a kernel parent-death signal.
const (
	// EnvBackendParentWatch toggles the parent-death watcher. It is enabled by
	// default; set it to a falsey value ("false", "0", "no", "off") to disable
	// (e.g. when running a backend standalone for debugging under a shell whose
	// lifetime shouldn't govern the backend).
	EnvBackendParentWatch = "LOCALAI_BACKEND_PARENT_WATCH"
	// EnvBackendParentWatchInterval overrides the poll interval as a Go
	// duration string (e.g. "500ms"). Defaults to defaultParentWatchInterval.
	EnvBackendParentWatchInterval = "LOCALAI_BACKEND_PARENT_WATCH_INTERVAL"

	defaultParentWatchInterval = 2 * time.Second
)

// parentWatchEnabled reports whether the watcher should run in this process.
func parentWatchEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(EnvBackendParentWatch))) {
	case "false", "0", "no", "off":
		return false
	}
	// Windows does not reparent orphans to a well-known init PID, so the
	// getppid() heuristic used here doesn't apply there.
	return runtime.GOOS != "windows"
}

// parentWatchInterval returns the configured poll interval, or the default.
func parentWatchInterval() time.Duration {
	if v := os.Getenv(EnvBackendParentWatchInterval); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return defaultParentWatchInterval
}

// parentDied reports whether this process has been reparented away from the
// parent it had when the watcher started. Reparenting is the standard POSIX
// signal that the original parent (here, the LocalAI process that spawned this
// backend) has exited: the orphan is handed to the nearest sub-reaper or to
// init (PID 1), so getppid() no longer matches the value captured at startup.
func parentDied(origPPID int) bool {
	ppid := os.Getppid()
	return ppid != origPPID || ppid == 1
}

// watchParentDeath polls until parentDied reports the original parent is gone,
// then invokes onDeath. It blocks, so run it in its own goroutine.
func watchParentDeath(origPPID int, interval time.Duration, onDeath func()) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		if parentDied(origPPID) {
			onDeath()
			return
		}
	}
}

// startParentDeathWatcher installs the best-effort safety net described above
// on the calling backend process. It is a no-op when disabled or on platforms
// where the mechanism doesn't apply. This is a backstop alongside — never a
// replacement for — LocalAI's graceful SIGTERM->grace->SIGKILL teardown.
func startParentDeathWatcher() {
	if !parentWatchEnabled() {
		return
	}
	origPPID := os.Getppid()
	// A parent of 1 at startup means we were already orphaned (or launched
	// directly under init) — there's no original parent to watch for.
	if origPPID <= 1 {
		return
	}
	interval := parentWatchInterval()
	go watchParentDeath(origPPID, interval, func() {
		log.Printf("backend parent process (pid %d) exited without stopping this backend; self-terminating to avoid orphaning", origPPID)
		os.Exit(1)
	})
}
