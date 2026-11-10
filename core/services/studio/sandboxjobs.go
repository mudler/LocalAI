package studio

import (
	"context"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/mudler/LocalAI/core/services/studio/bgjob"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// The background jobs of a chat whose commands run in an add-on (the
// Sandbox). The add-on keeps the jobs in its process: the table, the output
// ring and the processes. This file is the host side: it calls the add-on's
// jobs tool (contract.ExecutionProvider.Jobs) through the add-on broker and
// turns the answers into the job vocabulary of the registry (bgjob). The
// registry keeps the per-chat and per-user limits and the stubs; the add-on
// keeps the per-process limit and the lifetime.

// AddonJobCaller is implemented by an add-on broker that can call the jobs
// tool of an add-on for a chat as the user. The broker checks the answer
// against the arguments (contract.JobReply.Validate) before it returns it.
// An add-on that declares no jobs tool is ErrAddonNoJobs, and nothing is sent.
type AddonJobCaller interface {
	CallJobs(ctx context.Context, user, conv, addonID string, args contract.JobArgs) (contract.JobReply, error)
}

// ErrAddonNoJobs says the add-on that runs the chat's commands cannot run
// background jobs: it is older than this LocalAI.
var ErrAddonNoJobs = errors.New("studio: the add-on cannot run background jobs")

// SandboxJobsTooOld is what the model reads when the add-on cannot run jobs.
// A Sandbox chat never falls back to the built-in bash for a job: isolation
// must not change silently.
const SandboxJobsTooOld = "The Sandbox add-on here cannot run background jobs: it is older than this LocalAI. An administrator can update it."

// JobAuditMax bounds the text of a job audit line (the command of a start,
// the input of an input), as for a host command.
const JobAuditMax = 4 << 10

// SandboxJobScope names the jobs of one chat in one add-on. User is the
// principal that owns the chat, Chat the chat whose sandbox runs the job, and
// AddonID the add-on that serves the chat's commands. They come from the
// host's own record of the chat, never from the model.
type SandboxJobScope struct {
	User, Chat, AddonID string
}

// SandboxJobEvent is one audit line of a Sandbox job. Text is the command of a
// start and the input of an input, cut at JobAuditMax; empty otherwise. The
// output of a job is never audited.
type SandboxJobEvent struct {
	Kind              string // job_start, job_input, job_kill
	User, Chat, Addon string
	Job               string
	Text              string
	At                time.Time
}

// SandboxJobRefusal is the add-on's own refusal: Text is for the model. Code
// is one of the contract.JobCode values.
type SandboxJobRefusal struct{ Code, Text string }

func (e *SandboxJobRefusal) Error() string { return e.Text }

// Unwrap makes an unknown job bgjob.ErrUnknown, so the registry marks a live
// stub lost: the add-on restarted, or the job was dropped.
func (e *SandboxJobRefusal) Unwrap() error {
	if e.Code == contract.JobCodeUnknown {
		return bgjob.ErrUnknown
	}
	return nil
}

// SandboxJobs is the job backend of the chats whose commands an add-on runs.
// It keeps no state: every call goes to the add-on.
type SandboxJobs struct {
	call  AddonJobCaller
	audit func(ctx context.Context, ev SandboxJobEvent)
	now   func() time.Time
}

// NewSandboxJobs returns the backend over an add-on broker. audit records the
// start, input and kill lines; nil records none.
func NewSandboxJobs(call AddonJobCaller, audit func(ctx context.Context, ev SandboxJobEvent)) *SandboxJobs {
	return &SandboxJobs{call: call, audit: audit, now: time.Now}
}

// sandboxJobBackend is the method set the job tool (studio/jobtool.go) uses
// for a provider other than the built-in bash. SandboxJobs implements it; the
// registry may wrap it.
type sandboxJobBackend interface {
	Start(ctx context.Context, s SandboxJobScope, command, label string) (bgjob.Info, error)
	List(ctx context.Context, s SandboxJobScope) ([]bgjob.Info, error)
	Read(ctx context.Context, s SandboxJobScope, id string, since *int64, maxBytes int) (bgjob.Observation, error)
	Observe(ctx context.Context, s SandboxJobScope, req bgjob.ObserveRequest) (bgjob.Observation, error)
	Input(ctx context.Context, s SandboxJobScope, id, text string, closeInput bool) (written int, pending bool, err error)
	Kill(ctx context.Context, s SandboxJobScope, id, signal string) (bgjob.Info, error)
}

var _ sandboxJobBackend = (*SandboxJobs)(nil)

func (s SandboxJobScope) valid() bool { return s.User != "" && s.Chat != "" && s.AddonID != "" }

// do sends one call and turns a refusal into a SandboxJobRefusal.
func (j *SandboxJobs) do(ctx context.Context, s SandboxJobScope, a contract.JobArgs) (contract.JobReply, error) {
	if j == nil || j.call == nil || !s.valid() {
		return contract.JobReply{}, ErrAddonNoJobs
	}
	if a.ID != "" && !contract.ValidJobID(a.ID) {
		// Never sent: an ID of another shape names no job anywhere.
		return contract.JobReply{}, &SandboxJobRefusal{Code: contract.JobCodeUnknown, Text: fmt.Sprintf("No job %q in this chat.", a.ID)}
	}
	if err := a.Validate(); err != nil {
		return contract.JobReply{}, &SandboxJobRefusal{Code: contract.JobCodeInvalid, Text: err.Error()}
	}
	r, err := j.call.CallJobs(ctx, s.User, s.Chat, s.AddonID, a)
	if err != nil {
		return contract.JobReply{}, err
	}
	if !r.OK {
		return contract.JobReply{}, &SandboxJobRefusal{Code: r.Code, Text: r.Error}
	}
	return r, nil
}

func (j *SandboxJobs) record(ctx context.Context, s SandboxJobScope, kind, id, text string) {
	if j.audit == nil {
		return
	}
	j.audit(ctx, SandboxJobEvent{Kind: kind, User: s.User, Chat: s.Chat, Addon: s.AddonID, Job: id, Text: capText(text, JobAuditMax), At: j.now().UTC()})
}

// capText cuts s at max bytes, on a character.
func capText(s string, max int) string {
	if len(s) <= max {
		return s
	}
	s = s[:max]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s + " [cut]"
}

// info is the registry's view of one job of the add-on.
func (s SandboxJobScope) info(in contract.JobInfo) bgjob.Info {
	return bgjob.Info{ID: in.ID, Owner: s.User, Chat: s.Chat, Instance: "addon:" + s.AddonID, Provider: s.AddonID, Label: in.Label,
		State: bgjob.State(in.State), End: bgjob.EndReason(in.End), Exit: in.Exit, Started: in.Started, Ended: in.Ended,
		LastOutput: in.LastOutput, Total: in.Total}
}

func (s SandboxJobScope) observation(r contract.JobReply) bgjob.Observation {
	o := bgjob.Observation{Info: s.info(*r.Job), Output: []byte(r.Output), From: r.From, Next: r.Next, Tail: r.Tail}
	if !o.State.Finished() {
		o.Idle = time.Duration(r.Job.IdleMS) * time.Millisecond
	}
	return o
}

// Start starts a job. The command is audited (cut at JobAuditMax) before it
// is sent; the approval of the start is the caller's.
func (j *SandboxJobs) Start(ctx context.Context, s SandboxJobScope, command, label string) (bgjob.Info, error) {
	a := contract.JobArgs{Verb: contract.JobStart, Command: command, Label: label}
	if err := a.Validate(); err != nil {
		return bgjob.Info{}, err
	}
	j.record(ctx, s, "job_start", "", command)
	r, err := j.do(ctx, s, a)
	if err != nil {
		return bgjob.Info{}, err
	}
	return s.info(*r.Job), nil
}

// List returns the jobs of the chat in the add-on: live first, then the
// newest finished.
func (j *SandboxJobs) List(ctx context.Context, s SandboxJobScope) ([]bgjob.Info, error) {
	r, err := j.do(ctx, s, contract.JobArgs{Verb: contract.JobList})
	if err != nil {
		return nil, err
	}
	out := make([]bgjob.Info, 0, len(r.Jobs))
	for _, in := range r.Jobs {
		out = append(out, s.info(in))
	}
	return out, nil
}

// Read is the model's read: since nil reads on from where its last read
// ended, which the add-on keeps. maxBytes zero is the default.
func (j *SandboxJobs) Read(ctx context.Context, s SandboxJobScope, id string, since *int64, maxBytes int) (bgjob.Observation, error) {
	r, err := j.do(ctx, s, contract.JobArgs{Verb: contract.JobRead, ID: id, Since: since, MaxBytes: maxBytes})
	if err != nil {
		return bgjob.Observation{}, err
	}
	return s.observation(r), nil
}

// Observe is the read of a wait check: it does not move the model's cursor
// and carries the tail. Owner and Chat of the request must be those of the
// scope.
func (j *SandboxJobs) Observe(ctx context.Context, s SandboxJobScope, req bgjob.ObserveRequest) (bgjob.Observation, error) {
	if req.Owner != s.User || req.Chat != s.Chat {
		return bgjob.Observation{}, bgjob.ErrUnknown
	}
	n := req.MaxBytes
	if n <= 0 {
		n = bgjob.DefaultObserveBytes
	}
	n = min(max(n, contract.MinJobReadBytes), bgjob.MaxObserveBytes, contract.MaxJobReadBytes)
	since := max(req.Since, 0)
	r, err := j.do(ctx, s, contract.JobArgs{Verb: contract.JobRead, ID: req.ID, Since: &since, MaxBytes: n, Observe: true})
	if err != nil {
		return bgjob.Observation{}, err
	}
	o := s.observation(r)
	tail := req.TailBytes
	if tail <= 0 {
		tail = bgjob.DefaultTailBytes
	}
	if len(o.Tail) > tail {
		t := o.Tail[len(o.Tail)-tail:]
		for len(t) > 0 && !utf8.RuneStart(t[0]) {
			t = t[1:]
		}
		o.Tail = t
	}
	return o, nil
}

// Input writes to the job's standard input. The input is audited (cut at
// JobAuditMax) before it is sent. pending says the job had not read it all
// after 5 s: the rest is written as it reads.
func (j *SandboxJobs) Input(ctx context.Context, s SandboxJobScope, id, text string, closeInput bool) (int, bool, error) {
	a := contract.JobArgs{Verb: contract.JobInput, ID: id, Input: text, CloseInput: closeInput}
	if err := a.Validate(); err != nil {
		if len(text) > contract.MaxJobInputBytes {
			return 0, false, &SandboxJobRefusal{Code: contract.JobCodeInvalid, Text: fmt.Sprintf("Not written: input is at most %d bytes per call.", contract.MaxJobInputBytes)}
		}
		return 0, false, err
	}
	j.record(ctx, s, "job_input", id, text)
	r, err := j.do(ctx, s, a)
	if err != nil {
		return 0, false, err
	}
	return r.Written, r.Pending, nil
}

// Kill signals the job: term (the default), int or kill. The add-on sends
// SIGKILL after 10 s.
func (j *SandboxJobs) Kill(ctx context.Context, s SandboxJobScope, id, signal string) (bgjob.Info, error) {
	j.record(ctx, s, "job_kill", id, "")
	r, err := j.do(ctx, s, contract.JobArgs{Verb: contract.JobKill, ID: id, Signal: signal})
	if err != nil {
		return bgjob.Info{}, err
	}
	return s.info(*r.Job), nil
}

// Observer returns the read side of the jobs of one add-on, for a wait_for
// check. List returns the jobs of the chat itself; the jobs of its
// sub-agents are in their own chats.
func (j *SandboxJobs) Observer(addonID string) bgjob.Observer {
	return sandboxObserver{j: j, addon: addonID}
}

type sandboxObserver struct {
	j     *SandboxJobs
	addon string
}

func (o sandboxObserver) Observe(ctx context.Context, req bgjob.ObserveRequest) (bgjob.Observation, error) {
	return o.j.Observe(ctx, SandboxJobScope{User: req.Owner, Chat: req.Chat, AddonID: o.addon}, req)
}

func (o sandboxObserver) List(ctx context.Context, owner, chat string) ([]bgjob.Info, error) {
	return o.j.List(ctx, SandboxJobScope{User: owner, Chat: chat, AddonID: o.addon})
}

// SandboxJobText is what the model reads for an error of a Sandbox job call:
// the add-on's own refusal, the too-old text, or a plain outage text. The
// second value is false for an error the caller should treat as its own.
func SandboxJobText(err error) (string, bool) {
	var r *SandboxJobRefusal
	switch {
	case errors.As(err, &r):
		return r.Text, true
	case errors.Is(err, ErrAddonNoJobs):
		return SandboxJobsTooOld, true
	case errors.Is(err, contract.ErrJobReply):
		return "The Sandbox add-on gave an answer about the job that is not valid; nothing is shown. Try again.", true
	}
	return "", false
}

// addonJobText is the answer of a chat whose bash the Sandbox add-on serves
// when that add-on cannot run background jobs: it is older than this LocalAI.
// A Sandbox chat never falls back to the built-in bash for a job.
const addonJobText = "The Sandbox add-on here cannot run background jobs: it is older than this LocalAI. An administrator can update it."

// addonJobBackend returns the JobBackend of the add-on that serves bash. The
// add-on side of the job tool replaces this body; until an add-on declares
// jobs, the answer is the "too old" text and nothing else happens.
func (s *Service) addonJobBackend(_ context.Context, addon string) JobBackend {
	return tooOldJobs{addon: addon}
}

// tooOldJobs is the backend of an add-on that declares no jobs.
type tooOldJobs struct{ addon string }

func (t tooOldJobs) Provider() string { return t.addon }

func (t tooOldJobs) Do(_ context.Context, _ workspace.Actor, c JobCall) (JobResult, error) {
	if c.Action == JobList {
		return JobResult{}, nil
	}
	return JobResult{Text: addonJobText}, nil
}

func (t tooOldJobs) EndChat(context.Context, workspace.Actor, bgjob.EndReason) error { return nil }
