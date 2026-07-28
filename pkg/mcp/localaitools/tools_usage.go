package localaitools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func registerUsageTools(s *mcp.Server, client LocalAIClient, _ Options) {
	mcp.AddTool(s, &mcp.Tool{
		Name: ToolGetUsageStats,
		Description: "Return aggregated token usage. Defaults to the calling user's own usage over the last month. " +
			"Use period=day|week|month|all to change the window. Set all=true for a cluster-wide admin view " +
			"(only meaningful when auth is on and the caller is admin; in single-user mode there is only one user).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args UsageStatsQuery) (*mcp.CallToolResult, any, error) {
		stats, err := client.GetUsageStats(ctx, args)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(stats), nil, nil
	})
}
