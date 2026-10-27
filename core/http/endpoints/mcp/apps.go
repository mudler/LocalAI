package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/mcpapps"
)

// AppTool is a tool of a server with what it says about its view.
type AppTool struct {
	Server, Name string
	UI           mcpapps.UI
}

// AppExecutor is a ToolExecutor that can also serve MCP Apps: it says which
// tools have a view, reads the page of a view from the server that owns it, and
// runs a tool for an app. An executor that routes to workers (distributed mode)
// does not implement it.
type AppExecutor interface {
	ToolExecutor
	// AppTools lists every tool the executor offers, with its ui metadata.
	AppTools() []AppTool
	// ReadAppResource reads a ui:// resource from the named server.
	ReadAppResource(ctx context.Context, server, uri string) (mcpapps.Resource, error)
	// ExecuteAppTool runs a tool of the named server and returns its text and
	// its full result.
	ExecuteAppTool(ctx context.Context, server, tool string, arguments json.RawMessage) (mcpapps.CallResult, error)
}

var _ AppExecutor = (*LocalToolExecutor)(nil)

func (e *LocalToolExecutor) AppTools() []AppTool {
	out := make([]AppTool, 0, len(e.tools))
	for _, t := range e.tools {
		out = append(out, AppTool{Server: t.ServerName, Name: t.ToolName, UI: mcpapps.ParseUI(t.Meta)})
	}
	return out
}

func (e *LocalToolExecutor) ReadAppResource(ctx context.Context, server, uri string) (mcpapps.Resource, error) {
	if err := mcpapps.CheckURI(uri); err != nil {
		return mcpapps.Resource{}, err
	}
	for _, t := range e.tools {
		if t.ServerName != server {
			continue
		}
		res, err := t.Session.ReadResource(ctx, &mcp.ReadResourceParams{URI: uri})
		if err != nil {
			return mcpapps.Resource{}, fmt.Errorf("the server could not read the resource: %w", err)
		}
		return mcpapps.FromReadResult(res, uri)
	}
	return mcpapps.Resource{}, errors.New("no such MCP server")
}

func (e *LocalToolExecutor) ExecuteAppTool(ctx context.Context, server, tool string, arguments json.RawMessage) (mcpapps.CallResult, error) {
	if server == "" {
		return mcpapps.CallResult{}, errors.New("a server is required")
	}
	ctx, cancel := context.WithTimeout(ctx, config.DefaultMCPToolTimeout)
	defer cancel()
	res, err := callMCPTool(ctx, e.tools, server, tool, string(arguments))
	if err != nil {
		return mcpapps.CallResult{}, err
	}
	return mcpapps.CallResult{Text: resultText(res), Result: mcpapps.FromCallResult(res)}, nil
}
