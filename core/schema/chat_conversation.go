package schema

import (
	"encoding/json"
	"time"
)

// Conversation represents a chat conversation persisted server-side.
// Issue #9432: enables chat history to survive browser refresh / device switch.
//
// The History field is intentionally json.RawMessage so the server stays
// agnostic to message shape — the React UI mixes user / assistant / thinking /
// tool_call / tool_result entries with text, image_url, audio_url, and file
// attachments, and storing them opaquely avoids lossy round-trips.
type Conversation struct {
	ID               string            `json:"id"`
	Name             string            `json:"name"`
	Model            string            `json:"model,omitempty"`
	History          json.RawMessage   `json:"history,omitempty"`
	SystemPrompt     string            `json:"systemPrompt,omitempty"`
	MCPMode          bool              `json:"mcpMode,omitempty"`
	MCPServers       []string          `json:"mcpServers,omitempty"`
	MCPResources     []string          `json:"mcpResources,omitempty"`
	ClientMCPServers json.RawMessage   `json:"clientMCPServers,omitempty"`
	Temperature      *float64          `json:"temperature,omitempty"`
	TopP             *float64          `json:"topP,omitempty"`
	TopK             *float64          `json:"topK,omitempty"`
	TokenUsage       *ConvTokenUsage   `json:"tokenUsage,omitempty"`
	ContextSize      *int              `json:"contextSize,omitempty"`
	Metadata         map[string]string `json:"metadata,omitempty"`
	CreatedAt        int64             `json:"createdAt"`
	UpdatedAt        int64             `json:"updatedAt"`
}

// ConvTokenUsage mirrors the React UI's tokenUsage object on each chat.
type ConvTokenUsage struct {
	Prompt     int `json:"prompt"`
	Completion int `json:"completion"`
	Total      int `json:"total"`
}

// ConversationsFile is the on-disk representation for a user's conversations.
type ConversationsFile struct {
	Conversations []Conversation `json:"conversations"`
	UpdatedAt     time.Time      `json:"updated_at,omitempty"`
}
