package model

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mudler/LocalAI/pkg/grpc"
	"github.com/mudler/LocalAI/pkg/sanitize"
	process "github.com/mudler/go-processmanager"
	"github.com/mudler/xlog"
	"github.com/phayes/freeport"
)

// DefaultStartCheckTimeout is how long a start check waits for a new backend
// process to answer a health check. It is the readiness window a model load
// gives a backend with the default loader options (20 attempts, 2 s apart).
const DefaultStartCheckTimeout = 40 * time.Second

// startCheckOutputBytes bounds the process output a failed start check keeps.
// The cause has to fit in an error message, an operation status and a reply
// of a worker, so only the end of the output is kept.
const startCheckOutputBytes = 512

const startCheckPollInterval = 200 * time.Millisecond

// StartCheckError is a backend build that did not start: its process exited,
// or it did not answer a health check in time. Output is the end of what the
// process wrote, bounded and without control characters.
type StartCheckError struct {
	// Reason says what went wrong, in words, without the output.
	Reason string
	// Output is the end of the stderr of the process (stdout when stderr is
	// empty). It can be empty.
	Output string
}

// Error is safe to show and to store: the text reaches the operation status,
// /api/operations, the run and the inbox, so paths are cut to their file name
// and what looks like a credential is removed.
func (e *StartCheckError) Error() string {
	if e.Output == "" {
		return sanitize.Text(e.Reason, 0)
	}
	return sanitize.Text(e.Reason+": "+e.Output, 0)
}

// CheckBackendStart starts the backend at runFile the way a model load starts
// it (the same process start, on a free local port of its own), waits until it
// answers a gRPC health check, and stops it again. It loads no model: a check
// that passes proves that the build starts and listens, not that a model
// loads on the GPU.
//
// The process is stopped on every path, also when ctx ends, before the call
// returns. It never touches a running backend: it has its own process, port
// and state directory, and processes that run the build that was replaced
// keep their open files. A timeout of zero or less means
// DefaultStartCheckTimeout.
func (ml *ModelLoader) CheckBackendStart(ctx context.Context, id, runFile string, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = DefaultStartCheckTimeout
	}
	port, err := freeport.GetFreePort()
	if err != nil {
		return fmt.Errorf("allocating a port for the start check: %w", err)
	}
	address := fmt.Sprintf("127.0.0.1:%d", port)

	proc, err := ml.StartProcess(runFile, id, address, nil)
	if err != nil {
		if proc != nil {
			ml.CleanupProcessRuntime(proc)
		}
		return &StartCheckError{Reason: fmt.Sprintf("the backend process could not be started: %v", err)}
	}
	// A start check is not a loaded backend: the watchdog must not count it
	// for the LRU limit or pick it for an eviction.
	ml.untrackProcess(proc)
	defer ml.stopStartCheck(proc, id)

	client := grpc.NewClient(address, false, nil, false)
	defer grpc.CloseBackend(client)
	return waitBackendHealthy(ctx, proc, client, timeout)
}

// waitBackendHealthy polls the health check of the process until it answers,
// the process exits, the timeout ends or ctx ends.
func waitBackendHealthy(ctx context.Context, proc *process.Process, client grpc.Backend, timeout time.Duration) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	tick := time.NewTicker(startCheckPollInterval)
	defer tick.Stop()
	for {
		healthCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		ok, _ := client.HealthCheck(healthCtx)
		cancel()
		// An answer counts only while the process that was started is alive:
		// a process that lost the port to another listener exits at once, and
		// that listener's answer is not its own.
		if ok && !processExited(proc) {
			return nil
		}
		if processExited(proc) {
			return &StartCheckError{
				Reason: "the backend process exited before it answered a health check" + exitCodeSuffix(proc),
				Output: startCheckOutput(proc),
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return &StartCheckError{
				Reason: fmt.Sprintf("the backend process did not answer a health check within %s", timeout),
				Output: startCheckOutput(proc),
			}
		case <-proc.Done():
		case <-tick.C:
		}
	}
}

// stopStartCheck stops the process of a start check and frees its runtime.
// The output is read by the caller before this runs, because the cleanup
// removes the state directory that holds it.
func (ml *ModelLoader) stopStartCheck(proc *process.Process, id string) {
	if !processExited(proc) {
		ml.NoteIntentionalStop(proc)
		if err := proc.Stop(); err != nil && !processExited(proc) {
			xlog.Warn("Could not stop the start check process", "id", id, "error", err)
		}
	}
	ml.CleanupProcessRuntime(proc)
}

func processExited(proc *process.Process) bool {
	select {
	case <-proc.Done():
		return true
	default:
		return false
	}
}

func exitCodeSuffix(proc *process.Process) string {
	code, err := proc.ExitCode()
	if err != nil {
		return ""
	}
	return " (exit code " + strings.TrimSpace(code) + ")"
}

// startCheckOutput returns the end of the stderr of the process, or of its
// stdout when stderr is empty, bounded and sanitised.
func startCheckOutput(proc *process.Process) string {
	if out := tailOfFile(proc.StderrPath(), startCheckOutputBytes); out != "" {
		return out
	}
	return tailOfFile(proc.StdoutPath(), startCheckOutputBytes)
}

// tailOfFile reads at most maxBytes from the end of path, drops control
// characters (newlines become "; " so the text stays on one line), cuts paths
// to their file name, removes what looks like a credential and trims it. A
// file that cannot be read gives "".
func tailOfFile(path string, maxBytes int64) string {
	// #nosec G304 -- path is the output file of a process this package started.
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return ""
	}
	start := max(info.Size()-maxBytes, 0)
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return ""
	}
	raw, err := io.ReadAll(io.LimitReader(f, maxBytes))
	if err != nil && !errors.Is(err, io.EOF) {
		return ""
	}
	return sanitize.Text(sanitize.JoinLines(string(raw)), 0)
}
