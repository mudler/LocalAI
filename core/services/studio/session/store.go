package session

import (
	"context"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/sandbox"
)

// Store keeps chat records and transcripts. A chat belongs to one principal.
// sessiontest.RunStoreSuite checks every implementation.
type Store interface {
	// CreateChat stores c. It returns ErrExists when any principal has a chat
	// with this ID.
	CreateChat(ctx context.Context, c ChatRecord) error
	// GetChat returns ErrNotFound for a missing chat and for another
	// principal's chat.
	GetChat(ctx context.Context, principal, chatID string) (ChatRecord, error)
	// ListChats returns the principal's chats in the order Updated
	// descending, then ID descending. It returns the chats strictly after
	// the cursor in that order. The zero ChatCursor starts from the newest.
	ListChats(ctx context.Context, principal string, before ChatCursor, limit int) ([]ChatRecord, error)
	// UpdateChat replaces Title, Folder, Snapshot, HarnessState, Turns and
	// Updated. ID, Principal and Created never change.
	UpdateChat(ctx context.Context, c ChatRecord) error
	// DeleteChat removes the chat and its entries.
	DeleteChat(ctx context.Context, principal, chatID string) error
	// AppendEntry adds e at the end of the chat's transcript. The store sets
	// Seq (1, 2, 3 ...) and, when e.At is zero, At. It does not change
	// ChatRecord.Updated; the caller updates the chat with UpdateChat. It
	// returns the Seq it set (0 with an error).
	AppendEntry(ctx context.Context, chatID string, e Entry) (int64, error)
	// AppendEntries adds entries in order as one write: all or none. It is the
	// way to store a long transcript, one write for a batch instead of one per
	// entry. An empty list does nothing.
	AppendEntries(ctx context.Context, chatID string, entries []Entry) error
	// Entries returns up to limit entries with Seq greater than after, in order.
	Entries(ctx context.Context, chatID string, after int64, limit int) ([]Entry, error)
}

// ChatRecord is the stored state of one chat.
type ChatRecord struct {
	ID, Principal, Title string
	Folder               string // name of the chat folder under Chats; empty until the first upload
	Created, Updated     time.Time
	Snapshot             *sandbox.SnapshotRef // set while the sandbox is suspended
	HarnessState         []byte
	Turns                int
	// Kind is KindChat (empty counts as a chat) or KindSubAgent. ParentID is the
	// chat that started a sub-agent run. Neither changes after CreateChat.
	Kind     string
	ParentID string
	// Model is the model the chat talks to. It does not change after CreateChat.
	Model string
	// Status is the last turn outcome ("", "done", "stopped", "failed",
	// "interrupted"). UpdateChat replaces it. The text of a compaction is not
	// kept here: nib reports only the sizes, and keeps the summary in the
	// harness state.
	Status string
	// SkillsOff holds the names of the skills this chat has switched off. It is
	// read by GetChat and set by the store's SetSkillsOff; UpdateChat leaves it
	// alone.
	SkillsOff []string
	// AddonsOff holds the IDs of the add-ons whose tools this chat has switched
	// off. It is kept the way SkillsOff is: SetAddonsOff sets it, UpdateChat
	// leaves it alone.
	AddonsOff []string
	// McpOff holds what this chat has switched off among its MCP tools: the
	// name of a server of the model, or "connector:<id>" for a connector of the
	// user. It is kept the way AddonsOff is.
	McpOff []string
	// AgentID is the saved agent this chat runs with; empty for a plain chat.
	// Origin says what started the chat ("" for a person, OriginTrigger,
	// OriginConnector) and OriginID names the trigger or the connector binding.
	// None of the three changes after CreateChat.
	AgentID  string
	Origin   string
	OriginID string
	// Assistant marks a chat that has the LocalAI management tools. It is set
	// by CreateChat and does not change.
	Assistant bool
	// Settings are the per-chat model settings. SetChatMeta sets them;
	// UpdateChat leaves them alone, like the switches above.
	Settings ChatSettings
}

// ChatSettings are the model settings of one chat. A nil pointer or a zero
// value means "the model's own default". They apply from the next turn.
type ChatSettings struct {
	// SystemPrompt is added to the instructions of the chat, before any agent
	// instructions.
	SystemPrompt    string   `json:"system_prompt,omitempty"`
	Temperature     *float64 `json:"temperature,omitempty"`
	TopP            *float64 `json:"top_p,omitempty"`
	TopK            *int     `json:"top_k,omitempty"`
	ReasoningEffort string   `json:"reasoning_effort,omitempty"`
	// ContextSize is the context window, in tokens, that the harness plans
	// with (when it compacts the conversation). 0: the model's own size.
	ContextSize int `json:"context_size,omitempty"`
}

// ChatMeta is a change to the parts of a chat that a person edits. A nil field
// is left alone.
type ChatMeta struct {
	Title    *string
	Model    *string
	Settings *ChatSettings
}

// Chat origins.
const (
	OriginTrigger   = "trigger"
	OriginConnector = "connector"
	// OriginJob marks a chat that an agent job runs in; OriginID is the task.
	OriginJob = "job"
)

// Chat kinds.
const (
	KindChat     = "chat"
	KindSubAgent = "subagent"
)

// ChatCursor marks a position in the ListChats order. It is the (Updated, ID)
// of the last record of the previous page. Chats that share an Updated value
// are told apart by ID, so paging never skips or repeats one.
type ChatCursor struct {
	Updated time.Time
	ID      string
}

// Entry is one line of a transcript.
type Entry struct {
	Seq  int64
	Turn int
	// Role is user, assistant, tool, checkpoint or reasoning. A reasoning
	// entry is for display: it is not replayed to the model.
	Role string
	Text string // tool entries hold the summary and the capped output
	Rev  int64  // checkpoint entries
	// Dur is how long the model thought, for reasoning entries (0 when
	// unknown). It is 0 for every other role. Stores keep it to the
	// millisecond.
	Dur time.Duration
	// Card is the card of a tool result, for tool entries. It is nil for
	// every other role and for entries written before cards existed. It is
	// for the interface: it is never replayed to the model.
	Card *ToolCard
	At   time.Time
}

// Entry roles.
const (
	RoleUser       = "user"
	RoleAssistant  = "assistant"
	RoleTool       = "tool"
	RoleCheckpoint = "checkpoint"
	// RoleReasoning is the reasoning of one step, at most MaxReasoningBytes.
	RoleReasoning = "reasoning"
)

// MaxReasoningBytes is the size limit of a stored reasoning entry, including
// the final "…" of a cut one.
const MaxReasoningBytes = 64 << 10
