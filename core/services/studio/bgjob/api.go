// Package bgjob holds the vocabulary of background jobs: processes that a
// chat starts in a sandbox entry and that outlive the tool call that started
// them. This file is the small surface the sandbox runner needs to know about
// a job. The registry, the ring and the carrier are separate pieces.
package bgjob

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
