package localaitools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// registerCarrierWeightTools wires the weights that break a tie between equally
// fit nodes reached over different carriers.
func registerCarrierWeightTools(s *mcp.Server, client LocalAIClient, opts Options) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolGetCarrierWeights,
		Description: "Read the carrier weights of a distributed cluster (nats, websocket, p2p; 0 to 1000, higher wins). A weight only breaks a tie between nodes that fit a request equally well; it never overrides fit. 0 means: use that carrier only when nothing else fits. Returns distributed=false when this server is not distributed.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		info, err := client.GetCarrierWeights(ctx)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(info), nil, nil
	})
	if opts.DisableMutating {
		return
	}
	mcp.AddTool(s, &mcp.Tool{
		Name: ToolSetCarrierWeights,
		Description: "Change carrier weights (nats, websocket, p2p; whole numbers 0 to 1000); a weight left out stays. It applies to every frontend within seconds. " +
			"Requires user confirmation per safety rule 1.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args SetCarrierWeightsRequest) (*mcp.CallToolResult, any, error) {
		info, err := client.SetCarrierWeights(ctx, args)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(info), nil, nil
	})
}
