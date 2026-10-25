// Package session runs chats: an agent harness whose tools run in a sandbox
// that sees the user's workspace.
package session

import "github.com/mudler/LocalAI/core/services/studio/workspace"

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
)

// Event is one thing that happened in a chat.
type Event struct {
	Kind   EventKind
	Turn   int
	Text   string       // text, reasoning: a delta; error: a fixed reason
	Tool   *ToolEvent   // tool_start, tool_end
	Change *ChangeEvent // checkpoint
	IDs    []string     // pending_delivered: the ids of the messages
}

// ToolEvent describes one tool call.
type ToolEvent struct {
	Name    string // bash, read, write, edit, glob, grep
	Summary string // at most 200 bytes: the command line or the path
	OK      bool   // tool_end only
	Output  string // tool_end only, at most 2 KiB
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
type ToolCard struct{ Kind, ID string }

// ChangeEvent describes the checkpoint at the end of a turn.
type ChangeEvent struct {
	Rev       int64 // 0 when the turn changed nothing
	Paths     []string
	Conflicts []workspace.Conflict
	Err       string // when the checkpoint failed
}
