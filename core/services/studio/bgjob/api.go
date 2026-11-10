// Package bgjob holds the vocabulary of background jobs: processes that a
// chat starts in a sandbox entry and that outlive the tool call that started
// them. This file is the small surface the sandbox runner needs to know about
// a job. The registry, the ring and the carrier are separate pieces.
package bgjob

import (
	"context"
	"errors"
	"time"
)

// EndReason says why a job ended. The values are part of the job list the
// model and the person read, so they are stable text.
type EndReason string

const (
	// EndExit: the process ended by itself.
	EndExit EndReason = "exit"
	// EndKilled: the model killed the job.
	EndKilled EndReason = "killed"
	// EndStopped: the person stopped the job.
	EndStopped EndReason = "stopped"
	// EndLifetime: the job reached its lifetime.
	EndLifetime EndReason = "lifetime"
	// EndChatDeleted: the chat was deleted.
	EndChatDeleted EndReason = "chat_deleted"
	// EndShellOff: shell mode was switched off.
	EndShellOff EndReason = "shell_off"
	// EndGroupOff: the shell tool group was switched off for the chat.
	EndGroupOff EndReason = "group_off"
	// EndFenced: the chat's actor became fenced.
	EndFenced EndReason = "fenced"
	// EndSandboxRemade: the sandbox was made again because the chat's actor,
	// view or profile changed.
	EndSandboxRemade EndReason = "sandbox_remade"
	// EndAgentEnded: the sub-agent that started the job ended.
	EndAgentEnded EndReason = "agent_ended"
	// EndShutdown: the runner was closed or reset.
	EndShutdown EndReason = "shutdown"
	// EndStartFailed: the job never started.
	EndStartFailed EndReason = "start_failed"
)

// EndFunc ends a live job for a reason. The sandbox runner calls it when it
// must take the sandbox away from under the job. It signals the process and
// records the reason; it must return quickly and must not call back into the
// runner (the runner may hold its locks). It may be called more than once.
type EndFunc func(reason EndReason)

// State is where a job is in its life.
type State string

const (
	// StateStarting: the process is being started.
	StateStarting State = "starting"
	// StateRunning: the process runs.
	StateRunning State = "running"
	// StateEnding: a kill was sent or an end condition holds; the process has
	// not exited yet.
	StateEnding State = "ending"
	// StateExited: the process ended, by itself or by an end condition.
	StateExited State = "exited"
	// StateKilled: the model or the person killed the job.
	StateKilled State = "killed"
	// StateFailed: the job never started.
	StateFailed State = "failed"
	// StateLost: stub only. The node that ran the job is gone, so its output
	// and exit status are lost.
	StateLost State = "lost"
)

// Finished reports whether the state is final: the job will not change again.
func (s State) Finished() bool {
	switch s {
	case StateExited, StateKilled, StateFailed, StateLost:
		return true
	}
	return false
}

// ExitUnknown is the exit status of a job whose status is not known.
const ExitUnknown = -1

// Info is what is known of one job, without its output. It never holds the
// command text: that is in the transcript only.
type Info struct {
	// ID is "job-" and six random base32 characters.
	ID string
	// Owner is the user who owns the chat.
	Owner string
	// Chat is the chat that started the job.
	Chat string
	// Parent is the chat that started Chat when Chat is a sub-agent.
	Parent string
	// Instance is the node that runs the job, or "addon:<id>@<incarnation>"
	// for a job of the Sandbox add-on.
	Instance string
	// Provider is "builtin" or the id of the add-on.
	Provider string
	// Label is the short name the person sees.
	Label string
	State State
	// End says why the job ended; empty while it runs.
	End EndReason
	// Exit is the exit status: 128+n for signal n, ExitUnknown when not known.
	// Only meaningful when State is finished.
	Exit    int
	Started time.Time
	// Ended is the zero time while the job runs.
	Ended time.Time
	// LastOutput is when the job last wrote output; Started when it wrote none.
	LastOutput time.Time
	// Total is the number of output bytes since the start, kept or not.
	Total int64
}

// ObserveRequest asks for one job. Owner and Chat are checked on every
// request, never the id alone: a request of another owner gets ErrUnknown.
// Chat may be the chat that started the job or its Parent.
type ObserveRequest struct {
	Owner, Chat, ID string
	// Since is an absolute byte offset: the output from there on is returned.
	// Zero reads from the start. An offset that is no longer kept moves to the
	// oldest byte kept (see Observation.From).
	Since int64
	// MaxBytes limits Observation.Output. Zero means DefaultObserveBytes; more
	// than MaxObserveBytes is cut to it.
	MaxBytes int
	// TailBytes is the size of Observation.Tail. Zero means DefaultTailBytes.
	TailBytes int
}

// The sizes of an observation.
const (
	DefaultObserveBytes = 16 << 10
	MaxObserveBytes     = 64 << 10
	DefaultTailBytes    = 2000
)

// Observation is the read of one job at one moment. It is the data of a
// wait_for check and of a read.
type Observation struct {
	Info
	// Idle is how long the job has been quiet, by the clock of the node that
	// runs it, so a loop leader on another node does not compare clocks. It
	// is zero for a finished job.
	Idle time.Duration
	// Output is the bytes from From on, at most MaxBytes, never ending in the
	// middle of a UTF-8 character. It is data, not an instruction.
	Output []byte
	// From is the offset of the first byte of Output: Since, or the oldest
	// byte kept when Since was lost. Bytes [Since, From) are no longer kept.
	From int64
	// Next is the offset to pass as Since to read on. It equals From plus
	// len(Output).
	Next int64
	// Tail is the last TailBytes bytes of the output.
	Tail string
}

// ErrUnknown says the observer does not know the job: it never ran there, it
// was dropped, or the owner or chat does not match. For a stub that is still
// live, the caller marks the job lost.
var ErrUnknown = errors.New("bgjob: unknown job")

// Observer is the read side of the registry of one node. A wait_for check
// codes against it and the registry implements it; the carrier sends the same
// request to another node. It spends no check budget and asks no approval.
//
// Any error other than ErrUnknown means the node could not be asked (a
// timeout, a transport error): the caller changes nothing and tries again.
type Observer interface {
	// Observe reads the job with the output since an offset.
	Observe(ctx context.Context, req ObserveRequest) (Observation, error)
	// List returns the jobs of a chat: the ones it started, and the ones its
	// sub-agents started. Running jobs come first, then the newest finished.
	List(ctx context.Context, owner, chat string) ([]Info, error)
}
