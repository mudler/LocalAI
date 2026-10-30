package localaitools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The built-in chat tools are the tools of the chats that an add-on may
// replace: bash, which runs commands directly on this machine with no isolation
// when no Sandbox add-on serves it, and the web tools. Both tools here only
// read; an administrator changes the settings (chats_host_exec,
// chats_tool_providers) in Settings > Tools.
func registerChatToolTools(s *mcp.Server, client LocalAIClient, _ Options) {
	mcp.AddTool(s, &mcp.Tool{
		Name: ToolListChatTools,
		Description: "List the built-in chat tools that an add-on may replace: whether each is on, who serves it (builtin, or an add-on), " +
			"the add-ons that replace it and the one pinned, and for bash the mode of chats_host_exec (off, ask or on) and why this server cannot run it, if so.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		out, err := client.ListChatTools(ctx)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(out), nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: ToolListHostExecEvents,
		Description: "Read the audit of the commands the chats' built-in bash ran directly on this machine, newest first: " +
			"user, chat, command, exit code, duration and whether it timed out.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args HostExecEventsQuery) (*mcp.CallToolResult, any, error) {
		limit := args.Limit
		if limit <= 0 {
			limit = 50
		}
		out, err := client.ListHostExecEvents(ctx, min(limit, 500))
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(out), nil, nil
	})
}
