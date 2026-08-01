package chat

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/pkg/httpclient"
)

// ErrDeclined means no server was started, either because the session is not
// interactive or because the user said no.
var ErrDeclined = errors.New("no server started")

// errServerExited means the process we spawned died before it ever reported
// ready, so there is no point in polling out the rest of the budget.
var errServerExited = errors.New("the LocalAI server exited before it became ready")

const (
	// defaultReadyTimeout bounds the wait for a freshly spawned server. A cold
	// start probes hardware and may pull a backend, so the budget is generous.
	defaultReadyTimeout = 2 * time.Minute
	// readyPollInterval is how long to wait between readiness polls.
	readyPollInterval = 500 * time.Millisecond
	// readyProbeTimeout bounds a single readiness request, so one connection
	// that hangs cannot swallow the whole budget.
	readyProbeTimeout = 5 * time.Second
	// shutdownGrace is how long a server we started gets to unload models and
	// stop its backends after SIGINT before it is killed outright.
	shutdownGrace = 10 * time.Second
)

// Confirmer asks a yes/no question. Nil means the session is not interactive.
type Confirmer func(question string) (bool, error)

// StartOptions configures OfferToStart.
type StartOptions struct {
	// Endpoint is the address the user expected a server on, used in the
	// question and polled for readiness. This is the endpoint root, not the
	// /v1 API base URL: readiness is served at the root.
	Endpoint string
	// Confirm asks whether to start a server. Nil means never start.
	Confirm Confirmer
	// Stderr receives the child's output.
	Stderr io.Writer
	// Executable overrides the binary to run. Empty means os.Executable().
	Executable string
	// ReadyTimeout bounds the wait for readiness. Zero means defaultReadyTimeout.
	ReadyTimeout time.Duration
}

// StartedServer is a server this process started and is responsible for.
type StartedServer struct {
	cmd *exec.Cmd
	// exited is closed once the child has been reaped. One background waiter
	// owns cmd.Wait: it may only be called once, and it is what closes the
	// pipes exec created for Stdout/Stderr and joins the goroutines copying
	// them, so calling os.Process.Wait directly instead would leak both.
	exited chan struct{}
	// waitErr is the child's exit status. It is written before exited is
	// closed and must only be read after that channel is observed closed.
	waitErr error

	stopOnce sync.Once
}

// OfferToStart asks whether to start a LocalAI server and, if allowed, spawns
// one and waits for it to report ready.
//
// A child process rather than an in-process boot: RunCMD.Run installs its own
// signal handling and blocks until shutdown, so re-entering it from a chat
// session would entangle two lifecycles in one process.
func OfferToStart(ctx context.Context, opts StartOptions) (*StartedServer, error) {
	if opts.Confirm == nil {
		// Not interactive. Spawning a server nobody asked for is the one thing
		// this function must never do: in CI, in a pipeline, or under a
		// supervisor there is no one to see it or shut it down.
		return nil, ErrDeclined
	}
	ok, err := opts.Confirm(fmt.Sprintf("No LocalAI server at %s. Start one now?", opts.Endpoint))
	if err != nil {
		return nil, fmt.Errorf("asking whether to start a server: %w", err)
	}
	if !ok {
		return nil, ErrDeclined
	}

	bin := opts.Executable
	if bin == "" {
		if bin, err = os.Executable(); err != nil {
			return nil, fmt.Errorf("locating the local-ai binary: %w", err)
		}
	}

	cmd := exec.Command(bin, "run")
	// Stdin is left nil, so the child gets /dev/null: it is a background
	// server, and sharing the terminal would have it stealing keystrokes from
	// the chat REPL.
	cmd.Stdout = opts.Stderr // the child's logs are diagnostics, not chat output
	cmd.Stderr = opts.Stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting a LocalAI server with %s: %w", bin, err)
	}

	s := &StartedServer{cmd: cmd, exited: make(chan struct{})}
	go func() {
		s.waitErr = cmd.Wait()
		close(s.exited)
	}()

	timeout := opts.ReadyTimeout
	if timeout <= 0 {
		timeout = defaultReadyTimeout
	}
	if err := waitReady(ctx, opts.Endpoint, timeout, s.exited); err != nil {
		if errors.Is(err, errServerExited) && s.waitErr != nil {
			// Safe to read: errServerExited is only returned once exited has
			// been observed closed, which happens after waitErr is written.
			err = fmt.Errorf("%w: %w", err, s.waitErr)
		}
		s.Stop()
		return nil, fmt.Errorf("%w. Run 'local-ai run' in another terminal to see why it did not come up", err)
	}
	return s, nil
}

// Stop terminates the server this process started, giving it a chance to shut
// down cleanly first. It is safe to call on a nil or never-started server, and
// safe to call more than once.
func (s *StartedServer) Stop() {
	if s == nil || s.cmd == nil || s.cmd.Process == nil {
		return
	}
	s.stopOnce.Do(func() {
		// SIGINT rather than SIGKILL: local-ai run installs its own handler and
		// needs it to unload models and stop backend subprocesses. Killing it
		// outright would strand those children.
		_ = s.cmd.Process.Signal(os.Interrupt)

		select {
		case <-s.exited:
		case <-time.After(shutdownGrace):
			// It ignored the interrupt or wedged on the way down. The user is
			// waiting on their shell prompt, so stop being polite.
			_ = s.cmd.Process.Kill()
		}
	})
}

// waitReady polls the endpoint's /readyz until the server reports ready, the
// budget expires, the caller gives up, or exited signals that the process we
// are waiting on is gone. A nil exited channel means there is no process to
// watch.
//
// Readiness lives on the endpoint ROOT, not under the /v1 API base URL, and it
// answers 503 for as long as startup is still in progress.
func waitReady(ctx context.Context, endpoint string, timeout time.Duration, exited <-chan struct{}) error {
	url := strings.TrimSuffix(endpoint, "/") + "/readyz"

	// A real deadline rather than context.WithCancel plus a timer: the latter
	// expires as context.Canceled, which every classifier here reads as "the
	// caller gave up" rather than "the endpoint never answered".
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	client := httpclient.NewWithTimeout(readyProbeTimeout)
	ticker := time.NewTicker(readyPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-exited:
			return errServerExited
		case <-waitCtx.Done():
			// Distinguish our budget from the caller's: only ours is advice
			// about the server.
			if err := ctx.Err(); err != nil {
				return err
			}
			return fmt.Errorf("the LocalAI server did not become ready within %s", timeout)
		case <-ticker.C:
		}

		req, err := http.NewRequestWithContext(waitCtx, http.MethodGet, url, nil)
		if err != nil {
			return fmt.Errorf("building the readiness request for %s: %w", url, err)
		}
		resp, err := client.Do(req)
		if err != nil {
			continue // nothing listening yet
		}
		// Drain before closing so the next poll can reuse the connection
		// instead of opening a socket every 500ms for two minutes.
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			return nil
		}
		// Anything else means startup is still in progress; keep polling.
	}
}
