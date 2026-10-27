package schema

import "time"

// ChatsSettings are the model settings of one chat. A missing or zero field
// means the model's own default. They apply from the next turn.
type ChatsSettings struct {
	// SystemPrompt is added to the chat's instructions, before the saved agent's.
	SystemPrompt string `json:"system_prompt,omitempty"`
	// Temperature is between 0 and 2.
	Temperature *float64 `json:"temperature,omitempty"`
	// TopP is above 0 and at most 1.
	TopP *float64 `json:"top_p,omitempty"`
	TopK *int     `json:"top_k,omitempty"`
	// ReasoningEffort is none, minimal, low, medium or high.
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
	// ContextSize is the context window in tokens that the loop plans with
	// (when it compacts the conversation), 0 for the model's own.
	ContextSize int `json:"context_size,omitempty"`
}

// ChatsUpdateRequest changes a chat. A missing field is left alone. A new title
// or model needs a chat with no turn running.
type ChatsUpdateRequest struct {
	Title *string `json:"title,omitempty"`
	// Model moves the chat to another model; the history stays.
	Model *string `json:"model,omitempty"`
	// Settings replaces the chat's settings as a whole.
	Settings *ChatsSettings `json:"settings,omitempty"`
}

// ChatsEditRequest is the new text of a message you sent.
type ChatsEditRequest struct {
	Text string `json:"text"`
}

// ChatsForkRequest copies a chat into a new one.
type ChatsForkRequest struct {
	// FromSeq is the last transcript line to copy; 0 copies everything.
	FromSeq int64 `json:"from_seq,omitempty"`
	// Title names the new chat; it defaults to the old title and "(fork)".
	Title string `json:"title,omitempty"`
}

// ChatsChatHit is a chat that matches a search.
type ChatsChatHit struct {
	Chat ChatsConversation `json:"chat"`
	// InTitle is true when the title matches.
	InTitle bool `json:"in_title"`
	// Seq is the first matching transcript line, or 0 when only the title matches.
	Seq int64 `json:"seq,omitempty"`
	// Snippet is the matching text with a little around it.
	Snippet string `json:"snippet,omitempty"`
}

// ChatsChatSearchResponse is the response of the chat search.
type ChatsChatSearchResponse struct {
	Hits []ChatsChatHit `json:"hits"`
}

// ChatsExport is a chat as a file. Version is 1.
type ChatsExport struct {
	Version    int                  `json:"version"`
	ExportedAt time.Time            `json:"exported_at"`
	Title      string               `json:"title"`
	Model      string               `json:"model"`
	Settings   ChatsSettings        `json:"settings"`
	Created    time.Time            `json:"created"`
	Updated    time.Time            `json:"updated"`
	Messages   []ChatsExportMessage `json:"messages"`
}

// ChatsExportMessage is one line of an exported chat. Role is user, assistant,
// tool or reasoning.
type ChatsExportMessage struct {
	Role string    `json:"role"`
	Text string    `json:"text"`
	At   time.Time `json:"at"`
}

// ChatsImportRequest imports an exported chat as a new chat.
type ChatsImportRequest struct {
	Chat ChatsExport `json:"chat"`
	// Model replaces the model of the file. It is needed when the file names none.
	Model string `json:"model,omitempty"`
}

// ChatsSubAgent is a sub-agent run of a chat. Its transcript is read with
// GET /api/chats/{id}/agents/{agent_id}/messages.
type ChatsSubAgent struct {
	ID      string    `json:"id"`
	Title   string    `json:"title"`
	Created time.Time `json:"created"`
	Updated time.Time `json:"updated"`
	// Running is true while the run's turn is going. LastTurn is how its last
	// turn ended: done, stopped, failed or interrupted.
	Running  bool   `json:"running"`
	LastTurn string `json:"last_turn,omitempty"`
}

// ChatsSubAgentList lists the sub-agent runs of a chat.
type ChatsSubAgentList struct {
	SubAgents []ChatsSubAgent `json:"sub_agents"`
	// Running is how many of them run now, on any frontend of the server.
	Running int `json:"running"`
	// BackgroundLimit is how many a chat may run in the background at once.
	BackgroundLimit int `json:"background_limit"`
}
