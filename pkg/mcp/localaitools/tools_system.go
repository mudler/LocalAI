package localaitools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func registerSystemTools(s *mcp.Server, client LocalAIClient, _ Options) {
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
}
