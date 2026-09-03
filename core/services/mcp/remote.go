package mcp

import (
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/functions"
)

// MCPToolRequest is the control-RPC payload for executing an MCP tool on an
// agent worker. The frontend serializes the model's MCP server config so the
// worker can create sessions and execute the tool.
//
// It lives in this package rather than beside either side of the call, because
// both sides need it and they are in packages that must not import each other:
// core/http/endpoints/mcp is the caller and core/cli is the worker.
type MCPToolRequest struct {
	ModelName     string                                           `json:"model_name"`
	ToolName      string                                           `json:"tool_name"`
	Arguments     map[string]any                                   `json:"arguments,omitempty"`
	RemoteServers config.MCPGenericConfig[config.MCPRemoteServers] `json:"remote_servers"`
	StdioServers  config.MCPGenericConfig[config.MCPSTDIOServers]  `json:"stdio_servers"`
}

// MCPToolResponse is the reply to an MCP tool execution.
//
// A set Error is the WORKER'S OWN ANSWER, carried inside a successful reply
// rather than as a transport failure, which is what lets a caller tell "this
// tool rejected your arguments" from "this worker could not be reached".
type MCPToolResponse struct {
	Result string `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}

// MCPDiscoveryRequest is the control-RPC payload for discovering available MCP
// tools, prompts, and resources from a model's MCP servers.
type MCPDiscoveryRequest struct {
	ModelName     string                                           `json:"model_name"`
	RemoteServers config.MCPGenericConfig[config.MCPRemoteServers] `json:"remote_servers"`
	StdioServers  config.MCPGenericConfig[config.MCPSTDIOServers]  `json:"stdio_servers"`
}

// MCPDiscoveryResponse is the reply to an MCP discovery request.
type MCPDiscoveryResponse struct {
	Servers []MCPServerInfo `json:"servers,omitempty"`
	Tools   []MCPToolDef    `json:"tools,omitempty"` // flattened tool list with functions
	Error   string          `json:"error,omitempty"`
}

// MCPServerInfo describes an MCP server and its available capabilities.
type MCPServerInfo struct {
	Name      string   `json:"name"`
	Type      string   `json:"type"`
	Tools     []string `json:"tools"`
	Prompts   []string `json:"prompts,omitempty"`
	Resources []string `json:"resources,omitempty"`
	Error     string   `json:"error,omitempty"`
}

// MCPToolDef is a serializable tool definition (function schema) that can
// travel between processes. Unlike MCPToolInfo which holds a live session
// pointer, this is pure data.
type MCPToolDef struct {
	ServerName string             `json:"server_name"`
	ToolName   string             `json:"tool_name"`
	Function   functions.Function `json:"function"`
}
