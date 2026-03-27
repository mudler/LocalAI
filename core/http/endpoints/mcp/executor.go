package mcp

import (
	"context"
	"slices"

	"github.com/mudler/LocalAI/core/config"
	mcpRemote "github.com/mudler/LocalAI/core/services/mcp"
	"github.com/mudler/LocalAI/pkg/functions"
	"github.com/mudler/xlog"
)

// ToolExecutor abstracts MCP tool discovery and execution.
// Implementations handle local (in-process sessions) vs distributed (NATS) modes.
type ToolExecutor interface {
	// DiscoverTools returns the tool function schemas available from MCP servers.
	DiscoverTools(ctx context.Context) ([]functions.Function, error)
	// IsTool returns true if the given function name is an MCP tool.
	IsTool(name string) bool
	// ExecuteTool executes an MCP tool by name with the given JSON arguments.
	ExecuteTool(ctx context.Context, toolName, arguments string) (string, error)
	// HasTools returns true if any MCP tools are available.
	HasTools() bool
}

// LocalToolExecutor uses in-process MCP sessions for tool operations.
type LocalToolExecutor struct {
	tools []MCPToolInfo
}

// NewLocalToolExecutor creates a ToolExecutor from local named sessions.
// It discovers tools immediately and caches the result.
func NewLocalToolExecutor(ctx context.Context, sessions []NamedSession) *LocalToolExecutor {
	tools, err := DiscoverMCPTools(ctx, sessions)
	if err != nil {
		xlog.Error("Failed to discover MCP tools (local)", "error", err)
	}
	return &LocalToolExecutor{tools: tools}
}

func (e *LocalToolExecutor) DiscoverTools(_ context.Context) ([]functions.Function, error) {
	var fns []functions.Function
	for _, t := range e.tools {
		fns = append(fns, t.Function)
	}
	return fns, nil
}

func (e *LocalToolExecutor) IsTool(name string) bool {
	return IsMCPTool(e.tools, name)
}

func (e *LocalToolExecutor) ExecuteTool(ctx context.Context, toolName, arguments string) (string, error) {
	return ExecuteMCPToolCall(ctx, e.tools, toolName, arguments)
}

func (e *LocalToolExecutor) HasTools() bool {
	return len(e.tools) > 0
}

// DistributedToolExecutor routes tool operations through NATS to agent workers.
type DistributedToolExecutor struct {
	modelName string
	remote    config.MCPGenericConfig[config.MCPRemoteServers]
	stdio     config.MCPGenericConfig[config.MCPSTDIOServers]
	toolDefs  []mcpRemote.MCPToolDef
}

// NewDistributedToolExecutor creates a ToolExecutor that routes through NATS.
// It discovers tools immediately via a NATS request-reply to an agent worker.
func NewDistributedToolExecutor(ctx context.Context, modelName string,
	remote config.MCPGenericConfig[config.MCPRemoteServers],
	stdio config.MCPGenericConfig[config.MCPSTDIOServers],
) *DistributedToolExecutor {
	e := &DistributedToolExecutor{
		modelName: modelName,
		remote:    remote,
		stdio:     stdio,
	}
	resp, err := DiscoverMCPToolsRemote(ctx, modelName, remote, stdio)
	if err != nil {
		xlog.Error("Failed to discover MCP tools (distributed)", "error", err)
	} else if resp != nil {
		e.toolDefs = resp.Tools
	}
	return e
}

func (e *DistributedToolExecutor) DiscoverTools(_ context.Context) ([]functions.Function, error) {
	var fns []functions.Function
	for _, td := range e.toolDefs {
		fns = append(fns, td.Function)
	}
	return fns, nil
}

func (e *DistributedToolExecutor) IsTool(name string) bool {
	return slices.ContainsFunc(e.toolDefs, func(td mcpRemote.MCPToolDef) bool {
		return td.ToolName == name
	})
}

func (e *DistributedToolExecutor) ExecuteTool(ctx context.Context, toolName, arguments string) (string, error) {
	return ExecuteMCPToolCallRemote(ctx, e.modelName, e.remote, e.stdio, toolName, arguments)
}

func (e *DistributedToolExecutor) HasTools() bool {
	return len(e.toolDefs) > 0
}

// NewToolExecutor creates the appropriate ToolExecutor based on the current mode.
// In distributed mode (NATS configured), returns a DistributedToolExecutor.
// In local mode, creates sessions and returns a LocalToolExecutor.
func NewToolExecutor(ctx context.Context, modelName string,
	remote config.MCPGenericConfig[config.MCPRemoteServers],
	stdio config.MCPGenericConfig[config.MCPSTDIOServers],
	enabledServers []string,
) ToolExecutor {
	if IsDistributed() {
		return NewDistributedToolExecutor(ctx, modelName, remote, stdio)
	}
	sessions, err := NamedSessionsFromMCPConfig(modelName, remote, stdio, enabledServers)
	if err != nil || len(sessions) == 0 {
		if err != nil {
			xlog.Error("Failed to create MCP sessions", "error", err)
		}
		return &LocalToolExecutor{} // empty, HasTools() returns false
	}
	return NewLocalToolExecutor(ctx, sessions)
}
