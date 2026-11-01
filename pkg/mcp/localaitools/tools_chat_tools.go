package localaitools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The built-in chat tools are the tools of the chats that an add-on may
// replace: bash, which runs commands directly on this machine with no isolation
// when no Sandbox add-on serves it, and the web tools. Both tools here only
// read; an administrator changes the settings (chats_host_exec,
// chats_tool_providers) in Settings > Tools. The same list carries the
// built-in skills that installed add-ons replace, pinned in the same map.
func registerChatToolTools(s *mcp.Server, client LocalAIClient, _ Options) {
	mcp.AddTool(s, &mcp.Tool{
		Name: ToolListChatTools,
		Description: "List the built-in chat tools that an add-on may replace: whether each is on, who serves it (builtin, or an add-on), " +
			"the add-ons that replace it (oldest install first, with their install times), the default one that serves with no pin (the one installed first) and the one pinned, and for bash the mode of chats_host_exec (off, ask or on) and why this server cannot run it, if so. " +
			"skills lists the built-in skills (xlsx, docx, pptx, pdf) that installed add-ons replace: who serves each, the add-ons that replace it (same order, default and install times) and the one pinned in chats_tool_providers.",
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

// registerChatEgressTools exposes the settings and the audit of the network of
// the chats' Sandbox commands. Only the ceiling can be changed here; the
// private-network switch needs a typed confirmation in Settings > Tools, and
// the lists of hosts (extra registries, always-allowed hosts) are edited there
// too: get_chat_egress reads them, no tool writes them.
func registerChatEgressTools(s *mcp.Server, client LocalAIClient, opts Options) {
	mcp.AddTool(s, &mcp.Tool{
		Name: ToolGetChatEgress,
		Description: "Read the network settings of the chats' Sandbox commands: the ceiling (off, registries, all), the default of a new chat, " +
			"whether private addresses may be reached, the ports, the extra registries, the always-allowed hosts (allowed_hosts), the audit retention, the package registries list, " +
			"which add-on runs commands and how, and the allowed and blocked requests of the last 24 hours.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		out, err := client.GetChatEgress(ctx)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(out), nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: ToolListEgressEvents,
		Description: "Read the audit of the network requests of the chats' Sandbox commands, newest first: user, chat, host, port, " +
			"verdict (allow, block, or done with the bytes moved) and reason. Filters by user, chat, verdict or host.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args EgressEventsQuery) (*mcp.CallToolResult, any, error) {
		if args.Limit <= 0 {
			args.Limit = 50
		}
		args.Limit = min(args.Limit, 500)
		out, err := client.ListEgressEvents(ctx, args)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(out), nil, nil
	})

	if opts.DisableMutating {
		return
	}
	mcp.AddTool(s, &mcp.Tool{
		Name: ToolSetChatEgressCeiling,
		Description: "Set the widest network preset any chat's Sandbox commands may use (chats_egress_ceiling): off, registries (the package registries only) " +
			"or all (every public host). Lowering it applies within 30 seconds. Confirm with the user first (safety rule 1).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args SetChatEgressCeilingRequest) (*mcp.CallToolResult, any, error) {
		out, err := client.SetChatEgressCeiling(ctx, args.Ceiling)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(out), nil, nil
	})
}
