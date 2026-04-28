package localaitools

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func registerStateTools(s *mcp.Server, client LocalAIClient, opts Options) {
	if opts.DisableMutating {
		return
	}

	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolToggleModelState,
		Description: "Enable or disable an installed model. action must be 'enable' or 'disable'. Requires user confirmation per safety rule 1.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args struct {
		Name   string `json:"name"   jsonschema:"The installed model name."`
		Action string `json:"action" jsonschema:"Either 'enable' or 'disable'."`
	}) (*mcp.CallToolResult, any, error) {
		if err := requireToggleArgs(args.Name, args.Action, "enable", "disable"); err != nil {
			return errorResult(err), nil, nil
		}
		if err := client.ToggleModelState(ctx, args.Name, args.Action); err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(map[string]any{"name": args.Name, "action": args.Action}), nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolToggleModelPinned,
		Description: "Pin or unpin an installed model. action must be 'pin' or 'unpin'. Requires user confirmation per safety rule 1.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args struct {
		Name   string `json:"name"   jsonschema:"The installed model name."`
		Action string `json:"action" jsonschema:"Either 'pin' or 'unpin'."`
	}) (*mcp.CallToolResult, any, error) {
		if err := requireToggleArgs(args.Name, args.Action, "pin", "unpin"); err != nil {
			return errorResult(err), nil, nil
		}
		if err := client.ToggleModelPinned(ctx, args.Name, args.Action); err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(map[string]any{"name": args.Name, "action": args.Action}), nil, nil
	})
}

func requireToggleArgs(name, action string, allowed ...string) error {
	if name == "" {
		return fmt.Errorf("name is required")
	}
	for _, a := range allowed {
		if action == a {
			return nil
		}
	}
	return fmt.Errorf("action must be one of %v, got %q", allowed, action)
}
