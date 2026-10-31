// Package session runs chats: an agent harness whose tools run in a sandbox
// that sees the user's workspace.
package session

import (
	"encoding/json"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// EventKind tells what an Event carries.
type EventKind string

// Event kinds. Every turn ends with KindCheckpoint then KindTurnEnd.
const (
	KindTurnStart  EventKind = "turn_start"
	KindText       EventKind = "text"
	KindToolStart  EventKind = "tool_start"
	KindToolEnd    EventKind = "tool_end"
	KindCheckpoint EventKind = "checkpoint"
	KindTurnEnd    EventKind = "turn_end"
	KindError      EventKind = "error"
	// KindReasoning carries a delta of the model's reasoning. It is for
	// display only and has its own bound (see Events).
	KindReasoning EventKind = "reasoning"
	// KindPendingDelivered says that messages the user sent during the turn
	// reached the model. Event.IDs names them.
	KindPendingDelivered EventKind = "pending_delivered"
	// KindSubAgentDone says that a sub-agent run ended. Event.IDs holds its
	// conversation id and Text its title.
	KindSubAgentDone EventKind = "sub_agent_done"
	// KindCompacted says that the harness shrank the model's context to make
	// room. The transcript keeps every message; only what the model sees
	// shrinks. Event.Before and Event.After are token counts.
	KindCompacted EventKind = "compacted"
	// KindUsage reports what one model request used: Event.Usage holds the
	// size of the context the request sent, the context limit, and the
	// generation speed. The interface draws the context meter from it.
	KindUsage EventKind = "usage"
	// KindSubAgentStarted says that a sub-agent run began. Event.IDs holds its
	// conversation id and Text its title.
	KindSubAgentStarted EventKind = "sub_agent_started"
	// KindTriggerProposed says that the chat proposed a trigger, which waits
	// for the person to confirm, edit or dismiss it. Event.IDs holds the
	// proposal id and Text the trigger's name.
	KindTriggerProposed EventKind = "trigger_proposed"
	// KindHostExecConsent asks the person to allow one command of the built-in
	// bash on this machine: IDs holds the ID to answer with, Text the command.
	// The chat record keeps the card (ChatRecord.HostConsent) while it waits;
	// the call ends with "no answer" after a while.
	KindHostExecConsent EventKind = "host_exec_consent"
	// KindFiles says that the files of the chat may have changed: files were
	// attached, a turn or a tool changed files, or the chat folder was renamed.
	// It carries nothing; read the file index again.
	KindFiles EventKind = "files"
	// KindEgressConsent asks the person whether a command of the chat's
	// Sandbox may reach a host: IDs holds the card ID, Text the host:port. The
	// card is kept with the chat (ChatRecord.Egress), so a reload shows it.
	KindEgressConsent EventKind = "egress_consent"
	// KindEgressCard says that the network of the chat changed: a card was
	// answered or expired (IDs holds its ID), or the preset or a rule changed
	// (no ID). Read GET /api/chats/{id}/egress again.
	KindEgressCard EventKind = "egress_card"
	// KindPaused says that the tool loop of the turn reached its step limit.
	// The model was asked for an answer from what it had, and the turn ends
	// as paused; another message lets the work go on. Event.Steps is the limit.
	KindPaused EventKind = "paused"
	// KindCleared says that the person cleared the model's context: the
	// transcript has a new marker and the next turn starts fresh. Read the
	// transcript again.
	KindCleared EventKind = "cleared"
)

// Event is one thing that happened in a chat.
type Event struct {
	Kind   EventKind
	Turn   int
	Text   string       // text, reasoning: a delta; error: a fixed reason
	Tool   *ToolEvent   // tool_start, tool_end
	Change *ChangeEvent // checkpoint
	IDs    []string     // pending_delivered: the ids of the messages
	// Fatal marks the error that ended the turn (error only). Other errors, such
	// as a failed save or a recovered tool panic, leave the turn to finish.
	Fatal bool
	// Code, Model, Retry and Detail say why the turn failed (fatal error only).
	// Code is one of the Code* constants, Model names the model when the cause
	// is about one, Retry is the seconds to wait for a model in cooldown and
	// Detail is the error text with addresses and secrets removed.
	Code   string `json:",omitempty"`
	Model  string `json:",omitempty"`
	Retry  int    `json:",omitempty"`
	Detail string `json:",omitempty"`
	// Before and After are the size of the model's context in tokens, before
	// and after a compaction (compacted only).
	Before, After int
	// Steps is the step limit that the turn reached (paused only).
	Steps int `json:",omitempty"`
	// Usage is set on KindUsage.
	Usage *UsageEvent `json:",omitempty"`
}

// UsageEvent is the usage of one model request.
type UsageEvent struct {
	// Used is the number of tokens in the context the request sent. Limit is
	// the context window the loop plans with, or 0 when it is not known.
	Used, Limit int
	// Completion is the number of tokens the request generated.
	Completion int
	// TokensPerSecond is the generation speed, or 0 when it is not known.
	TokensPerSecond float64
	// Model is the model the request used.
	Model string
	// Node is the worker that served the request, when the server exposes it.
	Node string
	// PromptMillis is the backend's prompt processing time, or 0 when it is
	// not known.
	PromptMillis float64
}

// ToolEvent describes one tool call.
type ToolEvent struct {
	Name    string // bash, read, write, edit, glob, grep
	Summary string // at most 200 bytes: the command line or the path
	OK      bool   // tool_end only
	Output  string // tool_end only, at most 2 KiB
	// Seq is the sequence number of the transcript entry of this call, on
	// tool_end. It is 0 when the entry could not be stored. The interface
	// uses it to read that one entry and to keep the same element for the
	// call when the transcript is read again.
	Seq int64 `json:",omitempty"`
	// Card points the product's interface at a card that belongs to this
	// result. A host tool sets it with SetToolCard; the chat attaches it to
	// the tool_end of that call. It is nil when the tool set none or the call
	// failed. It is for the interface only: the model never sees it and it
	// does not change Output.
	Card *ToolCard // tool_end only
}

// ToolCard points the product's interface at a card that belongs to a tool
// result. Kind is a product string (1 to 32 characters of [a-z0-9_]); ID is
// opaque (1 to 128 printable ASCII characters). The session does not look at
// either beyond that check.
type ToolCard struct {
	Kind, ID string
	// App is set when the result belongs to an MCP App: an interactive view
	// that the interface frames next to the result.
	App *ToolApp `json:",omitempty"`
	// Files are the user's files that the call created, changed or deleted,
	// when the tool is not a file tool: a command saves what it wrote, and
	// the interface lists those files under the call the way it lists the
	// file of a write_file. A host tool sets them with SetToolFiles, next to
	// its own card. At most MaxToolFiles; FilesMore counts the rest.
	Files     []ToolFile `json:",omitempty"`
	FilesMore int        `json:",omitempty"`
	// FileKind is the kind (workfiles.Deliverable or workfiles.Working) of
	// the file of a ChangeCardKind card, which names it in its ID. The chat
	// sets it when it keeps the card.
	FileKind string `json:",omitempty"`
}

// ChangeCardKind is the card of a file tool call that changed a file: its ID
// is "<rev>:<path>", the path in the home or, for a shared folder, the path
// the model used.
const ChangeCardKind = "files_change"

// ToolFile is one file of a tool call's card.
type ToolFile struct {
	// Path is the path in the user's home ("/report.docx"), or for a shared
	// folder the path the model uses (/workspace/shared-<name>/...), as in
	// the ID of a files_change card.
	Path string
	// Rev is the change set of the call that last touched the file.
	Rev int64
	// Op is ToolFileCreated, ToolFileChanged or ToolFileDeleted.
	Op string
	// Kind is workfiles.Deliverable or workfiles.Working, set by the chat
	// when it keeps the card.
	Kind string `json:",omitempty"`
}

// The operations of a ToolFile.
const (
	ToolFileCreated = "created"
	ToolFileChanged = "changed"
	ToolFileDeleted = "deleted"
)

// FilesCardKind is the card of a call that set files and no card of its own.
// Its ID is the newest revision of the files.
const FilesCardKind = "files"

// Limits of the files of a card.
const (
	MaxToolFiles    = 50
	maxToolFilePath = 1024
)

// ToolApp says which view belongs to a tool result and gives the view what
// the tool call was and returned. The interface reads the page from the
// server named by Server; the session only stores the reference.
type ToolApp struct {
	// Server is "connector:<id>" for a connector of the user, or the name of an
	// MCP server of the model.
	Server string
	// Tool is the name of the tool on that server.
	Tool string
	// ResourceURI is the ui:// resource that holds the page.
	ResourceURI string
	// Visibility is who may call the tool: "model", "app" or both.
	Visibility []string
	// Input are the arguments of the call and Result the full MCP result, as
	// JSON. They are cut to maxAppInput and maxAppResult; Cut says one was.
	Input  json.RawMessage `json:",omitempty"`
	Result json.RawMessage `json:",omitempty"`
	Cut    bool            `json:",omitempty"`
}

// ChangeEvent describes the checkpoint at the end of a turn.
type ChangeEvent struct {
	Rev       int64 // 0 when the turn changed nothing
	Paths     []string
	Conflicts []workspace.Conflict
	Err       string // when the checkpoint failed
}
