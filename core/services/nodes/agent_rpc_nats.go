package nodes

import (
	"context"
	"fmt"
	"time"

	"github.com/mudler/LocalAI/core/config"
	mcpremote "github.com/mudler/LocalAI/core/services/mcp"
	"github.com/mudler/LocalAI/core/services/messaging"
)

// NATSAgentControl sends the frontend's MCP verbs to the agent-worker queue
// group as NATS request/reply.
type NATSAgentControl struct{ bus messaging.MessagingClient }

func NewNATSAgentControl(bus messaging.MessagingClient) *NATSAgentControl {
	return &NATSAgentControl{bus: bus}
}

func (a *NATSAgentControl) ExecuteMCPTool(ctx context.Context, req mcpremote.MCPToolRequest) (*mcpremote.MCPToolResponse, error) {
	timeout, err := agentRequestTimeout(ctx, config.DefaultMCPToolTimeout)
	if err != nil {
		return nil, err
	}
	return controlRequestJSON[mcpremote.MCPToolRequest, mcpremote.MCPToolResponse](a.bus, messaging.SubjectMCPToolExecute, req, timeout)
}

func (a *NATSAgentControl) DiscoverMCPTools(ctx context.Context, req mcpremote.MCPDiscoveryRequest) (*mcpremote.MCPDiscoveryResponse, error) {
	timeout, err := agentRequestTimeout(ctx, config.DefaultMCPDiscoveryTimeout)
	if err != nil {
		return nil, err
	}
	return controlRequestJSON[mcpremote.MCPDiscoveryRequest, mcpremote.MCPDiscoveryResponse](a.bus, messaging.SubjectMCPDiscovery, req, timeout)
}

// agentRequestTimeout reads only the deadline of ctx, never its cancellation:
// a chat client that disconnects must not abort a tool call already running on
// a worker, which is how these requests have always behaved.
func agentRequestTimeout(ctx context.Context, fallback time.Duration) (time.Duration, error) {
	deadline, ok := ctx.Deadline()
	if !ok {
		return fallback, nil
	}
	timeout := time.Until(deadline)
	if timeout <= 0 {
		return 0, fmt.Errorf("agent request budget spent: %w", context.DeadlineExceeded)
	}
	return timeout, nil
}
