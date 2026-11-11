package studio

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mudler/LocalAI/core/services/studio/bgjob"
	"github.com/mudler/LocalAI/core/services/studio/session"
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
const SandboxJobsTooOld = "The add-on here cannot run background jobs: it is older than this LocalAI. An administrator can update it."

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
	Kind              string // job_start, job_input, job_kill, job_end
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
	End(ctx context.Context, s SandboxJobScope, id string, reason bgjob.EndReason) (bgjob.Info, error)
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
	r, err := j.do(ctx, s, a)
	if err != nil {
		return bgjob.Info{}, err
	}
	// The line is written once the add-on has started the job, so it names
	// the job; a start the add-on refused ran nothing and is not audited.
	j.record(ctx, s, "job_start", r.Job.ID, command)
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
	o.Since = since
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
// SIGKILL after 10 s. Its end reads "killed".
func (j *SandboxJobs) Kill(ctx context.Context, s SandboxJobScope, id, signal string) (bgjob.Info, error) {
	return j.kill(ctx, s, id, signal, "")
}

// End ends the job for a reason of the host (the person's Stop, a deleted
// chat, commands switched off): term, then kill after 10 s. The add-on
// records the reason as the job's end.
func (j *SandboxJobs) End(ctx context.Context, s SandboxJobScope, id string, reason bgjob.EndReason) (bgjob.Info, error) {
	return j.kill(ctx, s, id, "term", reason)
}

func (j *SandboxJobs) kill(ctx context.Context, s SandboxJobScope, id, signal string, reason bgjob.EndReason) (bgjob.Info, error) {
	j.record(ctx, s, "job_kill", id, string(reason))
	r, err := j.do(ctx, s, contract.JobArgs{Verb: contract.JobKill, ID: id, Signal: signal, Reason: string(reason)})
	if err != nil {
		return bgjob.Info{}, err
	}
	return s.info(*r.Job), nil
}

// Ended writes the audit line of a job the host saw end: its end reason and
// exit status. The host sees the end in an answer of the add-on, so the line
// may come some seconds after the end.
func (j *SandboxJobs) Ended(ctx context.Context, s SandboxJobScope, in bgjob.Info) {
	if j == nil {
		return
	}
	j.record(ctx, s, "job_end", in.ID, fmt.Sprintf("%s, exit %d", in.End, in.Exit))
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
		return "The add-on gave an answer about the job that is not valid; nothing is shown. Try again.", true
	}
	return "", false
}

// ---------------------------------------------------------------------------
// The job tool of a chat whose bash the add-on serves.

// addonJobBackend returns the JobBackend of the add-on that serves bash. A
// server with no add-on job caller (Deps.SandboxJobs nil) answers with the
// "too old" text, as an add-on that declares no jobs does. A Sandbox chat never
// falls back to the built-in bash for a job.
func (s *Service) addonJobBackend(_ context.Context, addon string) JobBackend {
	return &addonJobs{s: s, j: s.d.SandboxJobs, addon: addon}
}

// addonJobs adapts SandboxJobs to the job tool. The stubs of its jobs are
// written through RecordJob, as the built-in writes its own; an end that an
// answer of the add-on reports is recorded and wakes the waits on the job.
type addonJobs struct {
	s     *Service
	j     *SandboxJobs
	addon string
}

var _ JobBackend = (*addonJobs)(nil)

func (b *addonJobs) Provider() string { return b.addon }

func (b *addonJobs) scope(a workspace.Actor) SandboxJobScope {
	return SandboxJobScope{User: a.Principal, Chat: a.ChatID, AddonID: b.addon}
}

// errText is the text of an error of the add-on, and false for an outage.
func (b *addonJobs) errText(err error) (string, bool) {
	if t, ok := SandboxJobText(err); ok {
		return t, true
	}
	return "", false
}

// noteEnd records a job that an answer reports finished while its stub still
// says live, and tells the listeners (a wait on the job wakes at once).
func (b *addonJobs) noteEnd(stub session.JobStub, in bgjob.Info) {
	if !in.State.Finished() || stub.State.Finished() {
		return
	}
	b.s.sandboxJobEnded(context.Background(), in)
}

func (b *addonJobs) Do(ctx context.Context, a workspace.Actor, c JobCall) (JobResult, error) {
	switch c.Action {
	case JobStart:
		return b.start(ctx, a, c)
	case JobList:
		return JobResult{Text: b.list(ctx, a, c)}, nil
	case JobRead, JobInput, JobKill:
		return b.act(ctx, a, c)
	}
	return JobResult{}, errors.New("job needs an action")
}

func (b *addonJobs) start(ctx context.Context, a workspace.Actor, c JobCall) (JobResult, error) {
	if b.j == nil {
		return JobResult{Text: SandboxJobsTooOld}, nil
	}
	if why := b.sandboxOK(ctx, a); why != "" {
		return JobResult{Text: "Not started: " + why + "."}, nil
	}
	// The add-on bounds the jobs of its process; the host bounds those of the
	// chat and of the person. A stub can be stale (the add-on never reports
	// an end), so the chat's own stubs are brought in step first.
	rec, err := b.s.d.Store.GetChat(ctx, a.Principal, a.ChatID)
	if err != nil {
		return JobResult{}, err
	}
	if b.liveOf(rec.Jobs) > 0 {
		b.s.refreshAddonJobs(ctx, a.Principal, a.ChatID, b.addon, rec.Jobs)
	}
	// The place is held in the same step as the count, so two starts at once
	// cannot both pass the limit; it is freed when the start makes no job.
	held, err := b.reserveJob(ctx, a, c.Label)
	var le *bgjob.LimitError
	switch {
	case errors.As(err, &le):
		return JobResult{Text: "Not started: " + le.Error() + "."}, nil
	case err != nil:
		return JobResult{}, err
	}
	keep := false
	defer func() {
		if !keep {
			b.dropReservation(ctx, a, held)
		}
	}()
	// The jobs of the person's other chats and sub-agents count too, the
	// places held by their starts included: the limit is by owner. Only this
	// add-on's jobs count, as the host registry counts only the built-in ones.
	// The count follows the hold, so of two starts at once that would pass
	// the limit together, neither does.
	if over, err := b.overUserLimit(ctx, a.Principal); err != nil {
		return JobResult{}, err
	} else if over {
		return JobResult{Text: "Not started: " + (&bgjob.LimitError{Scope: bgjob.ScopeUser, Max: bgjob.PerUser}).Error() + "."}, nil
	}
	in, err := b.j.Start(ctx, b.scope(a), c.Command, c.Label)
	if err != nil {
		if t, ok := b.errText(err); ok {
			return JobResult{Text: t}, nil
		}
		return JobResult{Text: "Not started: the add-on could not be reached; try again."}, nil
	}
	in.Parent = c.Parent
	if err := b.commitReservation(ctx, a, held, in); err != nil {
		if errors.Is(err, errJobIDTaken) {
			return JobResult{Text: "Not started: the add-on gave an answer about the job that is not valid; nothing is shown. Try again."}, nil
		}
		return JobResult{}, err
	}
	keep = true
	b.s.followSandboxChat(a.Principal, a.ChatID, b.addon)
	text := fmt.Sprintf("Started job %s (%q) %s. It ends after 2h at the latest. Read it with job read; to be woken when it ends, use wait_for with job %q and until \"exit\". The input you write to the job with job input needs no approval of its own. Show the person this command:\n",
		in.ID, in.Label, jobWhere(b.addon), in.ID)
	f := fenceOf(c.Command)
	text += f + "\n" + c.Command + "\n" + f
	return JobResult{Text: text, Started: &in}, nil
}

// overUserLimit says whether the person has more live jobs of this add-on
// than the limit, the place this start holds included. Stubs that look live
// are asked about before a refusal: an add-on that restarted leaves stubs
// that look live in every chat, and they must not block the person.
func (b *addonJobs) overUserLimit(ctx context.Context, user string) (bool, error) {
	n, chats, err := b.liveJobsOfUser(ctx, user, bgjob.PerUser+1)
	if err != nil || n <= bgjob.PerUser {
		return false, err
	}
	for _, rec := range chats {
		b.s.refreshAddonJobs(ctx, user, rec.ID, b.addon, rec.Jobs)
	}
	n, _, err = b.liveJobsOfUser(ctx, user, bgjob.PerUser+1)
	return n > bgjob.PerUser, err
}

// liveJobsOfUser counts the live stubs of this add-on in every chat of the
// user, sub-agents included, and stops once it has counted stop. It also
// returns the chats that hold live jobs, not the places of starts.
func (b *addonJobs) liveJobsOfUser(ctx context.Context, user string, stop int) (int, []session.ChatRecord, error) {
	n := 0
	var chats []session.ChatRecord
	var cur session.ChatCursor
	for {
		page, err := b.s.d.Store.ListChats(ctx, user, cur, 200)
		if err != nil {
			return 0, nil, err
		}
		for _, rec := range page {
			k := b.liveOf(rec.Jobs)
			if k == 0 {
				continue
			}
			n += k
			if slices.ContainsFunc(rec.Jobs, func(st session.JobStub) bool { return !isJobReservation(st) && b.s.liveAddonStub(st, b.addon) }) {
				chats = append(chats, rec)
			}
			if n >= stop {
				return n, chats, nil
			}
		}
		if len(page) < 200 {
			return n, chats, nil
		}
		last := page[len(page)-1]
		cur = session.ChatCursor{Updated: last.Updated, ID: last.ID}
	}
}

// liveOf counts the stubs of this add-on that hold a place: live jobs and
// starts in flight.
func (b *addonJobs) liveOf(stubs []session.JobStub) int {
	n := 0
	for _, st := range stubs {
		if b.s.liveAddonStub(st, b.addon) {
			n++
		}
	}
	return n
}

func (b *addonJobs) list(ctx context.Context, a workspace.Actor, c JobCall) string {
	now := b.s.now()
	stubs := withoutReservations(c.Stubs)
	var live map[string]bgjob.Info
	if b.j != nil && slices.ContainsFunc(stubs, func(st session.JobStub) bool { return !st.State.Finished() }) {
		live, _ = b.s.refreshAddonJobs(ctx, a.Principal, a.ChatID, b.addon, stubs)
	}
	slices.SortStableFunc(stubs, func(x, y session.JobStub) int {
		switch rx, ry := !x.State.Finished(), !y.State.Finished(); {
		case rx != ry && rx:
			return -1
		case rx != ry:
			return 1
		}
		return y.Started.Compare(x.Started)
	})
	lines := make([]string, 0, len(stubs))
	for _, st := range stubs {
		if in, ok := live[st.ID]; ok {
			lines = append(lines, jobLine(in, now))
			continue
		}
		if !st.State.Finished() && live != nil {
			// The add-on answered and does not know a job its stub calls
			// live: it restarted. refreshAddonJobs marked it lost.
			st.State = bgjob.StateLost
		}
		lines = append(lines, stubLine(st, now))
	}
	return strings.Join(lines, "\n")
}

func (b *addonJobs) act(ctx context.Context, a workspace.Actor, c JobCall) (JobResult, error) {
	st := c.Stub
	if b.j == nil {
		if st.State == bgjob.StateLost {
			return JobResult{Text: lostText(st.ID)}, nil
		}
		return JobResult{Text: SandboxJobsTooOld}, nil
	}
	sc := b.scope(a)
	var (
		res JobResult
		in  bgjob.Info
		err error
	)
	// A lost stub is asked too, as the observer does: the answer decides.
	switch c.Action {
	case JobRead:
		var o bgjob.Observation
		o, err = b.j.Read(ctx, sc, st.ID, c.Since, c.MaxBytes)
		if err == nil {
			// The add-on keeps the cursor of the model's reads: with no
			// offset asked, nothing before From was skipped by this read.
			o.Since = o.From
			if c.Since != nil {
				o.Since = *c.Since
			}
			in, res.Text = o.Info, readText(o, b.s.now())
		}
	case JobInput:
		if st.State == bgjob.StateLost {
			return JobResult{Text: lostText(st.ID)}, nil
		}
		if why := b.sandboxOK(ctx, a); why != "" {
			return JobResult{Text: "Not written: " + why + "."}, nil
		}
		var n int
		var pending bool
		n, pending, err = b.j.Input(ctx, sc, st.ID, c.Input, c.CloseInput)
		res.Text = fmt.Sprintf("Wrote %d bytes to %s.", n, st.ID)
		if pending {
			res.Text = fmt.Sprintf("Wrote %d bytes to %s; it has not read them all yet, the rest is written as it reads.", n, st.ID)
		}
	case JobKill:
		in, err = b.j.Kill(ctx, sc, st.ID, c.Signal)
		res.Text = fmt.Sprintf("Sent %s to %s.", c.Signal, st.ID)
	}
	if err != nil {
		var r *SandboxJobRefusal
		switch {
		case errors.As(err, &r) && r.Code == contract.JobCodeUnknown:
			if st.State.Finished() {
				if st.State == bgjob.StateLost {
					return JobResult{Text: lostText(st.ID)}, nil
				}
				return JobResult{Text: fmt.Sprintf("%s %q: %s. Its output is no longer kept.", st.ID, st.Label, endText(st))}, nil
			}
			b.s.markJobLost(ctx, a.Principal, a.ChatID, st.ID)
			return JobResult{Text: lostText(st.ID)}, nil
		case errors.As(err, &r) && r.Code == contract.JobCodeEnded && !st.State.Finished():
			// The refusal carries no job: one list brings the stub in step,
			// so the views stop showing it live.
			b.s.refreshAddonJobs(ctx, a.Principal, a.ChatID, b.addon, []session.JobStub{st})
		}
		if t, ok := b.errText(err); ok {
			return JobResult{Text: t}, nil
		}
		return JobResult{Text: fmt.Sprintf("Could not reach the add-on that runs %s; try again.", st.ID)}, nil
	}
	if in.ID != "" {
		in.Parent = c.Parent
		switch {
		case st.State == bgjob.StateLost && !in.State.Finished():
			b.s.RecordJob(ctx, in)
		default:
			b.noteEnd(st, in)
		}
		if !in.State.Finished() {
			b.s.followSandboxChat(a.Principal, a.ChatID, b.addon)
		}
	}
	return res, nil
}

// sandboxOK says why the chat may not start a job or write to its jobs now,
// or "": the shell group of the chat was switched off. The approval of the
// start covers the input; the add-on needs no card.
func (b *addonJobs) sandboxOK(ctx context.Context, a workspace.Actor) string {
	rec, err := b.s.d.Store.GetChat(ctx, a.Principal, a.ChatID)
	if err != nil {
		return "the chat cannot be read"
	}
	if b.s.hiddenFor(ctx, rec)[commandTool] {
		return "commands were switched off for this chat"
	}
	return ""
}

// end ends one job of the chat for a reason of the host and records what the
// add-on answered. A job the add-on reports ended already is recorded so.
func (b *addonJobs) end(ctx context.Context, a workspace.Actor, st session.JobStub, reason bgjob.EndReason) (bgjob.Info, error) {
	if b.j == nil {
		return bgjob.Info{}, ErrAddonNoJobs
	}
	in, err := b.j.End(ctx, b.scope(a), st.ID, reason)
	var r *SandboxJobRefusal
	switch {
	case err == nil:
		b.noteEnd(st, in)
	case errors.As(err, &r) && r.Code == contract.JobCodeEnded:
		b.s.refreshAddonJobs(ctx, a.Principal, a.ChatID, b.addon, []session.JobStub{st})
	case errors.As(err, &r) && r.Code == contract.JobCodeUnknown:
		b.s.markJobLost(ctx, a.Principal, a.ChatID, st.ID)
	}
	return in, err
}

// EndChat ends every live job of the chat in the add-on for the reason: the
// chat is deleted, or the sub-agent ended. The add-on records the reason.
func (b *addonJobs) EndChat(ctx context.Context, a workspace.Actor, reason bgjob.EndReason) error {
	if b.j == nil {
		return nil
	}
	sc := b.scope(a)
	list, err := b.j.List(ctx, sc)
	if err != nil {
		if _, ok := b.errText(err); ok {
			return nil
		}
		return err
	}
	var first error
	for _, in := range list {
		if in.State.Finished() {
			continue
		}
		if _, err := b.j.End(ctx, sc, in.ID, reason); err != nil && first == nil {
			if _, ok := b.errText(err); !ok {
				first = err
			}
		}
	}
	return first
}
