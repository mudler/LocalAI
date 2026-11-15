package schema

import "time"

// ChatsProjectDefaults are the resources a new chat in a project starts with.
// An empty list and a missing list both mean no override; a non-empty list
// means only those that are available are on.
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
	Updated  time.Time            `json:"updated"`
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
