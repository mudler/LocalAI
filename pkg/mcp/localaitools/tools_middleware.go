package localaitools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// registerMiddlewareTools wires the routing-module admin surface for the
// MCP server, mirroring what the React /app/middleware page exposes:
//
//   - get_middleware_status: read-only aggregator. The agent can ask
//     "what's filtering my requests?" and get back the per-model PII
//     enabled state + the detector models each references, recent event
//     count, plus the active router models and their classifier configs.
//   - get_router_decisions: read-only routing-decision log.
//
// PII detection policy lives on each detector model's pii_detection
// block, edited via the model-config tools — there is no global pattern
// set to mutate here anymore.
func registerMiddlewareTools(s *mcp.Server, client LocalAIClient, _ Options) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolGetMiddlewareStatus,
		Description: "Aggregated routing-module status: per-model resolved PII state and the NER detector models each one references, recent event count, plus the active router models and their classifier configs. Read-only.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		status, err := client.GetMiddlewareStatus(ctx)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(status), nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolGetRouterDecisions,
		Description: "Recent intelligent-routing decisions. Each row records which router model the client called, which candidate the classifier picked, the classifier's score and latency, and a correlation id that joins back to the usage record. Filter by correlation_id, user_id, or router_model. Read-only.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args RouterDecisionsQuery) (*mcp.CallToolResult, any, error) {
		decisions, err := client.GetRouterDecisions(ctx, args)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(decisions), nil, nil
	})
}
