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
		Description: "List federated worker nodes with the live state of each route (heartbeat, nats, tunnel); a node stays healthy while one nats or tunnel route is live. Only meaningful in distributed mode; returns an empty list otherwise.",
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

	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolSetNodeLimits,
		Description: "Override the memory settings of one node (models kept loaded, GPU memory budget, memory threshold, model swap), or clear every override with reset. Returns the values in force and their source. Only meaningful in distributed mode. Requires user confirmation per safety rule 1.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args SetNodeLimitsRequest) (*mcp.CallToolResult, any, error) {
		if args.NodeID == "" {
			return errorResultf("node_id is required"), nil, nil
		}
		limits, err := client.SetNodeLimits(ctx, args)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(limits), nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolDrainNode,
		Description: "Drain a backend node (action drain) or resume a draining one (action resume). A drain places nothing new on the node; its loaded models keep serving while replacements load on other nodes, and each leaves once replaced, idle and not needed, or at the deadline (timeout). force removes the node's models at once. An unhealthy or offline node drains at once. Resume cancels the drain. Only meaningful in distributed mode. Requires user confirmation per safety rule 1.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args DrainNodeRequest) (*mcp.CallToolResult, any, error) {
		if args.NodeID == "" {
			return errorResultf("node_id is required"), nil, nil
		}
		if args.Action != DrainActionDrain && args.Action != DrainActionResume {
			return errorResultf("action must be drain or resume"), nil, nil
		}
		r, err := client.DrainNode(ctx, args)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(r), nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolSetModelMemoryPolicy,
		Description: "Change Settings > Models in memory: models kept loaded, model swap, eviction order and retries, idle unload, the memory reclaimer and the GPU memory budget. Only the given fields change. In distributed mode the values apply to every frontend and, per node, to every worker. Requires user confirmation per safety rule 1.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args ModelMemoryPolicy) (*mcp.CallToolResult, any, error) {
		if err := client.SetModelMemoryPolicy(ctx, args); err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(map[string]any{"updated": args}), nil, nil
	})
}
