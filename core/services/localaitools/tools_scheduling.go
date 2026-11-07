package localaitools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func registerSchedulingTools(s *mcp.Server, client LocalAIClient, opts Options) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolListScheduling,
		Description: "List distributed per-model scheduling configs (only meaningful in distributed mode).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		configs, err := client.ListScheduling(ctx)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(configs), nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolGetScheduling,
		Description: "Get the distributed scheduling config for one model, or null when none is configured.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args DeleteSchedulingRequest) (*mcp.CallToolResult, any, error) {
		if args.ModelName == "" {
			return errorResultf("model_name is required"), nil, nil
		}
		config, err := client.GetScheduling(ctx, args.ModelName)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(config), nil, nil
	})

	if opts.DisableMutating {
		return
	}

	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolSetScheduling,
		Description: "Create or update a distributed per-model scheduling config. Requires user confirmation per safety rule 1.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args SetSchedulingRequest) (*mcp.CallToolResult, any, error) {
		if args.ModelName == "" {
			return errorResultf("model_name is required"), nil, nil
		}
		if args.SpreadAll && (args.MinReplicas != 0 || args.MaxReplicas != 0) {
			return errorResultf("spread_all and min_replicas/max_replicas are mutually exclusive"), nil, nil
		}
		if args.MinReplicas < 0 {
			return errorResultf("min_replicas must be >= 0"), nil, nil
		}
		if args.MaxReplicas < 0 {
			return errorResultf("max_replicas must be >= 0"), nil, nil
		}
		if args.MaxReplicas > 0 && args.MinReplicas > args.MaxReplicas {
			return errorResultf("min_replicas must be <= max_replicas"), nil, nil
		}
		config, err := client.SetScheduling(ctx, args)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(config), nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolDeleteScheduling,
		Description: "Delete a distributed per-model scheduling config. Requires user confirmation per safety rule 1.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args DeleteSchedulingRequest) (*mcp.CallToolResult, any, error) {
		if args.ModelName == "" {
			return errorResultf("model_name is required"), nil, nil
		}
		if err := client.DeleteScheduling(ctx, args.ModelName); err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(map[string]string{"deleted": args.ModelName}), nil, nil
	})
}
