package localaitools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// registerClusterTools wires the read-only status of the cluster carrier.
// A change of carrier has no tool on purpose. It moves every frontend and
// worker, and it needs a dry run and a list of the frontends that only an
// admin can confirm, so the assistant points to the dashboard or to the
// `local-ai cluster carrier` command.
func registerClusterTools(s *mcp.Server, client LocalAIClient) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolGetClusterCarrier,
		Description: "Read which transport a distributed cluster uses (NATS or the database tunnel), the state and epoch of a change, the live frontends and the workers that cannot follow a change. Read-only. Returns distributed=false when this server is not distributed.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		status, err := client.GetClusterCarrier(ctx)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(status), nil, nil
	})
}
