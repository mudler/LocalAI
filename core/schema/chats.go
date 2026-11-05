package schema

import (
	"encoding/json"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/connsets"
	"github.com/mudler/LocalAI/core/services/studio/resourcesets"
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
	// Switches are the tools, add-ons and skills the chat starts with
	// switched off (and the opt-in tools switched on), as the composer of a
	// new chat drafts them. They are stored with the chat: the chat never
	// exists without them. A bad entry refuses the whole request.
	Switches *ChatsSwitches `json:"switches,omitempty"`
}

// ChatsSwitches are the switches of a chat, each list in the form its own
// PUT endpoint takes: add-on IDs, MCP server names and connector:<id>,
// groups of built-in tools, opt-in groups that are on, and skill ids.
type ChatsSwitches struct {
	AddonsOff   []string `json:"addons_off,omitempty"`
	McpOff      []string `json:"mcp_off,omitempty"`
	BuiltinsOff []string `json:"builtins_off,omitempty"`
	BuiltinsOn  []string `json:"builtins_on,omitempty"`
	SkillsOff   []string `json:"skills_off,omitempty"`
	// Connections is the set of connections (connector binding IDs) the chat
	// may use for each add-on whose tools run with a credential, by add-on ID.
	// Each must be one of your own for that add-on. A single string is read as
	// a set of one.
	Connections connsets.Sets `json:"connections,omitempty"`
}

// ChatsAgent is a saved agent. The four lists are exact grants: an empty list
// grants nothing, and a chat gets only what you may use at that moment.
type ChatsAgent struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Instructions string   `json:"instructions"`
	Model        string   `json:"model,omitempty"`
	MCPServers   []string `json:"mcp_servers"`
	Addons       []string `json:"addons"`
	Collections  []string `json:"collections"`
	Skills       []string `json:"skills"`
	// Network is the network preset of the Sandbox commands of the agent's
	// unattended runs: off (the default), registries or all, capped by the
	// administrator's ceiling.
	Network string `json:"network"`
	// Memory is the choice of notes for this agent: own (its own notes only),
	// mine_and_own (reads your notes and its own, saves to its own; the
	// default of a new agent in the interface), mine (your notes only) or
	// none. The host enforces it: it decides which notes go in the prompt of
	// a chat and where a note is saved. Absent is "as before", an agent saved
	// before the choice existed: it reads notes when its add-ons name Memory
	// and the model picks the scope of a note.
	Memory string `json:"memory,omitempty" enums:"own,mine_and_own,mine,none" example:"mine_and_own"`
	// MemorySave is the agent's own rule for saving a note: always (every note
	// waits for the person, and the model cannot undo or forget by itself),
	// auto (Save it and tell me: this agent saves a note at once and tells the
	// person, with an Undo, even when the person's level is Ask me first) or
	// never (it reads notes and saves none). Absent uses the person's setting
	// (Account > Memory). Always and never are never looser than that setting.
	// auto loosens ask and auto for this agent only: the person's Always
	// confirm and Never still hold, and so do the limits of an automatic save.
	MemorySave string `json:"memory_save,omitempty" enums:"always,auto,never" example:"always"`
	// BuiltinsOff are the groups of built-in tools (web, files, triggers...)
	// the agent's chats never have, and BuiltinsOn the opt-in groups (saved
	// agents) they start with. The names are the ones GET /api/chats/{id}/tools
	// lists. Empty for an agent that has every group a chat has by default.
	BuiltinsOff []string `json:"builtins_off"`
	BuiltinsOn  []string `json:"builtins_on"`
	// Builtins are the builtin capabilities granted to the agent, by the names
	// that GET /api/chats/agents/builtin-grants lists (memory.write,
	// skills.write and so on). Empty grants none. A chat of the agent holds
	// the grants you may use at that moment.
	Builtins []string `json:"builtins"`
	// Connections names, for each add-on whose tools run with a credential
	// (by add-on ID), the connections (binding IDs) the agent's chats may use.
	// A chat of the agent cannot change the set. An add-on with no entry gets
	// none from the agent: the person chooses in the chat.
	Connections connsets.Sets `json:"connections,omitempty"`
	// Resources names, for each add-on whose manifest scopes its resources per
	// agent (by add-on ID), the resources the agent may use and the mode of
	// each, read or write. An add-on of that kind with no entry gives the
	// agent no resource; the field has no effect on any other add-on. The host
	// checks it on every tool call that takes a resource, on top of what you
	// may use yourself, and the resource list and the selection of a chat show
	// the agent nothing outside it.
	Resources resourcesets.Sets `json:"resources,omitempty"`
	// Folder is the one folder that chats with the agent work in. It is
	// absent for an agent that works on all your files.
	Folder *ChatsAgentFolder `json:"folder,omitempty"`
	// Folders are all the folders of the agent (at most 8), in order; Folder
	// is the first of them. Absent for an agent that works on all your files.
	Folders []ChatsAgentFolder `json:"folders,omitempty"`
	// FolderNotice is set when shared folders do not fit the access limit of
	// a chat of this agent and are left out.
	FolderNotice *ChatsAgentFolderNotice `json:"folder_notice,omitempty"`
	Created      time.Time               `json:"created"`
	Updated      time.Time               `json:"updated"`
}

// ChatsAgentFolderNotice says how many shared folders a chat of the agent
// does not mount.
type ChatsAgentFolderNotice struct {
	NotMounted int `json:"not_mounted"`
}

// ChatsAgentFolder is the folder of a saved agent as it is now. The server
// keeps the folder itself, not its path, so a renamed or moved folder shows
// its new public path when one is available.
type ChatsAgentFolder struct {
	// Path is home-relative ("/" is the whole home), or /shared/<name>/...
	// for a followed readable shared folder. It is absent when missing,
	// not chosen, or readable outside these public locations; a readable
	// folder without a public path still has state ok.
	Path string `json:"path,omitempty"`
	// Name is the folder's own name; absent for "/".
	Name string `json:"name,omitempty"`
	// Mode is read (the agent's chats can read the folder) or write (they can
	// read and change it).
	Mode string `json:"mode"`
	// Effective is the mode the folder has in a chat: read for a shared
	// folder, and the mode of the outer folder when the folder is inside
	// another folder of the same agent.
	Effective string `json:"effective,omitempty"`
	// State is ok; missing when the folder was deleted or you can no longer
	// read it; or unset when no folder is chosen yet (an imported agent whose
	// folder was not found). An agent whose folder is missing or unset does
	// not start.
	State string `json:"state"`
}

// ChatsAgentFolderWrite chooses the folder of a saved agent.
type ChatsAgentFolderWrite struct {
	// Path is a folder of your files, "/" based; "/" is all of them, read
	// only or not. It cannot be LocalAI or Chats, a folder inside them, or a
	// shared folder. An empty path clears the folder.
	Path string `json:"path"`
	// Mode is read or write; it is required with a path.
	Mode string `json:"mode,omitempty"`
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
	Network      string   `json:"network,omitempty"`
	// Memory is the choice of notes (see ChatsAgent). A write without it keeps
	// the stored choice, so a client that does not know it cannot widen the
	// agent; the value must be own, mine_and_own, mine or none (400 otherwise).
	// A new agent written without it is "as before".
	Memory string `json:"memory,omitempty" enums:"own,mine_and_own,mine,none"`
	// MemorySave is the save rule of the agent (see ChatsAgent). A write
	// without it keeps the stored rule, so a client that does not know the
	// field cannot loosen the agent. use_mine clears the rule. ask is not
	// accepted (400).
	MemorySave string `json:"memory_save,omitempty" enums:"always,auto,never,use_mine"`
	// BuiltinsOff and BuiltinsOn are the switches of the built-in tools of
	// the agent's chats (see ChatsAgent). A write without them keeps the stored ones (an older client cannot widen
	// the agent); an empty list clears them.
	BuiltinsOff []string `json:"builtins_off,omitempty"`
	BuiltinsOn  []string `json:"builtins_on,omitempty"`
	// Builtins are the builtin grants of the agent (see ChatsAgent). A write
	// without them keeps the stored ones; an empty list clears them; a name
	// the server does not declare is a 400, except in an import, which drops it.
	Builtins []string `json:"builtins,omitempty"`
	// Connections names the connections the agent's chats may use for each
	// add-on with a credential (see ChatsAgent). A write without it keeps the
	// stored ones; an empty object clears them. Each binding must be your own
	// for that add-on. A single string is read as a set of one. An export file
	// never carries it: a binding ID belongs to one server.
	Connections connsets.Sets `json:"connections,omitempty"`
	// Resources names the resources the agent may use for each add-on with
	// per-agent resources (see ChatsAgent). A write without it keeps the
	// stored list; an empty object clears it. Each mode is read or write (400
	// otherwise). An export file never carries it and an import drops it, a
	// resource ID belongs to one server.
	Resources resourcesets.Sets `json:"resources,omitempty"`
	// Folder chooses the one folder the agent's chats work in. A write
	// without it keeps the folder the agent has; {"path": ""} clears it. In
	// an export file it is the home-relative path and mode when the file was
	// made. A missing, unchosen or non-home folder has an empty export path;
	// import preserves its mode as unset and warns instead of clearing scope.
	Folder *ChatsAgentFolderWrite `json:"folder,omitempty"`
	// Folders chooses up to 8 folders, each with its own mode. When both are
	// sent, folders wins. A write without folders and folder keeps the folders
	// the agent has; "folders": [] clears them. Where a folder is inside
	// another one, it gets the outer folder's mode.
	Folders []ChatsAgentFolderWrite `json:"folders,omitempty"`
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
	// ended: done, stopped, failed, interrupted, or paused (the tool loop
	// reached its step limit; send a message to go on).
	Running  bool   `json:"running"`
	LastTurn string `json:"last_turn,omitempty"`
	// Settings are the chat's own model settings, when it has any.
	Settings *ChatsSettings `json:"settings,omitempty"`
	// Goal is the chat's goal, when it has one.
	Goal *ChatsGoal `json:"goal,omitempty"`
	// Place is set on a chat that a messaging connection started, when the
	// connection kept where it lives.
	Place *ChatsPlace `json:"place,omitempty"`
}

// ChatsGoal is the persistent objective of a chat. While State is active the
// server starts the next turn by itself when one ends, up to MaxTurns
// automatic turns and MaxSteps model steps.
type ChatsGoal struct {
	Text string `json:"text"`
	// State is active, paused, done or blocked.
	State string `json:"state"`
	// PausedBy says why a paused goal paused: user, turn_limit, step_limit,
	// error or interrupted.
	PausedBy string `json:"paused_by,omitempty"`
	// Note is the model's summary of a done goal, or the reason of a blocked
	// one.
	Note string `json:"note,omitempty"`
	// Turns and Steps count the automatic turns and the model steps since the
	// goal was set or last resumed; MaxTurns and MaxSteps are the limits that
	// apply now (chats_goal_max_turns, chats_goal_max_steps).
	Turns    int       `json:"turns"`
	MaxTurns int       `json:"max_turns"`
	Steps    int       `json:"steps"`
	MaxSteps int       `json:"max_steps"`
	Set      time.Time `json:"set"`
	Updated  time.Time `json:"updated"`
}

// ChatsGoalRequest sets the goal of a chat.
type ChatsGoalRequest struct {
	// Text is the goal, 1 to 2000 characters.
	Text string `json:"text"`
}

// ChatsGoalResponse is the goal of a chat after a request. Goal is null when
// the chat has none. Replaced is the goal that a PUT replaced, or that a DELETE
// removed.
type ChatsGoalResponse struct {
	Goal     *ChatsGoal `json:"goal"`
	Replaced *ChatsGoal `json:"replaced,omitempty"`
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
	// HostConsent is the card of a command of the built-in bash that waits for
	// a yes while a turn runs; answer it with POST /api/chats/{id}/host-exec/consent.
	HostConsent *ChatsHostConsent `json:"host_consent,omitempty"`
	// Loops are the loops of the chat, oldest first (GET /api/chats/{id}/loops).
	Loops []ChatsLoop `json:"loops"`
}

// ChatsLoop is a loop of a chat: a prompt the chat sends to itself again, at
// an interval or when the model asks for the next run.
type ChatsLoop struct {
	ID     string `json:"id"`
	Prompt string `json:"prompt"`
	// Interval is the time between runs, such as 5m; empty for a self-paced
	// loop, which runs again when the model asks.
	Interval string `json:"interval,omitempty"`
	// IntervalSeconds is Interval in seconds; 0 for a self-paced loop.
	IntervalSeconds int64 `json:"interval_seconds"`
	// State is pending (waits for your yes), active, paused or ended.
	State string `json:"state"`
	// Reason says why the loop is paused or ended, or why it waits.
	Reason string `json:"reason,omitempty"`
	Runs   int    `json:"runs"`
	// Failures counts the failed runs in a row; three pause the loop.
	Failures int `json:"failures,omitempty"`
	// Next is when the next run is due; absent while a run goes on or the
	// loop does not run.
	Next *time.Time `json:"next,omitempty"`
	// Running is true while a run of the loop goes on in the chat.
	Running bool `json:"running"`
	// Wait is what the model of a self-paced loop said it waits for.
	Wait    string     `json:"wait,omitempty"`
	Created time.Time  `json:"created"`
	LastRun *time.Time `json:"last_run,omitempty"`
}

// ChatsLoopRequest starts a loop in a chat.
type ChatsLoopRequest struct {
	// Interval is the time between runs, such as 30s, 5m or 2h. Leave it
	// empty for a self-paced loop.
	Interval string `json:"interval,omitempty"`
	// Prompt is plain text, or a skill: /skill-name and its words.
	Prompt string `json:"prompt"`
}

// ChatsLoopStateRequest pauses a loop or makes it active.
type ChatsLoopStateRequest struct {
	// State is paused or active. Making a pending loop active confirms it.
	State string `json:"state"`
}

// ChatsLoopList is the loops of a chat.
type ChatsLoopList struct {
	Loops []ChatsLoop `json:"loops"`
}

// ChatsLoopMark names the loop run that sent a user line.
type ChatsLoopMark struct {
	ID  string `json:"id"`
	Run int    `json:"run"`
}

// ChatsHostConsent is a command that waits for the person's answer.
type ChatsHostConsent struct {
	ID      string `json:"id"`
	Command string `json:"command"`
}

// ChatsSendRequest is a message to a conversation.
type ChatsSendRequest struct {
	Text string `json:"text"`
	// Context adds MCP prompts and resources to this message as context. Each
	// is expanded on the server, as you, from a server the chat can use.
	Context []ChatsMCPRef `json:"context,omitempty"`
	// Skill runs the message with one of the chat's skills: the model gets the
	// skill's instructions with this message, and the transcript shows the
	// skill's name next to the text. An unknown skill is refused with the names
	// the chat has. Text may then be empty.
	Skill string `json:"skill,omitempty"`
	// Agent hands the task in Text to one of your saved agents (its id or
	// name). The chat must have the saved agents switched on.
	Agent string `json:"agent,omitempty"`
}

// ChatsInvocation is the command a message was sent with.
type ChatsInvocation struct {
	Kind string `json:"kind"` // skill or agent
	Name string `json:"name"`
	ID   string `json:"id,omitempty"` // the saved agent's id
}

// ChatsContextResult says how a context command changed what the model sees:
// its size in tokens before and after.
type ChatsContextResult struct {
	Before int `json:"before"`
	After  int `json:"after"`
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
	Role string `json:"role"` // user, assistant, tool, checkpoint, reasoning, compacted, cleared, goal, failure, thread or hostnote
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
	// Loop is set on a user line that a loop of the chat sent by itself.
	Loop *ChatsLoopMark `json:"loop,omitempty"`
	// Rev is the change set of a checkpoint line, whose text lists the paths
	// it changed inside your home, one a line. POST /api/chats/files/undo
	// {rev} undoes it.
	Rev int64 `json:"rev,omitempty"`
	// Invoked is the skill or saved agent a user line was sent with.
	Invoked *ChatsInvocation `json:"invoked,omitempty"`
	// Before and After are the size of the model's context, in tokens, on a
	// compacted line.
	Before int `json:"before,omitempty"`
	After  int `json:"after,omitempty"`
	// GoalEvent is set on a goal line: set, replaced, continue (a message the
	// server sent by itself to go on; Text is that message and GoalTurn its
	// automatic turn), paused (Text is the reason), resumed, done (Text is the
	// summary), blocked (Text is the reason) or cleared.
	GoalEvent string `json:"goal_event,omitempty"`
	GoalTurn  int64  `json:"goal_turn,omitempty"`
	// Failure is set on a failure line: why the turn ended (Text is the reason
	// in English).
	Failure *ChatsFailure `json:"failure,omitempty"`
	// Sender is set on a user line that a messaging connection delivered: who
	// wrote it, as the connection told it.
	Sender *ChatsSender `json:"sender,omitempty"`
	// Thread is set on a thread line: the earlier messages of the messaging
	// thread, exactly as the model's first turn got them. The line is for
	// reading only; the model never gets it as a message.
	Thread *ChatsThread `json:"thread,omitempty"`
	// HostNote is set on a hostnote line: something the host did on its own
	// about a message of a messaging connection. Text is the sentence the
	// sender was told, when there was one.
	HostNote *ChatsHostNote `json:"host_note,omitempty"`
	At       time.Time      `json:"at"`
}

// ChatsSender is who wrote a message that came from a messaging connection.
type ChatsSender struct {
	// ID is the platform's id of the sender, for the small print; the name is
	// what to read.
	ID       string `json:"id,omitempty"`
	Name     string `json:"name,omitempty"`
	Username string `json:"username,omitempty"`
	// Standing is how the host saw the sender: owner, verified, listed,
	// channel or anyone.
	Standing string `json:"standing,omitempty"`
}

// ChatsThread is the block of earlier thread messages that a connector chat
// kept for the owner.
type ChatsThread struct {
	// Lines are the messages, oldest first, each as "Author: text".
	Lines []string `json:"lines"`
	// Cut is true when older messages were left out.
	Cut bool `json:"cut,omitempty"`
}

// ChatsHostNote is a note of the host about a connector message.
type ChatsHostNote struct {
	// Code is one of paired, unsupported, too_long, no_upload, rate,
	// held_back, new_chat or refused.
	Code string `json:"code"`
}

// ChatsPlace says where a connector chat lives on the messaging platform. Every
// field is optional.
type ChatsPlace struct {
	// Direct is true for a conversation with one person; Thread when the chat
	// belongs to a thread of the platform.
	Direct bool `json:"direct,omitempty"`
	Thread bool `json:"thread,omitempty"`
	// Name is the channel or group name, when the connection told it.
	Name string `json:"name,omitempty"`
	// Link opens the thread or message on the platform. It is an https address.
	Link string `json:"link,omitempty"`
}

// ChatsFailure is the cause of a turn that ended on an error.
type ChatsFailure struct {
	// Code names the cause, such as model_not_found, upstream, timeout or agent.
	Code string `json:"code"`
	// Model is the model the cause is about, when it is known.
	Model string `json:"model,omitempty"`
	// RetryAfter is the seconds until a model in cooldown loads again.
	RetryAfter int `json:"retry_after,omitempty"`
	// Detail is the error text, cleaned, for the technical view.
	Detail string `json:"detail,omitempty"`
}

// ChatsToolCard points the interface at a record that belongs to a tool result.
type ChatsToolCard struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
	// Files are the files in your home that a command (bash) created,
	// changed or deleted, on the card of that call. Paths are inside your
	// home, which is "/". FilesMore counts the files the card does not list.
	Files     []ChatsToolFile `json:"files,omitempty"`
	FilesMore int             `json:"files_more,omitempty"`
	// FileKind is the kind, "deliverable" or "working", of the file that a
	// files_change card names in its id (see ChatsChatFile.Kind).
	FileKind string `json:"file_kind,omitempty"`
}

// ChatsToolFile is one file that a tool call changed.
type ChatsToolFile struct {
	Path string `json:"path"`
	// Rev is the change set that last touched the file in this call.
	Rev int64 `json:"rev"`
	// Op is created, changed or deleted.
	Op string `json:"op"`
	// Kind is "deliverable" or "working" (see ChatsChatFile.Kind).
	Kind string `json:"kind,omitempty"`
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
	// Steps is the step limit that a paused turn reached (paused only).
	Steps int `json:"steps,omitempty"`
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
	// Modified is when the entry last changed. A folder listing sets it.
	Modified *time.Time `json:"modified,omitempty"`
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

// ChatsFilePreviewBlock is one paragraph or table of a document. Type is
// "heading" (Level 1 to 9), "item" (a list item, Level 0 and up), "paragraph"
// or "table" (Rows holds the cells; Text is the rows as tab-separated lines).
type ChatsFilePreviewBlock struct {
	Type  string     `json:"type"`
	Level int        `json:"level,omitempty"`
	Text  string     `json:"text"`
	Rows  [][]string `json:"rows,omitempty"`
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
	// MaxFileSize is the largest file an upload may send, in bytes. Zero or
	// absent: the server reports no limit.
	MaxFileSize int64 `json:"max_file_size,omitempty"`
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
	// Files, Folders and Bytes say what deleting the entry for good removes.
	Files   int   `json:"files"`
	Folders int   `json:"folders"`
	Bytes   int64 `json:"bytes"`
}

// ChatsTrashTotals counts what is in the Trash, or what a purge removed.
type ChatsTrashTotals struct {
	Files   int   `json:"files"`
	Folders int   `json:"folders"`
	Bytes   int64 `json:"bytes"`
}

// ChatsTrash lists the deletions that can be undone, newest first.
type ChatsTrash struct {
	Entries []ChatsTrashEntry `json:"entries"`
	// Totals counts everything in the Trash, also what a long list leaves out.
	// It is what "empty the Trash" removes.
	Totals ChatsTrashTotals `json:"totals"`
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
	// Document is true for a line of the text read from a PDF, Word, Excel or
	// PowerPoint file. Line is then a line of that text.
	Document bool `json:"document,omitempty"`
	// Page, Sheet and Slide say where a document hit is, when the document has
	// pages, sheets or slides.
	Page  int    `json:"page,omitempty"`
	Sheet string `json:"sheet,omitempty"`
	Slide int    `json:"slide,omitempty"`
}

// ChatsSearchResult lists name matches first, then lines of text files, then
// lines of documents.
type ChatsSearchResult struct {
	Hits []ChatsSearchHit `json:"hits"`
	// Indexing counts the documents below the path whose text is not read
	// yet: the same search a moment later may find more.
	Indexing int `json:"indexing,omitempty"`
	// Limited is set when the search stopped at one of its limits before it
	// looked at every file below the path: a file that matches may be
	// missing, so narrow the path or the query.
	Limited bool `json:"limited,omitempty"`
}

// ChatsSkill is a skill a chat can use.
type ChatsSkill struct {
	// ID is "layer:name". A switch with the ID turns off the skill of that
	// layer; a switch with the plain name turns off the skill that wins.
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	// Layer is where it comes from: folder (a .skills folder of your files),
	// agent (a saved agent proposed it), personal, shared, addon (an installed
	// add-on ships it) or builtin.
	Layer string `json:"layer"`
	// Addon and AddonTitle name the add-on that provides a skill of the addon
	// layer.
	Addon      string `json:"addon,omitempty"`
	AddonTitle string `json:"addon_title,omitempty"`
	// Agent and AgentName name the saved agent that owns a skill of the agent
	// layer: the agent proposed it and the owner confirmed it.
	Agent     string `json:"agent,omitempty"`
	AgentName string `json:"agent_name,omitempty"`
	// Removable is true when the skill can be deleted from the Skills page
	// although it cannot be edited there (a skill of the agent layer).
	Removable bool `json:"removable,omitempty"`
	// Path is the path of its SKILL.md as the model sees it.
	Path string `json:"path"`
	// Enabled is false when the chat has switched it off.
	Enabled bool `json:"enabled"`
	// Shadowed names the layer of a nearer skill with the same name, which wins.
	Shadowed string `json:"shadowed,omitempty"`
	// ShadowedBy is the add-on whose skill wins over this one, when the
	// winner ships with an add-on (a built-in skill an add-on replaces, or
	// the skill of an add-on that is not the chosen one).
	ShadowedBy string `json:"shadowed_by,omitempty"`
	// Fallback is set on a built-in skill that serves because the add-on
	// skill that replaces it is off or not running in the chat: the title of
	// that add-on.
	Fallback string `json:"fallback,omitempty"`
	// Invalid says why a SKILL.md was skipped.
	Invalid string `json:"invalid,omitempty"`
	// Editable is true when you may change or delete it here.
	Editable bool `json:"editable"`
	// Provider names the provider slot whose active provider ships the skill
	// ("memory"), and Locked is true then: the host grants it to a chat that
	// uses the slot, an agent does not list it. Both are absent for any other
	// skill.
	Provider string `json:"provider,omitempty"`
	Locked   bool   `json:"locked,omitempty"`
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
	// Title and Description are what the add-on's manifest declares for
	// people. Title is empty when the add-on declares none; the interface then shows the ID.
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	// Credentialed is true when the tools of the add-on run with the token of
	// your connections. Connections lists them, oldest first. Enabled is the
	// IDs of the ones this chat may use (empty when none is enabled) and
	// ConnectionBy says why: chat (you chose them), agent (the agent of the
	// chat names the set; you cannot change it here), binding (the chat was
	// started by that binding) or only (it is your only connection).
	// NeedsConnection is true while none is enabled: the tools are not offered
	// to the model until you enable one. With two or more enabled, the model
	// names the connection in every call.
	Credentialed    bool              `json:"credentialed,omitempty"`
	Connections     []ChatsConnection `json:"connections,omitempty"`
	Enabled         []string          `json:"enabled_connections,omitempty"`
	ConnectionBy    string            `json:"connection_by,omitempty"`
	NeedsConnection bool              `json:"needs_connection,omitempty"`
	// ToolList names the tools of the add-on you may use, by the name the
	// add-on declares, with kind read or write. Sorted by name.
	ToolList []ChatsAddonTool `json:"tool_list,omitempty"`
	// Resources is set when the add-on scopes its resources per agent: a saved
	// agent then names the resources it may use (agent field resources). It
	// carries the word for them. GET /api/chats/agents/addons/{addon}/resources
	// lists what you can give an agent.
	Resources *ChatsAddonResources `json:"resources,omitempty"`
}

// ChatsAddonTool is one tool of an add-on.
type ChatsAddonTool struct {
	Name string `json:"name"`
	Kind string `json:"kind" enums:"read,write" example:"read"`
}

// ChatsAddonResources is the word an add-on uses for its resources. Noun and
// NounPlural are in Language when the manifest translates them, else in
// English; Language is empty for English.
type ChatsAddonResources struct {
	Noun       string `json:"noun,omitempty" example:"database"`
	NounPlural string `json:"noun_plural,omitempty" example:"databases"`
	Language   string `json:"language,omitempty" example:"it"`
}

// ChatsAgentResourceChoice is one resource you can give an agent.
type ChatsAgentResourceChoice struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
	// Role is your own role on it: viewer, editor or owner. A viewer cannot
	// give an agent the write mode in a useful way: the owner of the resource
	// still decides what you may change.
	Role string `json:"role" enums:"viewer,editor,owner"`
}

// ChatsAgentResourceChoices lists the resources of one add-on that you can
// give an agent.
type ChatsAgentResourceChoices struct {
	AddonID    string `json:"addon_id"`
	Noun       string `json:"noun,omitempty"`
	NounPlural string `json:"noun_plural,omitempty"`
	Language   string `json:"language,omitempty"`
	// Items are the resources you may use now, ordered by ID.
	Items []ChatsAgentResourceChoice `json:"items"`
	// Truncated is true when the list is cut short.
	Truncated bool `json:"truncated,omitempty"`
}

// ChatsConnection is one connection you can enable for an add-on. Name is its
// display name; Handle is the name the model uses in the connection argument
// of a tool call.
type ChatsConnection struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Handle string `json:"handle,omitempty"`
}

// ChatsConnectionSwitch sets the connections a chat may use for an add-on.
type ChatsConnectionSwitch struct {
	// Addon is the add-on ID.
	Addon string `json:"addon"`
	// Connections are the IDs of your connector bindings for that add-on that
	// the chat may use; empty clears the choice.
	Connections []string `json:"connections"`
	// Connection is the older form: one binding ID, read as a set of one when
	// connections is absent.
	Connection string `json:"connection,omitempty"`
}

// ChatsAddonList lists the add-ons a chat can use.
type ChatsAddonList struct {
	ExecutionStatus *ChatsExecutionStatus `json:"execution_status,omitempty"`

	Addons []ChatsAddon `json:"addons"`
	// Commands says whether the chat can run commands and code at its next
	// turn: available, off (the add-on that runs them is switched off for this
	// chat or not named by its agent), unavailable (installed, but it cannot run
	// them on this server), none (no add-on that runs them is installed for
	// the user) or switched_off (the chat switched the shell group off, so
	// nothing runs them whoever would serve them).
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
	// CommandsHostExec is the mode of the setting chats_host_exec as it applies
	// now (off, ask or on), so a client can say that the built-in bash is off
	// when no add-on runs commands. Every signed-in user may read it.
	CommandsHostExec string `json:"commands_host_exec,omitempty"`
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
	// Candidates come oldest install first; Default is the first, the one
	// that serves with no pin ("installed first"). InstalledAt maps a
	// candidate to its install time (RFC 3339, UTC) when known.
	Candidates  []string          `json:"candidates"`
	Default     string            `json:"default,omitempty"`
	Pinned      string            `json:"pinned,omitempty"`
	InstalledAt map[string]string `json:"installed_at,omitempty"`
	// Titles maps a candidate to its title. Stopped lists the candidates that
	// are installed but stopped or disabled: they can be chosen, and serve
	// once they run.
	Titles  map[string]string `json:"titles,omitempty"`
	Stopped []string          `json:"stopped,omitempty"`
	// Note is "not running" when an installed add-on replaces the tool but
	// none serves now, so the built-in serves (or nothing, when it is off).
	Note string `json:"note,omitempty"`
	// Mode is chats_host_exec as it applies (bash only): off, ask or on.
	Mode string `json:"mode,omitempty"`
	// Unavailable says why this server cannot run the built-in, or is empty.
	Unavailable string `json:"unavailable,omitempty"`
}

// ChatsBuiltinSkill is one built-in skill that installed add-ons replace, in
// Settings > Tools.
type ChatsBuiltinSkill struct {
	ID string `json:"id"`
	// Provider serves it now: builtin or an add-on's ID.
	Provider string `json:"provider"`
	// Candidates are the add-ons that replace it, by ID, and Titles their
	// titles; Pinned the one chosen in chats_tool_providers.
	// Candidates come oldest install first; Default is the first, the one
	// that serves with no pin. InstalledAt maps a candidate to its install
	// time (RFC 3339, UTC) when known.
	Candidates  []string          `json:"candidates"`
	Default     string            `json:"default,omitempty"`
	Titles      map[string]string `json:"titles,omitempty"`
	Pinned      string            `json:"pinned,omitempty"`
	InstalledAt map[string]string `json:"installed_at,omitempty"`
	// Note says why the built-in serves although an add-on replaces it, or is
	// empty: the chosen add-on is not running.
	Note string `json:"note,omitempty"`
}

// ChatsBuiltinToolList lists the built-in chat tools, and the built-in skills
// that at least one installed add-on replaces.
type ChatsBuiltinToolList struct {
	Tools  []ChatsBuiltinTool  `json:"tools"`
	Skills []ChatsBuiltinSkill `json:"skills"`
}

// ChatsNotification is one item of the inbox. Kind is run_ended, run_failed or
// needs_attention; Status is ok or failed. Source is what sent it: trigger,
// connector or system (empty on items stored before sources existed). Link is
// the in-app path that opens what it is about.
type ChatsNotification struct {
	ID      int64     `json:"id"`
	Kind    string    `json:"kind"`
	Status  string    `json:"status"`
	Source  string    `json:"source,omitempty"`
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
	// Total is how many notifications the user keeps, more than the list when a limit cut it.
	Total int `json:"total"`
}

// ChatsNotificationCount is the number of unread notifications. After a
// delete, Deleted is how many it removed.
type ChatsNotificationCount struct {
	Unread  int `json:"unread"`
	Deleted int `json:"deleted,omitempty"`
}

// ChatsNotificationsRead marks notifications read: the named ones, or all.
type ChatsNotificationsRead struct {
	IDs []int64 `json:"ids,omitempty"`
	All bool    `json:"all,omitempty"`
}

// ChatsNotificationsDelete names the notifications to delete: the ids, or all
// of them. With all, read_only keeps the unread ones. Ids and all together are
// refused.
type ChatsNotificationsDelete struct {
	IDs      []int64 `json:"ids,omitempty"`
	All      bool    `json:"all,omitempty"`
	ReadOnly bool    `json:"read_only,omitempty"`
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
	// Acts are the action classes that a run with nobody present may use:
	// act.files, act.addons, act.addons:<id>, act.send, act.spend. A run
	// without any only reads.
	Acts []string `json:"acts,omitempty"`
	// ActsHeld are the act.* names that apply to a run now: Acts within the
	// grants of the agent, plus act.files when the plan names a file it writes.
	// ReadOnly is true when a run can only read, and ReadOnlyWhy says why:
	// declared (the proposal said so), binding (a connection in read only mode
	// made it), no_acts (it holds none) or agent (its agent holds none of them).
	ActsHeld    []string `json:"acts_held,omitempty"`
	ReadOnly    bool     `json:"read_only"`
	ReadOnlyWhy string   `json:"read_only_why,omitempty"`
	// Delivery says where the answer of each run goes.
	Delivery ChatsTriggerDelivery `json:"delivery"`
}

// ChatsTriggerDelivery is where the answer of each run of a trigger goes.
// Target is inbox (the Inbox, the default), origin (the chat that proposed the
// trigger; InThread is set when that is a thread) or channel (a channel of a
// messaging app, at its top level, not in a thread; ChannelLabel is the name
// the owner gave it).
type ChatsTriggerDelivery struct {
	Target       string `json:"target"`
	ChannelLabel string `json:"channel_label,omitempty"`
	InThread     bool   `json:"in_thread,omitempty"`
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

// ChatsTriggerChange is a change of a trigger that a model asked for in a
// chat, and what became of it. Op is update, delete, pause or resume. State is
// pending (an update or a delete waits for Apply), applied, dismissed or
// undone; a pause or a resume is applied when it is asked for. Before and
// After are the trigger before and after the change (After is absent for a
// delete), and Fields the fields that differ. Stale is set on a pending change
// whose trigger was changed or removed since, so it can no longer apply.
// CanUndo is set on an applied change that can still be undone (for a day);
// UndoBlocked says why not: expired, edited, removed or full.
type ChatsTriggerChange struct {
	ID          string           `json:"id"`
	Op          string           `json:"op"`
	State       string           `json:"state"`
	TriggerID   string           `json:"trigger_id"`
	Before      ChatsTrigger     `json:"before"`
	After       *ChatsTrigger    `json:"after,omitempty"`
	Fields      []string         `json:"fields"`
	AnsweredAt  *time.Time       `json:"answered_at,omitempty"`
	Stale       bool             `json:"stale"`
	CanUndo     bool             `json:"can_undo"`
	UndoBlocked string           `json:"undo_blocked,omitempty"`
	LastRun     *ChatsTriggerRun `json:"last_run,omitempty"`
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
	// Acts are the action classes that a run with nobody present may use
	// (act.files, act.addons, act.addons:<id>, act.send, act.spend). Leave it
	// out to keep the trigger's value; an empty list takes all of them away.
	Acts []string `json:"acts,omitempty"`
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
	ID    string `json:"id"`
	Name  string `json:"name"`
	Kind  string `json:"kind"`
	Addon string `json:"addon"`
	// AgentID is empty for a binding of a kind that declares agent_optional.
	AgentID string `json:"agent_id"`
	// CredentialsOnly is true for a binding without an agent: it holds the
	// token, the resource list and the write mode, and never answers messages.
	CredentialsOnly bool     `json:"credentials_only,omitempty"`
	Enabled         bool     `json:"enabled"`
	AcceptAny       bool     `json:"accept_any"`
	Allow           []string `json:"allow"`
	// AllowTrust sets, for a chat entry of allow, what the people who write in
	// that chat may do: "answer" (the bot answers there, but its members are not
	// trusted one by one; the level of an entry that is not listed) or "act"
	// (every member may use the capabilities that are not sensitive, as a listed
	// person does; sensitive ones still need a pairing).
	AllowTrust map[string]string `json:"allow_trust,omitempty"`
	NotifyChat string            `json:"notify_chat,omitempty"`
	HasToken   bool              `json:"has_token"`
	Created    time.Time         `json:"created"`
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
	// ReplyLanguage is the language of the sentences the bot writes itself in
	// the chat (confirmations, refusals, notes about files): auto (the
	// language of the sender's message), en, it, de, es, fr or pt-BR. The
	// answers of the model follow the conversation, not this setting.
	ReplyLanguage string `json:"reply_language"`
	// UILanguage is the interface language the owner's page last sent, such as
	// it or pt-BR. With reply_language auto, the bot speaks in it before it
	// reads the language of the sender. Absent when the server was not told.
	UILanguage string `json:"ui_language,omitempty"`
	// Options are the choices made for the options of the kind, by option id.
	// An option that is absent has its default (see ChatsConnectorKind).
	Options map[string]string `json:"options,omitempty"`
	// Active is false when the add-on that serves the binding is not running. The
	// binding is kept and works once the add-on is installed.
	Active bool `json:"active"`
	// Reason says in plain words why the binding is not active.
	Reason string `json:"reason,omitempty"`
	// Files are the limits of the files the bot reads and sends, with the
	// limit that you set for this bot.
	Files ChatsConnectorFiles `json:"files"`
	// Duplicate is present when another enabled connection of yours is the
	// same bot (the add-on reported the same upstream account for both) and
	// this one is enabled too: both then answer the same message.
	Duplicate *ChatsConnectorDuplicate `json:"duplicate,omitempty"`
	// FileReceiveMaxBytes and FileSendMaxBytes are your limits for this bot;
	// zero means you set none and the limit of the kind applies.
	FileReceiveMaxBytes int64 `json:"file_receive_max_bytes"`
	FileSendMaxBytes    int64 `json:"file_send_max_bytes"`
	// Resources are the external resources, such as repositories, that the
	// tools of the add-on may reach with this binding. Nothing else is
	// reachable.
	Resources []string `json:"resources"`
	// Channels are the chats where the agent may write besides the
	// conversation. Only you can change the list.
	Channels []ChatsConnectorChannel `json:"channels"`
	// WriteMode says what the tools of the add-on may change with this
	// binding's credential: off (nothing), propose (each change waits for your
	// confirmation) or auto (a chat of yours applies it at once, except a tool
	// the add-on marks as always needing your confirmation).
	WriteMode string `json:"write_mode" enums:"off,propose,auto"`
	// Caps are the capabilities you enabled for this binding: the ceiling of
	// what any sender can use. Grants give roles of the kind some of them.
	Caps   []string                  `json:"caps"`
	Grants []ChatsConnectorRoleGrant `json:"grants"`
	// Paired are the senders you proved with a pairing code. A pairing ends
	// at expires_at.
	Paired []ChatsConnectorPaired `json:"paired"`
	// Approval is what the host does with a trigger that the agent proposes
	// from a chat of this binding.
	Approval ChatsConnectorApproval `json:"approval"`
}

// ChatsConnectorApproval is the owner's choice for the triggers that the agent
// proposes from a chat of a binding. mode is ask (the person answers the
// proposal in the chat in plain words; the default), auto (the trigger is
// made at once and the person is told how to cancel it) or auto_readonly (a
// trigger whose runs only read and report is made at once; any other
// proposal asks). The limits bound the modes that make a trigger without
// asking; a zero limit means its default (5 live triggers, one hour between
// runs of a recurring trigger, 10 runs a day, 30 days).
//
// acts is "runs may act": the act.* permissions that a trigger proposed from a
// chat of this binding may carry (act.files, act.addons, act.send, act.spend).
// An empty list, the default, means the runs only read and report. The list is
// a ceiling: the agent must also hold an act, when the trigger is proposed and
// at each run. Any other name is refused with 400.
type ChatsConnectorApproval struct {
	Mode            string   `json:"mode"`
	MaxPending      int      `json:"max_pending"`
	MinGapMinutes   int      `json:"min_gap_minutes"`
	MaxRunsPerDay   int      `json:"max_runs_per_day"`
	MaxLifetimeDays int      `json:"max_lifetime_days"`
	Acts            []string `json:"acts"`
}

// ChatsConnectorRoleGrant gives the capabilities caps to the senders that the
// add-on reports with the role role.
type ChatsConnectorRoleGrant struct {
	Role string   `json:"role"`
	Caps []string `json:"caps"`
}

// ChatsConnectorPaired is a paired sender of a binding.
type ChatsConnectorPaired struct {
	SenderID  string    `json:"sender_id"`
	PairedAt  time.Time `json:"paired_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// ChatsConnectorAccessDecision is whether one kind of sender may use one
// capability, and why not.
type ChatsConnectorAccessDecision struct {
	Capability string `json:"capability"`
	Allowed    bool   `json:"allowed"`
	// Via is how the capability is reached: paired, or role:<id>.
	Via    string `json:"via,omitempty"`
	Reason string `json:"reason,omitempty"`
	// Code says which link of the chain refused first. It is one of
	// server_setting_off, tools_switch_off, agent_permission_off,
	// agent_unavailable, connection_permission_off, no_agent,
	// sender_not_listed, sender_needs_pairing, channel_not_trusted,
	// role_not_granted, kind_not_running, context_rule or
	// unknown_capability. Only a refused capability has it.
	Code string `json:"code,omitempty"`
	// BlockedBy is the link that refused: server, tools, agent, connection
	// or sender.
	BlockedBy string `json:"blocked_by,omitempty"`
	// Message is one plain sentence for the owner. It names no setting.
	Message string `json:"message,omitempty"`
	// LocalizedMessage is message in the language of the request (see
	// ChatsConnectorAccess.Language), found by code. Absent when the
	// language is English or the code has no translation; message stays
	// English and code stays unchanged.
	LocalizedMessage string `json:"localized_message,omitempty"`
	// Fix points at the one place that solves it. The server sends no URL.
	Fix *ChatsConnectorAccessFix `json:"fix,omitempty"`
	// ThenCheck lists the links after the first problem that were not
	// looked at, in order: tools, agent, connection, sender and, for
	// triggers.propose, approval. More problems may follow.
	ThenCheck []string `json:"then_check,omitempty"`
}

// ChatsConnectorAccessFix is the one place that solves a refusal.
type ChatsConnectorAccessFix struct {
	// Kind is open_server_setting, open_agent, open_connection, pair_sender
	// or add_person.
	Kind string `json:"kind"`
	// Target is a stable ID: the key of a server setting, an agent ID, a field
	// of the connection form (caps, grants, agent_id, allow) or pairing.
	Target string `json:"target"`
	// Field narrows the target: the capability to tick on the connection or
	// to grant on the agent, or tools:<group> for a Tools switch.
	Field string `json:"field,omitempty"`
}

// ChatsConnectorAccessSubject is what one kind of sender may do: a sender on
// the allowlist (id anyone), a paired sender (paired) or a sender of a role
// the kind declares (role:<id>).
type ChatsConnectorAccessSubject struct {
	ID        string                         `json:"id"`
	Title     string                         `json:"title"`
	Decisions []ChatsConnectorAccessDecision `json:"decisions"`
	// NoteSave is present only when the agent of the connection has the save
	// rule auto (Save it and tell me): now when a note from this kind of
	// sender, in a one-to-one chat, is saved at once with an Undo, ask when it
	// waits for the owner.
	NoteSave string `json:"note_save,omitempty" enums:"now,ask"`
}

// ChatsConnectorAccessEvent is a recorded decision. It holds IDs, never text.
type ChatsConnectorAccessEvent struct {
	At         time.Time `json:"at"`
	Senders    []string  `json:"senders,omitempty"`
	Roles      []string  `json:"roles,omitempty"`
	Capability string    `json:"capability"`
	Allowed    bool      `json:"allowed"`
	Via        string    `json:"via,omitempty"`
	Reason     string    `json:"reason,omitempty"`
	// Code is the code of a refusal, as in the decisions.
	Code string `json:"code,omitempty"`
	// Addon and Tool name the add-on tool call, for an event in tool_outcomes;
	// capability is empty then. Outcome is ok, denied, held or error, code
	// the cause and status the HTTP status of the service behind
	// the add-on, when it answered one. Message is the owner's sentence for the
	// reason, in English, and localized_message the same in the language of the
	// request. An outcome holds names and codes, never arguments or results.
	Addon            string `json:"addon,omitempty"`
	Tool             string `json:"tool,omitempty"`
	Outcome          string `json:"outcome,omitempty" enums:"ok,denied,held,error"`
	Status           int    `json:"status,omitempty"`
	Message          string `json:"message,omitempty"`
	LocalizedMessage string `json:"localized_message,omitempty"`
}

// ChatsConnectorAccess is the effective permissions of a binding, as the host
// enforces them, with the latest decisions this server made.
type ChatsConnectorAccess struct {
	Binding  string                        `json:"binding"`
	Subjects []ChatsConnectorAccessSubject `json:"subjects"`
	Recent   []ChatsConnectorAccessEvent   `json:"recent"`
	// ToolOutcomes are the latest add-on tool calls that used this connection
	// or came in through this bot, newest first, at most 20. They reset when
	// the server restarts, like recent.
	ToolOutcomes []ChatsConnectorAccessEvent `json:"tool_outcomes,omitempty"`
	// Approvals lists the tools of the add-on whose confirmation the owner can
	// relax, and for whom each runs without asking now. Absent when the
	// add-on has none.
	Approvals []ChatsConnectorToolApproval `json:"approvals,omitempty"`
	// AddonTools lists the tools of the add-ons the agent uses that a
	// permission of the connection decides in a messaging chat, so the page can
	// say which tools a missing permission holds back. Absent when none.
	AddonTools []ChatsConnectorAddonTools `json:"addon_tools,omitempty"`
	// Preview is true when the view is for a form that is not saved.
	Preview bool `json:"preview,omitempty"`
	// Language is the language of the localized_message fields, negotiated
	// from the lang query parameter, then Accept-Language. Absent for
	// English.
	Language string `json:"language,omitempty"`
}

// ChatsConnectorAddonTools names the tools of one add-on that need one
// permission of the connection in a messaging chat.
type ChatsConnectorAddonTools struct {
	// Addon is the ID of the add-on.
	Addon string `json:"addon"`
	// Capability is the permission the tools need, such as tools.write.
	Capability string `json:"capability"`
	// Tools are the names of the tools inside the add-on.
	Tools []string `json:"tools"`
	// Connections are the connections of the add-on that the agent selected,
	// with their write modes. Only for tools.write; absent otherwise.
	Connections []ChatsConnectorAddonConnection `json:"connections,omitempty"`
	// WithoutTick lists the senders who use the tools although the permission
	// is not ticked, because the agent is connected to the add-on: "paired".
	// Absent when the tick is set, was taken away by the owner, or every
	// selected connection is in write mode off.
	WithoutTick []string `json:"without_tick,omitempty"`
	// TickRemoved is true when the owner took the tick away, so nobody is
	// covered without it.
	TickRemoved bool `json:"tick_removed,omitempty"`
}

// ChatsConnectorAddonConnection is a connection the agent selected for an
// add-on, as the access view names it.
type ChatsConnectorAddonConnection struct {
	// Name is the name the owner gave the connection.
	Name string `json:"name"`
	// WriteMode is the write mode of that connection: off, propose or auto.
	WriteMode string `json:"write_mode"`
}

// ChatsConnectorToolApproval is the confirmation of one tool that the owner
// can relax with their own setting on the page of the add-on.
type ChatsConnectorToolApproval struct {
	// Tool is the name of the tool inside the add-on.
	Tool string `json:"tool"`
	// Skip is true when the owner chose not to be asked for this tool.
	Skip bool `json:"skip"`
	// RunsWithoutAsking says for whom the tool runs without a confirmation
	// now: owner (the owner's own chats) and paired (a sender paired with the
	// bot). Empty when the setting is on ask or the write mode is off. Every
	// other sender is still asked.
	RunsWithoutAsking []string `json:"runs_without_asking,omitempty" enums:"owner,paired"`
}

// ChatsConnectorAccessPreviewRequest is the state of a connection form that
// the permission chain reads, saved or not. A field that is absent keeps the
// saved value. It has no token: a preview never takes or returns one.
type ChatsConnectorAccessPreviewRequest struct {
	// Kind is read only for the id "new" (a connection that is not saved).
	Kind      string   `json:"kind,omitempty"`
	AgentID   *string  `json:"agent_id,omitempty"`
	AcceptAny *bool    `json:"accept_any,omitempty"`
	Allow     []string `json:"allow,omitempty"`
	// AllowTrust is the form's trust levels for the chat entries of allow.
	AllowTrust map[string]string `json:"allow_trust,omitempty"`
	// Caps and Grants are the form's permissions; an empty list clears.
	Caps   []string                  `json:"caps,omitempty"`
	Grants []ChatsConnectorRoleGrant `json:"grants,omitempty"`
	// Sender limits the answer to one kind of sender: listed, paired,
	// channel_member, channel_acts, anyone or role:<id>. Absent means every kind.
	Sender string `json:"sender,omitempty"`
}

// ChatsConnectorPairing is a pairing code. Send it from the account to pair, as
// a direct message to the bot, before it expires.
type ChatsConnectorPairing struct {
	Code    string    `json:"code"`
	Expires time.Time `json:"expires"`
}

// ChatsConnectorRole is a role that a kind can give to a sender.
type ChatsConnectorRole struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
}

// ChatsConnectorCapability describes one capability of the closed list.
type ChatsConnectorCapability struct {
	Name        string `json:"name"`
	Title       string `json:"title"`
	Description string `json:"description"`
	// Sensitive capabilities are never granted to a role: only a paired
	// sender can use them.
	Sensitive bool `json:"sensitive"`
	// Usual capabilities are what a one-click preset turns on.
	Usual bool `json:"usual"`
	// Language is the language of localized_title and localized_description:
	// the one the request asked for (lang, else Accept-Language) among the
	// translated ones. Absent for English and for a language with no
	// translation; title and description stay English.
	Language             string `json:"language,omitempty"`
	LocalizedTitle       string `json:"localized_title,omitempty"`
	LocalizedDescription string `json:"localized_description,omitempty"`
}

// ChatsConnectorPermission is a capability that a kind lists. Sensitive ones
// are never granted to a role: only a paired sender can use them.
type ChatsConnectorPermission struct {
	Name      string `json:"name"`
	Sensitive bool   `json:"sensitive"`
}

// ChatsConnectorFiles are the file limits of a binding or of a kind, for both
// directions.
type ChatsConnectorFiles struct {
	// Receive is the limit of the files that people send to the bot.
	Receive ChatsConnectorFileLimit `json:"receive"`
	// Send is the limit of the files that the bot sends.
	Send ChatsConnectorFileLimit `json:"send"`
	// HostCeilingBytes is the ceiling of the server for a connector file, set
	// by the add-on owner; no limit above it applies.
	HostCeilingBytes int64 `json:"host_ceiling_bytes"`
}

// ChatsConnectorFileLimit is the limit of one direction.
type ChatsConnectorFileLimit struct {
	// EffectiveBytes is the largest file that moves now: the smallest of the
	// host ceiling, the declared ability and your limit.
	EffectiveBytes int64 `json:"effective_bytes"`
	// Layer names the one that sets EffectiveBytes: owner (your limit for
	// this bot), addon (the ability the kind declares), host (the ceiling of
	// the server) or inline (the cap of the inline path).
	Layer string `json:"layer"`
	// DeclaredBytes is the ability the kind declares, or zero.
	DeclaredBytes int64 `json:"declared_bytes"`
	// AllowedBytes is the most you may set for a bot.
	AllowedBytes int64 `json:"allowed_bytes"`
}

// ChatsConnectorKind is a kind of binding that an installed connector add-on serves.
type ChatsConnectorKind struct {
	Addon       string `json:"addon"`
	Kind        string `json:"kind"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	// Titles and Descriptions carry the translated text of the manifest by
	// language tag, when it has some. Title and Description stay English.
	Titles       map[string]string `json:"titles,omitempty"`
	Descriptions map[string]string `json:"descriptions,omitempty"`
	// Language, LocalizedTitle and LocalizedDescription are present only when
	// the request names a language (the lang query parameter, else the
	// Accept-Language header) that the kind translates. Each falls back to
	// English on its own.
	Language             string `json:"language,omitempty"`
	LocalizedTitle       string `json:"localized_title,omitempty"`
	LocalizedDescription string `json:"localized_description,omitempty"`
	SenderHint           string `json:"sender_hint,omitempty"`
	// Page is the add-on page that adds and edits bindings of this kind.
	Page string `json:"page,omitempty"`
	// AgentOptional is true when a binding of this kind may have no agent. It
	// then only holds credentials (token, resource list, write mode).
	AgentOptional bool `json:"agent_optional,omitempty"`
	// WildcardResources is true when a binding of this kind may list owner/*
	// or * as resources; * needs confirm_any_resource.
	WildcardResources bool `json:"wildcard_resources,omitempty"`
	// TokenGuards say which tokens of this kind are held back until the
	// binding chooses an option, and the confirm_wider_token text it needs.
	TokenGuards []ChatsConnectorTokenGuard `json:"token_guards,omitempty"`
	// ThreadHistory is true when the platform can give the earlier messages
	// of a thread. The host then reads them whenever the bot is mentioned in a
	// thread.
	ThreadHistory bool `json:"thread_history,omitempty"`
	// Options are the choices the add-on lets each binding of this kind make.
	Options []ChatsConnectorOption `json:"options,omitempty"`
	// Files are the limits of a binding of this kind that has no limit of
	// your own.
	Files ChatsConnectorFiles `json:"files"`
	// Roles are the roles the add-on can report for a sender, and
	// Permissions the capabilities the kind lists. Grant them to a role with
	// the grants of a binding.
	Roles       []ChatsConnectorRole       `json:"roles,omitempty"`
	Permissions []ChatsConnectorPermission `json:"permissions,omitempty"`
}

// ChatsConnectorTokenGuard holds back tokens that start with Prefix until the
// binding sets the option Option to Allow, in a request that also carries
// confirm_wider_token.
type ChatsConnectorTokenGuard struct {
	Prefix string `json:"prefix"`
	Option string `json:"option"`
	Allow  string `json:"allow"`
}

// ChatsConnectorOption is one choice of a connector kind.
type ChatsConnectorOption struct {
	ID          string                      `json:"id"`
	Title       string                      `json:"title"`
	Description string                      `json:"description,omitempty"`
	Values      []ChatsConnectorOptionValue `json:"values"`
	Default     string                      `json:"default"`
}

// ChatsConnectorOptionValue is one answer of a connector option.
type ChatsConnectorOptionValue struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// ChatsConnectorKindList lists the connector kinds of the add-ons that run now.
type ChatsConnectorKindList struct {
	Kinds []ChatsConnectorKind `json:"kinds"`
	// Capabilities is the closed list of capabilities that a binding can
	// enable, with a title and a description of each, for a settings page.
	Capabilities []ChatsConnectorCapability `json:"capabilities,omitempty"`
}

// ChatsConnectorWrite creates or replaces a connector binding. token is needed to
// create one; an update without it keeps the stored token.
type ChatsConnectorWrite struct {
	// Kind is the platform, as a connector add-on declares it (see the kinds
	// call). It is required to create a binding and ignored by an update.
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
	// AllowTrust sets the level of the chat entries of allow, "answer" or
	// "act" (see ChatsConnector.AllowTrust). Absent keeps the saved levels; an
	// entry with no level is "answer".
	AllowTrust map[string]string `json:"allow_trust,omitempty"`
	NotifyChat string            `json:"notify_chat,omitempty"`
	// ReplyMode is off, progress or stream; empty means progress.
	ReplyMode string `json:"reply_mode,omitempty"`
	// Conversation is sender, chat, window or thread; empty means sender.
	Conversation string `json:"conversation,omitempty"`
	// WindowMinutes is the time window of the window mode, 1 to 10080.
	WindowMinutes int `json:"window_minutes,omitempty"`
	// ReplyLanguage is auto, en, it, de, es, fr or pt-BR. Absent means auto
	// for a new binding and unchanged for an update.
	ReplyLanguage *string `json:"reply_language,omitempty" enums:"auto,en,it,de,es,fr,pt-BR"`
	// UILanguage is the interface language of the person who saves the
	// binding (a language tag such as it or pt-BR). The page of the add-on
	// sends it with each save. Absent or empty keeps the stored value; a value
	// that is not a language tag is ignored.
	UILanguage *string `json:"ui_language,omitempty"`
	// SendFiles turns the sending of a turn's files on or off. Absent means
	// on for a new binding and unchanged for an update.
	SendFiles *bool `json:"send_files,omitempty"`
	// Options are the choices for the options of the kind, by option id. Absent
	// means the defaults for a new binding and unchanged for an update.
	Options map[string]string `json:"options,omitempty"`
	// FileReceiveMaxBytes and FileSendMaxBytes limit the files the bot reads
	// and sends, in bytes. A limit can only lower the limit of the kind, which
	// the kind list shows as allowed_bytes. Zero removes your limit. Absent
	// means none for a new binding and unchanged for an update.
	FileReceiveMaxBytes *int64 `json:"file_receive_max_bytes,omitempty"`
	FileSendMaxBytes    *int64 `json:"file_send_max_bytes,omitempty"`
	// Caps are the capabilities you enable for the binding (a closed list,
	// see the kind's permissions for the ones it can grant to a role).
	// Absent means none for a new binding and unchanged for an update; an
	// empty list clears.
	Caps []string `json:"caps,omitempty"`
	// Grants map roles of the kind to capabilities within caps. A sensitive
	// capability is never granted to a role. Absent means unchanged; an empty
	// list clears.
	Grants []ChatsConnectorRoleGrant `json:"grants,omitempty"`
	// Resources are the external resources the tools of the add-on may reach
	// with this binding, such as "owner/repo", at most 50. Absent means none
	// for a new binding and unchanged for an update; an empty list clears it.
	Resources []string `json:"resources,omitempty"`
	// ConfirmAnyResource must be the exact text "allow any resource" in the
	// request that adds the entry * to resources. A kind that lists
	// wildcard_resources lets resources hold owner/* and *.
	ConfirmAnyResource string `json:"confirm_any_resource,omitempty"`
	// ConfirmWiderToken must be the exact text "accept wider token" in the
	// request that chooses the option a token guard of the kind names (for
	// example a classic access token). The token is refused without it.
	ConfirmWiderToken string `json:"confirm_wider_token,omitempty"`
	// ConfirmDuplicate must be the exact text "answer twice" to save an
	// enabled connection that is the same bot as another enabled connection of
	// yours. Without it the request is a 409 of type duplicate_connection, and
	// nothing is saved.
	ConfirmDuplicate string `json:"confirm_duplicate,omitempty"`
	// Channels are the chats of the platform where the agent may write
	// besides the conversation, at most 10: {id, label, approved}. A channel
	// that is not approved needs a paired sender for each message. Absent
	// means none for a new binding and unchanged for an update; an empty list
	// clears it.
	Channels []ChatsConnectorChannel `json:"channels,omitempty"`
	// WriteMode is off, propose or auto (see the connector record). Absent
	// means off for a new binding and unchanged for an update.
	WriteMode *string `json:"write_mode,omitempty" enums:"off,propose,auto"`
	// Approval sets how triggers that the agent proposes from a chat of the
	// binding are approved, and the limits of the automatic modes. Absent
	// means ask for a new binding and unchanged for an update. When it is
	// present it replaces the whole approval, acts included: send the acts
	// again to keep them. An act that is not in the closed list is refused.
	Approval *ChatsConnectorApproval `json:"approval,omitempty"`
}

// ChatsConnectorDuplicate says that a connection is the same bot as others.
type ChatsConnectorDuplicate struct {
	// Code is duplicate_connection.
	Code string `json:"code"`
	// Message is the plain sentence for the owner, in the language of the
	// request (lang, else Accept-Language) with English as the fallback.
	Message string `json:"message"`
	// Others are the other enabled connections of the same bot, oldest first.
	// The oldest enabled one is the one that answers.
	Others []ChatsConnectorDuplicateRef `json:"others"`
}

// ChatsConnectorDuplicateRef names another connection of the same bot.
type ChatsConnectorDuplicateRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ChatsConnectorChannel is a chat of the platform where the agent may write.
type ChatsConnectorChannel struct {
	ID       string `json:"id"`
	Label    string `json:"label,omitempty"`
	Approved bool   `json:"approved,omitempty"`
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
	// ToolDetails is each tool with the description its server gives, in the
	// order of tools.
	ToolDetails []ChatsMCPConnectorTool `json:"tool_details"`
	Error       string                  `json:"error,omitempty"`
}

// ChatsMCPConnectorTool is one tool of an MCP connector.
type ChatsMCPConnectorTool struct {
	Name        string `json:"name" example:"search"`
	Description string `json:"description,omitempty" example:"Search the documentation."`
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
	// OptIn says that the group is off until the chat switches it on with
	// "on" (saved_agents).
	OptIn bool `json:"opt_in,omitempty"`
	// Usable is false when the group cannot serve this chat now; Reason says
	// why.
	Usable bool   `json:"usable"`
	Reason string `json:"reason,omitempty"`
	// Provider is "builtin" when LocalAI serves the tools itself, the ID of
	// the add-on that serves them in this chat when one shadows them, or
	// "mixed" when the tools have different providers.
	Provider string `json:"provider" example:"builtin"`
	// Providers says who serves each tool, by tool name.
	Providers map[string]string `json:"providers" example:"web_search:builtin"`
	// Title and Description say what the group is and what the model can do
	// when its switch is on; WhenOff says what it cannot do when it is off.
	// They are English; the interface may translate them by group.
	Title       string `json:"title,omitempty" example:"Look things up online"`
	Description string `json:"description,omitempty" example:"The model can search the web and read a public page by its address."`
	WhenOff     string `json:"when_off,omitempty" example:"The model cannot look anything up online."`
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
	// Providers says what each provider of the order did with the query, in
	// order: every provider is asked, so the administrator sees which work.
	Providers []ChatsWebProviderTest `json:"providers"`
}

// ChatsWebProviderTest is what one search provider did with the test query.
type ChatsWebProviderTest struct {
	Name string `json:"name" example:"duckduckgo"`
	Host string `json:"host,omitempty" example:"duckduckgo.com"`
	// Outcome is ok, no_results, challenged (a bot check), blocked (403 or
	// 429), timeout or error.
	Outcome string `json:"outcome" example:"challenged"`
	Results int    `json:"results"`
	Reason  string `json:"reason,omitempty" example:"challenged by a bot check"`
}

// ChatsBuiltinSwitches is the body that sets the groups of built-in tools a
// chat has switched off.
type ChatsBuiltinSwitches struct {
	Off []string `json:"off"`
	// On lists the opt-in groups the chat switches on (saved_agents). Omit
	// it to keep them as they are.
	On *[]string `json:"on,omitempty"`
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
	// Fallbacks are the built-in tools that an installed add-on replaces but
	// that no add-on serves in this chat now: the add-on is stopped, off for
	// the chat, not granted by its agent, or cannot run.
	Fallbacks []ChatsToolFallback `json:"fallbacks,omitempty"`
	// Memory says whether memory works in this chat and, when it does not,
	// which level turned it off and where to turn it on again.
	Memory ChatsMemoryState `json:"memory"`
}

// ChatsMemoryState is whether memory works in one chat. The narrowest level
// that is off wins: this chat (a private chat), the agent of the chat, your
// memory switch, then the server.
type ChatsMemoryState struct {
	// On is true when the chat reads and may save notes.
	On bool `json:"on" example:"false"`
	// Code names the level that is off: memory_off_chat, memory_off_agent,
	// memory_off_user, memory_off_files or memory_provider_unavailable (the
	// add-on you picked to serve memory cannot serve now; provider names it).
	// Absent when on.
	Code string `json:"code,omitempty" example:"memory_off_chat"`
	// BlockedBy is who holds that switch: chat, agent, user or server.
	BlockedBy string `json:"blocked_by,omitempty" example:"chat"`
	// Reason is an English sentence for logs; the interface words it by Code.
	Reason string `json:"reason,omitempty"`
	// Fix says where to turn memory on again: kind open_tools_menu,
	// open_memory_settings, open_agent or open_server_setting.
	Fix *ChatsMemoryFix `json:"fix,omitempty"`
	// Provider is the ID of the add-on that cannot serve, with the code
	// memory_provider_unavailable, and ProviderTitle the name it shows.
	Provider      string `json:"provider,omitempty"`
	ProviderTitle string `json:"provider_title,omitempty"`
}

// ChatsProviderSlots is the list of provider slots of the host.
type ChatsProviderSlots struct {
	Slots []ChatsProviderSlot `json:"slots"`
}

// ChatsProviderSlot is a job the host does itself and lets an add-on do in its
// place, with the providers you can pick and the choice in force.
type ChatsProviderSlot struct {
	ID          string `json:"id" example:"memory"`
	Version     int    `json:"version" example:"1"`
	Title       string `json:"title" example:"Memory"`
	Description string `json:"description"`
	// OffMeans is what Off does for this slot.
	OffMeans   string                  `json:"off_means,omitempty"`
	Providers  []ChatsProvider         `json:"providers"`
	Resolution ChatsProviderResolution `json:"resolution"`
	// Health says when the host leaves the provider alone after repeated
	// failures. Absent while the provider answers.
	Health *ChatsProviderHealth `json:"health,omitempty"`
}

// ChatsProviderHealth is the circuit breaker of the provider that serves a slot.
type ChatsProviderHealth struct {
	// CoolingDown is true while the host does not call the provider.
	CoolingDown bool `json:"cooling_down"`
	// RetryAt is when the host calls the provider again (RFC 3339).
	RetryAt string `json:"retry_at,omitempty"`
	// Last is the outcome of the failure that stopped the calls: failed,
	// timeout or bad_result.
	Last string `json:"last,omitempty" example:"timeout"`
	// Notice is a plain sentence for the person.
	Notice string `json:"notice,omitempty"`
}

// ChatsMemoryForget is what a forget of the provider that serves your memory did.
type ChatsMemoryForget struct {
	// Done is true when the provider forgot. Otherwise the change waits for you
	// to confirm it on the Add-ons page.
	Done bool `json:"done"`
	// Proposal names the change that waits.
	Proposal string `json:"proposal,omitempty"`
}

// ChatsProvider is one provider you can pick for a slot.
type ChatsProvider struct {
	// ID is builtin, off or the ID of an add-on.
	ID          string `json:"id" example:"builtin"`
	Title       string `json:"title" example:"Built-in"`
	Description string `json:"description,omitempty"`
	// Kind is builtin, addon or off.
	Kind string `json:"kind" enums:"builtin,addon,off" example:"builtin"`
	// Available is false for an add-on that cannot serve now.
	Available bool `json:"available"`
	// Properties are the facts the add-on declared about itself, such as
	// remote and undo.
	Properties map[string]any `json:"properties,omitempty"`
	// Roles are the roles of the slot that the add-on fills with tools.
	Roles    []string `json:"roles,omitempty"`
	Selected bool     `json:"selected"`
}

// ChatsProviderResolution is what serves a slot for you now.
type ChatsProviderResolution struct {
	Slot string `json:"slot" example:"memory"`
	// Selected is your stored choice, or the default of the slot.
	Selected string `json:"selected" example:"builtin"`
	// Stored is true once you chose.
	Stored bool `json:"stored"`
	// Active is what serves: builtin, an add-on ID or off.
	Active string `json:"active" example:"builtin"`
	Off    bool   `json:"off"`
	// Code says why the slot is off: provider_off, provider_unavailable (the
	// add-on you picked cannot serve; the slot does not move to the built-in
	// provider) or provider_unreadable. Absent when on.
	Code string `json:"code,omitempty" example:"provider_unavailable"`
	// Reason is an English sentence for logs; the interface words it by Code.
	Reason string `json:"reason,omitempty"`
	// AddonID and AddonTitle name the add-on a failure is about.
	AddonID    string `json:"addon_id,omitempty"`
	AddonTitle string `json:"addon_title,omitempty"`
}

// ChatsProviderRequest picks the provider of a slot.
type ChatsProviderRequest struct {
	// Provider is builtin, off (when the slot allows it) or the ID of an add-on
	// that declares the slot.
	Provider string `json:"provider" example:"builtin"`
}

// ChatsMemoryFix points at the one place that turns memory on again.
type ChatsMemoryFix struct {
	Kind   string `json:"kind" example:"open_memory_settings"`
	Target string `json:"target" example:"memory"`
	Field  string `json:"field,omitempty"`
}

// ChatsMemorySettings is your memory choices.
type ChatsMemorySettings struct {
	// Enabled is memory On or Off for all your chats and agents.
	Enabled bool `json:"enabled" example:"true"`
	// EnabledSet is true once you chose. Before that memory is on, unless you
	// had disabled the Memory add-on to stop it.
	EnabledSet bool `json:"enabled_set" example:"false"`
	// SaveMode is how a note is saved: auto (save it and tell me), ask (ask me
	// first, the default), always (always confirm: like ask, and nothing can
	// loosen it; undo and forget by the model wait for you too) or never.
	SaveMode string `json:"save_mode" enums:"auto,ask,always,never" example:"ask"`
	// MessagingSave is how a note proposed in a chat of a messaging app is
	// saved: ask (the bot asks in the chat; the default), paired_private
	// (saved at once to the conversation when the sender is paired and the
	// chat is one-to-one) or never. It cannot loosen save_mode: with always or
	// never the row has no looser effect.
	MessagingSave string `json:"messaging_save" enums:"ask,paired_private,never" example:"ask"`
	// SaveModeSet is true once you chose in this setting.
	SaveModeSet bool `json:"save_mode_set" example:"false"`
	// Provider is what serves memory: builtin.
	Provider string `json:"provider" example:"builtin"`
	// Available is false when the server has no files, which notes need.
	Available bool `json:"available" example:"true"`
}

// ChatsMemoryRequest changes your memory choices. A field left out stays as
// it is.
type ChatsMemoryRequest struct {
	Enabled  *bool   `json:"enabled,omitempty" example:"false"`
	SaveMode *string `json:"save_mode,omitempty" enums:"auto,ask,always,never" example:"ask"`
	// MessagingSave is the messaging row (see ChatsMemorySettings).
	MessagingSave *string `json:"messaging_save,omitempty" enums:"ask,paired_private,never" example:"ask"`
}

// ChatsToolFallback is one built-in tool whose replacing add-on cannot serve
// the chat now.
type ChatsToolFallback struct {
	// Tool is the built-in tool, such as bash.
	Tool string `json:"tool" example:"bash"`
	// Addon and AddonTitle name the add-on that would serve it: the one an
	// administrator chose, or the one installed first.
	Addon      string `json:"addon" example:"sandbox"`
	AddonTitle string `json:"addon_title" example:"Sandbox"`
	// Builtin says that the built-in tool serves instead; false when the
	// built-in is off too, so the chat has no such tool.
	Builtin bool `json:"builtin"`
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

// ChatsEgressRule is one allow or deny rule of a chat's Sandbox network.
type ChatsEgressRule struct {
	Host  string    `json:"host"`
	Port  int       `json:"port"`
	Allow bool      `json:"allow"`
	By    string    `json:"by,omitempty"`
	Admin bool      `json:"admin,omitempty"`
	At    time.Time `json:"at"`
	// AboveLimit says the ceiling no longer admits the host, so the rule
	// does not apply until an administrator raises it.
	AboveLimit bool `json:"above_limit,omitempty"`
}

// ChatsEgressCard is a card that asked whether a command may reach a host.
type ChatsEgressCard struct {
	ID      string `json:"id"`
	Host    string `json:"host"`
	Port    int    `json:"port"`
	Kind    string `json:"kind"`
	State   string `json:"state"` // pending, once, chat, denied, expired, refused
	Private bool   `json:"private,omitempty"`
	// AboveCeiling says the administrator's limit does not admit this host,
	// so no answer can make it work; only a higher limit does.
	AboveCeiling bool       `json:"above_ceiling,omitempty"`
	ToolSeq      int64      `json:"tool_seq,omitempty"`
	AskedAt      time.Time  `json:"asked_at"`
	AnsweredAt   *time.Time `json:"answered_at,omitempty"`
	AnsweredBy   string     `json:"answered_by,omitempty"`
}

// ChatsEgressRegistry is one package registry the preset registries admits.
type ChatsEgressRegistry struct {
	ID    string   `json:"id"`
	Title string   `json:"title"`
	Hosts []string `json:"hosts"`
}

// ChatsEgress is the network of a chat's Sandbox commands.
type ChatsEgress struct {
	Preset    string `json:"preset"`    // off, registries, all
	Ceiling   string `json:"ceiling"`   // the administrator's limit
	Effective string `json:"effective"` // the lower of the two
	// Applies is true when the add-on that runs the commands reports that it
	// enforces the network policy. It is false when commands run on this
	// machine (applies_reason host), or no Sandbox serves bash or the add-on
	// does not report enforcement (none).
	Applies       bool   `json:"applies"`
	AppliesReason string `json:"applies_reason,omitempty"`
	// Addon is the add-on that runs the chat's commands and carries the
	// network-policy host control: the chat draws its Network row and links
	// an administrator to that add-on's settings. Absent when applies is
	// false or no such add-on runs the commands.
	Addon string `json:"addon,omitempty" example:"sandbox"`
	// Network names the network of addon in text and gives the preset titles
	// the add-on declares. Set with addon.
	Network    *ChatsEgressNetwork   `json:"network,omitempty"`
	Registries []ChatsEgressRegistry `json:"registries"`
	// AllowedHosts is the administrator's always-allowed list (host or
	// host:port), read only here: it applies when the effective preset is
	// registries or all.
	AllowedHosts []string          `json:"allowed_hosts"`
	Rules        []ChatsEgressRule `json:"rules"`
	Cards        []ChatsEgressCard `json:"cards"`
}

// ChatsEgressNetwork is the network of the add-on that runs the commands, as
// the add-on declares it.
type ChatsEgressNetwork struct {
	// Title names the network in text: the declared title, else the title of
	// the commands tool.
	Title string `json:"title" example:"Sandbox"`
	// Presets are the titles the add-on gives the host's presets; a preset
	// that is not listed keeps the host's title.
	Presets []ChatsEgressNetworkPreset `json:"presets"`
}

// ChatsEgressNetworkPreset is the title an add-on gives one preset.
type ChatsEgressNetworkPreset struct {
	ID    string `json:"id" enums:"off,registries,all" example:"off"`
	Title string `json:"title" example:"Closed"`
}

// ChatsEgressPreset sets the preset of a chat.
type ChatsEgressPreset struct {
	Preset string `json:"preset"`
}

// ChatsEgressConsent answers a card: once (for the command in progress),
// chat (a rule for the chat) or deny (remembered for the chat).
type ChatsEgressConsent struct {
	ID       string `json:"id"`
	Decision string `json:"decision"`
}

// ChatsEgressAdmin is the administrator's view of the network of commands.
type ChatsEgressAdmin struct {
	Ceiling         string                `json:"ceiling"`
	Default         string                `json:"default"`
	AllowPrivate    bool                  `json:"allow_private"`
	Ports           []int                 `json:"ports"`
	ExtraRegistries []string              `json:"extra_registries"`
	AllowedHosts    []string              `json:"allowed_hosts"`
	AuditDays       int                   `json:"audit_days"`
	Registries      []ChatsEgressRegistry `json:"registries"`
	// Sandbox is the add-on that runs commands, empty when none serves (the
	// name of the field is kept for existing clients); Isolation is isolated,
	// host, or empty when unknown.
	Sandbox   string `json:"sandbox,omitempty"`
	Isolation string `json:"isolation,omitempty"`
	// Network names the network of that add-on in text. Set with sandbox.
	Network *ChatsEgressNetwork `json:"network,omitempty"`
	Allowed int64               `json:"allowed_24h"`
	Blocked int64               `json:"blocked_24h"`
}

// ChatsEgressEvent is one line of the audit of network requests.
type ChatsEgressEvent struct {
	User       string    `json:"user"`
	UserName   string    `json:"user_name,omitempty"`
	Chat       string    `json:"chat"`
	Host       string    `json:"host"`
	Port       int       `json:"port"`
	Kind       string    `json:"kind"`
	Verdict    string    `json:"verdict"` // allow, block, done
	Reason     string    `json:"reason"`
	Addr       string    `json:"addr,omitempty"`
	BytesUp    int64     `json:"bytes_up,omitempty"`
	BytesDown  int64     `json:"bytes_down,omitempty"`
	DurationMS int64     `json:"duration_ms,omitempty"`
	At         time.Time `json:"at"`
}

// ChatsEgressEventList is the audit, newest first.
type ChatsEgressEventList struct {
	Events []ChatsEgressEvent `json:"events"`
}

// ChatsExecutionStatus is presentation-only; it grants no execution authority.
type ChatsExecutionStatus struct {
	Readiness   string `json:"readiness"`
	Isolation   string `json:"isolation"`
	Explanation string `json:"explanation,omitempty"`
	// UnknownReason says why the status is unknown: owner_unreachable,
	// not_reported, stale, invalid_status, not_selected, no_answer,
	// reported_unknown or draft (the chat does not exist yet). Absent when the status is known.
	UnknownReason string `json:"unknown_reason,omitempty" example:"owner_unreachable"`
	Generation    string `json:"generation,omitempty"`
	Incarnation   string `json:"incarnation,omitempty"`
}

// ChatsBuiltinGrant describes one builtin grant for the agent editor.
type ChatsBuiltinGrant struct {
	// Name is the value to put in an agent's builtins list.
	Name  string `json:"name"`
	Title string `json:"title"`
	// Description says what the agent can do with the grant, in English.
	Description string `json:"description"`
	// Sensitive grants change what the agent may do or reach later. They are
	// never on by default and the editor marks them.
	Sensitive bool `json:"sensitive"`
}

// ChatsBuiltinGrantList is the list of builtin grants, in display order.
type ChatsBuiltinGrantList struct {
	Grants []ChatsBuiltinGrant `json:"grants"`
}
