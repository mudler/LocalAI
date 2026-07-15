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
//
// The router corpus tools manage the knn classifier's labelled
// exemplar store: get_router_corpus_stats is read-only (counts, never
// texts); seed_router_corpus / clear_router_corpus mutate routing
// behaviour and sit behind the DisableMutating gate like the
// model-config tools.
func registerMiddlewareTools(s *mcp.Server, client LocalAIClient, opts Options) {
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

	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolGetRouterCorpusStats,
		Description: "Size and per-label exemplar counts of a knn router's corpus. Counts only — corpus texts are never exposed by any tool. Read-only.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args RouterCorpusQuery) (*mcp.CallToolResult, any, error) {
		if args.Router == "" {
			return errorResultf("router is required"), nil, nil
		}
		stats, err := client.GetRouterCorpusStats(ctx, args.Router)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(stats), nil, nil
	})

	if opts.DisableMutating {
		return
	}

	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolSeedRouterCorpus,
		Description: "Add labelled example prompts to a knn router's corpus. Entries are embedded server-side with the router's knn.embedding_model, persisted, and indexed immediately — routing behaviour changes right away, so confirm with the user first (safety rule 1). Labels must be declared in the router's policies and unique per entry; duplicate texts are skipped.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args RouterCorpusSeedRequest) (*mcp.CallToolResult, any, error) {
		if args.Router == "" {
			return errorResultf("router is required"), nil, nil
		}
		if len(args.Entries) == 0 {
			return errorResultf("entries is required"), nil, nil
		}
		res, err := client.SeedRouterCorpus(ctx, args)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(res), nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolClearRouterCorpus,
		Description: "Wipe a knn router's corpus — the persisted file and the live index. The router falls back for every prompt until reseeded. Destructive; requires explicit user confirmation per safety rule 1.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args RouterCorpusQuery) (*mcp.CallToolResult, any, error) {
		if args.Router == "" {
			return errorResultf("router is required"), nil, nil
		}
		res, err := client.ClearRouterCorpus(ctx, args.Router)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(res), nil, nil
	})
}
