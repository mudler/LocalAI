// SPDX-License-Identifier: MIT
package assetpipeline

type EventKind string

const (
	CreatedKind         EventKind = "created"
	ConfigurationKind   EventKind = "configuration"
	ForkedKind          EventKind = "forked"
	StartedKind         EventKind = "started"
	ProgressKind        EventKind = "progress"
	CompletedKind       EventKind = "completed"
	SelectionKind       EventKind = "selection"
	RetryKind           EventKind = "retry"
	WarningKind         EventKind = "warning"
	RunAllCompletedKind EventKind = "run-all-completed"
	SourceKind          EventKind = "source"
	RunNodeKind         EventKind = "run-node"
	RunAllKind          EventKind = "run-all"
	CancellingKind      EventKind = "cancelling"
	InterruptedKind     EventKind = "interrupted"
)

type CommandAction string

const (
	CreateCommand    CommandAction = "create"
	ConfigureCommand CommandAction = "configure"
	SelectCommand    CommandAction = "select"
	ForkCommand      CommandAction = "fork"
	RetryCommand     CommandAction = "retry"
	RunNodeCommand   CommandAction = "run-node"
	RunAllCommand    CommandAction = "run-all"
	ContinueCommand  CommandAction = "continue"
	RunCommand       CommandAction = "run"
	CancelCommand    CommandAction = "cancel"
)
