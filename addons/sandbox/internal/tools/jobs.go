// SPDX-License-Identifier: MIT
package tools

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/addons/internal/protocol"
	"github.com/mudler/LocalAI/core/services/studio/sandbox"
	"github.com/mudler/LocalAI/core/services/studio/sandbox/runner"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// The states, end reasons and unknown exit status the job tool reports
// (contract.JobInfo). They are the words of the host's job registry; the
// add-on does not import it, so it keeps to the workspace and sandbox
// packages of the core.
type jobState string

const (
	stateStarting jobState = "starting"
	stateRunning  jobState = "running"
	stateEnding   jobState = "ending"
	stateExited   jobState = "exited"
	stateKilled   jobState = "killed"

	endExit     runner.EndReason = "exit"
	endKilled   runner.EndReason = "killed"
	endStopped  runner.EndReason = "stopped"
	endLifetime runner.EndReason = "lifetime"

	exitUnknown = -1
)

func (s jobState) finished() bool { return s == stateExited || s == stateKilled || s == "failed" }

// ToolJob is the tool of the host's job tool (ExecutionProvider.Jobs). It is
// page-only: no model sees it; the host calls it with contract.JobArgs.
const ToolJob = "job"

// JobRunner starts a job in the sandbox of a chat and commits what it wrote
// when it ends. *runner.Manager implements it.
type JobRunner interface {
	StartJob(ctx context.Context, r runner.Request, launch runner.Launch) (release func(), err error)
	CheckpointJob(ctx context.Context, r runner.Request, title string) error
}

var _ JobRunner = (*runner.Manager)(nil)

// JobEgress opens the network of a job: the proxy lets the job's connections
// through under the job's operation while no call runs. *egress.Proxy
// implements it.
type JobEgress interface {
	BeginJob(user, conv, op string) (end func())
}

// Option configures the runtime.
type Option func(*runtime)

// WithJobEgress gives the jobs the network of the proxy. Without it a job
// reaches the network only while a call of its chat runs.
func WithJobEgress(e JobEgress) Option { return func(r *runtime) { r.jobs.egress = e } }

// WithJobClock sets the clock and the lifetimes of the jobs. Tests only.
func WithJobClock(now func() time.Time, lifetime, grace time.Duration) Option {
	return func(r *runtime) { r.jobs.now, r.jobs.lifetime, r.jobs.grace = now, lifetime, grace }
}

// inputWait is how long an input call waits for the job to read its input.
const inputWait = 5 * time.Second

// checkpointTimeout bounds the final checkpoint of a job.
const checkpointTimeout = 2 * time.Minute

// jobTable holds the jobs of the add-on process. A job belongs to one user and
// one chat: every call names both from the envelope, and a job of another user
// or chat is not found.
type jobTable struct {
	run      JobRunner
	egress   JobEgress
	now      func() time.Time
	lifetime time.Duration
	grace    time.Duration

	mu   sync.Mutex
	jobs map[string]*job
}

func newJobTable(run Runner) *jobTable {
	t := &jobTable{now: time.Now, lifetime: contract.JobLifetime, grace: contract.JobKillGrace, jobs: map[string]*job{}}
	if jr, ok := run.(JobRunner); ok {
		t.run = jr
	}
	return t
}

// job is one background process.
type job struct {
	id, user, conv, op, label string
	req                       runner.Request
	ring                      *ring

	mu      sync.Mutex
	state   jobState
	end     runner.EndReason
	exit    int
	started time.Time
	ended   time.Time
	cursor  int64 // where the model's last read ended
	proc    sandbox.Process
	kill    context.CancelFunc // SIGKILL to the process group
	killing *time.Timer
	life    *time.Timer
	// stdin is the write end of the job's standard input and stdinR the read
	// end the process got. An os pipe, not io.Pipe: a backend built on
	// os/exec copies any other reader in a goroutine that its Wait waits
	// for, so a job whose input stays open would never be seen to end.
	stdin  *os.File
	stdinR *os.File
	closed bool // stdin closed

	inMu sync.Mutex // one input write at a time
}

var jobIDEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

func newJobID() (string, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "job-" + strings.ToLower(jobIDEncoding.EncodeToString(b[:]))[:6], nil
}

// refuse is an answer the model reads.
func refuse(code, msg string) contract.JobReply {
	return contract.JobReply{Code: code, Error: msg}
}

// act serves one call of the job tool.
func (t *jobTable) act(ctx context.Context, user, conv, op string, call sandbox.CallOptions, raw map[string]json.RawMessage) (contract.Result, error) {
	b, _ := json.Marshal(raw)
	var a contract.JobArgs
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if dec.Decode(&a) != nil {
		return contract.Result{}, protocol.ErrBadRequest
	}
	var r contract.JobReply
	switch err := a.Validate(); {
	case err != nil:
		r = refuse(contract.JobCodeInvalid, err.Error())
	case t.run == nil:
		r = refuse(contract.JobCodeFailed, "This Sandbox cannot run background jobs.")
	default:
		t.prune()
		switch a.Verb {
		case contract.JobStart:
			r = t.start(ctx, user, conv, op, call, a)
		case contract.JobList:
			r = t.list(user, conv)
		case contract.JobRead:
			r = t.read(user, conv, a)
		case contract.JobInput:
			r = t.input(user, conv, a)
		case contract.JobKill:
			r = t.killJob(user, conv, a)
		}
	}
	out, err := json.Marshal(r)
	if err != nil {
		return contract.Result{}, err
	}
	return contract.Result{Payload: out}, nil
}

// find returns the job of a user and a chat. A job of another user or chat
// is unknown, as is a job that was never there.
func (t *jobTable) find(user, conv, id string) *job {
	t.mu.Lock()
	defer t.mu.Unlock()
	j := t.jobs[id]
	if j == nil || j.user != user || j.conv != conv {
		return nil
	}
	return j
}

func unknownJob(id string) contract.JobReply {
	return refuse(contract.JobCodeUnknown, fmt.Sprintf("No job %q in this chat.", id))
}

func (t *jobTable) live() int {
	n := 0
	for _, j := range t.jobs {
		j.mu.Lock()
		if !j.state.finished() {
			n++
		}
		j.mu.Unlock()
	}
	return n
}

func (t *jobTable) start(ctx context.Context, user, conv, op string, call sandbox.CallOptions, a contract.JobArgs) contract.JobReply {
	if call.ReadOnly {
		return refuse(contract.JobCodeInvalid, "A check that changes no file cannot start a job.")
	}
	t.mu.Lock()
	if t.live() >= contract.MaxJobsPerProcess {
		t.mu.Unlock()
		return refuse(contract.JobCodeLimit, fmt.Sprintf("Not started: this server runs %d jobs, the most it may.", contract.MaxJobsPerProcess))
	}
	var id string
	for range 8 {
		cand, err := newJobID()
		if err != nil {
			t.mu.Unlock()
			return refuse(contract.JobCodeFailed, "Not started: no job ID could be made.")
		}
		if t.jobs[cand] == nil {
			id = cand
			break
		}
	}
	if id == "" {
		t.mu.Unlock()
		return refuse(contract.JobCodeFailed, "Not started: no job ID could be made.")
	}
	now := t.now()
	j := &job{id: id, user: user, conv: conv, op: op, label: a.Label, ring: newRing(contract.JobRingBytes, t.now),
		state: stateStarting, exit: exitUnknown, started: now}
	j.req = runner.Request{User: user, Conversation: conv, Operation: op, Tool: ToolJob, Call: call}
	// The slot is taken before the sandbox is made, so two starts cannot pass
	// the limit together.
	t.jobs[id] = j
	t.mu.Unlock()

	var stopEgress func()
	release, err := t.run.StartJob(ctx, j.req, func(sb sandbox.Sandbox) (runner.EndFunc, error) {
		pr, pw, err := os.Pipe()
		if err != nil {
			return nil, err
		}
		pctx, kill := context.WithCancel(context.Background())
		if t.egress != nil {
			// Before the process starts, so its first connection finds it.
			stopEgress = t.egress.BeginJob(user, conv, op)
		}
		proc, err := sb.Exec(pctx, sandbox.Cmd{Args: []string{"bash", "-lc", a.Command}, Dir: runner.GuestHome, Stdin: pr, Stdout: j.ring, Stderr: j.ring})
		if err != nil {
			kill()
			_, _ = pw.Close(), pr.Close()
			if stopEgress != nil {
				stopEgress()
			}
			return nil, err
		}
		j.mu.Lock()
		j.proc, j.kill, j.stdin, j.stdinR, j.state = proc, kill, pw, pr, stateRunning
		j.mu.Unlock()
		return func(reason runner.EndReason) { t.endJob(j, reason, syscall.SIGTERM) }, nil
	})
	if err != nil {
		t.mu.Lock()
		delete(t.jobs, id)
		t.mu.Unlock()
		if errors.Is(err, runner.ErrBusy) {
			return refuse(contract.JobCodeBusy, "Not started: you have sandboxes with work in other chats; end a job in another chat first.")
		}
		return refuse(contract.JobCodeFailed, "Not started: "+err.Error())
	}
	j.mu.Lock()
	j.life = time.AfterFunc(t.lifetime, func() { t.endJob(j, endLifetime, syscall.SIGTERM) })
	j.mu.Unlock()
	go t.wait(j, release, stopEgress)
	return contract.JobReply{OK: true, Job: j.info(t.now())}
}

// endJob signals a live job for a reason, and sends SIGKILL to its group
// after the grace time. The first reason stays. It returns quickly and does
// not call the runner: the runner may hold its locks.
func (t *jobTable) endJob(j *job, reason runner.EndReason, sig syscall.Signal) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.state.finished() || j.proc == nil {
		return
	}
	if j.end == "" {
		j.end = reason
	}
	j.state = stateEnding
	if sig == syscall.SIGKILL {
		j.kill()
		return
	}
	_ = j.proc.Signal(sig)
	if j.killing == nil {
		kill := j.kill
		j.killing = time.AfterFunc(t.grace, kill)
	}
}

// wait waits for the process, commits what it wrote and records how it ended.
// The output and the exit status do not depend on any connection of the host.
func (t *jobTable) wait(j *job, release, stopEgress func()) {
	code, err := j.proc.Wait()
	if stopEgress != nil {
		stopEgress()
	}
	j.mu.Lock()
	// An input still waiting returns, and a backend that feeds the input
	// from the read end stops.
	_, _ = j.stdin.Close(), j.stdinR.Close()
	j.kill()
	if j.life != nil {
		j.life.Stop()
	}
	if j.killing != nil {
		j.killing.Stop()
	}
	end := j.end
	j.mu.Unlock()
	switch {
	case err == nil:
	case errors.Is(err, context.Canceled):
		code = 128 + int(syscall.SIGKILL) // the job was killed through its context
	default:
		code = exitUnknown
	}
	// The final checkpoint, under the entry lock, before the sandbox is let go.
	title := "Job " + j.id
	if j.label != "" {
		title += ": " + j.label
	}
	ctx, cancel := context.WithTimeout(context.Background(), checkpointTimeout)
	var ce *runner.ConflictsError
	switch cerr := t.run.CheckpointJob(ctx, j.req, title); {
	case errors.As(cerr, &ce):
		// The files were saved; some conflict with newer changes. That is
		// not a failure.
		xlog.Warn("sandbox: the files of a job conflict with newer changes", "job", j.id, "conflicts", len(ce.Conflicts), "error", cerr)
	case cerr != nil:
		xlog.Warn("sandbox: the final checkpoint of a job failed", "job", j.id, "error", cerr)
	}
	cancel()
	release()
	j.mu.Lock()
	j.exit, j.ended = code, t.now()
	switch end {
	case "":
		j.end, j.state = endExit, stateExited
	case endKilled, endStopped:
		j.state = stateKilled
	default:
		j.state = stateExited
	}
	j.mu.Unlock()
}

// info is the state of a job. The lock is not held.
func (j *job) info(now time.Time) *contract.JobInfo {
	total, last := j.ring.state()
	j.mu.Lock()
	defer j.mu.Unlock()
	in := &contract.JobInfo{ID: j.id, Label: j.label, State: string(j.state), End: string(j.end), Exit: j.exit,
		Started: j.started, Ended: j.ended, LastOutput: last, Total: total}
	if !j.state.finished() {
		in.Exit = exitUnknown
		if idle := now.Sub(last); idle > 0 {
			in.IdleMS = idle.Milliseconds()
		}
	}
	return in
}

func (t *jobTable) list(user, conv string) contract.JobReply {
	t.mu.Lock()
	var mine []*job
	for _, j := range t.jobs {
		if j.user == user && j.conv == conv {
			mine = append(mine, j)
		}
	}
	t.mu.Unlock()
	now := t.now()
	infos := make([]contract.JobInfo, 0, len(mine))
	for _, j := range mine {
		infos = append(infos, *j.info(now))
	}
	// Live jobs first, oldest first; then the newest finished.
	slices.SortFunc(infos, func(a, b contract.JobInfo) int {
		switch {
		case !a.Finished() && b.Finished():
			return -1
		case a.Finished() && !b.Finished():
			return 1
		case !a.Finished():
			return a.Started.Compare(b.Started)
		}
		return b.Ended.Compare(a.Ended)
	})
	if len(infos) > contract.MaxJobsListed {
		infos = infos[:contract.MaxJobsListed]
	}
	return contract.JobReply{OK: true, Jobs: infos}
}

func (t *jobTable) read(user, conv string, a contract.JobArgs) contract.JobReply {
	j := t.find(user, conv, a.ID)
	if j == nil {
		return unknownJob(a.ID)
	}
	j.mu.Lock()
	since := j.cursor
	j.mu.Unlock()
	if a.Since != nil {
		since = *a.Since
	}
	max := a.MaxBytes
	if max == 0 {
		max = contract.DefaultJobReadBytes
	}
	for {
		data, from, next := j.ring.page(since, max)
		r := contract.JobReply{OK: true, Job: j.info(t.now()), Output: strings.ToValidUTF8(string(data), "�"), From: from, Next: next}
		if a.Observe {
			r.Tail = strings.ToValidUTF8(string(j.ring.tail(contract.JobTailBytes)), "�")
		}
		// Escapes can make the answer larger than the bytes it carries; the
		// host takes a bounded answer, so a large one reads less.
		if b, err := json.Marshal(r); err == nil && len(b) > contract.MaxJobReplyBytes && max > contract.MinJobReadBytes {
			max = max / 2
			continue
		}
		if !a.Observe {
			j.mu.Lock()
			j.cursor = next
			j.mu.Unlock()
		}
		return r
	}
}

func endedText(in *contract.JobInfo) string {
	if in.Exit >= 0 {
		return fmt.Sprintf("%s has ended (exited with status %d).", in.ID, in.Exit)
	}
	return fmt.Sprintf("%s has ended (%s).", in.ID, in.End)
}

func (t *jobTable) input(user, conv string, a contract.JobArgs) contract.JobReply {
	j := t.find(user, conv, a.ID)
	if j == nil {
		return unknownJob(a.ID)
	}
	in := j.info(t.now())
	j.mu.Lock()
	ended, closed, w := j.state.finished() || j.state == stateEnding, j.closed, j.stdin
	j.mu.Unlock()
	if ended {
		return refuse(contract.JobCodeEnded, endedText(in))
	}
	if closed {
		return refuse(contract.JobCodeEnded, fmt.Sprintf("The input of %s is closed.", a.ID))
	}
	if !j.inMu.TryLock() {
		return refuse(contract.JobCodeBusy, fmt.Sprintf("Not written: %s has not read the earlier input yet.", a.ID))
	}
	if a.CloseInput {
		j.mu.Lock()
		j.closed = true
		j.mu.Unlock()
	}
	done := make(chan error, 1)
	go func() {
		defer j.inMu.Unlock()
		var err error
		if a.Input != "" {
			_, err = io.WriteString(w, a.Input)
		}
		if a.CloseInput && err == nil {
			err = w.Close()
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			return refuse(contract.JobCodeEnded, fmt.Sprintf("Not written: %s does not take input any more.", a.ID))
		}
		return contract.JobReply{OK: true, Job: j.info(t.now()), Written: len(a.Input)}
	case <-time.After(inputWait):
		// The write goes on as the job reads; the next input waits for it.
		return contract.JobReply{OK: true, Job: j.info(t.now()), Written: len(a.Input), Pending: true}
	}
}

func (t *jobTable) killJob(user, conv string, a contract.JobArgs) contract.JobReply {
	j := t.find(user, conv, a.ID)
	if j == nil {
		return unknownJob(a.ID)
	}
	if in := j.info(t.now()); in.Finished() {
		return refuse(contract.JobCodeEnded, endedText(in))
	}
	sig := syscall.SIGTERM
	switch a.Signal {
	case "int":
		sig = syscall.SIGINT
	case "kill":
		sig = syscall.SIGKILL
	}
	reason := endKilled
	if a.Reason != "" {
		// The host ends the job for a reason of its own (the person's Stop, a
		// deleted chat, commands switched off): the end says so.
		reason = runner.EndReason(a.Reason)
	}
	t.endJob(j, reason, sig)
	return contract.JobReply{OK: true, Job: j.info(t.now())}
}

// prune drops the finished jobs older than contract.JobKeepFinished, and the
// oldest finished jobs of a chat beyond contract.MaxFinishedJobsPerChat.
func (t *jobTable) prune() {
	now := t.now()
	t.mu.Lock()
	defer t.mu.Unlock()
	byChat := map[string][]*job{}
	for id, j := range t.jobs {
		j.mu.Lock()
		fin, ended := j.state.finished(), j.ended
		j.mu.Unlock()
		if !fin {
			continue
		}
		if now.Sub(ended) > contract.JobKeepFinished {
			delete(t.jobs, id)
			continue
		}
		k := j.user + "\x00" + j.conv
		byChat[k] = append(byChat[k], j)
	}
	for _, list := range byChat {
		if len(list) <= contract.MaxFinishedJobsPerChat {
			continue
		}
		slices.SortFunc(list, func(a, b *job) int { return b.ended.Compare(a.ended) })
		for _, j := range list[contract.MaxFinishedJobsPerChat:] {
			delete(t.jobs, j.id)
		}
	}
}
