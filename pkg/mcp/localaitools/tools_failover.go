package localaitools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// registerFailoverTools wires the conversational failover-chain tools.
// list_failover_chains reports the health of every chain, pin_failover_target
// forces a chain to one target, and unpin_failover_target hands control back
// to health-based selection.
func registerFailoverTools(s *mcp.Server, client LocalAIClient, opts Options) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolListFailoverChains,
		Description: "List model failover chains, the target serving each one now, and the health of every target.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		chains, err := client.ListFailoverChains(ctx)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(chains), nil, nil
	})

	if opts.DisableMutating {
		return
	}

	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolPinFailoverTarget,
		Description: "Force a failover chain to serve every request from one target, regardless of health, until it is unpinned. Requires user confirmation per safety rule 1.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args struct {
		Chain  string `json:"chain"  jsonschema:"failover chain name"`
		Target string `json:"target" jsonschema:"target model to pin"`
	}) (*mcp.CallToolResult, any, error) {
		if err := client.PinFailoverTarget(ctx, args.Chain, args.Target); err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(map[string]string{"chain": args.Chain, "pinned": args.Target}), nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolUnpinFailoverTarget,
		Description: "Remove the pin from a failover chain so health decides the target again. Requires user confirmation per safety rule 1.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args struct {
		Chain string `json:"chain" jsonschema:"failover chain name"`
	}) (*mcp.CallToolResult, any, error) {
		if err := client.UnpinFailoverTarget(ctx, args.Chain); err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(map[string]string{"chain": args.Chain, "pinned": ""}), nil, nil
	})
}
