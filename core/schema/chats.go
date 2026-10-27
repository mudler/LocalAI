package schema

import "time"

// ChatsCreateRequest starts a chat.
type ChatsCreateRequest struct {
	// Title is optional; the conversation is named after its first message otherwise.
	Title string `json:"title,omitempty"`
	// Model is the model the conversation talks to. It cannot change later.
	Model string `json:"model"`
	// AgentID starts the chat with one of your saved agents. The agent's model,
	// when it has one, replaces Model, which is then optional.
	AgentID string `json:"agent_id,omitempty"`
}

// ChatsAgent is a saved agent. The four lists are exact grants: an empty list
// grants nothing, and a chat gets only what you may use at that moment.
type ChatsAgent struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Description  string    `json:"description"`
	Instructions string    `json:"instructions"`
	Model        string    `json:"model,omitempty"`
	MCPServers   []string  `json:"mcp_servers"`
	Addons       []string  `json:"addons"`
	Collections  []string  `json:"collections"`
	Skills       []string  `json:"skills"`
	Created      time.Time `json:"created"`
	Updated      time.Time `json:"updated"`
}

// ChatsAgentWrite creates or replaces a saved agent.
type ChatsAgentWrite struct {
	Name         string   `json:"name"`
	Description  string   `json:"description,omitempty"`
	Instructions string   `json:"instructions,omitempty"`
	Model        string   `json:"model,omitempty"`
	MCPServers   []string `json:"mcp_servers,omitempty"`
	Addons       []string `json:"addons,omitempty"`
	Collections  []string `json:"collections,omitempty"`
	Skills       []string `json:"skills,omitempty"`
}

// ChatsAgentList is the response of the agent list endpoint.
type ChatsAgentList struct {
	Agents []ChatsAgent `json:"agents"`
}

// ChatsConversation is a chat and the state of its turn.
type ChatsConversation struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Model    string `json:"model"`
	Kind     string `json:"kind"` // chat or subagent
	ParentID string `json:"parent_id,omitempty"`
	// AgentID is the saved agent the chat runs with. Origin is trigger or
	// connector for a chat that started by itself, and OriginID names the trigger
	// or binding.
	AgentID  string    `json:"agent_id,omitempty"`
	Origin   string    `json:"origin,omitempty"`
	OriginID string    `json:"origin_id,omitempty"`
	Created  time.Time `json:"created"`
	Updated  time.Time `json:"updated"`
	// Running is true while a turn runs. LastTurn is how the last finished turn
	// ended: done, stopped, failed or interrupted.
	Running  bool   `json:"running"`
	LastTurn string `json:"last_turn,omitempty"`
}

// ChatsConversationList is the response of the list endpoint.
type ChatsConversationList struct {
	Conversations []ChatsConversation `json:"conversations"`
	// Persistent is false when conversations live in memory and are lost when
	// the server restarts.
	Persistent bool `json:"persistent"`
	// Files is false when the workspace is off; FilesReason then says why.
	Files       bool   `json:"files"`
	FilesReason string `json:"files_reason,omitempty"`
}

// ChatsConversationDetail is a conversation with the messages waiting for the model.
type ChatsConversationDetail struct {
	ChatsConversation
	Pending []ChatsPending `json:"pending"`
}

// ChatsSendRequest is a message to a conversation.
type ChatsSendRequest struct {
	Text string `json:"text"`
}

// ChatsSendResponse says what happened to a message. With a turn running the
// message waits: Started is false and PendingID names it.
type ChatsSendResponse struct {
	Started   bool   `json:"started"`
	PendingID string `json:"pending_id,omitempty"`
}

// ChatsPending is a message that waits for the model's next request.
type ChatsPending struct {
	ID   string `json:"id"`
	Text string `json:"text"`
	Seq  int64  `json:"seq"`
}

// ChatsPendingEdit changes the text of a waiting message.
type ChatsPendingEdit struct {
	Text string `json:"text"`
}

// ChatsMessage is one line of a conversation's transcript.
type ChatsMessage struct {
	Seq  int64  `json:"seq"`
	Turn int    `json:"turn"`
	Role string `json:"role"` // user, assistant, tool, checkpoint or reasoning
	Text string `json:"text"`
	// DurMs is how long the model thought, for reasoning lines.
	DurMs int64     `json:"dur_ms,omitempty"`
	At    time.Time `json:"at"`
}

// ChatsMessageList is the response of the transcript endpoint.
type ChatsMessageList struct {
	Messages []ChatsMessage `json:"messages"`
}

// ChatsEvent is one server-sent event of a conversation. Kind is text,
// reasoning, tool_start, tool_end, checkpoint, turn_start, turn_end, error,
// pending_delivered, sub_agent_done or compacted.
type ChatsEvent struct {
	Kind string `json:"kind"`
	Turn int    `json:"turn,omitempty"`
	Text string `json:"text,omitempty"`
	// Tool is set for tool_start and tool_end.
	Tool *ChatsToolEvent `json:"tool,omitempty"`
	// IDs names the messages of pending_delivered, or the conversation of sub_agent_done.
	IDs []string `json:"ids,omitempty"`
	// Before and After are context sizes in tokens, for compacted.
	Before int `json:"before,omitempty"`
	After  int `json:"after,omitempty"`
}

// ChatsToolEvent describes a tool call.
type ChatsToolEvent struct {
	Name    string `json:"name"`
	Summary string `json:"summary,omitempty"`
	OK      bool   `json:"ok,omitempty"`
	Output  string `json:"output,omitempty"`
}

// ChatsFile is a file or folder of the user's workspace. Paths are inside the
// user's home folder, which is "/".
type ChatsFile struct {
	Path    string `json:"path"`
	Name    string `json:"name"`
	Kind    string `json:"kind"` // file or dir
	Size    int64  `json:"size"`
	Version int64  `json:"version"`
	Rev     int64  `json:"rev"`
	// ConflictOf is set when an upload kept both files: it is the path the
	// caller asked for, and Path is where the new content went.
	ConflictOf string `json:"conflict_of,omitempty"`
}

// ChatsFileList is a folder with its entries, sorted by name.
type ChatsFileList struct {
	Path    string      `json:"path"`
	Entries []ChatsFile `json:"entries"`
}

// ChatsFolderRequest creates a folder.
type ChatsFolderRequest struct {
	Path string `json:"path"`
}

// ChatsMoveRequest moves or renames a file or folder.
type ChatsMoveRequest struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// ChatsUndoRequest undoes one change set.
type ChatsUndoRequest struct {
	Rev           int64 `json:"rev"`
	SkipConflicts bool  `json:"skip_conflicts,omitempty"`
}

// ChatsFileConflict is something an undo could not revert.
type ChatsFileConflict struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// ChatsUndoResponse is the result of an undo.
type ChatsUndoResponse struct {
	Rev       int64               `json:"rev"`
	Conflicts []ChatsFileConflict `json:"conflicts,omitempty"`
}

// ChatsTrashEntry is a deletion that can be undone.
type ChatsTrashEntry struct {
	Rev   int64     `json:"rev"`
	Title string    `json:"title"`
	At    time.Time `json:"at"`
	Paths []string  `json:"paths"`
}

// ChatsTrash lists the deletions that can be undone, newest first.
type ChatsTrash struct {
	Entries []ChatsTrashEntry `json:"entries"`
}

// ChatsFileOp is one change of a change set.
type ChatsFileOp struct {
	Kind string `json:"kind"` // create, write, move or delete
	Path string `json:"path"`
	From string `json:"from,omitempty"`
}

// ChatsHistoryEntry is one change set.
type ChatsHistoryEntry struct {
	Rev   int64         `json:"rev"`
	Title string        `json:"title"`
	Kind  string        `json:"kind"` // user, agent, upload or undo
	Chat  string        `json:"chat,omitempty"`
	At    time.Time     `json:"at"`
	Ops   []ChatsFileOp `json:"ops"`
}

// ChatsHistory lists change sets, newest first.
type ChatsHistory struct {
	Entries []ChatsHistoryEntry `json:"entries"`
}

// ChatsSearchHit is a file whose name matches a search, or a line of a file
// that contains the text.
type ChatsSearchHit struct {
	Path    string `json:"path"`
	Kind    string `json:"kind"`
	Line    int    `json:"line,omitempty"`
	Snippet string `json:"snippet,omitempty"`
}

// ChatsSearchResult lists name matches first, then lines.
type ChatsSearchResult struct {
	Hits []ChatsSearchHit `json:"hits"`
}

// ChatsSkill is a skill a chat can use.
type ChatsSkill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	// Layer is where it comes from: folder (a .skills folder of your files),
	// personal, shared or builtin.
	Layer string `json:"layer"`
	// Path is the path of its SKILL.md as the model sees it.
	Path string `json:"path"`
	// Enabled is false when the chat has switched it off.
	Enabled bool `json:"enabled"`
	// Shadowed names the layer of a nearer skill with the same name, which wins.
	Shadowed string `json:"shadowed,omitempty"`
	// Invalid says why a SKILL.md was skipped.
	Invalid string `json:"invalid,omitempty"`
	// Editable is true when you may change or delete it here.
	Editable bool `json:"editable"`
}

// ChatsSkillList is the skills a user has.
type ChatsSkillList struct {
	Skills []ChatsSkill `json:"skills"`
	// Notes say what could not be read or searched.
	Notes []string `json:"notes,omitempty"`
}

// ChatsSkillWrite creates a skill.
type ChatsSkillWrite struct {
	Layer       string `json:"layer"` // personal or shared
	Name        string `json:"name"`
	Description string `json:"description"`
	Body        string `json:"body"`
}

// ChatsSkillUpdate replaces a skill's description and body.
type ChatsSkillUpdate struct {
	Description string `json:"description"`
	Body        string `json:"body"`
}

// ChatsSkillDetail is one skill with its text.
type ChatsSkillDetail struct {
	Name        string `json:"name"`
	Layer       string `json:"layer"`
	Description string `json:"description"`
	Body        string `json:"body"`
	Editable    bool   `json:"editable"`
}

// ChatsSkillSwitches sets the skills a chat has switched off.
type ChatsSkillSwitches struct {
	Off []string `json:"off"`
}

// ChatsAddonSwitches sets the add-ons whose tools a chat has switched off.
type ChatsAddonSwitches struct {
	Off []string `json:"off"`
}

// ChatsAddon is one add-on whose tools the user may use, with the chat's switch.
type ChatsAddon struct {
	ID    string `json:"id"`
	Tools int    `json:"tools"`
	Off   bool   `json:"off"`
}

// ChatsAddonList lists the add-ons a chat can use.
type ChatsAddonList struct {
	Addons []ChatsAddon `json:"addons"`
}

// ChatsNotification is one item of the inbox. Kind is run_ended, run_failed or
// needs_attention; Status is ok or failed. Link is the in-app path that opens
// what it is about.
type ChatsNotification struct {
	ID      int64     `json:"id"`
	Kind    string    `json:"kind"`
	Status  string    `json:"status"`
	Title   string    `json:"title"`
	Body    string    `json:"body"`
	Link    string    `json:"link"`
	Read    bool      `json:"read"`
	Created time.Time `json:"created"`
}

// ChatsNotificationList is the response of the inbox list endpoint.
type ChatsNotificationList struct {
	Notifications []ChatsNotification `json:"notifications"`
	Unread        int                 `json:"unread"`
}

// ChatsNotificationCount is the number of unread notifications.
type ChatsNotificationCount struct {
	Unread int `json:"unread"`
}

// ChatsNotificationsRead marks notifications read: the named ones, or all.
type ChatsNotificationsRead struct {
	IDs []int64 `json:"ids,omitempty"`
	All bool    `json:"all,omitempty"`
}

// ChatsNotificationsDelete names the notifications to delete.
type ChatsNotificationsDelete struct {
	IDs []int64 `json:"ids"`
}
