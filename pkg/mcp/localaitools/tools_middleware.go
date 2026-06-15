package localaitools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// registerMiddlewareTools wires the routing-module admin surface for the
// MCP server. The two tools mirror what the React /app/middleware page
// exposes:
//
//   - get_middleware_status: read-only aggregator. The agent can ask
//     "what's filtering my requests?" and get back the active PII
//     pattern set, the per-model resolved enabled/override state, and
//     a placeholder for routing.
//   - set_pii_pattern_action: mutating. Mutations are TRANSIENT — they
//     live until process restart, when patterns reload from the YAML
//     defaults. The skill prompt should warn the user about that
//     before applying lasting changes.
func registerMiddlewareTools(s *mcp.Server, client LocalAIClient, opts Options) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolGetMiddlewareStatus,
		Description: "Aggregated routing-module status: PII pattern catalogue with current actions, per-model resolved PII state and overrides, recent event count, plus the active router models and their classifier configs. Read-only.",
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

	if opts.DisableMutating {
		return
	}

	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolSetPIIPatternAction,
		Description: "Change a PII pattern's action (mask|block|allow) and/or disabled state in-process. TRANSIENT: the mutation is lost on restart unless followed by persist_pii_patterns. Admin-required.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args PIIPatternActionUpdate) (*mcp.CallToolResult, any, error) {
		if args.ID == "" {
			return errorResultf("id is required"), nil, nil
		}
		if args.Action == "" && args.Disabled == nil {
			return errorResultf("at least one of action (mask, block, allow) or disabled must be set"), nil, nil
		}
		if err := client.SetPIIPatternAction(ctx, args); err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(map[string]any{
			"id":        args.ID,
			"action":    args.Action,
			"disabled":  args.Disabled,
			"persisted": false,
		}), nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolPersistPIIPatterns,
		Description: "Snapshot the live PII redactor's per-pattern (action, disabled) state into runtime_settings.json so it re-applies on the next process start. Pairs with set_pii_pattern_action — that one is in-process; this one persists. Admin-required.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		if err := client.PersistPIIPatterns(ctx); err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(map[string]any{"persisted": true}), nil, nil
	})
}
