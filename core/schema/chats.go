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
	// Settings are the chat's own model settings, when it has any.
	Settings *ChatsSettings `json:"settings,omitempty"`
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
	// ImageModel is the model that describes the images of a chat whose own
	// model cannot look at them. It is empty when no model can.
	ImageModel string `json:"image_model,omitempty"`
}

// ChatsConversationDetail is a conversation with the messages waiting for the model.
type ChatsConversationDetail struct {
	ChatsConversation
	Pending []ChatsPending `json:"pending"`
}

// ChatsSendRequest is a message to a conversation.
type ChatsSendRequest struct {
	Text string `json:"text"`
	// Context adds MCP prompts and resources to this message as context. Each
	// is expanded on the server, as you, from a server the chat can use.
	Context []ChatsMCPRef `json:"context,omitempty"`
}

// ChatsSendResponse says what happened to a message. With a turn running the
// message waits: Started is false and PendingID names it.
type ChatsSendResponse struct {
	Started   bool   `json:"started"`
	PendingID string `json:"pending_id,omitempty"`
}

// ChatsPending is a message that waits for the model's next request. Text is
// what you wrote; the files of the message are in Attachments.
type ChatsPending struct {
	ID          string            `json:"id"`
	Text        string            `json:"text"`
	Attachments []ChatsAttachment `json:"attachments,omitempty"`
	Seq         int64             `json:"seq"`
}

// ChatsAttachment is a file attached to one of your messages. The model gets
// its path with the message; the transcript keeps it apart from the text.
type ChatsAttachment struct {
	// Path is the file's path inside your home, for the files API
	// (GET /api/chats/files/content?path=). It follows a rename of the chat's
	// folder.
	Path string `json:"path"`
	Name string `json:"name"`
	// Size is in bytes.
	Size int64  `json:"size,omitempty"`
	Mime string `json:"mime,omitempty"`
	// Version is the version of the file that the message carried.
	Version int64 `json:"version,omitempty"`
	// Missing is true when the file is no longer in your files.
	Missing bool `json:"missing,omitempty"`
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
	// Text is what was written. For a user line it is the text you typed:
	// the files you attached are in Attachments.
	Text string `json:"text"`
	// Attachments are the files of a user line.
	Attachments []ChatsAttachment `json:"attachments,omitempty"`
	// DurMs is how long the model thought, for reasoning lines.
	DurMs int64 `json:"dur_ms,omitempty"`
	// App is set on a tool line whose tool has an MCP App: the interactive page
	// that goes with the result.
	App *ChatsToolApp `json:"app,omitempty"`
	// Card is set on a tool line that points at a record, such as a trigger
	// proposal (kind trigger_proposal, id the proposal's id).
	Card *ChatsToolCard `json:"card,omitempty"`
	// Rev is the change set of a checkpoint line, whose text lists the paths
	// it changed inside your home, one a line. POST /api/chats/files/undo
	// {rev} undoes it.
	Rev int64     `json:"rev,omitempty"`
	At  time.Time `json:"at"`
}

// ChatsToolCard points the interface at a record that belongs to a tool result.
type ChatsToolCard struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
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
// pending_delivered, sub_agent_started, sub_agent_done, compacted, usage or
// trigger_proposed.
type ChatsEvent struct {
	Kind string `json:"kind"`
	Turn int    `json:"turn,omitempty"`
	Text string `json:"text,omitempty"`
	// Tool is set for tool_start and tool_end.
	Tool *ChatsToolEvent `json:"tool,omitempty"`
	// IDs names the messages of pending_delivered, the conversation of
	// sub_agent_started and sub_agent_done, or the proposal of trigger_proposed.
	IDs []string `json:"ids,omitempty"`
	// Before and After are context sizes in tokens, for compacted.
	Before int `json:"before,omitempty"`
	After  int `json:"after,omitempty"`
	// Usage is set on usage events: what one model request used.
	Usage *ChatsUsage `json:"usage,omitempty"`
	// Code, Model, Retry and Detail say why a turn failed (error only). Code
	// is a stable name of the cause, such as model_backend_missing; Model names
	// the model it is about; Retry is the seconds until a model in cooldown
	// loads again; Detail is the error text without addresses and secrets.
	Code   string `json:"code,omitempty"`
	Model  string `json:"model,omitempty"`
	Retry  int    `json:"retry,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// ChatsUsage is what one model request used. It feeds the context meter and
// the tokens-per-second figure.
type ChatsUsage struct {
	// Used is the number of tokens in the context the request sent.
	Used int `json:"used"`
	// Limit is the context window the loop plans with, 0 when it is not known.
	Limit int `json:"limit,omitempty"`
	// Completion is the number of tokens the request generated.
	Completion int `json:"completion"`
	// TokensPerSecond is the generation speed, 0 when the backend gave no timing.
	TokensPerSecond float64 `json:"tokens_per_second,omitempty"`
	// Model is the model the request used.
	Model string `json:"model,omitempty"`
	// Node is the worker node that served the request. It is set only when
	// the server exposes the node header (--expose-node-header).
	Node string `json:"node,omitempty"`
	// PromptMs is the time the backend took to read the prompt, in
	// milliseconds, when the backend reports it.
	PromptMs float64 `json:"prompt_ms,omitempty"`
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
	// Card is the record the result points at, on tool_end; see ChatsMessage.
	Card *ChatsToolCard `json:"card,omitempty"`
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
	// Managed is set on a path LocalAI manages: "localai" for the LocalAI
	// folder and the settings files in it, "chats" for the Chats folder.
	Managed string `json:"managed,omitempty"`
}

// ChatsFilePreview is what the server reads out of a file that a browser
// cannot show by itself. Kind says which of the lists is set: "table" (a
// spreadsheet, a CSV or a TSV file), "document" (a Word document) or "slides"
// (a PowerPoint presentation). The values are text: the formatting is not
// kept.
type ChatsFilePreview struct {
	Kind   string                  `json:"kind"`
	Sheets []ChatsFilePreviewSheet `json:"sheets,omitempty"`
	Blocks []ChatsFilePreviewBlock `json:"blocks,omitempty"`
	Slides []ChatsFilePreviewSlide `json:"slides,omitempty"`
	// Truncated is set when a document or presentation had more text than
	// the preview keeps.
	Truncated bool `json:"truncated,omitempty"`
}

// ChatsFilePreviewSheet is one sheet of a table: at most the first 200 rows and
// 30 columns. TotalRows and TotalCols count the whole sheet.
type ChatsFilePreviewSheet struct {
	Name      string     `json:"name"`
	Rows      [][]string `json:"rows"`
	TotalRows int        `json:"total_rows"`
	TotalCols int        `json:"total_cols"`
}

// ChatsFilePreviewBlock is one paragraph of a document. Type is "heading"
// (Level 1 to 9), "item" (a list item, Level 0 and up) or "paragraph".
type ChatsFilePreviewBlock struct {
	Type  string `json:"type"`
	Level int    `json:"level,omitempty"`
	Text  string `json:"text"`
}

// ChatsFilePreviewSlide is one slide: its title, its other text one line per
// paragraph, and its speaker notes.
type ChatsFilePreviewSlide struct {
	N     int                    `json:"n"`
	Title string                 `json:"title,omitempty"`
	Lines []ChatsFilePreviewLine `json:"lines,omitempty"`
	Notes string                 `json:"notes,omitempty"`
}

// ChatsFilePreviewLine is a paragraph of a slide and its outline level (0 is
// the top).
type ChatsFilePreviewLine struct {
	Text  string `json:"text"`
	Level int    `json:"level,omitempty"`
}

// WorkspaceChange is one event of the change feed of the user's files. Paths
// are inside the user's home folder, without a leading slash. A "resync" event
// has the revision only: changes were left out, so read again what you show.
type WorkspaceChange struct {
	// Revision is the newest revision of the changes; it is also the event id,
	// which a client sends back (Last-Event-ID or ?after=) to resume.
	Revision int64    `json:"revision"`
	Paths    []string `json:"paths,omitempty"`
	// Kinds are the kinds of the change sets: user, agent, upload or undo.
	Kinds []string `json:"kinds,omitempty"`
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
	// ID is "layer:name". A switch with the ID turns off the skill of that
	// layer; a switch with the plain name turns off the skill that wins.
	ID          string `json:"id"`
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
	// Commands says whether the chat can run commands and code at its next
	// turn: available, off (the add-on that runs them is switched off for this
	// chat or not named by its agent), unavailable (installed, but it cannot run
	// them on this server) or none (no add-on that runs them is installed for
	// the user).
	Commands string `json:"commands"`
	// CommandsReason is why commands cannot run, when Commands is unavailable.
	CommandsReason string `json:"commands_reason,omitempty"`
	// CommandsIsolation is isolated (a sandbox) or host (directly on this
	// machine, with its files and network), when Commands is available.
	CommandsIsolation string `json:"commands_isolation,omitempty"`
	// CommandsProvider serves bash, when Commands is available: builtin (the
	// built-in bash on this machine) or the ID of the add-on.
	CommandsProvider string `json:"commands_provider,omitempty"`
	// CommandsConsent is ask when each command of the built-in bash waits for
	// a yes in the chat.
	CommandsConsent string `json:"commands_consent,omitempty"`
	// CommandsFallback is the add-on that would serve bash but does not (it is
	// off for this chat, or cannot run), when the built-in serves instead;
	// CommandsFallbackReason says why: off, or the add-on's reason.
	CommandsFallback       string `json:"commands_fallback,omitempty"`
	CommandsFallbackReason string `json:"commands_fallback_reason,omitempty"`
}

// ChatsHostExecConsent answers the card that asks to run one command on this
// machine: decision is once (administrators only) or deny.
type ChatsHostExecConsent struct {
	ID       string `json:"id"`
	Decision string `json:"decision" enums:"once,deny"`
}

// ChatsHostExecEvent is one command the built-in bash ran on this machine.
type ChatsHostExecEvent struct {
	User       string    `json:"user"`
	UserName   string    `json:"user_name,omitempty"`
	Chat       string    `json:"chat"`
	Command    string    `json:"command"`
	Exit       int       `json:"exit"`
	DurationMS int64     `json:"duration_ms"`
	TimedOut   bool      `json:"timed_out,omitempty"`
	At         time.Time `json:"at"`
}

// ChatsHostExecEventList is the audit of the commands, newest first.
type ChatsHostExecEventList struct {
	Events []ChatsHostExecEvent `json:"events"`
}

// ChatsBuiltinTool is one built-in chat tool in Settings > Tools.
type ChatsBuiltinTool struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description"`
	// Group is the per-chat switch group (web), or empty.
	Group string `json:"group,omitempty"`
	// Setting is the runtime setting that switches it off; Options are the
	// other runtime settings that configure it.
	Setting string   `json:"setting"`
	Options []string `json:"options"`
	Enabled bool     `json:"enabled"`
	// ReadOnly says the tool only reads; Replaceable that an add-on tool may
	// replace it by declaring shadows with its name.
	ReadOnly    bool `json:"read_only"`
	Replaceable bool `json:"replaceable"`
	// Provider serves it now: builtin, an add-on's ID, or empty for nothing.
	Provider string `json:"provider"`
	// Candidates are the add-ons that replace it; Pinned the one chosen.
	Candidates []string `json:"candidates"`
	Pinned     string   `json:"pinned,omitempty"`
	// Mode is chats_host_exec as it applies (bash only): off, ask or on.
	Mode string `json:"mode,omitempty"`
	// Unavailable says why this server cannot run the built-in, or is empty.
	Unavailable string `json:"unavailable,omitempty"`
}

// ChatsBuiltinToolList lists the built-in chat tools.
type ChatsBuiltinToolList struct {
	Tools []ChatsBuiltinTool `json:"tools"`
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
	// LastRun is the newest run, if any, and LastDone the end of the newest
	// run that finished without an error.
	LastRun  *ChatsTriggerRun `json:"last_run,omitempty"`
	LastDone *time.Time       `json:"last_done,omitempty"`
	// MinGapSeconds is the shortest time between two runs of a file trigger.
	MinGapSeconds int `json:"min_gap_seconds,omitempty"`
	// Plan says what a run reads and writes and which Home widget shows the
	// file it writes; Cost what a run costs.
	Plan ChatsTriggerPlan `json:"plan"`
	Cost ChatsTriggerCost `json:"cost"`
}

// ChatsTriggerProposal is a trigger a chat proposed and what became of it.
// State is pending (it waits), accepted (trigger is the trigger that accepting
// made, the same id), declined or undone (trigger is the record kept so the
// chat can show a receipt and offer to restore it). CanUndo is set on an
// accepted trigger that has not run and was not edited; UndoBlocked says why
// not: ran or edited.
type ChatsTriggerProposal struct {
	State       string       `json:"state"`
	Trigger     ChatsTrigger `json:"trigger"`
	AnsweredAt  *time.Time   `json:"answered_at,omitempty"`
	CanUndo     bool         `json:"can_undo"`
	UndoBlocked string       `json:"undo_blocked,omitempty"`
}

// ChatsTriggerPlan is what a run of a trigger does, as the model that proposed
// it described it. Every field is optional.
type ChatsTriggerPlan struct {
	Reads string `json:"reads,omitempty"`
	// Writes is a path in the owner's home, such as data/inbox.json.
	Writes string `json:"writes,omitempty"`
	// Widget is the id of the Home widget in home.json that shows Writes.
	Widget string `json:"widget,omitempty"`
}

// ChatsTriggerCost is what the runs of a trigger cost, in tokens. LocalAI keeps
// no price list, so there is no price.
type ChatsTriggerCost struct {
	// EstimatedTokens is the estimate of the proposal; 0 is unknown.
	EstimatedTokens int `json:"estimated_tokens,omitempty"`
	// AverageTokens is the mean of the last finished runs that reported their
	// tokens, and Runs how many runs that is.
	AverageTokens int `json:"average_tokens,omitempty"`
	Runs          int `json:"runs,omitempty"`
	// MaxRunsPerDay is the most runs one day can have: the daily limit, or
	// fewer when the schedule runs less often.
	MaxRunsPerDay int `json:"max_runs_per_day"`
	// Remote is true when the model runs on a remote service, so the tokens
	// leave this server.
	Remote bool `json:"remote"`
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
	// MinGapSeconds is the shortest time between two runs of a file trigger;
	// 0 keeps the trigger's value (one minute for a new one).
	MinGapSeconds int `json:"min_gap_seconds,omitempty"`
	// Plan describes the runs. An empty field keeps the trigger's value.
	Plan *ChatsTriggerPlan `json:"plan,omitempty"`
	// EstimatedTokens is about how many tokens one run uses; 0 keeps the
	// trigger's value.
	EstimatedTokens int `json:"estimated_tokens,omitempty"`
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
	// Tokens is what the run's model requests used, when it is known.
	Tokens int `json:"tokens,omitempty"`
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
	Addon      string    `json:"addon"`
	AgentID    string    `json:"agent_id"`
	Enabled    bool      `json:"enabled"`
	AcceptAny  bool      `json:"accept_any"`
	Allow      []string  `json:"allow"`
	NotifyChat string    `json:"notify_chat,omitempty"`
	HasToken   bool      `json:"has_token"`
	Created    time.Time `json:"created"`
	// ReplyMode is what a sender sees while a turn runs: off (typing, then
	// the answer), progress (a working message that names the tools as they
	// start) or stream (a working message that shows the answer as it is
	// written). The answer replaces the working message.
	ReplyMode string `json:"reply_mode"`
	// Conversation says which chat a message belongs to: sender (one chat
	// per sender and per platform thread), chat (one chat per group, channel
	// or direct chat), window (sender, and a new chat after window_minutes
	// without a message) or thread (a new chat per message; a reply to an
	// answer goes on in its chat).
	Conversation  string `json:"conversation"`
	WindowMinutes int    `json:"window_minutes,omitempty"`
	// SendFiles sends the files that a turn makes to the sender, after the
	// answer.
	SendFiles bool `json:"send_files"`
	// Active is false when the add-on that serves the binding is not running. The
	// binding is kept and works once the add-on is installed.
	Active bool `json:"active"`
	// Reason says in plain words why the binding is not active.
	Reason string `json:"reason,omitempty"`
}

// ChatsConnectorKind is a kind of binding that an installed connector add-on serves.
type ChatsConnectorKind struct {
	Addon       string `json:"addon"`
	Kind        string `json:"kind"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	SenderHint  string `json:"sender_hint,omitempty"`
	// Page is the add-on page that adds and edits bindings of this kind.
	Page string `json:"page,omitempty"`
}

// ChatsConnectorKindList lists the connector kinds of the add-ons that run now.
type ChatsConnectorKindList struct {
	Kinds []ChatsConnectorKind `json:"kinds"`
}

// ChatsConnectorWrite creates or replaces a connector binding. token is needed to
// create one; an update without it keeps the stored token.
type ChatsConnectorWrite struct {
	// Kind is the platform, such as telegram. It is used to create a binding and
	// ignored by an update. Empty means telegram.
	Kind      string `json:"kind,omitempty"`
	Name      string `json:"name"`
	Token     string `json:"token,omitempty"`
	AgentID   string `json:"agent_id"`
	Enabled   bool   `json:"enabled"`
	AcceptAny bool   `json:"accept_any"`
	// ConfirmAcceptAny must be the exact text "accept any message" in the
	// request that turns accept_any on (a create with accept_any, or an update
	// that changes it from off to on). Anyone who can message the bot then uses
	// the agent's tools as you, so the server asks you to say you mean it.
	ConfirmAcceptAny string   `json:"confirm_accept_any,omitempty"`
	Allow            []string `json:"allow,omitempty"`
	NotifyChat       string   `json:"notify_chat,omitempty"`
	// ReplyMode is off, progress or stream; empty means progress.
	ReplyMode string `json:"reply_mode,omitempty"`
	// Conversation is sender, chat, window or thread; empty means sender.
	Conversation string `json:"conversation,omitempty"`
	// WindowMinutes is the time window of the window mode, 1 to 10080.
	WindowMinutes int `json:"window_minutes,omitempty"`
	// SendFiles turns the sending of a turn's files on or off. Absent means
	// on for a new binding and unchanged for an update.
	SendFiles *bool `json:"send_files,omitempty"`
}

// ChatsConnectorList is the response of the connector list endpoint.
type ChatsConnectorList struct {
	Connectors []ChatsConnector `json:"connectors"`
}

// ChatsConnectorTest is the bot that a token belongs to.
type ChatsConnectorTest struct {
	BotID       string `json:"bot_id"`
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

// ChatsBuiltinGroup is a group of built-in tools of a chat, such as the web
// tools, with the chat's switch. Only the tools an administrator has on are
// listed; a group with none is absent.
type ChatsBuiltinGroup struct {
	Group string   `json:"group" example:"web"`
	Tools []string `json:"tools" example:"web_search,web_fetch"`
	Off   bool     `json:"off"`
	// Provider is "builtin" when LocalAI serves the tools itself.
	Provider string `json:"provider" example:"builtin"`
}

// ChatsWebTestRequest is the body of a test web search.
type ChatsWebTestRequest struct {
	Query string `json:"query" example:"LocalAI"`
}

// ChatsWebResult is one result of a web search.
type ChatsWebResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

// ChatsWebTestResponse is the answer of a test web search: the provider and
// its first results, or the error a user would read.
type ChatsWebTestResponse struct {
	OK       bool             `json:"ok"`
	Provider string           `json:"provider" example:"duckduckgo"`
	Host     string           `json:"host" example:"duckduckgo.com"`
	Results  []ChatsWebResult `json:"results"`
	Error    string           `json:"error,omitempty"`
}

// ChatsBuiltinSwitches is the body that sets the groups of built-in tools a
// chat has switched off.
type ChatsBuiltinSwitches struct {
	Off []string `json:"off"`
}

// ChatsToolsPanel lists what a chat can use besides its own tools: the groups
// of built-in tools, add-ons, the MCP servers of its model and your
// connectors, each with the chat's switch.
type ChatsToolsPanel struct {
	Builtins   []ChatsBuiltinGroup       `json:"builtins"`
	Addons     []ChatsAddon              `json:"addons"`
	Servers    []ChatsMCPServerSwitch    `json:"servers"`
	Connectors []ChatsMCPConnectorSwitch `json:"connectors"`
	// Off is the list of MCP switches in force.
	Off []string `json:"off"`
}

// ChatsAdminAgent is a saved agent as an administrator sees it: with its owner,
// and without the instructions, which are the owner's text.
type ChatsAdminAgent struct {
	ID          string    `json:"id"`
	Owner       string    `json:"owner"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Model       string    `json:"model,omitempty"`
	MCPServers  []string  `json:"mcp_servers"`
	Addons      []string  `json:"addons"`
	Collections []string  `json:"collections"`
	Skills      []string  `json:"skills"`
	Created     time.Time `json:"created"`
	Updated     time.Time `json:"updated"`
}

// ChatsAdminAgentList is the response of the administrator's agents listing.
type ChatsAdminAgentList struct {
	Agents []ChatsAdminAgent `json:"agents"`
}

// ChatsAdminTrigger is a trigger with its owner.
type ChatsAdminTrigger struct {
	Owner string `json:"owner"`
	ChatsTrigger
}

// ChatsAdminTriggerList is the response of the administrator's triggers listing.
type ChatsAdminTriggerList struct {
	Triggers []ChatsAdminTrigger `json:"triggers"`
}

// ChatsAdminConnector is a connector binding with its owner. It never has the token.
type ChatsAdminConnector struct {
	Owner string `json:"owner"`
	ChatsConnector
}

// ChatsAdminConnectorList is the response of the administrator's connectors listing.
type ChatsAdminConnectorList struct {
	Connectors []ChatsAdminConnector `json:"connectors"`
}

// ChatsWellKnownFile is a well-known file of the home (persona.json,
// user.json, shortcuts.json, home.json, ui.json, or the data file of a Home
// widget), validated against its JSON Schema.
type ChatsWellKnownFile struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Exists bool   `json:"exists"`
	// Version is what a write sends back in If-Match. It is 0 when the file
	// does not exist.
	Version int64 `json:"version"`
	Rev     int64 `json:"rev"`
	// Data is the file when it is valid, its last valid version when it is
	// not (fallback is then true), or an empty object.
	Data map[string]any `json:"data"`
	// Valid is false when the file breaks its schema; errors says why.
	Valid       bool     `json:"valid"`
	Errors      []string `json:"errors,omitempty"`
	Fallback    bool     `json:"fallback,omitempty"`
	FallbackRev int64    `json:"fallback_rev,omitempty"`
	// Prefilled names the keys of data that do not come from the file, and
	// where they come from ("account").
	Prefilled map[string]string `json:"prefilled,omitempty"`
}

// ChatsWellKnownWrite changes a well-known file. The keys of values replace
// the keys of the file and a null removes one; the other keys of the file
// stay. With replace, values is the whole new file.
type ChatsWellKnownWrite struct {
	Values  map[string]any `json:"values"`
	Replace bool           `json:"replace,omitempty"`
}

// ChatsWellKnownRevert names the version a revert writes back. Without rev,
// the previous valid version.
type ChatsWellKnownRevert struct {
	Rev int64 `json:"rev,omitempty"`
}

// ChatsWellKnownVersion is one content a well-known file had.
type ChatsWellKnownVersion struct {
	Rev int64     `json:"rev"`
	At  time.Time `json:"at"`
	// Kind says who made it: user (you, in a form or the Files page), agent
	// (the assistant), upload or undo.
	Kind string `json:"kind"`
	// Chat is the chat that wrote it, and ChatTitle its title when it still exists.
	Chat      string         `json:"chat,omitempty"`
	ChatTitle string         `json:"chat_title,omitempty"`
	Data      map[string]any `json:"data,omitempty"`
	Valid     bool           `json:"valid"`
	Errors    []string       `json:"errors,omitempty"`
}

// ChatsWellKnownVersions lists the versions of a well-known file, newest first.
type ChatsWellKnownVersions struct {
	Versions []ChatsWellKnownVersion `json:"versions"`
}

// ChatsWellKnownError is the answer to a write that was refused: the errors
// of a content that breaks the schema, or, on a conflict, what the file holds now.
type ChatsWellKnownError struct {
	Error   *APIError           `json:"error"`
	Errors  []string            `json:"errors,omitempty"`
	Current *ChatsWellKnownFile `json:"current,omitempty"`
}
