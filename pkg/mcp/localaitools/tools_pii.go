package localaitools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func registerPIITools(s *mcp.Server, client LocalAIClient, _ Options) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolListPIIPatterns,
		Description: "List the active PII regex pattern set. Each entry shows the pattern id, description, and current action (mask, block, allow). Read-only.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		patterns, err := client.ListPIIPatterns(ctx)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(patterns), nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolGetPIIEvents,
		Description: "Recent PII redaction events. Filter by correlation_id (joins to a usage record), user_id, or pattern_id. Events never carry the matched value — only an 8-char sha256 prefix so admins can dedupe recurring leaks.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args PIIEventsQuery) (*mcp.CallToolResult, any, error) {
		events, err := client.GetPIIEvents(ctx, args)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(events), nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolTestPIIRedaction,
		Description: "Dry-run the PII redactor against text without recording a real event. Useful for tuning patterns: paste a candidate string and see whether it would be masked, blocked, or routed locally.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args PIIRedactTestRequest) (*mcp.CallToolResult, any, error) {
		if args.Text == "" {
			return errorResultf("text is required"), nil, nil
		}
		res, err := client.TestPIIRedaction(ctx, args)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(res), nil, nil
	})
}
