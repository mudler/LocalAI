package workerctl

// The verbs of an agent worker. An agent worker runs no backend processes and
// stages no files, so it serves none of AllVerbs except the stop of a backend
// (VerbBackendStop), which it answers with no body. What it serves instead is MCP
// and the runs that a frontend hands to it.
//
// They are not in AllVerbs, because a spec over that list asks the backend
// worker to serve every entry.
const (
	// VerbMCPToolExecute runs one MCP tool. Unary.
	VerbMCPToolExecute = "mcp.tools.execute"
	// VerbMCPDiscovery lists the MCP servers, tools, prompts and resources of a
	// model. Unary.
	VerbMCPDiscovery = "mcp.discovery"
	// VerbAgentExecute runs one agent chat. It streams: progress lines carry the
	// events of the run, and the reply line is a RunReply.
	VerbAgentExecute = "agent.execute"
	// VerbMCPCIRun runs one MCP CI job. It streams like VerbAgentExecute.
	VerbMCPCIRun = "mcp-ci.run"
)

// AgentVerbs returns the verbs of an agent worker once, not counting
// VerbBackendStop.
func AgentVerbs() []string {
	return []string{VerbMCPToolExecute, VerbMCPDiscovery, VerbAgentExecute, VerbMCPCIRun}
}

// RunReply is the reply line of a streaming run verb.
//
// It says what JobResultEvent says, because it is the same fact: the work
// ended, and how. On a bus the result is a message that a subscriber must
// already have. Here it is the last line of the response that the caller is
// reading, so it cannot be published to nobody. A run that names no job (an
// agent chat) has an empty JobID.
type RunReply struct {
	JobID  string `json:"job_id,omitempty"`
	Status string `json:"status,omitempty"` // "completed", "failed" or "cancelled"
	Result string `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}
