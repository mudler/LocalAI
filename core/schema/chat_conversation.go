package schema

import (
	"encoding/json"
)

// Conversation represents a chat conversation persisted server-side.
// Issue #9432: enables chat history to survive browser refresh / device switch.
//
// The History field is intentionally json.RawMessage so the server stays
// agnostic to message shape — the React UI mixes user / assistant / thinking /
// tool_call / tool_result entries with text, image_url, audio_url, and file
// attachments, and storing them opaquely avoids lossy round-trips.
type Conversation struct {
	ID               string          `json:"id"`
	Name             string          `json:"name"`
	Model            string          `json:"model,omitempty"`
	History          json.RawMessage `json:"history,omitempty" swaggertype:"array,object"`
	SystemPrompt     string          `json:"systemPrompt,omitempty"`
	MCPMode          bool            `json:"mcpMode,omitempty"`
	MCPServers       []string        `json:"mcpServers,omitempty"`
	MCPResources     []string        `json:"mcpResources,omitempty"`
	ClientMCPServers json.RawMessage `json:"clientMCPServers,omitempty" swaggertype:"array,object"`
	// LocalAIAssistant wires the chat to the in-process admin MCP server
	// (the LocalAI Assistant mode of the Chat page).
	LocalAIAssistant bool     `json:"localaiAssistant,omitempty"`
	Temperature      *float64 `json:"temperature,omitempty"`
	TopP             *float64 `json:"topP,omitempty"`
	TopK             *float64 `json:"topK,omitempty"`
	// ReasoningEffort is the chat's reasoning_effort choice ('' = model config,
	// 'none' also hides reasoning traces).
	ReasoningEffort string            `json:"reasoningEffort,omitempty"`
	TokenUsage      *ConvTokenUsage   `json:"tokenUsage,omitempty"`
	ContextSize     *int              `json:"contextSize,omitempty"`
	Metadata        map[string]string `json:"metadata,omitempty"`
	CreatedAt       int64             `json:"createdAt"`
	UpdatedAt       int64             `json:"updatedAt"`
	// Version is assigned by the server: 1 on the first save, +1 on every
	// later write including the delete. Clients use a non-zero version to
	// tell a conversation the server has seen from a never-synced draft.
	// Ignored on input.
	Version int64 `json:"version,omitempty"`
}

// ConversationTombstone marks a conversation the user deleted. The server
// keeps it (without content) so a client holding a stale copy can learn the
// conversation is gone instead of uploading it again.
type ConversationTombstone struct {
	ID        string `json:"id"`
	Version   int64  `json:"version"`
	DeletedAt int64  `json:"deletedAt"` // Unix milliseconds
}

// ConversationList is the GET /api/conversations response.
type ConversationList struct {
	Conversations []Conversation          `json:"conversations"`
	Deleted       []ConversationTombstone `json:"deleted"`
}

// ConversationBulkRequest is the PUT /api/conversations/bulk payload.
type ConversationBulkRequest struct {
	Conversations []Conversation `json:"conversations"`
}

// ConversationBulkResponse reports how many conversations a bulk replace
// stored and which ids it refused because they had been deleted.
type ConversationBulkResponse struct {
	Status  string   `json:"status"`
	Count   int      `json:"count"`
	Skipped []string `json:"skipped"`
}

// ConvTokenUsage mirrors the React UI's tokenUsage object on each chat.
type ConvTokenUsage struct {
	Prompt     int `json:"prompt"`
	Completion int `json:"completion"`
	Total      int `json:"total"`
}
