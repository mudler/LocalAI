// Package runner keeps the sandboxes of the chats and runs bash in them. A
// sandbox belongs to one user and one conversation; every call commits what the
// code changed as one change set, so a sandbox holds no work that is not in
// the user's files. The Sandbox add-on uses it with its isolated backends and
// the chats' built-in bash with the host backend.
package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/sandbox"
)

// Limits of bash.
const (
	// MaxOutput is the most output bytes a call returns.
	MaxOutput = 64 << 10
	// DefaultTimeout bounds a call that names no timeout.
	DefaultTimeout = 2 * time.Minute
)

// GuestRoot is the working directory of the guest: the mounts are below it.
const GuestRoot = sandbox.DefaultDir

// ErrInvalid marks an argument the tool refuses.
var ErrInvalid = errors.New("invalid argument")

// ErrNoSandbox says the host cannot run sandboxes.
var ErrNoSandbox = errors.New("no sandbox on this host")

// Tool is one tool that runs in a sandbox.
type Tool struct {
	Name, Description string
	Schema            json.RawMessage
	Run               func(ctx context.Context, args json.RawMessage) (string, error)
}

// NoSandboxText is the result of a tool on a host without sandboxes.
const NoSandboxText = "This host cannot run commands."

// Acquire returns the chat's sandbox, creating or resuming it when needed,
// and a function that the tool calls when it is done. It returns ErrNoSandbox
// when the host cannot run sandboxes.
type Acquire func(ctx context.Context) (sandbox.Sandbox, func(), error)

// BashOptions shapes the bash tool.
type BashOptions struct {
	// Description is what the model is told. Empty: a description that says
	// bash runs in GuestRoot.
	Description string
	// MaxTimeout is the longest a call may run. It must stay below the
	// caller's own limit, with room to commit and answer.
	MaxTimeout time.Duration
	// Done, when set, is told about every call that ran a command, for an
	// audit. The request of the call is in ctx (RequestFrom).
	Done func(ctx context.Context, r BashResult)
}

// BashResult is what one bash call did.
type BashResult struct {
	Command  string
	Exit     int // -1 when it did not exit by itself (timeout, stop, error)
	Duration time.Duration
	TimedOut bool
}

// Bash returns the bash tool. Each call runs in the sandbox that acquire
// returns.
func Bash(acquire Acquire, o BashOptions) Tool {
	if o.MaxTimeout <= 0 {
		o.MaxTimeout = DefaultTimeout
	}
	maxSec := int(o.MaxTimeout / time.Second)
	if o.Description == "" {
		o.Description = fmt.Sprintf("Run a shell script with bash in %s. Output is cut at 64 KiB. A call stops after timeoutSeconds (default 120, at most %d).", GuestRoot, maxSec)
	}
	schema := fmt.Sprintf(`{"type":"object","properties":{"command":{"type":"string","description":"The script to run."},"timeoutSeconds":{"type":"integer","minimum":1,"maximum":%d}},"required":["command"]}`, maxSec)
	return Tool{
		Name: "bash", Description: o.Description, Schema: json.RawMessage(schema),
		Run: func(ctx context.Context, args json.RawMessage) (string, error) {
			sb, done, err := acquire(ctx)
			if errors.Is(err, ErrNoSandbox) {
				return NoSandboxText, nil
			}
			if err != nil {
				return "", err
			}
			defer done()
			return bash(ctx, sb, args, o)
		},
	}
}

// noNUL refuses a text argument that carries a NUL byte.
func noNUL(s, what string) error {
	if strings.ContainsRune(s, 0) {
		return fmt.Errorf("%w: the %s contains a NUL byte", ErrInvalid, what)
	}
	return nil
}

// capWriter keeps the first max bytes and counts the rest. It is safe for
// the two writers (stdout and stderr) of one process. When the cap is
// reached, full is called once, so that the process can be stopped.
type capWriter struct {
	mu    sync.Mutex
	max   int
	buf   bytes.Buffer
	extra int64
	full  func()
}

func (w *capWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	room := w.max - w.buf.Len()
	if room > len(p) {
		room = len(p)
	}
	if room > 0 {
		w.buf.Write(p[:room])
	}
	if w.extra == 0 && room < len(p) && w.full != nil {
		w.full()
	}
	w.extra += int64(len(p) - max(room, 0))
	return len(p), nil
}

// cut reports whether output was dropped at the cap.
func (w *capWriter) cut() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.extra > 0
}

func (w *capWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	s := strings.ToValidUTF8(w.buf.String(), "�")
	if w.extra > 0 {
		s += fmt.Sprintf("\n[output cut: %d more bytes]", w.extra)
	}
	return s
}

// run runs args in the guest and returns the output of stdout and stderr in
// one string, and the exit code. When the output reaches limit, run stops the
// process at once and returns the output with its cut marker, and no error.
func run(ctx context.Context, sb sandbox.Sandbox, args []string, stdin io.Reader, limit int) (string, int, error) {
	rctx, stop := context.WithCancel(ctx)
	defer stop()
	out := &capWriter{max: limit, full: stop}
	p, err := sb.Exec(rctx, sandbox.Cmd{Args: args, Stdin: stdin, Stdout: out, Stderr: out})
	if err != nil {
		return "", 0, err
	}
	code, err := p.Wait()
	if ctx.Err() == nil && rctx.Err() != nil && out.cut() {
		return out.String(), 0, nil // stopped by us at the cap
	}
	return out.String(), code, err
}

// bash runs one call. A timeout of the model is capped at max.
func bash(ctx context.Context, sb sandbox.Sandbox, raw json.RawMessage, o BashOptions) (string, error) {
	maxTimeout := o.MaxTimeout
	var a struct {
		Command        string `json:"command"`
		TimeoutSeconds int    `json:"timeoutSeconds"`
	}
	if err := json.Unmarshal(raw, &a); err != nil || strings.TrimSpace(a.Command) == "" {
		return "", errors.New("bash needs a command")
	}
	if err := noNUL(a.Command, "command"); err != nil {
		return "", err
	}
	timeout := DefaultTimeout
	if a.TimeoutSeconds > 0 {
		timeout = maxTimeout
		if a.TimeoutSeconds < int(maxTimeout/time.Second) {
			timeout = time.Duration(a.TimeoutSeconds) * time.Second
		}
	}
	tctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	start := time.Now()
	out, code, err := run(tctx, sb, []string{"bash", "-lc", a.Command}, nil, MaxOutput)
	if o.Done != nil {
		res := BashResult{Command: a.Command, Exit: code, Duration: time.Since(start), TimedOut: errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil}
		if err != nil {
			res.Exit = -1
		}
		o.Done(ctx, res)
	}
	switch {
	case ctx.Err() != nil:
		return "", ctx.Err() // the turn was stopped
	case errors.Is(err, context.DeadlineExceeded):
		return out + fmt.Sprintf("\n[stopped after %d seconds]", int(timeout.Seconds())), nil
	case err != nil:
		return "", err
	case code != 0:
		return out + fmt.Sprintf("\n[exit code %d]", code), nil
	}
	return out, nil
}
