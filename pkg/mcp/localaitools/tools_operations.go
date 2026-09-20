package localaitools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func registerOperationTools(s *mcp.Server, client LocalAIClient, opts Options) {
	if opts.DisableMutating {
		return
	}

	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolThrottleOperation,
		Description: "Throttle an active gallery download to a byte-per-second rate without restarting it. Rate like 2mb or 500kb, or 0 to remove the limit. Requires user confirmation per safety rule 1.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args ThrottleOperationRequest) (*mcp.CallToolResult, any, error) {
		if args.JobID == "" {
			return errorResultf("job_id is required"), nil, nil
		}
		if args.Rate == "" {
			return errorResultf("rate is required (e.g. 2mb, 500kb, or 0 to remove the limit)"), nil, nil
		}
		if err := client.ThrottleOperation(ctx, args); err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(map[string]any{"job_id": args.JobID, "rate": args.Rate}), nil, nil
	})
}
