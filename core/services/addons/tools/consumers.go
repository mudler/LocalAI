// SPDX-License-Identifier: MIT
package tools

// Decision says what a consumer does with add-on tools.
type Decision string

const (
	// Route serves add-on tools through the host broker with an immutable
	// actor taken from the authenticated request.
	Route Decision = "route"
	// Reject never serves add-on tools: the namespace is refused or filtered
	// there, so a call can only fail with a typed error.
	Reject Decision = "reject"
)

// Consumer is one code path that can discover or execute a tool in the
// product, and what it does with add-on tools. The list is executable: the
// consumer matrix test scans the sources for the markers of tool discovery and
// execution and fails for a file no entry names, so a new consumer cannot ship
// without a decision. The same table is in docs/content/features/tables.md.
type Consumer struct {
	// ID is the stable name used in the documentation table.
	ID string
	// Surface says who uses it and how.
	Surface string
	// Files are the source files that hold the discovery or execution
	// markers of this consumer. Each must exist and must still hold a marker.
	Files []string
	// Evidence are other files that carry the decision (a route, a filter, a
	// rejection). Each must exist.
	Evidence []string
	Decision Decision
	// Actor says where the actor of a routed consumer comes from.
	Actor string
	// Reason and Enforcement say why a consumer rejects add-on tools and
	// what refuses them.
	Reason      string
	Enforcement string
}

// Consumers is the complete inventory of the code paths that discover or
// execute tools.
var Consumers = []Consumer{
	{
		ID:       "chat-completions",
		Surface:  "POST /v1/chat/completions, with metadata addon_tools set by the chat UI or an API client",
		Files:    []string{"core/http/endpoints/openai/chat.go"},
		Evidence: []string{"core/http/routes/openai.go", "core/http/endpoints/mcp/addon_tools.go"},
		Decision: Route,
		Actor:    "the named user or API key that authenticated the request, read once per request by auth.AddonActorForMode; local-owner mode comes from deployment configuration",
	},
	{
		ID:       "direct-api",
		Surface:  "POST /api/addons/dispatch, /api/addons/proposals and the other /api/addons routes, called by the UI, scripts and agents",
		Evidence: []string{"core/http/endpoints/addons/gateway.go", "core/http/endpoints/addons/proposals.go", "core/http/endpoints/addons/resources.go", "core/http/routes/addons.go"},
		Decision: Route,
		Actor:    "the named user or API key of the request, revalidated against stored accounts on every call",
	},
	{
		ID:          "addon-tool-executor",
		Surface:     "the executor that serves the add-on namespace inside a chat request",
		Files:       []string{"core/http/endpoints/mcp/addon_tools.go"},
		Decision:    Route,
		Actor:       "one actor value bound when the executor is created; it has no setter and arguments cannot change it",
		Enforcement: "ToolAdapter.Execute takes the actor as its own parameter and the Backend interface has no confirm, cancel or resolve method",
	},
	{
		ID:          "localai-assistant",
		Surface:     "the in-process admin MCP server of the LocalAI Assistant chat modality",
		Files:       []string{"core/http/endpoints/mcp/localai_assistant.go", "core/application/application.go"},
		Evidence:    []string{"pkg/mcp/localaitools/tools.go"},
		Decision:    Reject,
		Reason:      "one process-wide server answers every admin chat and holds no per-call principal; the admin catalog is a REST mirror, and add-on lifecycle and data tools are not part of it",
		Enforcement: "the catalog has no add-on tool and no route of /api/addons; the consumer matrix test scans both",
	},
	{
		ID:          "model-mcp-sessions",
		Surface:     "MCP sessions built from a model's MCP configuration, cached by model name and shared by every user of the model",
		Files:       []string{"core/http/endpoints/mcp/tools.go", "core/http/endpoints/mcp/executor.go"},
		Decision:    Reject,
		Reason:      "a shared session has no current-user state, so it cannot carry an actor",
		Enforcement: "DiscoverMCPTools drops a tool in the add-on namespace; ExecuteMCPToolCall and ExecuteMCPToolCallRemote refuse it with ErrAddonToolsUnsupported",
	},
	{
		ID:          "anthropic-messages",
		Surface:     "POST /v1/messages with model-bound MCP servers",
		Files:       []string{"core/http/endpoints/anthropic/messages.go"},
		Decision:    Reject,
		Reason:      "the surface is not wired to the broker",
		Enforcement: "a request that sets addon_tools gets the typed addon_tools_unsupported answer (400)",
	},
	{
		ID:          "open-responses",
		Surface:     "POST /v1/responses with model-bound MCP servers",
		Files:       []string{"core/http/endpoints/openresponses/responses.go"},
		Decision:    Reject,
		Reason:      "the surface is not wired to the broker, and background responses outlive the request principal",
		Enforcement: "a request that sets addon_tools gets the typed addon_tools_unsupported answer (400)",
	},
	{
		ID:          "realtime",
		Surface:     "the realtime WebSocket and WebRTC sessions, which can opt into the LocalAI Assistant tools",
		Files:       []string{"core/http/endpoints/openai/realtime.go"},
		Decision:    Reject,
		Reason:      "a long-lived session cannot be revalidated between utterances, and nothing binds its actor to a tool call",
		Enforcement: "no add-on tool is added to a session; the session executor is the assistant holder's, which rejects the namespace",
	},
	{
		ID:          "legacy-mcp-chat",
		Surface:     "POST /v1/mcp/chat/completions, which enables every MCP server of the model",
		Evidence:    []string{"core/http/endpoints/localai/mcp.go"},
		Decision:    Reject,
		Reason:      "the endpoint predates per-request actors and passes no broker",
		Enforcement: "it builds the chat handler without a provider, so addon_tools gets the typed answer (400)",
	},
	{
		ID:          "mcp-discovery-endpoints",
		Surface:     "GET /v1/mcp/servers, prompts and resources of a model",
		Files:       []string{"core/http/endpoints/localai/mcp_tools.go", "core/http/endpoints/localai/mcp_prompts.go", "core/http/endpoints/localai/mcp_resources.go"},
		Decision:    Reject,
		Reason:      "they list the configured servers of a model and execute no tool",
		Enforcement: "they read model configuration only and have no add-on provider",
	},
	{
		ID:          "studio-chats-mcp",
		Surface:     "the MCP tools of a chat on the server-side chat loop: the MCP servers of the chat's model, through the same shared sessions as every other chat of that model",
		Files:       []string{"core/services/studio/mcptools.go", "core/application/studio_service.go"},
		Decision:    Reject,
		Reason:      "a model's MCP session is shared by every user of the model, so it carries no actor; add-on tools of a chat come through their own consumer, with the chat user's actor",
		Enforcement: "the executor's DiscoverTools drops the add-on namespace and its ExecuteTool refuses it; the chat bridge drops it again before it offers a tool",
	},
	{
		ID:          "studio-chats-addons",
		Surface:     "the add-on tools of a chat on the server-side chat loop, listed and run by the chat's user for the chat's own turns, action tools included",
		Evidence:    []string{"core/services/studio/addontools.go", "core/application/addon_gateway.go", "core/http/auth/addons.go"},
		Decision:    Route,
		Actor:       "the chat's user, built by the host for each call with the credential of a chat turn; the owner checks the account and the add-ons feature again on every call, and local-owner mode comes from deployment configuration",
		Enforcement: "only the adapter built by NewChatToolAdapter serves action tools; a chat switches an add-on off with its own list, and a sub-agent gets only the tools its parent names",
	},
	{
		ID:          "distributed-agent-worker",
		Surface:     "agent workers that execute MCP tools for a frontend over the agent control seam",
		Files:       []string{"core/cli/agent_worker.go", "core/services/nodes/agent_rpc_nats.go"},
		Decision:    Reject,
		Reason:      "a worker holds sessions built from the model configuration the frontend sent; the frontend's actor never travels with a tool call",
		Enforcement: "the tool and discovery RPCs use DiscoverMCPTools and ExecuteMCPToolCall, which refuse the namespace; the frontend executor refuses it before the request is sent",
	},
}
