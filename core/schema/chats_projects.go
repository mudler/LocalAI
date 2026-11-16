package schema

import "time"

// ChatsProjectDefaults are the resources a new chat in a project starts with:
// skills, add-ons, connectors, MCP servers and collections. An empty
// list and a missing list both mean no override; a non-empty list means only
// those that are available are on.
type ChatsProjectDefaults struct {
	Skills      []string `json:"skills,omitempty"`
	Addons      []string `json:"addons,omitempty"`
	Connectors  []string `json:"connectors,omitempty"`
	MCPServers  []string `json:"mcp_servers,omitempty"`
	Collections []string `json:"collections,omitempty"`
}

// ChatsProject is one project of the caller.
type ChatsProject struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Slug is the name of the project's folder under Projects/ in the caller's files.
	Slug string `json:"slug"`
	// Status is active, archived or broken. Error says why a project is broken.
	Status   string               `json:"status"`
	Error    string               `json:"error,omitempty"`
	Defaults ChatsProjectDefaults `json:"defaults"`
	// Attached lists the tools the project holds. Every chat and agent of the
	// project has them on, with no per-chat step, on each turn, and a chat
	// cannot switch them off. Defaults stay the limit: an attached entry
	// outside a non-empty limit is refused on write. The list is empty while
	// AttachedBlocked is true.
	Attached ChatsProjectDefaults `json:"attached"`
	// AttachedBlocked is true when the project file holds an attached block
	// that is ignored because another person may write the project folder (a
	// share with write access). The skills of the project have the same rule
	// (SkillsBlocked).
	AttachedBlocked bool `json:"attached_blocked,omitempty"`
	// SkillsBlocked is true when the project's own skills are off because
	// another person may write the project folder (a share with write access).
	// It is set on a read of one project; the list leaves it out.
	SkillsBlocked bool      `json:"skills_blocked,omitempty"`
	Updated       time.Time `json:"updated"`
	// ChatCount is the number of chats in the project, counted up to 500.
	ChatCount int `json:"chat_count"`
	// AgentCount is the number of saved agents that belong to the project.
	AgentCount int `json:"agent_count"`
}

// ChatsProjectAgent is one agent of a project, as a brief row.
type ChatsProjectAgent struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Model       string `json:"model,omitempty"`
	// Unavailable is true while the agent cannot start: its project is
	// archived, deleted or broken, or its folders changed.
	Unavailable bool `json:"unavailable"`
	// FilesMode is read or write: how the agent uses the files of the project.
	FilesMode string `json:"files_mode"`
	// MemoryWrite is true when the agent holds the grant project.memory.write.
	MemoryWrite bool `json:"memory_write"`
}

// ChatsProjectAgentList is the response of the project agents endpoint.
type ChatsProjectAgentList struct {
	Agents []ChatsProjectAgent `json:"agents"`
}

// ChatsProjectConnection is a messaging connection bound to an agent of a
// project, as a brief row. It never carries a token or a list of senders.
type ChatsProjectConnection struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	AgentID   string `json:"agent_id"`
	AgentName string `json:"agent_name"`
	Enabled   bool   `json:"enabled"`
}

// ChatsProjectConnectionList is the response of the project connections endpoint.
type ChatsProjectConnectionList struct {
	Connections []ChatsProjectConnection `json:"connections"`
}

// ChatsProjectTrigger is a trigger of an agent of a project, as a brief row.
type ChatsProjectTrigger struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	AgentID   string `json:"agent_id"`
	AgentName string `json:"agent_name"`
	Schedule  string `json:"schedule,omitempty"`
	Folder    string `json:"folder,omitempty"`
	Enabled   bool   `json:"enabled"`
	// Proposed is always false for now: a trigger that a model proposed and
	// you have not confirmed is not listed. The field keeps the shape stable.
	Proposed bool `json:"proposed"`
	// Next is when a schedule trigger runs next. It is absent for a trigger
	// that is off, a file trigger, or a schedule with no later time.
	Next *time.Time `json:"next,omitempty"`
}

// ChatsProjectTriggerList is the response of the project triggers endpoint.
type ChatsProjectTriggerList struct {
	Triggers []ChatsProjectTrigger `json:"triggers"`
}

// ChatsProjectLoop is a loop in a chat of an agent of a project, as a brief
// row. Prompt is cut to 200 characters.
type ChatsProjectLoop struct {
	ID        string `json:"id"`
	ChatID    string `json:"chat_id"`
	ChatTitle string `json:"chat_title"`
	Prompt    string `json:"prompt"`
	State     string `json:"state"`
}

// ChatsProjectLoopList is the response of the project loops endpoint.
type ChatsProjectLoopList struct {
	Loops []ChatsProjectLoop `json:"loops"`
}

// ChatsProjectWrite creates or changes a project. On a change a field that is
// left out keeps its value.
type ChatsProjectWrite struct {
	Name        *string               `json:"name,omitempty"`
	Description *string               `json:"description,omitempty"`
	Defaults    *ChatsProjectDefaults `json:"defaults,omitempty"`
	Attached    *ChatsProjectDefaults `json:"attached,omitempty"`
}

// ChatsProjectList is the response of the project list endpoint.
type ChatsProjectList struct {
	Projects []ChatsProject `json:"projects"`
}

// ChatsProjectMemoryFile is one topic file of the memory of a project.
type ChatsProjectMemoryFile struct {
	Name     string    `json:"name"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
}

// ChatsProjectMemory is the memory of a project: the index, and the topic
// files next to it.
type ChatsProjectMemory struct {
	Index string                   `json:"index"`
	Files []ChatsProjectMemoryFile `json:"files"`
}

// ChatsProjectMemoryWrite replaces one memory file of a project.
type ChatsProjectMemoryWrite struct {
	Content string `json:"content"`
}

// ChatsChatProject moves a chat into a project. An empty project_id takes it out.
type ChatsChatProject struct {
	ProjectID string `json:"project_id"`
}

// ChatsChatPulledProjects sets the other projects a chat reads from.
type ChatsChatPulledProjects struct {
	ProjectIDs []string `json:"project_ids"`
}

// ChatsSharedProject is a project of another person that is shared with you.
// ref is the only identity: it is opaque ("s:<number>"), and the owner's
// account, the paths and the manifest id never appear.
type ChatsSharedProject struct {
	// Ref names the project in the other shared-project routes and in the
	// list a chat pulls from.
	Ref         string `json:"ref" example:"s:12"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Status is "active", "archived" or "broken". An archived or broken
	// project is listed but is not available.
	Status string `json:"status" enums:"active,archived,broken"`
	// Mode is what you may do in the project folder: "read" or "write".
	Mode string `json:"mode" enums:"read,write"`
	// OwnerName is the display name of the owner.
	OwnerName string `json:"owner_name"`
	// Error says why a broken project cannot be read.
	Error string `json:"error,omitempty"`
}

// ChatsSharedProjectList is the response of the shared projects endpoint.
type ChatsSharedProjectList struct {
	Projects []ChatsSharedProject `json:"projects"`
}

// ChatsSharedProjectMemory is the memory of a shared project: the index, and
// the names of the topic files next to it.
type ChatsSharedProjectMemory struct {
	Index string   `json:"index"`
	Files []string `json:"files"`
}

// ChatsAgentMove is the body of the move of an agent to another project, or
// out of one.
type ChatsAgentMove struct {
	// ProjectID is an active project of yours, or empty to take the agent out
	// of its project.
	ProjectID string `json:"project_id"`
	// DryRun reports what the move would do and writes nothing.
	DryRun bool `json:"dry_run,omitempty"`
	// FilesMode is read or write (the default): how the agent uses the files
	// folder of the new project.
	FilesMode string `json:"files_mode,omitempty"`
	// Folders are the folders the agent keeps when it leaves a project. They
	// are required then, unless WholeHome is true.
	Folders []ChatsAgentFolderWrite `json:"folders,omitempty"`
	// WholeHome lets an agent that leaves a project work on all your files.
	WholeHome bool `json:"whole_home,omitempty"`
	// RevokeShares removes the grants of a shared agent so that it can enter a
	// project.
	RevokeShares bool `json:"revoke_shares,omitempty"`
	// DisableTriggers switches off the file triggers whose folder is outside
	// the new folders, in place of refusing the move.
	DisableTriggers bool `json:"disable_triggers,omitempty"`
}

// ChatsAgentMoveTrigger is a trigger the move of an agent touches.
type ChatsAgentMoveTrigger struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ChatsAgentMoveTools lists what the limits of the new project take away from
// the agent.
type ChatsAgentMoveTools struct {
	Skills      []string `json:"skills"`
	Addons      []string `json:"addons"`
	MCPServers  []string `json:"mcp_servers"`
	Collections []string `json:"collections"`
}

// ChatsAgentMoveAttached lists tools a project attaches, by kind.
type ChatsAgentMoveAttached struct {
	Skills      []string `json:"skills"`
	Addons      []string `json:"addons"`
	Connectors  []string `json:"connectors"`
	MCPServers  []string `json:"mcp_servers"`
	Collections []string `json:"collections"`
}

// ChatsAgentMoveReport is what a move of an agent does, or would do.
type ChatsAgentMoveReport struct {
	DryRun bool `json:"dry_run"`
	// Moved is true when the agent was moved.
	Moved bool   `json:"moved"`
	From  string `json:"from,omitempty"`
	To    string `json:"to,omitempty"`
	// Chats is the number of chats of the agent that stop taking turns, and
	// Loops the number of their loops that end.
	Chats int `json:"chats"`
	Loops int `json:"loops"`
	// TriggersOutside are the file triggers that watch a folder outside the
	// new folders.
	TriggersOutside []ChatsAgentMoveTrigger `json:"triggers_outside"`
	// Shares is the number of people the agent is shared with.
	Shares int `json:"shares"`
	// Proposals is the number of waiting project notes of the agent that are
	// voided.
	Proposals          int                 `json:"proposals"`
	DroppedTools       ChatsAgentMoveTools `json:"dropped_tools"`
	MemoryGrantDropped bool                `json:"memory_grant_dropped"`
	// AttachedNew is what the new project attaches: the agent gets it on every
	// run after the move.
	AttachedNew ChatsAgentMoveAttached `json:"attached_new"`
	// AttachedOld is what the old project attached and the new one does not.
	// It stops applying to the agent.
	AttachedOld ChatsAgentMoveAttached `json:"attached_old"`
	// ProjectSkillsSwitch is true when the agent's project skills change: the
	// skills of the old project stop and those of the new project start. It is
	// false when neither project has a skill in its own layer.
	ProjectSkillsSwitch bool `json:"project_skills_switch"`
	// Blockers lists why the move cannot be done as asked: busy, shared or
	// triggers_outside.
	Blockers []string `json:"blockers"`
	// Warnings lists steps after the move that did not finish.
	Warnings []string `json:"warnings,omitempty"`
}
