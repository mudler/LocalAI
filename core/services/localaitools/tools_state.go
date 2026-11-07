package localaitools

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mudler/LocalAI/core/services/modeladmin"
)

func registerStateTools(s *mcp.Server, client LocalAIClient, opts Options) {
	if opts.DisableMutating {
		return
	}

	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolToggleModelState,
		Description: "Enable or disable an installed model. action must be 'enable' or 'disable'. Requires user confirmation per safety rule 1.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args struct {
		Name   string            `json:"name"   jsonschema:"The installed model name."`
		Action modeladmin.Action `json:"action" jsonschema:"Either 'enable' or 'disable'."`
	}) (*mcp.CallToolResult, any, error) {
		if err := requireToggleArgs(args.Name, args.Action, modeladmin.ActionEnable, modeladmin.ActionDisable); err != nil {
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
		Name   string            `json:"name"   jsonschema:"The installed model name."`
		Action modeladmin.Action `json:"action" jsonschema:"Either 'pin' or 'unpin'."`
	}) (*mcp.CallToolResult, any, error) {
		if err := requireToggleArgs(args.Name, args.Action, modeladmin.ActionPin, modeladmin.ActionUnpin); err != nil {
			return errorResult(err), nil, nil
		}
		if err := client.ToggleModelPinned(ctx, args.Name, args.Action); err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(map[string]any{"name": args.Name, "action": args.Action}), nil, nil
	})
}

func requireToggleArgs(name string, action modeladmin.Action, allowed ...modeladmin.Action) error {
	if name == "" {
		return fmt.Errorf("name is required")
	}
	if !action.Valid(allowed...) {
		return fmt.Errorf("action must be one of %v, got %q", allowed, action)
	}
	return nil
}
