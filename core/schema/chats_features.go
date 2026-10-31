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
	// turn ended: done, stopped, failed, interrupted or paused.
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

// ChatsMCPPromptArg is an argument of an MCP prompt.
type ChatsMCPPromptArg struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

// ChatsMCPPrompt is a prompt of an MCP server the chat can use. Server is the
// name of a server of the model, or connector:<id>.
type ChatsMCPPrompt struct {
	Server      string              `json:"server"`
	Name        string              `json:"name"`
	Title       string              `json:"title,omitempty"`
	Description string              `json:"description,omitempty"`
	Arguments   []ChatsMCPPromptArg `json:"arguments,omitempty"`
}

// ChatsMCPPromptList lists the prompts a chat can use.
type ChatsMCPPromptList struct {
	Prompts []ChatsMCPPrompt `json:"prompts"`
}

// ChatsMCPResource is a resource of an MCP server the chat can use.
type ChatsMCPResource struct {
	Server      string `json:"server"`
	URI         string `json:"uri"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mime_type,omitempty"`
}

// ChatsMCPResourceList lists the resources a chat can use.
type ChatsMCPResourceList struct {
	Resources []ChatsMCPResource `json:"resources"`
}

// ChatsMCPPromptRequest expands a prompt.
type ChatsMCPPromptRequest struct {
	Server    string            `json:"server"`
	Name      string            `json:"name"`
	Arguments map[string]string `json:"arguments,omitempty"`
}

// ChatsMCPReadRequest reads a resource.
type ChatsMCPReadRequest struct {
	Server string `json:"server"`
	URI    string `json:"uri"`
}

// ChatsMCPContent is the text of an expanded prompt or a read resource.
type ChatsMCPContent struct {
	Text string `json:"text"`
}

// ChatsMCPRef names a prompt (name and arguments) or a resource (uri) of an MCP
// server to add to a message as context for that turn.
type ChatsMCPRef struct {
	Server    string            `json:"server"`
	Name      string            `json:"name,omitempty"`
	Arguments map[string]string `json:"arguments,omitempty"`
	URI       string            `json:"uri,omitempty"`
}

// ChatsAgentExport is a saved agent as a file. Version is 1 and Kind is
// localai-agent. The file holds the definition only: no ID, owner or time.
type ChatsAgentExport struct {
	Version    int             `json:"version"`
	Kind       string          `json:"kind"`
	ExportedAt time.Time       `json:"exported_at"`
	Agent      ChatsAgentWrite `json:"agent"`
}

// ChatsAgentImportResult is the response of an agent import.
type ChatsAgentImportResult struct {
	Agent ChatsAgent `json:"agent"`
	// Warnings say what the import changed: a model this server does not have
	// or you may not use is dropped, and a name you already use gets a number.
	Warnings []string `json:"warnings,omitempty"`
}

// ChatsAgentTemplate is a starter agent. It grants nothing.
type ChatsAgentTemplate struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	Instructions string `json:"instructions"`
}

// ChatsAgentTemplateList lists the starter agents.
type ChatsAgentTemplateList struct {
	Templates []ChatsAgentTemplate `json:"templates"`
}

// ChatsAgentTemplateUse saves a starter agent.
type ChatsAgentTemplateUse struct {
	// Name replaces the template's name.
	Name string `json:"name,omitempty"`
}

// ChatsAgentRun is a chat that ran with a saved agent.
type ChatsAgentRun struct {
	ChatID string `json:"chat_id"`
	Title  string `json:"title"`
	Model  string `json:"model"`
	// Origin is empty for a chat you had with the agent, trigger or connector
	// for one that started by itself; OriginID names the trigger or binding.
	Origin   string    `json:"origin,omitempty"`
	OriginID string    `json:"origin_id,omitempty"`
	Created  time.Time `json:"created"`
	Updated  time.Time `json:"updated"`
	Running  bool      `json:"running"`
	// LastTurn is how its last turn ended: done, stopped, failed, interrupted or paused.
	LastTurn string `json:"last_turn,omitempty"`
}

// ChatsAgentHistory is the run history of a saved agent, newest first.
type ChatsAgentHistory struct {
	Runs []ChatsAgentRun `json:"runs"`
	// Total, Failed and Running count every run of the agent among your newest
	// 5,000 chats, not only the ones listed.
	Total   int `json:"total"`
	Failed  int `json:"failed"`
	Running int `json:"running"`
}

// ChatsChatFile is one file of a chat.
type ChatsChatFile struct {
	Name string `json:"name"`
	// Path is the current path in the user's files ("/" is the home folder),
	// for the files API. GuestPath is the path the model's tools use.
	Path      string `json:"path"`
	GuestPath string `json:"guest_path"`
	Size      int64  `json:"size"`
	Mime      string `json:"mime"`
	// Origin is "user" (attached by the user or put in the chat folder),
	// "model" (written by the model in the sandbox) or "tool" (written by a
	// host tool of the chat, such as write_file).
	Origin string `json:"origin"`
	// Changed is true when the chat changed a file it did not create.
	Changed bool `json:"changed,omitempty"`
	// Turn is the turn that brought the file in; 0 when not known.
	Turn int        `json:"turn,omitempty"`
	At   *time.Time `json:"at,omitempty"`
	// Media is true for an attachment that the model received as an image
	// or a video inside its message.
	Media bool `json:"media,omitempty"`
}

// ChatsChatFileList is the file index of a chat.
type ChatsChatFileList struct {
	Files []ChatsChatFile `json:"files"`
	// Folder is the guest path of the chat folder; empty until a file is
	// attached. FolderPath is the same folder in the user's files.
	Folder     string `json:"folder,omitempty"`
	FolderPath string `json:"folder_path,omitempty"`
	// Stale are old chat folder paths that earlier messages name, such as
	// the folder's name before a rename. The model is told the new one.
	Stale []string `json:"stale,omitempty"`
}
