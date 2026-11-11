package bgjob

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// The limits and times of the registry. They are constants in this release,
// not settings.
const (
	// PerChat, PerUser and PerNode bound the live jobs of a chat, of a user
	// and of one process.
	PerChat = 4
	PerUser = 8
	PerNode = 32
	// Lifetime is how long a job may run.
	Lifetime = 2 * time.Hour
	// Retention is how long a finished job stays readable.
	Retention = 30 * time.Minute
	// KeepFinished is the most finished jobs a chat keeps.
	KeepFinished = 16
	// KeepFinishedTotal is the most finished jobs the process keeps, whatever
	// their chats. A ring holds up to 2 x 256 KiB, so the cap bounds the
	// memory of finished jobs to about 64 MiB however many chats end jobs.
	KeepFinishedTotal = 128
	// KillGrace is the wait between a polite signal and SIGKILL.
	KillGrace = 10 * time.Second
	// SweepEvery is how often a process checks the end conditions of its jobs.
	SweepEvery = 30 * time.Second
	// InputTimeout is how long an input waits for a child that does not read.
	InputTimeout = 5 * time.Second
	// MaxInput is the most bytes of one input.
	MaxInput = 2000
)

// Scope names the limit a start ran into.
type Scope string

// The scopes of a limit.
const (
	ScopeChat Scope = "chat"
	ScopeUser Scope = "user"
	ScopeNode Scope = "node"
)

// LimitError says a job was not started because a limit holds.
type LimitError struct {
	Scope Scope
	Max   int
}

func (e *LimitError) Error() string {
	switch e.Scope {
	case ScopeChat:
		return fmt.Sprintf("this chat runs %d jobs, the most it may", e.Max)
	case ScopeUser:
		return fmt.Sprintf("you run %d jobs, the most you may", e.Max)
	}
	return fmt.Sprintf("this server runs %d jobs, the most it may", e.Max)
}

// EndedError says the job has ended, so it takes no input and no kill.
type EndedError struct{ Info Info }

func (e *EndedError) Error() string { return "bgjob: the job has ended" }

// ErrForbidden says the caller may not do that to the job: a parent chat may
// read and kill the jobs of its sub-agents, never write to them.
var ErrForbidden = errors.New("bgjob: not allowed for this chat")

// ErrInputClosed says the input of the job was closed with close_input: it
// takes no more.
var ErrInputClosed = errors.New("bgjob: the input of the job is closed")

// ErrInputTimeout says the child did not read its input in time.
var ErrInputTimeout = errors.New("bgjob: the job did not read its input")

// Proc is the process of a job. sandbox.Process implements it.
type Proc interface {
	// Wait returns the exit status (128+n for signal n) when the process has
	// ended. An error means the status is not known.
	Wait() (int, error)
	// Signal sends sig to the process group.
	Signal(sig syscall.Signal) error
}

// Options configures a Registry.
type Options struct {
	// Instance is the node that runs the jobs, recorded in each Info.
	Instance string
	// Provider is recorded in each Info: "builtin" or an add-on id.
	Provider string
	// Now is the clock; time.Now when nil.
	Now func() time.Time
	// After is the timer of the kill grace and of the input timeout;
	// time.After when nil. Tests replace it.
	After func(time.Duration) <-chan time.Time
	// Lifetime, Retention, KeepFinished and KillGrace override the
	// constants; tests use them.
	Lifetime, Retention, KillGrace time.Duration
	KeepFinished                   int
	// PerChat, PerUser and PerNode override the limits.
	PerChat, PerUser, PerNode int
	// InputTimeout overrides how long an input waits for a child that does
	// not read.
	InputTimeout time.Duration
	// RingSize overrides the size of each job's ring.
	RingSize int
	// Check is asked by Sweep for every live job and returns the reason the
	// job must end now (the chat is gone, the shell group is off ...), or "".
	// It runs outside every lock of the registry.
	Check func(ctx context.Context, j Info) EndReason
	// Changed is called, outside every lock, when a job starts or fails to,
	// and when it has ended. The owner of the registry keeps the shared
	// stubs in step with it, and a wait on the job is woken by it.
	Changed func(Info)
	// Audit is called, outside every lock, for each input written to a job
	// and each kill sent to it, on the node that runs the job, whoever asked.
	Audit func(AuditEvent)
}

// The kinds of an AuditEvent.
const (
	AuditInput = "input"
	AuditKill  = "kill"
)

// AuditEvent is one action on a running job, for the audit of the owner of
// the registry. Text is the input as written; Signal is the signal of a kill.
type AuditEvent struct {
	Kind   string
	Info   Info
	Text   string
	Signal string
	// Reason is the end reason of a kill (killed or stopped).
	Reason EndReason
}

// Registry holds the background jobs of one process: their rings, their
// processes and their states. It implements Observer. It is per process, as
// the jobs are: a job lives in the memory of the node that started it.
type Registry struct {
	o    Options
	mu   sync.Mutex
	jobs map[string]*Job
	// closed refuses new jobs once Close ran.
	closed bool
}

// New returns a Registry.
func New(o Options) *Registry {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.After == nil {
		o.After = time.After
	}
	if o.Lifetime <= 0 {
		o.Lifetime = Lifetime
	}
	if o.Retention <= 0 {
		o.Retention = Retention
	}
	if o.KillGrace <= 0 {
		o.KillGrace = KillGrace
	}
	if o.KeepFinished <= 0 {
		o.KeepFinished = KeepFinished
	}
	if o.PerChat <= 0 {
		o.PerChat = PerChat
	}
	if o.PerUser <= 0 {
		o.PerUser = PerUser
	}
	if o.PerNode <= 0 {
		o.PerNode = PerNode
	}
	if o.InputTimeout <= 0 {
		o.InputTimeout = InputTimeout
	}
	if o.Provider == "" {
		o.Provider = "builtin"
	}
	return &Registry{o: o, jobs: map[string]*Job{}}
}

var _ Observer = (*Registry)(nil)

// Lifetime is how long a job of this registry may run.
func (r *Registry) Lifetime() time.Duration { return r.o.Lifetime }

// KillGrace is the wait between a polite signal and SIGKILL. The owner of a
// job's process gives its context the lifetime plus this grace, so the polite
// signal of the sweep comes before the hard stop of the context.
func (r *Registry) KillGrace() time.Duration { return r.o.KillGrace }

// Spec describes a job to reserve.
type Spec struct {
	Owner, Chat string
	// Parent is the chat that started Chat when Chat is a sub-agent.
	Parent string
	Label  string
	// Command stays in memory only. It is never in an Info or a stub.
	Command string
}

// Job is one background job. The registry makes it with Reserve; its owner
// gives it a process with Run, or says it did not start with Fail.
type Job struct {
	r    *Registry
	ring *Ring

	last atomic.Int64 // unix nanoseconds of the last output
	// stdin is an OS pipe, not an io.Pipe: a child that is given a plain
	// reader gets a copying goroutine that nothing can stop when the child
	// ends, and its Wait would never return.
	inMu     sync.Mutex
	stdinR   *os.File
	stdinW   *os.File
	inClosed bool // close_input ran; guarded by inMu

	mu      sync.Mutex
	info    Info
	command string
	cursor  int64
	proc    Proc
	cancel  context.CancelFunc
	onEnd   func(Info)
	endReq  EndReason // the reason asked for, before the process exits
	killing bool      // the escalation to SIGKILL runs
	exited  chan struct{}
	done    chan struct{}
}

// Reserve counts a job against the limits and returns it in the state
// starting. It returns a *LimitError when a limit holds.
func (r *Registry) Reserve(s Spec) (*Job, error) {
	if s.Owner == "" || s.Chat == "" {
		return nil, errors.New("bgjob: a job needs an owner and a chat")
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil, errors.New("bgjob: the registry is closed")
	}
	var chat, user, node int
	for _, j := range r.jobs {
		j.mu.Lock()
		live := !j.info.State.Finished()
		owner, c := j.info.Owner, j.info.Chat
		j.mu.Unlock()
		if !live {
			continue
		}
		node++
		if owner == s.Owner {
			user++
			if c == s.Chat {
				chat++
			}
		}
	}
	switch {
	case chat >= r.o.PerChat:
		r.mu.Unlock()
		return nil, &LimitError{Scope: ScopeChat, Max: r.o.PerChat}
	case user >= r.o.PerUser:
		r.mu.Unlock()
		return nil, &LimitError{Scope: ScopeUser, Max: r.o.PerUser}
	case node >= r.o.PerNode:
		r.mu.Unlock()
		return nil, &LimitError{Scope: ScopeNode, Max: r.o.PerNode}
	}
	now := r.o.Now()
	id := r.newIDLocked()
	j := &Job{r: r, ring: NewRing(r.o.RingSize), command: s.Command, exited: make(chan struct{}), done: make(chan struct{})}
	var perr error
	j.stdinR, j.stdinW, perr = os.Pipe()
	if perr != nil {
		delete(r.jobs, id)
		r.mu.Unlock()
		return nil, fmt.Errorf("bgjob: no pipe for the input: %w", perr)
	}
	j.last.Store(now.UnixNano())
	j.info = Info{ID: id, Owner: s.Owner, Chat: s.Chat, Parent: s.Parent, Instance: r.o.Instance, Provider: r.o.Provider,
		Label: s.Label, State: StateStarting, Exit: ExitUnknown, Started: now, LastOutput: now}
	r.jobs[id] = j
	r.mu.Unlock()
	return j, nil
}

func (r *Registry) newIDLocked() string {
	for {
		b := make([]byte, 4)
		_, _ = rand.Read(b)
		id := "job-" + strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b))[:6]
		if _, ok := r.jobs[id]; !ok {
			return id
		}
	}
}

// ID is the id of the job.
func (j *Job) ID() string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.info.ID
}

// Command is the text the job was started with. It never leaves memory.
func (j *Job) Command() string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.command
}

// Output is where the process writes its standard output and standard error.
func (j *Job) Output() io.Writer { return jobWriter{j} }

// Stdin is what the process reads as its standard input.
func (j *Job) Stdin() io.Reader { return j.stdinR }

type jobWriter struct{ j *Job }

func (w jobWriter) Write(p []byte) (int, error) {
	n, err := w.j.ring.Write(p)
	if n > 0 {
		w.j.last.Store(w.j.r.o.Now().UnixNano())
	}
	return n, err
}

// Info is what is known of the job now.
func (j *Job) Info() Info {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.infoLocked()
}

func (j *Job) infoLocked() Info {
	in := j.info
	in.Total = j.ring.Total()
	if t := time.Unix(0, j.last.Load()); t.After(in.LastOutput) {
		in.LastOutput = t
	}
	return in
}

// Run says the process started. cancel stops the context the process runs in
// (the lifetime); onEnd runs once after the process has exited and before the
// job reads as finished, so the owner can save the files it wrote: the job
// does not read as ended until onEnd returns. It receives the final Info.
func (j *Job) Run(p Proc, cancel context.CancelFunc, onEnd func(Info)) {
	j.mu.Lock()
	j.proc, j.cancel, j.onEnd = p, cancel, onEnd
	if j.endReq == "" {
		j.info.State = StateRunning
	}
	pending := j.endReq
	info := j.infoLocked()
	j.mu.Unlock()
	j.r.changed(info)
	go j.wait()
	if pending != "" {
		j.signalFor(pending, false)
	}
}

// Fail says the job never started.
func (j *Job) Fail() {
	j.mu.Lock()
	if j.info.State.Finished() {
		j.mu.Unlock()
		return
	}
	j.info.State, j.info.End, j.info.Ended = StateFailed, EndStartFailed, j.r.o.Now()
	j.info.Exit = ExitUnknown
	info := j.infoLocked()
	j.mu.Unlock()
	_ = j.stdinW.Close()
	_ = j.stdinR.Close()
	close(j.exited)
	close(j.done)
	j.r.changed(info)
}

// Abandon says the job never began and leaves no trace: it frees the slot and
// the pipes, drops the job from the registry and does not call Changed. Use it
// for a start that was refused before any process or stub existed (a busy
// sandbox, a failure to get one ready); Fail is for a start that began.
func (j *Job) Abandon() {
	j.mu.Lock()
	if j.info.State.Finished() || j.info.State != StateStarting {
		j.mu.Unlock()
		return
	}
	j.info.State, j.info.End, j.info.Ended = StateFailed, EndStartFailed, j.r.o.Now()
	id := j.info.ID
	j.mu.Unlock()
	_ = j.stdinW.Close()
	_ = j.stdinR.Close()
	close(j.exited)
	close(j.done)
	j.r.drop(id)
}

// wait follows the process to its end.
func (j *Job) wait() {
	code, err := j.proc.Wait()
	close(j.exited)
	if j.cancel != nil {
		j.cancel()
	}
	_ = j.stdinW.Close()
	_ = j.stdinR.Close()

	j.mu.Lock()
	reason := j.endReq
	if err != nil {
		code = ExitUnknown
		if reason == "" && errors.Is(err, context.DeadlineExceeded) {
			reason = EndLifetime
		}
	}
	if reason == "" {
		reason = EndExit
	}
	state := StateExited
	if reason == EndKilled || reason == EndStopped {
		state = StateKilled
	}
	j.info.State = StateEnding
	final := j.infoLocked()
	final.State, final.End, final.Exit, final.Ended = state, reason, code, j.r.o.Now()
	onEnd := j.onEnd
	j.mu.Unlock()

	if onEnd != nil {
		onEnd(final)
	}

	j.mu.Lock()
	final.Total = j.ring.Total()
	j.info = final
	j.mu.Unlock()
	close(j.done)
	j.r.changed(final)
}

// End ends the job for a reason: it signals the process group and records the
// reason. It returns at once, may be called more than once and must not call
// back into the sandbox runner (see EndFunc). A kill follows after the grace
// except for a shutdown and a remade sandbox, which kill at once.
func (j *Job) End(reason EndReason) {
	hard := reason == EndShutdown || reason == EndSandboxRemade
	j.signalFor(reason, hard)
}

func (j *Job) signalFor(reason EndReason, hard bool) {
	j.mu.Lock()
	if j.info.State.Finished() {
		j.mu.Unlock()
		return
	}
	if j.endReq == "" {
		j.endReq = reason
	}
	if j.info.State == StateRunning {
		j.info.State = StateEnding
	}
	p := j.proc
	j.mu.Unlock()
	if p == nil {
		return // Run signals it when the process exists
	}
	sig := syscall.SIGTERM
	if hard {
		sig = syscall.SIGKILL
	}
	j.signal(p, sig)
}

// signal sends sig and arranges the kill that follows a polite one.
func (j *Job) signal(p Proc, sig syscall.Signal) {
	_ = p.Signal(sig)
	if sig == syscall.SIGKILL {
		return
	}
	j.mu.Lock()
	if j.killing {
		j.mu.Unlock()
		return
	}
	j.killing = true
	j.mu.Unlock()
	go func() {
		select {
		case <-j.exited:
		case <-j.r.o.After(j.r.o.KillGrace):
			_ = p.Signal(syscall.SIGKILL)
		}
	}()
}

// Kill is the model's or the person's kill: reason is EndKilled or
// EndStopped, sig the first signal. A second kill with SIGKILL ends a job that
// ignores the first.
func (j *Job) Kill(reason EndReason, sig syscall.Signal) error {
	j.mu.Lock()
	if j.info.State.Finished() {
		info := j.infoLocked()
		j.mu.Unlock()
		return &EndedError{Info: info}
	}
	if j.endReq == "" || j.endReq != EndKilled && j.endReq != EndStopped {
		// An end condition that already runs becomes a kill: the one who asked
		// wants it gone, and the reason says who.
		j.endReq = reason
	}
	if j.info.State == StateRunning {
		j.info.State = StateEnding
	}
	p := j.proc
	j.mu.Unlock()
	if p != nil {
		j.signal(p, sig)
	}
	return nil
}

// Input writes text to the standard input of the job and closes it when
// closeInput is set. The write waits at most InputTimeout, so a child that
// does not read cannot hold a turn.
func (j *Job) Input(text string, closeInput bool) (int, error) {
	return j.input(text, closeInput, 0)
}

// input is Input with a shorter wait: timeout is used when it is above zero
// and below InputTimeout.
func (j *Job) input(text string, closeInput bool, timeout time.Duration) (int, error) {
	j.mu.Lock()
	if j.info.State.Finished() {
		info := j.infoLocked()
		j.mu.Unlock()
		return 0, &EndedError{Info: info}
	}
	j.mu.Unlock()
	j.inMu.Lock()
	defer j.inMu.Unlock()
	if j.inClosed {
		return 0, ErrInputClosed
	}
	if timeout <= 0 || timeout > j.r.o.InputTimeout {
		timeout = j.r.o.InputTimeout
	}
	_ = j.stdinW.SetWriteDeadline(time.Now().Add(timeout))
	n, err := j.stdinW.Write([]byte(text))
	switch {
	case err == nil:
		if closeInput {
			j.inClosed = true
			_ = j.stdinW.Close()
		}
		return n, nil
	case errors.Is(err, os.ErrDeadlineExceeded):
		return n, ErrInputTimeout
	}
	// The pipe is closed or broken: the job ended meanwhile.
	return n, &EndedError{Info: j.Info()}
}

// changed reports a job's state to the owner of the registry.
func (r *Registry) changed(in Info) {
	if r.o.Changed != nil {
		r.o.Changed(in)
	}
}

// visibleTo tells whether the chat of the owner started the job, or started
// the sub-agent that did when parentOK is set. An empty owner or chat sees
// nothing, and an empty Parent is no parent: a request without them must not
// match every top-level job.
func (j *Job) visibleTo(owner, chat string, parentOK bool) bool {
	if owner == "" || chat == "" || j.info.Owner != owner {
		return false
	}
	return j.info.Chat == chat || parentOK && j.info.Parent != "" && j.info.Parent == chat
}

func (r *Registry) find(owner, chat, id string, parentOK bool) *Job {
	r.mu.Lock()
	j := r.jobs[id]
	r.mu.Unlock()
	if j == nil {
		return nil
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if !j.visibleTo(owner, chat, parentOK) {
		return nil
	}
	return j
}

// Observe implements Observer.
func (r *Registry) Observe(_ context.Context, q ObserveRequest) (Observation, error) {
	j := r.find(q.Owner, q.Chat, q.ID, true)
	if j == nil {
		return Observation{}, ErrUnknown
	}
	return j.observe(q.Since, q.MaxBytes, q.TailBytes), nil
}

func (j *Job) observe(since int64, max, tail int) Observation {
	if max <= 0 {
		max = DefaultObserveBytes
	}
	max = min(max, MaxObserveBytes)
	if tail <= 0 {
		tail = DefaultTailBytes
	}
	out, from, next := j.ring.Page(since, max)
	info := j.Info()
	o := Observation{Info: info, Since: since, Output: out, From: from, Next: next, Tail: j.ring.Tail(tail)}
	if !info.State.Finished() {
		o.Idle = max0(j.r.o.Now().Sub(info.LastOutput))
	}
	return o
}

func max0(d time.Duration) time.Duration {
	if d < 0 {
		return 0
	}
	return d
}

// ReadRequest is a read of the model: with no offset it starts where the
// last read of the chat ended, and it moves that place.
type ReadRequest struct {
	Owner, Chat, ID string
	// Since is nil for "where the last read ended".
	Since    *int64
	MaxBytes int
}

// Read reads the job for the chat that asks and moves the read place when it
// is the chat that started the job (a parent reading a sub-agent's job leaves
// the sub-agent's place alone).
func (r *Registry) Read(_ context.Context, q ReadRequest) (Observation, error) {
	j := r.find(q.Owner, q.Chat, q.ID, true)
	if j == nil {
		return Observation{}, ErrUnknown
	}
	j.mu.Lock()
	since := j.cursor
	own := j.info.Chat == q.Chat
	j.mu.Unlock()
	if q.Since != nil {
		since = *q.Since
	}
	o := j.observe(since, q.MaxBytes, 0)
	if own {
		j.mu.Lock()
		j.cursor = o.Next
		j.mu.Unlock()
	}
	return o, nil
}

// List implements Observer.
func (r *Registry) List(_ context.Context, owner, chat string) ([]Info, error) {
	if owner == "" || chat == "" {
		return nil, nil
	}
	r.mu.Lock()
	all := make([]*Job, 0, len(r.jobs))
	for _, j := range r.jobs {
		all = append(all, j)
	}
	r.mu.Unlock()
	var out []Info
	for _, j := range all {
		in := j.Info()
		if in.Owner == owner && (in.Chat == chat || in.Parent != "" && in.Parent == chat) {
			out = append(out, in)
		}
	}
	slices.SortFunc(out, func(a, b Info) int {
		ra, rb := !a.State.Finished(), !b.State.Finished()
		switch {
		case ra != rb && ra:
			return -1
		case ra != rb:
			return 1
		}
		return b.Started.Compare(a.Started)
	})
	return out, nil
}

// Input writes to a job that the chat itself started. A parent chat gets
// ErrForbidden.
func (r *Registry) Input(owner, chat, id, text string, closeInput bool) (int, error) {
	return r.input(owner, chat, id, text, closeInput, 0)
}

func (r *Registry) input(owner, chat, id, text string, closeInput bool, timeout time.Duration) (int, error) {
	j := r.find(owner, chat, id, true)
	if j == nil {
		return 0, ErrUnknown
	}
	if j.Info().Chat != chat {
		return 0, ErrForbidden
	}
	n, err := j.input(text, closeInput, timeout)
	if err == nil && r.o.Audit != nil {
		r.o.Audit(AuditEvent{Kind: AuditInput, Info: j.Info(), Text: text[:min(len(text), n)]})
	}
	return n, err
}

// Kill kills a job of the chat or of a sub-agent of the chat.
func (r *Registry) Kill(owner, chat, id string, reason EndReason, sig syscall.Signal) (Info, error) {
	j := r.find(owner, chat, id, true)
	if j == nil {
		return Info{}, ErrUnknown
	}
	err := j.Kill(reason, sig)
	if err == nil && r.o.Audit != nil {
		r.o.Audit(AuditEvent{Kind: AuditKill, Info: j.Info(), Signal: sigName(sig), Reason: reason})
	}
	return j.Info(), err
}

func sigName(s syscall.Signal) string {
	switch s {
	case syscall.SIGINT:
		return "int"
	case syscall.SIGKILL:
		return "kill"
	}
	return "term"
}

// EndChat ends every live job started by the chat, and by its sub-agents when
// children is set.
func (r *Registry) EndChat(owner, chat string, reason EndReason) int {
	if owner == "" || chat == "" {
		return 0
	}
	r.mu.Lock()
	all := make([]*Job, 0, len(r.jobs))
	for _, j := range r.jobs {
		all = append(all, j)
	}
	r.mu.Unlock()
	n := 0
	for _, j := range all {
		in := j.Info()
		if in.Owner != owner || in.State.Finished() || in.Chat != chat && (in.Parent == "" || in.Parent != chat) {
			continue
		}
		j.End(reason)
		n++
	}
	return n
}

// Counts returns the live jobs of a chat, of an owner and of the process.
func (r *Registry) Counts(owner, chat string) (chatN, userN, nodeN int) {
	r.mu.Lock()
	all := make([]*Job, 0, len(r.jobs))
	for _, j := range r.jobs {
		all = append(all, j)
	}
	r.mu.Unlock()
	for _, j := range all {
		in := j.Info()
		if in.State.Finished() {
			continue
		}
		nodeN++
		if in.Owner == owner {
			userN++
			if in.Chat == chat {
				chatN++
			}
		}
	}
	return
}

// Sweep ends the jobs that reached their lifetime or that Options.Check says
// must end, and drops the finished jobs that are older than the retention or
// beyond the most a chat keeps. Start calls it every SweepEvery; tests call it
// by hand.
func (r *Registry) Sweep(ctx context.Context) {
	now := r.o.Now()
	r.mu.Lock()
	all := make([]*Job, 0, len(r.jobs))
	for _, j := range r.jobs {
		all = append(all, j)
	}
	r.mu.Unlock()
	finished := map[string][]*Job{}
	for _, j := range all {
		in := j.Info()
		if in.State.Finished() {
			if now.Sub(in.Ended) >= r.o.Retention {
				r.drop(in.ID)
				continue
			}
			finished[in.Owner+"\x00"+in.Chat] = append(finished[in.Owner+"\x00"+in.Chat], j)
			continue
		}
		switch {
		case in.State == StateStarting:
		case now.Sub(in.Started) >= r.o.Lifetime:
			j.End(EndLifetime)
		case r.o.Check != nil:
			if why := r.o.Check(ctx, in); why != "" {
				j.End(why)
			}
		}
	}
	var kept []*Job
	for _, list := range finished {
		slices.SortFunc(list, func(a, b *Job) int { return b.Info().Ended.Compare(a.Info().Ended) })
		if len(list) > r.o.KeepFinished {
			for _, j := range list[r.o.KeepFinished:] {
				r.drop(j.Info().ID)
			}
			list = list[:r.o.KeepFinished]
		}
		kept = append(kept, list...)
	}
	if len(kept) > KeepFinishedTotal {
		slices.SortFunc(kept, func(a, b *Job) int { return b.Info().Ended.Compare(a.Info().Ended) })
		for _, j := range kept[KeepFinishedTotal:] {
			r.drop(j.Info().ID)
		}
	}
}

func (r *Registry) drop(id string) {
	r.mu.Lock()
	delete(r.jobs, id)
	r.mu.Unlock()
}

// Start runs Sweep every SweepEvery until ctx ends.
func (r *Registry) Start(ctx context.Context) {
	go func() {
		t := time.NewTicker(SweepEvery)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				r.Sweep(ctx)
			}
		}
	}()
}

// Close ends every live job (EndShutdown), refuses new ones and waits for the
// jobs to finish until ctx ends.
func (r *Registry) Close(ctx context.Context) {
	r.mu.Lock()
	r.closed = true
	all := make([]*Job, 0, len(r.jobs))
	for _, j := range r.jobs {
		all = append(all, j)
	}
	r.mu.Unlock()
	for _, j := range all {
		if !j.Info().State.Finished() {
			j.End(EndShutdown)
		}
	}
	for _, j := range all {
		select {
		case <-j.done:
		case <-ctx.Done():
			return
		}
	}
}
