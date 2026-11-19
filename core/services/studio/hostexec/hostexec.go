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
//
// The actor and the view of a call come from the chat's resolved access
// (Options.Actor and Options.View). A chat whose agent works in one folder
// never runs a command here: no workspace rule can confine a process that
// reads what the server can read.
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
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/bgjob"
	"github.com/mudler/LocalAI/core/services/studio/sandbox"
	"github.com/mudler/LocalAI/core/services/studio/sandbox/host"
	"github.com/mudler/LocalAI/core/services/studio/sandbox/runner"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/workfiles"
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
	"The shell starts in $WORK, this chat's work folder (the user's home when the chat has none yet), inside a scratch folder: the same files as /workspace/home of the file tools; " +
	"a shared folder is ../shared-<name> (/workspace/shared-<name> of the file tools). The /workspace paths do not exist here: use $WORK or relative paths. " + runner.QuoteRule + " " +
	"What it changes in the home and the writable shared folders is saved to the user's files; anything else is lost. " +
	"It can read what the server can read and reach the internet and the local network. " +
	"Output is cut at 64 KiB. A call stops after timeoutSeconds (default 120, at most 1800). " +
	"For a command that runs for minutes or does not end (a server, a watcher), use job start. " + workfiles.ToolHint

// Options configures a Runner.
type Options struct {
	// Store is the workspace of the chats.
	Store workspace.Store
	// StateDir holds the scratch folders. What an earlier run left there is
	// removed.
	StateDir string
	// Actor returns the actor that reads and commits for a user in a chat:
	// the actor of the chat's resolved access, asked at every call. A fenced
	// actor (a chat whose agent has a folder) is refused: a command on this
	// machine can read what the server can read, so no fence confines it.
	Actor func(ctx context.Context, user, chat string) (workspace.Actor, error)
	// View returns the view of the scratch folder of a: the view of the
	// chat's resolved access (the home at home, the shared folders at
	// shared-<name>) with the skill layers read only, at the names the file
	// tools use, so a skill's scripts run in place.
	View func(ctx context.Context, a workspace.Actor) (workspace.View, error)
	// Audit keeps a line per call. Nil: memory.
	Audit AuditStore
	// Log gets the line of every command, with the prefix [host-exec].
	Log *slog.Logger
	// PerUser and Total bound the scratch folders (default 4 and 16), so at
	// most PerUser commands of one user run at once.
	PerUser, Total int
	// WorkDir returns the guest path of the chat's work folder for a command
	// of a (see studio.Service.CommandWorkDir): the shell starts there and gets
	// it as $WORK. With create the folder is made first. "" means the chat has
	// no folder yet, and the shell starts in the home with no $WORK. Nil: the
	// same, for every chat.
	WorkDir func(ctx context.Context, a workspace.Actor, create bool) string
	// Geteuid is os.Geteuid; tests set it.
	Geteuid func() int
	// Instance is the node that runs the background jobs: it is recorded in
	// each job and in its stub, and the carrier reaches the job there.
	Instance string
	// JobOptions tunes the registry of the background jobs. The hooks
	// (Check, Changed, Audit) are set by Runner itself and by OnJob.
	JobOptions bgjob.Options
}

// Runner runs the built-in bash of the chats.
type Runner struct {
	o      Options
	m      *runner.Manager
	reason string
	jobs   *bgjob.Registry

	hookMu  sync.RWMutex
	check   func(context.Context, bgjob.Info) bgjob.EndReason
	changed func(bgjob.Info)
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
	if o.Actor == nil || o.View == nil {
		r.reason = "the chats do not say whose files a command may use"
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
		Actor:        r.actor,
		View:         o.View,
		Title:        "Command on this machine: ",
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
	jo := o.JobOptions
	jo.Instance, jo.Provider = o.Instance, "builtin"
	jo.Check = r.jobCheck
	jo.Changed = r.jobChanged
	jo.Audit = r.jobAudit
	r.jobs = bgjob.New(jo)
	r.jobs.Start(context.Background())
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

// ErrScoped means the chat's agent works in one folder. A command on this
// machine can read every file LocalAI can read, so nothing keeps it in that
// folder: such a chat runs commands only in the Sandbox add-on.
var ErrScoped = errors.New("hostexec: this agent works in one folder, and a command on this machine cannot be kept inside it")

// ScopedText is the result of a call for a chat whose agent works in one
// folder.
const ScopedText = "Not run: this agent works in one folder, and a command on this machine could read files outside it. Commands for this agent run only in an add-on that isolates them."

// Run runs one call of bash for a in its chat's scratch folder.
func (r *Runner) Run(ctx context.Context, a workspace.Actor, args json.RawMessage) (string, error) {
	return r.run(ctx, a, args, false)
}

// RunReadOnly runs one call of bash that must not change the user's files: it
// runs in a scratch folder of its own whose folders are read-only, so what the
// command writes is refused and no change set is made. A wait uses it for its
// checks.
func (r *Runner) RunReadOnly(ctx context.Context, a workspace.Actor, args json.RawMessage) (string, error) {
	return r.run(ctx, a, args, true)
}

func (r *Runner) run(ctx context.Context, a workspace.Actor, args json.RawMessage, readOnly bool) (string, error) {
	if r.Unavailable() != "" {
		return "This server cannot run commands: " + r.Unavailable() + ".", nil
	}
	if workspace.HasFence(a) {
		return ScopedText, nil
	}
	op := make([]byte, 8)
	_, _ = rand.Read(op)
	// A check that must change no file starts in the folder only if it exists.
	out, err := r.m.Run(ctx, runner.Request{User: a.Principal, Conversation: a.ChatID, Operation: "hx" + hex.EncodeToString(op), Tool: "bash", Args: args,
		Call: sandbox.CallOptions{ReadOnly: readOnly, WorkDir: r.workDir(ctx, a, !readOnly)}})
	if errors.Is(err, ErrScoped) {
		return ScopedText, nil
	}
	return out, err
}

// Close removes every scratch folder. What the commands did is in the files.
func (r *Runner) Close(ctx context.Context) error {
	if r == nil || r.m == nil {
		return nil
	}
	// The jobs end first: each saves what it wrote while its sandbox still
	// exists.
	r.jobs.Close(ctx)
	return r.m.Close(ctx)
}

// actor is the chat's actor of this call. It is resolved again at every call,
// so a chat whose agent got a folder since the tool was built is refused too.
func (r *Runner) actor(ctx context.Context, user, chat string) (workspace.Actor, error) {
	a, err := r.o.Actor(ctx, user, chat)
	if err != nil {
		return workspace.Actor{}, err
	}
	if workspace.HasFence(a) {
		return workspace.Actor{}, ErrScoped
	}
	return a, nil
}

// workDir is the work folder of the chat of a, or "".
func (r *Runner) workDir(ctx context.Context, a workspace.Actor, create bool) string {
	if r.o.WorkDir == nil {
		return ""
	}
	return r.o.WorkDir(ctx, a, create)
}

// audit records one call.
func (r *Runner) audit(ctx context.Context, res runner.BashResult) {
	req, _ := runner.RequestFrom(ctx)
	ev := Event{Kind: KindRun, User: req.User, Chat: req.Conversation, Command: capAudit(res.Command), Exit: res.Exit, DurationMS: res.Duration.Milliseconds(),
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

// Prune removes the audit lines older than before.
func (r *Runner) Prune(ctx context.Context, before time.Time) (int64, error) {
	return r.o.Audit.Prune(ctx, before)
}

// capAudit cuts a command or an input to what an audit line keeps.
func capAudit(s string) string {
	if len(s) > maxAuditCommand {
		return strings.ToValidUTF8(s[:maxAuditCommand], "") + "..."
	}
	return s
}
