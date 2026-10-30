// Package hostexec runs the chats' built-in bash directly on this machine,
// with no isolation. It is what a chat gets when no Sandbox add-on serves bash.
//
// It reuses the Sandbox's host backend and runner (core/services/studio/sandbox):
// each chat gets a scratch folder with the user's files (home and shared
// folders) read from the workspace, every call commits what the command
// changed as one change set, and a folder whose files somebody else changed is
// read again before the next call. Commands run as the user LocalAI runs as
// (never root), with a fixed environment (PATH, HOME, LANG and TZ only), a
// timeout, a cap on the output and a kill of the whole process group. They
// have the network of the server. Every call is audited.
package hostexec

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/sandbox"
	"github.com/mudler/LocalAI/core/services/studio/sandbox/host"
	"github.com/mudler/LocalAI/core/services/studio/sandbox/runner"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// Limits of a call.
const (
	// MaxTimeout is the longest a call may run.
	MaxTimeout = 30 * time.Minute
	// maxAuditCommand bounds the command an audit line keeps.
	maxAuditCommand = 4096
)

// The modes of the setting chats_host_exec.
const (
	ModeOff = "off" // nobody gets the built-in bash
	ModeAsk = "ask" // administrators, and each command waits for their yes
	ModeOn  = "on"  // every user, with no card
)

// ValidMode reports whether m is a mode or empty (the default).
func ValidMode(m string) bool { return m == "" || m == ModeOff || m == ModeAsk || m == ModeOn }

// Description is what the model is told about the built-in bash.
const Description = "Run a shell script with bash directly on this machine, with no isolation. " +
	"It starts in a scratch folder that holds the user's files: home/ is their home and shared-<name>/ a shared folder " +
	"(the same files as /workspace/home and /workspace/shared-<name> of the file tools); what it changes there is saved to their files. " +
	"It can read what the server can read and reach the internet and the local network. " +
	"Output is cut at 64 KiB. A call stops after timeoutSeconds (default 120, at most 1800)."

// Options configures a Runner.
type Options struct {
	// Store is the workspace of the chats.
	Store workspace.Store
	// StateDir holds the scratch folders. What an earlier run left there is
	// removed.
	StateDir string
	// Shared lists the shared folders of a user, mounted next to the home.
	// Nil: the home only.
	Shared func(ctx context.Context, a workspace.Actor) ([]session.SharedRoot, error)
	// Actor returns the actor that reads and commits for a user in a chat.
	Actor func(user, chat string) workspace.Actor
	// Audit keeps a line per call. Nil: memory.
	Audit AuditStore
	// Log gets the line of every command, with the prefix [host-exec].
	Log *slog.Logger
	// PerUser and Total bound the scratch folders (default 4 and 16), so at
	// most PerUser commands of one user run at once.
	PerUser, Total int
	// Geteuid is os.Geteuid; tests set it.
	Geteuid func() int
}

// Runner runs the built-in bash of the chats.
type Runner struct {
	o      Options
	m      *runner.Manager
	reason string
}

// New returns a Runner. It never fails: a host that cannot run commands (LocalAI
// runs as root, the state folder is not usable) gets a Runner whose
// Unavailable says why.
func New(o Options) *Runner {
	if o.Audit == nil {
		o.Audit, _ = NewJSONAudit("")
	}
	if o.Log == nil {
		o.Log = slog.Default()
	}
	r := &Runner{o: o}
	if o.Geteuid != nil && o.Geteuid() == 0 {
		r.reason = "LocalAI runs as root, and commands must not run on the server as root"
		return r
	}
	if o.Store == nil || o.StateDir == "" {
		r.reason = "the server keeps no files"
		return r
	}
	b, err := host.New(host.Options{Root: o.StateDir, Log: func(principal, command string) {
		o.Log.Warn("[host-exec] command", "user", principal, "command", command)
	}})
	if err != nil {
		if errors.Is(err, sandbox.ErrUnsupported) {
			r.reason = "LocalAI runs as root, and commands must not run on the server as root"
		} else {
			r.reason = "the scratch folder cannot be made: " + err.Error()
		}
		return r
	}
	m, err := runner.New(runner.Options{
		Backend:  func() (sandbox.Backend, error) { return b, nil },
		Store:    o.Store,
		StateDir: o.StateDir,
		// The backend removed what an earlier run left; it keeps its own folders.
		KeepStateDir: true,
		PerUser:      o.PerUser,
		Total:        o.Total,
		Actor:        o.Actor,
		Title:        "Command on this machine: ",
		Mounts:       r.mounts,
		Tools: func(acquire runner.Acquire) []runner.Tool {
			return []runner.Tool{runner.Bash(acquire, runner.BashOptions{Description: Description, MaxTimeout: MaxTimeout, Done: r.audit})}
		},
	})
	if err != nil {
		r.reason = err.Error()
		return r
	}
	m.Start(context.Background())
	r.m = m
	return r
}

// Unavailable says why this server cannot run the built-in bash, or "".
func (r *Runner) Unavailable() string {
	if r == nil {
		return "commands on this machine are not set up"
	}
	return r.reason
}

// Tool returns the built-in bash of a chat: the user and the chat come from a,
// never from the model.
func (r *Runner) Tool(a workspace.Actor) session.Tool {
	def := runner.Bash(nil, runner.BashOptions{Description: Description, MaxTimeout: MaxTimeout})
	return session.Tool{Name: def.Name, Description: def.Description, Schema: def.Schema,
		Run: func(ctx context.Context, args json.RawMessage) (string, error) { return r.Run(ctx, a, args) }}
}

// Run runs one call of bash for a in its chat's scratch folder.
func (r *Runner) Run(ctx context.Context, a workspace.Actor, args json.RawMessage) (string, error) {
	if r.Unavailable() != "" {
		return "This server cannot run commands: " + r.Unavailable() + ".", nil
	}
	op := make([]byte, 8)
	_, _ = rand.Read(op)
	return r.m.Run(ctx, runner.Request{User: a.Principal, Conversation: a.ChatID, Operation: "hx" + hex.EncodeToString(op), Tool: "bash", Args: args})
}

// Close removes every scratch folder. What the commands did is in the files.
func (r *Runner) Close(ctx context.Context) error {
	if r == nil || r.m == nil {
		return nil
	}
	return r.m.Close(ctx)
}

// mounts is the view of a scratch folder: the home, writable, at home, and the
// shared folders of the user at shared-<name>, with the names the chat's file
// tools use.
func (r *Runner) mounts(ctx context.Context, a workspace.Actor) ([]workspace.MountRequest, error) {
	var shared []session.SharedRoot
	if r.o.Shared != nil {
		var err error
		if shared, err = r.o.Shared(ctx, a); err != nil {
			return nil, err
		}
	}
	v, err := session.BuildView(ctx, r.o.Store, a, shared, slog.New(slog.DiscardHandler))
	if err != nil {
		return nil, err
	}
	out := make([]workspace.MountRequest, 0, len(v.Mounts))
	for _, m := range v.Mounts {
		out = append(out, workspace.MountRequest{Path: m.Path, At: m.At, Mode: m.Mode})
	}
	return out, nil
}

// audit records one call.
func (r *Runner) audit(ctx context.Context, res runner.BashResult) {
	req, _ := runner.RequestFrom(ctx)
	cmd := res.Command
	if len(cmd) > maxAuditCommand {
		cmd = strings.ToValidUTF8(cmd[:maxAuditCommand], "") + "..."
	}
	ev := Event{User: req.User, Chat: req.Conversation, Command: cmd, Exit: res.Exit, DurationMS: res.Duration.Milliseconds(),
		TimedOut: res.TimedOut, At: time.Now().UTC()}
	r.o.Log.Warn("[host-exec] done", "user", ev.User, "chat", ev.Chat, "exit", ev.Exit, "ms", ev.DurationMS, "timed_out", ev.TimedOut)
	if err := r.o.Audit.Add(context.WithoutCancel(ctx), ev); err != nil {
		r.o.Log.Error("[host-exec] the audit line was not saved", "error", fmt.Sprint(err))
	}
}

// Events returns the newest lines of the audit, newest first.
func (r *Runner) Events(ctx context.Context, limit int) ([]Event, error) {
	return r.o.Audit.Events(ctx, limit)
}
