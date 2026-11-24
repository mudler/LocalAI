// SPDX-License-Identifier: MIT
package contract

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// The job tool of an execution provider (ExecutionProvider.Jobs). The host's
// own job tool calls it through Act, with the chat as the resource and the
// person as the actor, and with the arguments below. The add-on keeps each job
// in its process: the job table, the output ring and the process. The host
// keeps the per-chat and per-user limits; the add-on keeps the per-process
// limit and the lifetime.

// The verbs of the job tool. The key is "verb": "action" is a name of the
// envelope that no tool schema may use (ForbiddenToolProperty).
const (
	JobStart = "start"
	JobList  = "list"
	JobRead  = "read"
	JobInput = "input"
	JobKill  = "kill"
)

// The limits of a job. They are constants in this release, not settings; the
// host and the add-on both apply them.
const (
	// MaxJobInputBytes is the most bytes one input call writes.
	MaxJobInputBytes = 2000
	// MaxJobLabelChars bounds the label the person sees.
	MaxJobLabelChars = 80
	// MaxJobCommandBytes bounds the script of a job.
	MaxJobCommandBytes = 64 << 10
	// JobRingBytes is the output a job keeps: stdout and stderr together, the
	// last bytes written.
	JobRingBytes = 256 << 10
	// The size of one read: 16 KiB by default, 256 bytes to 64 KiB.
	DefaultJobReadBytes = 16 << 10
	MinJobReadBytes     = 256
	MaxJobReadBytes     = 64 << 10
	// JobTailBytes is the size of the tail of an observe read.
	JobTailBytes = 2000
	// JobLifetime is the longest a job runs. Then it gets SIGTERM, and
	// SIGKILL after JobKillGrace.
	JobLifetime  = 2 * time.Hour
	JobKillGrace = 10 * time.Second
	// JobKeepFinished is how long a finished job stays readable, and
	// MaxFinishedJobsPerChat how many finished jobs of one chat are kept.
	JobKeepFinished        = 30 * time.Minute
	MaxFinishedJobsPerChat = 16
	// MaxJobsPerProcess is the most live jobs one add-on process runs.
	MaxJobsPerProcess = 32
	// MaxJobsListed bounds the jobs of one answer: the live jobs of a chat
	// and its finished ones fit, with room.
	MaxJobsListed = 64
	// MaxJobReplyBytes bounds the encoded answer of the job tool, below the
	// size of a tool result the host accepts.
	MaxJobReplyBytes = 112 << 10
)

// The states and the end reasons the job tool reports. They match the job
// registry of the host (core/services/studio/bgjob).
var (
	jobStates = map[string]bool{"starting": true, "running": true, "ending": true, "exited": true, "killed": true, "failed": true}
	jobEnds   = map[string]bool{"": true, "exit": true, "killed": true, "stopped": true, "lifetime": true, "sandbox_remade": true, "shutdown": true, "start_failed": true,
		"chat_deleted": true, "agent_ended": true, "shell_off": true, "group_off": true, "fenced": true}
	// jobKillReasons are the end reasons the host may give with a kill. The
	// add-on records the reason as the job's end; with none it records
	// "killed". The reasons of the add-on's own ends (lifetime, shutdown,
	// sandbox_remade) are not the host's to give.
	jobKillReasons = map[string]bool{"": true, "stopped": true, "chat_deleted": true, "agent_ended": true, "shell_off": true, "group_off": true, "fenced": true}
	jobSignals     = map[string]bool{"": true, "term": true, "int": true, "kill": true}
)

// The refusal codes of the job tool. The text in JobReply.Error is for the
// model; the code is for the host.
const (
	// JobCodeUnknown: no such job for this person and chat.
	JobCodeUnknown = "unknown"
	// JobCodeEnded: the job has ended, so it takes no input or kill.
	JobCodeEnded = "ended"
	// JobCodeLimit: the add-on runs as many jobs as it may.
	JobCodeLimit = "limit"
	// JobCodeBusy: the sandbox of the chat cannot be had now.
	JobCodeBusy = "busy"
	// JobCodeInvalid: the arguments are not valid.
	JobCodeInvalid = "invalid"
	// JobCodeFailed: the job could not be started.
	JobCodeFailed = "failed"
)

var jobCodes = map[string]bool{JobCodeUnknown: true, JobCodeEnded: true, JobCodeLimit: true, JobCodeBusy: true, JobCodeInvalid: true, JobCodeFailed: true}

// jobIDShape is "job-" and six base32 characters. An ID is a key of the add-on's
// table, never a path; the shape is checked on both sides anyway.
var jobIDShape = regexp.MustCompile(`^job-[a-z2-7]{6}$`)

// ValidJobID reports whether id has the shape of a job ID.
func ValidJobID(id string) bool { return jobIDShape.MatchString(id) }

// JobArgs are the arguments of the job tool, besides "tool" and the host's
// ExecutionCall. The host builds them; a model never writes them.
type JobArgs struct {
	Verb string `json:"verb"`
	// ID names the job of read, input and kill.
	ID string `json:"id,omitempty"`
	// Command and Label are for start.
	Command string `json:"command,omitempty"`
	Label   string `json:"label,omitempty"`
	// Since is the absolute byte offset of a read. Nil: where the last read
	// of the model ended.
	Since *int64 `json:"since,omitempty"`
	// MaxBytes bounds a read: zero means DefaultJobReadBytes.
	MaxBytes int `json:"maxBytes,omitempty"`
	// Observe makes a read that does not move the model's cursor and that
	// carries the tail: the check of a wait.
	Observe bool `json:"observe,omitempty"`
	// Input and CloseInput are for input.
	Input      string `json:"input,omitempty"`
	CloseInput bool   `json:"closeInput,omitempty"`
	// Signal is for kill: term (default), int or kill.
	Signal string `json:"signal,omitempty"`
	// Reason is for kill: why the host ends the job (stopped, chat_deleted,
	// agent_ended, shell_off, group_off, fenced). The add-on records it as the
	// job's end. Empty is a kill of the model: the end is "killed".
	Reason string `json:"reason,omitempty"`
}

// ErrJobArgs marks arguments of the job tool that are not valid.
var ErrJobArgs = errors.New("invalid job arguments")

// Validate checks the arguments of one verb. Fields of another verb must be
// empty, so a host mistake does not pass silently.
func (a JobArgs) Validate() error {
	bad := func(why string) error { return fmt.Errorf("%w: %s", ErrJobArgs, why) }
	if a.ID != "" && !ValidJobID(a.ID) {
		return bad("id")
	}
	needID := a.Verb == JobRead || a.Verb == JobInput || a.Verb == JobKill
	if needID != (a.ID != "") {
		return bad("id")
	}
	if a.Verb != JobStart && (a.Command != "" || a.Label != "") {
		return bad("command and label are for start")
	}
	if a.Verb != JobRead && (a.Since != nil || a.MaxBytes != 0 || a.Observe) {
		return bad("since, maxBytes and observe are for read")
	}
	if a.Verb != JobInput && (a.Input != "" || a.CloseInput) {
		return bad("input is for input")
	}
	if a.Verb != JobKill && (a.Signal != "" || a.Reason != "") {
		return bad("signal and reason are for kill")
	}
	switch a.Verb {
	case JobStart:
		if strings.TrimSpace(a.Command) == "" || len(a.Command) > MaxJobCommandBytes || strings.ContainsRune(a.Command, 0) {
			return bad("command")
		}
		if !ValidJobLabel(a.Label) {
			return bad("label")
		}
	case JobList:
	case JobRead:
		if a.Since != nil && *a.Since < 0 {
			return bad("since")
		}
		if a.MaxBytes != 0 && (a.MaxBytes < MinJobReadBytes || a.MaxBytes > MaxJobReadBytes) {
			return bad("maxBytes")
		}
	case JobInput:
		if len(a.Input) > MaxJobInputBytes {
			return bad("input")
		}
		if a.Input == "" && !a.CloseInput {
			return bad("input is empty")
		}
	case JobKill:
		if !jobSignals[a.Signal] {
			return bad("signal")
		}
		if !jobKillReasons[a.Reason] {
			return bad("reason")
		}
	default:
		return bad("verb")
	}
	return nil
}

// ValidJobLabel accepts a short single-line label, or none.
func ValidJobLabel(s string) bool {
	if !utf8.ValidString(s) || utf8.RuneCountInString(s) > MaxJobLabelChars {
		return false
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) || r == ' ' || r == ' ' || (r >= '‪' && r <= '‮') || (r >= '⁦' && r <= '⁩') {
			return false
		}
	}
	return true
}

// JobInfo is what the add-on tells of one job. It never holds the command.
type JobInfo struct {
	ID    string `json:"id"`
	Label string `json:"label,omitempty"`
	State string `json:"state"`
	End   string `json:"end,omitempty"`
	// Exit is the exit status: 128+n for signal n, -1 when not known.
	Exit       int       `json:"exit"`
	Started    time.Time `json:"started"`
	Ended      time.Time `json:"ended,omitzero"`
	LastOutput time.Time `json:"lastOutput"`
	// Total is the number of output bytes since the start, kept or not.
	Total int64 `json:"total"`
	// IdleMS is how long the job has been quiet, by the add-on's clock. Zero
	// for a finished job.
	IdleMS int64 `json:"idleMs,omitempty"`
}

// Finished reports a final state.
func (j JobInfo) Finished() bool {
	return j.State == "exited" || j.State == "killed" || j.State == "failed"
}

func (j JobInfo) validate() error {
	if !ValidJobID(j.ID) || !ValidJobLabel(j.Label) || !jobStates[j.State] || !jobEnds[j.End] || j.Exit < -1 || j.Exit > 255 || j.Total < 0 || j.IdleMS < 0 {
		return errors.New("invalid job")
	}
	if j.Finished() == j.Ended.IsZero() {
		return errors.New("invalid job times")
	}
	return nil
}

// JobReply is the payload of the job tool. A refusal has OK false, a Code and
// an Error the model can read; nothing else is set.
type JobReply struct {
	OK    bool   `json:"ok"`
	Code  string `json:"code,omitempty"`
	Error string `json:"error,omitempty"`
	// Job is the job of start, read, input and kill.
	Job *JobInfo `json:"job,omitempty"`
	// Jobs are the jobs of the chat, for list: live ones first, then the
	// newest finished.
	Jobs []JobInfo `json:"jobs,omitempty"`
	// Output, From and Next are the bytes of a read: Output starts at the
	// absolute offset From (bytes [since, From) are no longer kept) and Next
	// is the offset to read on. Invalid UTF-8 in the output is replaced, so
	// Next is not From plus len(Output) in general.
	Output string `json:"output,omitempty"`
	From   int64  `json:"from,omitempty"`
	Next   int64  `json:"next,omitempty"`
	// Tail is the end of the output, for an observe read.
	Tail string `json:"tail,omitempty"`
	// Written is the bytes an input call wrote, and Pending says the job had
	// not read them all after 5 s: the rest is written as the job reads.
	Written int  `json:"written,omitempty"`
	Pending bool `json:"pending,omitempty"`
}

// ErrJobReply marks an answer of the job tool that is not valid: the host
// shows it to nobody.
var ErrJobReply = errors.New("the add-on's job answer is not valid")

// Validate checks an answer to a call with the given arguments. The answer
// comes from an add-on process; the host trusts none of its sizes.
func (r JobReply) Validate(a JobArgs) error {
	bad := func(why string) error { return fmt.Errorf("%w: %s", ErrJobReply, why) }
	if !r.OK {
		if !jobCodes[r.Code] || r.Error == "" || len(r.Error) > 1024 || !utf8.ValidString(r.Error) || r.Job != nil || r.Jobs != nil || r.Output != "" || r.Tail != "" || r.Written != 0 || r.Pending {
			return bad("refusal")
		}
		return nil
	}
	if r.Code != "" || r.Error != "" {
		return bad("an answer with a refusal")
	}
	if len(r.Jobs) > MaxJobsListed || len(r.Output) > 3*MaxJobReadBytes || len(r.Tail) > 3*JobTailBytes || r.Written < 0 || r.Written > MaxJobInputBytes {
		return bad("size")
	}
	if r.From < 0 || r.Next < r.From {
		return bad("offsets")
	}
	if a.Verb == JobList {
		if r.Job != nil {
			return bad("list has no job")
		}
		seen := map[string]bool{}
		for _, j := range r.Jobs {
			if err := j.validate(); err != nil || seen[j.ID] {
				return bad("job")
			}
			seen[j.ID] = true
		}
		return nil
	}
	if r.Job == nil || r.Jobs != nil {
		return bad("job")
	}
	if err := r.Job.validate(); err != nil {
		return bad(err.Error())
	}
	if a.ID != "" && r.Job.ID != a.ID {
		return bad("another job")
	}
	if a.Verb == JobRead {
		if r.Next > r.Job.Total || r.Next-r.From > MaxJobReadBytes {
			return bad("offsets")
		}
	} else if r.Output != "" || r.From != 0 || r.Next != 0 || r.Tail != "" {
		return bad("output outside read")
	}
	return nil
}
