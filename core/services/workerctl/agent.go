package workerctl

// The verbs of an agent worker. An agent worker runs no backend processes and
// stages no files, so it serves none of AllVerbs except the stop of a backend
// (VerbBackendStop), which it answers with no body. What it serves instead is MCP.
//
// They are not in AllVerbs, because a spec over that list asks the backend
// worker to serve every entry.
const (
	// VerbMCPToolExecute runs one MCP tool. Unary.
	VerbMCPToolExecute = "mcp.tools.execute"
	// VerbMCPDiscovery lists the MCP servers, tools, prompts and resources of a
	// model. Unary.
	VerbMCPDiscovery = "mcp.discovery"
)

// AgentVerbs returns the verbs of an agent worker once, not counting
// VerbBackendStop.
func AgentVerbs() []string {
	return []string{VerbMCPToolExecute, VerbMCPDiscovery}
}
