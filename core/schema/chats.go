package schema

import (
	"encoding/json"
	"time"
)

// ChatsCreateRequest starts a chat.
type ChatsCreateRequest struct {
	// Title is optional; the conversation is named after its first message otherwise.
	Title string `json:"title,omitempty"`
	// Model is the model the conversation talks to. It cannot change later.
	Model string `json:"model"`
	// AgentID starts the chat with one of your saved agents. The agent's model,
	// when it has one, replaces Model, which is then optional.
	AgentID string `json:"agent_id,omitempty"`
	// Assistant starts the chat with the LocalAI management tools (install
	// models and backends, change settings). It needs an administrator and the
	// assistant setting on, and it cannot be combined with AgentID.
	Assistant bool `json:"assistant,omitempty"`
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
	AgentID  string `json:"agent_id,omitempty"`
	Origin   string `json:"origin,omitempty"`
	OriginID string `json:"origin_id,omitempty"`
	// Assistant is true for a chat that has the LocalAI management tools.
	Assistant bool      `json:"assistant,omitempty"`
	Created   time.Time `json:"created"`
	Updated   time.Time `json:"updated"`
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
	DurMs int64 `json:"dur_ms,omitempty"`
	// App is set on a tool line whose tool has an MCP App: the interactive page
	// that goes with the result.
	App *ChatsToolApp `json:"app,omitempty"`
	At  time.Time     `json:"at"`
}

// ChatsToolApp tells the interface which MCP App belongs to a tool result. The
// page itself and the calls of the page go through the two mcp-apps routes of
// the chat. In a transcript line it carries the arguments and the full result
// of the call, for the page to show; in a live event it is the reference only.
type ChatsToolApp struct {
	// Server is "connector:<id>" for one of your connectors, or the name of an
	// MCP server of the model.
	Server string `json:"server"`
	// Tool is the name of the tool on that server.
	Tool string `json:"tool"`
	// ResourceURI is the ui:// resource that holds the page.
	ResourceURI string `json:"resource_uri"`
	// Visibility is who may call the tool: "model", "app" or both.
	Visibility []string `json:"visibility"`
	// Input is the arguments of the call, at most 16 KiB.
	Input json.RawMessage `json:"input,omitempty" swaggertype:"object"`
	// Result is the full MCP result of the call, at most 256 KiB.
	Result json.RawMessage `json:"result,omitempty" swaggertype:"object"`
	// Cut says that the input or the result was too large and is left out.
	Cut bool `json:"cut,omitempty"`
}

// ChatsMCPAppResource is the page of an MCP App, read from its server.
type ChatsMCPAppResource struct {
	URI      string `json:"uri"`
	MimeType string `json:"mime_type"`
	// HTML is the page. It must run in a sandboxed frame with no access to the
	// origin of the interface.
	HTML string `json:"html"`
	// Meta is the ui metadata of the resource as the MCP Apps specification
	// names it, cleaned: csp (connectDomains, resourceDomains, frameDomains and
	// baseUriDomains hold only https or wss origins), permissions and prefersBorder.
	Meta map[string]any `json:"meta"`
}

// ChatsMCPAppCall asks to run a tool of an MCP server for its app.
type ChatsMCPAppCall struct {
	// Server is "connector:<id>" or the name of an MCP server of the chat's model.
	Server string `json:"server"`
	// Tool is a tool of that server whose visibility includes "app".
	Tool string `json:"tool"`
	// Arguments is a JSON object, at most 16 KiB.
	Arguments json.RawMessage `json:"arguments,omitempty" swaggertype:"object"`
}

// ChatsMCPAppCallResult is the MCP result of the tool call: content,
// structuredContent and isError as the MCP specification names them.
type ChatsMCPAppCallResult struct {
	Result json.RawMessage `json:"result" swaggertype:"object"`
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
	// Seq is the sequence number of the call's transcript line, on tool_end. A
	// client that wants the app's input and result reads that one line with
	// GET /api/chats/{id}/messages?after=<seq-1>&limit=1.
	Seq int64 `json:"seq,omitempty"`
	// App is the reference of the MCP App of the call, on tool_end. It has no
	// input and no result: read the transcript line for them.
	App *ChatsToolApp `json:"app,omitempty"`
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

// ChatsTrigger is a trigger: work that starts by itself, as you, in a new chat.
// Kind is schedule or file. A schedule trigger has schedule and time_zone; a
// file trigger has folder, a path inside your files, and quiet_seconds.
type ChatsTrigger struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Prompt       string `json:"prompt"`
	Kind         string `json:"kind"`
	Schedule     string `json:"schedule,omitempty"`
	TimeZone     string `json:"time_zone,omitempty"`
	Folder       string `json:"folder,omitempty"`
	QuietSeconds int    `json:"quiet_seconds,omitempty"`
	DailyCap     int    `json:"daily_cap"`
	Enabled      bool   `json:"enabled"`
	AgentID      string `json:"agent_id,omitempty"`
	Model        string `json:"model,omitempty"`
	SourceChatID string `json:"source_chat_id,omitempty"`
	// Summary says when it runs in plain words. NextRun is set for a schedule
	// trigger that is on.
	Summary string     `json:"summary"`
	NextRun *time.Time `json:"next_run,omitempty"`
	// LastRun is the newest run, if any.
	LastRun *ChatsTriggerRun `json:"last_run,omitempty"`
}

// ChatsTriggerWrite creates or replaces a trigger.
type ChatsTriggerWrite struct {
	Name         string `json:"name"`
	Prompt       string `json:"prompt"`
	Kind         string `json:"kind"`
	Schedule     string `json:"schedule,omitempty"`
	TimeZone     string `json:"time_zone,omitempty"`
	Folder       string `json:"folder,omitempty"`
	QuietSeconds int    `json:"quiet_seconds,omitempty"`
	DailyCap     int    `json:"daily_cap,omitempty"`
	Enabled      bool   `json:"enabled"`
	// AgentID is optional. Model is needed when neither the agent nor the source
	// chat names one.
	AgentID      string `json:"agent_id,omitempty"`
	Model        string `json:"model,omitempty"`
	SourceChatID string `json:"source_chat_id,omitempty"`
}

// ChatsTriggerList is the response of the trigger list endpoint.
type ChatsTriggerList struct {
	Triggers []ChatsTrigger `json:"triggers"`
}

// ChatsTriggerRun is one start of a trigger. Status is queued, running, done,
// failed or skipped; reason says why for the last two.
type ChatsTriggerRun struct {
	ID      string     `json:"id"`
	Status  string     `json:"status"`
	Reason  string     `json:"reason,omitempty"`
	ChatID  string     `json:"chat_id,omitempty"`
	File    string     `json:"file,omitempty"`
	Due     time.Time  `json:"due"`
	Started *time.Time `json:"started,omitempty"`
	Ended   *time.Time `json:"ended,omitempty"`
}

// ChatsTriggerRunList is the response of the runs endpoint.
type ChatsTriggerRunList struct {
	Runs []ChatsTriggerRun `json:"runs"`
}

// ChatsTriggerPreview lists the next times a schedule runs.
type ChatsTriggerPreview struct {
	Times   []time.Time `json:"times"`
	Summary string      `json:"summary"`
}

// ChatsTriggerConfirm confirms a proposal. TimeZone is used when the proposal
// names none.
type ChatsTriggerConfirm struct {
	TimeZone string `json:"time_zone,omitempty"`
}

// ChatsPushKey is the VAPID public key a browser needs to subscribe to push.
type ChatsPushKey struct {
	PublicKey string `json:"public_key"`
}

// ChatsPushSubscription is a browser's PushSubscription as it serialises to JSON.
type ChatsPushSubscription struct {
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
}

// ChatsPushUnsubscribe names the subscription to remove.
type ChatsPushUnsubscribe struct {
	Endpoint string `json:"endpoint"`
}

// ChatsConnector is a connector binding: a bot bound to one of your saved agents.
// The bot token is never returned; has_token says whether one is stored.
type ChatsConnector struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Kind       string    `json:"kind"`
	AgentID    string    `json:"agent_id"`
	Enabled    bool      `json:"enabled"`
	AcceptAny  bool      `json:"accept_any"`
	Allow      []string  `json:"allow"`
	NotifyChat string    `json:"notify_chat,omitempty"`
	HasToken   bool      `json:"has_token"`
	Created    time.Time `json:"created"`
}

// ChatsConnectorWrite creates or replaces a connector binding. token is needed to
// create one; an update without it keeps the stored token.
type ChatsConnectorWrite struct {
	Name       string   `json:"name"`
	Token      string   `json:"token,omitempty"`
	AgentID    string   `json:"agent_id"`
	Enabled    bool     `json:"enabled"`
	AcceptAny  bool     `json:"accept_any"`
	Allow      []string `json:"allow,omitempty"`
	NotifyChat string   `json:"notify_chat,omitempty"`
}

// ChatsConnectorList is the response of the connector list endpoint.
type ChatsConnectorList struct {
	Connectors []ChatsConnector `json:"connectors"`
}

// ChatsConnectorTest is the bot that a token belongs to.
type ChatsConnectorTest struct {
	BotID       int64  `json:"bot_id"`
	BotUsername string `json:"bot_username"`
}

// ChatsConnectorSender is a person who messaged a bot.
type ChatsConnectorSender struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	Count   int       `json:"count"`
	First   time.Time `json:"first"`
	Last    time.Time `json:"last"`
	Allowed bool      `json:"allowed"`
}

// ChatsConnectorSenderList is the sender log of a binding, newest first.
type ChatsConnectorSenderList struct {
	Senders []ChatsConnectorSender `json:"senders"`
}

// ChatsConversionItem is one thing the conversion of the old data handled.
type ChatsConversionItem struct {
	// Kind is agent, trigger, skill, chat or connector.
	Kind string `json:"kind"`
	Name string `json:"name"`
	// Status is converted, skipped, failed or dropped.
	Status string `json:"status"`
	// Reason names what was dropped or what went wrong. It holds no secret.
	Reason string `json:"reason,omitempty"`
}

// ChatsConversionTotals counts the items of a report by status.
type ChatsConversionTotals struct {
	Converted int `json:"converted"`
	Skipped   int `json:"skipped"`
	Failed    int `json:"failed"`
	Dropped   int `json:"dropped"`
}

// ChatsConversion is the report of the conversion of the old chat, agents,
// triggers and skills, for one user. An administrator also gets the totals of
// every user.
type ChatsConversion struct {
	// Done says a conversion ran at least once.
	Done   bool                  `json:"done"`
	At     *time.Time            `json:"at,omitempty"`
	Counts ChatsConversionTotals `json:"counts"`
	Items  []ChatsConversionItem `json:"items"`
	// Totals is set for an administrator: the counts of all users together.
	Totals *ChatsConversionTotals `json:"totals,omitempty"`
}

// ChatsBrowserImport is the outcome of an upload of the chats the old chat page
// kept in the browser.
type ChatsBrowserImport struct {
	// Imported chats are new. Skipped ones were there already, or have no
	// creation date or no message to import. Failed ones could not be stored.
	Imported int `json:"imported"`
	Skipped  int `json:"skipped"`
	Failed   int `json:"failed"`
}

// ChatsMCPConnector is an MCP server that you defined, for your chats. Its token,
// headers and environment are never returned: the has_ fields say what is stored.
type ChatsMCPConnector struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Transport  string    `json:"transport"` // http or stdio
	URL        string    `json:"url,omitempty"`
	Command    string    `json:"command,omitempty"`
	Args       []string  `json:"args"`
	HasToken   bool      `json:"has_token"`
	HasHeaders bool      `json:"has_headers"`
	HasEnv     bool      `json:"has_env"`
	Enabled    bool      `json:"enabled"`
	Created    time.Time `json:"created"`
	Updated    time.Time `json:"updated"`
}

// ChatsMCPConnectorWrite creates or replaces an MCP connector. token, headers and
// env are write-only: left out they keep the stored value, and an empty value
// clears it.
type ChatsMCPConnectorWrite struct {
	Name      string             `json:"name"`
	Transport string             `json:"transport"`
	URL       string             `json:"url,omitempty"`
	Command   string             `json:"command,omitempty"`
	Args      []string           `json:"args,omitempty"`
	Token     *string            `json:"token,omitempty"`
	Headers   *map[string]string `json:"headers,omitempty"`
	Env       *map[string]string `json:"env,omitempty"`
	Enabled   bool               `json:"enabled"`
}

// ChatsMCPConnectorList is the response of the MCP connector list endpoint.
type ChatsMCPConnectorList struct {
	Connectors []ChatsMCPConnector `json:"connectors"`
}

// ChatsMCPConnectorTest is the outcome of a connection test: the names of the
// tools the server offers, or why it could not be used.
type ChatsMCPConnectorTest struct {
	OK    bool     `json:"ok"`
	Tools []string `json:"tools"`
	Error string   `json:"error,omitempty"`
}

// ChatsMCPSwitches sets the MCP servers and connectors a chat has switched off:
// the name of a server of the model, or "connector:<id>" for a connector.
type ChatsMCPSwitches struct {
	Off []string `json:"off"`
}

// ChatsMCPServerSwitch is a server of the model with the chat's switch.
type ChatsMCPServerSwitch struct {
	Name   string `json:"name"`
	Off    bool   `json:"off"`
	Usable bool   `json:"usable"`
	Reason string `json:"reason,omitempty"`
}

// ChatsMCPConnectorSwitch is one of your connectors with the chat's switch.
type ChatsMCPConnectorSwitch struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Off    bool   `json:"off"`
	Usable bool   `json:"usable"`
	Reason string `json:"reason,omitempty"`
}

// ChatsToolsPanel lists what a chat can use besides its built-in tools: add-ons,
// the MCP servers of its model and your connectors, each with the chat's switch.
type ChatsToolsPanel struct {
	Addons     []ChatsAddon              `json:"addons"`
	Servers    []ChatsMCPServerSwitch    `json:"servers"`
	Connectors []ChatsMCPConnectorSwitch `json:"connectors"`
	// Off is the list of MCP switches in force.
	Off []string `json:"off"`
}
