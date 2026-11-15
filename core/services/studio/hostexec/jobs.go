package hostexec

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/bgjob"
	"github.com/mudler/LocalAI/core/services/studio/sandbox"
	"github.com/mudler/LocalAI/core/services/studio/sandbox/runner"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// Background jobs of the built-in bash. A job is a command that outlives the
// call that started it, in the scratch folder of its chat: the runner counts
// it on the entry, so the folder is not evicted, suspended, refreshed or
// expired under it. It has the fixed environment and the group kill of every
// built-in command, and its output goes to a ring (bgjob), not to the call.
// What it wrote reaches the user's files at the next checkpoint of the entry:
// a foreground bash call, and a last one when the job ends.

// ErrJobsUnavailable means this server cannot run commands, so it cannot run
// a job. The text says why.
type ErrJobsUnavailable struct{ Reason string }

func (e *ErrJobsUnavailable) Error() string {
	return "This server cannot run commands: " + e.Reason + "."
}

// JobSpec is one job to start.
type JobSpec struct {
	// Actor is the actor of the chat that starts the job: user and chat.
	Actor workspace.Actor
	// Parent is the chat that started Actor's chat when that is a sub-agent.
	Parent string
	// Sandbox is the chat whose sandbox entry runs the job when it is not the
	// chat that starts it: a sub-agent's bash runs in its parent's.
	Sandbox string
	// Label is the short name the person sees.
	Label string
	// Command is run with bash. It never leaves memory but the audit line.
	Command string
}

// Jobs is the registry of the jobs this process runs. It is nil when the
// server cannot run commands.
func (r *Runner) Jobs() *bgjob.Registry {
	if r == nil {
		return nil
	}
	return r.jobs
}

// OnJob sets the hooks of the registry: check says why a live job must end now
// (the sweep asks every 30 s), and changed is called when a job starts and
// when it has ended. The owner of the chats keeps the shared stubs with them.
// It may be called once the Runner is built, because the chats are made after.
func (r *Runner) OnJob(check func(context.Context, bgjob.Info) bgjob.EndReason, changed func(bgjob.Info)) {
	r.hookMu.Lock()
	r.check, r.changed = check, changed
	r.hookMu.Unlock()
}

func (r *Runner) jobCheck(ctx context.Context, in bgjob.Info) bgjob.EndReason {
	r.hookMu.RLock()
	f := r.check
	r.hookMu.RUnlock()
	if f == nil {
		return ""
	}
	return f(ctx, in)
}

func (r *Runner) jobChanged(in bgjob.Info) {
	r.hookMu.RLock()
	f := r.changed
	r.hookMu.RUnlock()
	if f != nil {
		f(in)
	}
}

// StartJob starts the command as a background job of the chat. It returns the
// job's Info (state running). The errors are *bgjob.LimitError, runner.ErrBusy
// (every sandbox entry of the user holds a job), ErrScoped, and
// *ErrJobsUnavailable. The caller has done the approval and the checks of the
// chat's tool (see studio's job tool): this is the mechanism.
func (r *Runner) StartJob(ctx context.Context, s JobSpec) (bgjob.Info, error) {
	if r.Unavailable() != "" {
		return bgjob.Info{}, &ErrJobsUnavailable{Reason: r.Unavailable()}
	}
	if workspace.HasFence(s.Actor) {
		return bgjob.Info{}, ErrScoped
	}
	if s.Command == "" {
		return bgjob.Info{}, errors.New("a job needs a command")
	}
	job, err := r.jobs.Reserve(bgjob.Spec{Owner: s.Actor.Principal, Chat: s.Actor.ChatID, Parent: s.Parent, Label: s.Label, Command: s.Command})
	if err != nil {
		return bgjob.Info{}, err
	}
	op := make([]byte, 8)
	_, _ = rand.Read(op)
	conv := s.Actor.ChatID
	if s.Sandbox != "" {
		conv = s.Sandbox
	}
	workDir := r.workDir(ctx, s.Actor, true)
	req := runner.Request{User: s.Actor.Principal, Conversation: conv, Operation: "hj" + hex.EncodeToString(op), Tool: "job"}
	// The job saves its files and gives its slot back once its process ended;
	// the slot is known only after StartJob returns.
	released := make(chan func(), 1)
	onEnd := func(in bgjob.Info) {
		cctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		var ce *runner.ConflictsError
		switch err := r.m.CheckpointJob(cctx, req, fmt.Sprintf("Job %s: %s", in.ID, in.Label)); {
		case errors.As(err, &ce):
			r.o.Log.Warn("[host-exec] the files of a job conflict with newer changes", "job", in.ID, "conflicts", len(ce.Conflicts), "reason", ce.Conflicts[0].Reason, "path", ce.Conflicts[0].Path)
		case err != nil:
			r.o.Log.Warn("[host-exec] the files of a job were not saved", "job", in.ID, "error", err)
		}
		r.auditJob(Event{Kind: KindJobEnd, User: in.Owner, Chat: in.Chat, Job: in.ID, Exit: in.Exit, DurationMS: in.Ended.Sub(in.Started).Milliseconds(),
			Command: string(in.End)})
		if rel := <-released; rel != nil {
			rel()
		}
	}
	release, err := r.m.StartJob(ctx, req,
		func(sb sandbox.Sandbox) (bgjob.EndFunc, error) {
			// The process must not die with the call that starts it.
			// The lifetime ends the job with a polite signal first (the sweep), so
			// the hard stop of the context comes one grace later.
			pctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), r.jobs.Lifetime()+r.jobs.KillGrace())
			dir, env := sandbox.WorkStart(workDir, runner.GuestHome)
			proc, err := sb.Exec(pctx, sandbox.Cmd{Args: []string{"bash", "-lc", s.Command}, Dir: dir, Env: env, Job: true, Stdin: job.Stdin(), Stdout: job.Output(), Stderr: job.Output()})
			if err != nil {
				cancel()
				return nil, err
			}
			job.Run(proc, cancel, onEnd)
			return job.End, nil
		})
	if err != nil {
		// No process ever ran and no stub exists: leave no failed entry.
		job.Abandon()
		return bgjob.Info{}, err
	}
	released <- release
	r.auditJob(Event{Kind: KindJobStart, User: s.Actor.Principal, Chat: s.Actor.ChatID, Job: job.ID(), Command: capAudit(s.Command), Exit: -1})
	return job.Info(), nil
}

// auditJob keeps one line of a job.
func (r *Runner) auditJob(ev Event) {
	ev.At = time.Now().UTC()
	r.o.Log.Warn("[host-exec] job", "kind", ev.Kind, "user", ev.User, "chat", ev.Chat, "job", ev.Job, "exit", ev.Exit)
	if err := r.o.Audit.Add(context.Background(), ev); err != nil {
		r.o.Log.Error("[host-exec] the audit line was not saved", "error", fmt.Sprint(err))
	}
}

// jobAudit records the input and the kill of a job, on the node that runs it,
// whoever sent them (a turn here, a turn on another node, the person's Stop).
func (r *Runner) jobAudit(e bgjob.AuditEvent) {
	switch e.Kind {
	case bgjob.AuditInput:
		r.auditJob(Event{Kind: KindJobInput, User: e.Info.Owner, Chat: e.Info.Chat, Job: e.Info.ID, Command: capAudit(e.Text), Exit: -1})
	case bgjob.AuditKill:
		r.auditJob(Event{Kind: KindJobKill, User: e.Info.Owner, Chat: e.Info.Chat, Job: e.Info.ID, Command: e.Signal + " " + string(e.Reason), Exit: -1})
	}
}
