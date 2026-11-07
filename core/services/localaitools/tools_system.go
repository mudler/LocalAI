package localaitools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func registerSystemTools(s *mcp.Server, client LocalAIClient, opts Options) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolSystemInfo,
		Description: "Report LocalAI version, paths, distributed flag, currently loaded models, and installed backends.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		info, err := client.SystemInfo(ctx)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(info), nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolListNodes,
		Description: "List federated worker nodes (only meaningful in distributed mode; returns an empty list otherwise).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		nodes, err := client.ListNodes(ctx)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(nodes), nil, nil
	})

	if opts.DisableMutating {
		return
	}

	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolSetNodeVRAMBudget,
		Description: "Set a federated node's VRAM allocation budget (\"80%\" or \"12GB\"; empty clears the override). Only meaningful in distributed mode. Requires user confirmation per safety rule 1.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args SetNodeVRAMBudgetRequest) (*mcp.CallToolResult, any, error) {
		if args.NodeID == "" {
			return errorResultf("node_id is required"), nil, nil
		}
		if err := client.SetNodeVRAMBudget(ctx, args.NodeID, args.Budget); err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(map[string]string{"node_id": args.NodeID, "vram_budget": args.Budget}), nil, nil
	})
}
