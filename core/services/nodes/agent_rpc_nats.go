package nodes

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/config"
	mcpremote "github.com/mudler/LocalAI/core/services/mcp"
	"github.com/mudler/LocalAI/core/services/messaging"
)

// NATSAgentControl sends the frontend's MCP verbs as NATS request/reply: to the
// agent-worker queue group when the call names no node, and to the subject of
// one agent worker when it does (NodeAgentControl).
type NATSAgentControl struct{ bus messaging.MessagingClient }

var _ NodeAgentControl = (*NATSAgentControl)(nil)

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

// ExecuteMCPToolOn runs one MCP tool on the agent worker nodeID. No worker
// listening on the subject of the node wraps ErrNoRoute.
func (a *NATSAgentControl) ExecuteMCPToolOn(ctx context.Context, nodeID string, req mcpremote.MCPToolRequest) (*mcpremote.MCPToolResponse, error) {
	timeout, err := agentRequestTimeout(ctx, config.DefaultMCPToolTimeout)
	if err != nil {
		return nil, err
	}
	return controlRequestJSON[mcpremote.MCPToolRequest, mcpremote.MCPToolResponse](a.bus, messaging.SubjectNodeMCPToolExecute(nodeID), req, timeout)
}

// DiscoverMCPToolsOn asks the agent worker nodeID for the MCP servers and tools
// of a model.
func (a *NATSAgentControl) DiscoverMCPToolsOn(ctx context.Context, nodeID string, req mcpremote.MCPDiscoveryRequest) (*mcpremote.MCPDiscoveryResponse, error) {
	timeout, err := agentRequestTimeout(ctx, config.DefaultMCPDiscoveryTimeout)
	if err != nil {
		return nil, err
	}
	return controlRequestJSON[mcpremote.MCPDiscoveryRequest, mcpremote.MCPDiscoveryResponse](a.bus, messaging.SubjectNodeMCPDiscovery(nodeID), req, timeout)
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

// NATSAgentRPCServer is the agent worker's end of NATSAgentControl, plus the
// node's backend stop listener. It holds no subscription handles: they live as
// long as the process and nothing unsubscribes them.
type NATSAgentRPCServer struct {
	bus    messaging.MessagingClient
	nodeID string
	// tool and discovery keep one tool call and one discovery at a time,
	// whichever of the two subjects of each delivers it.
	tool      sync.Mutex
	discovery sync.Mutex
}

// NewNATSAgentRPCServer serves on bus for the agent worker registered as
// nodeID. The MCP requests arrive in the agent-workers queue group, where any
// worker may answer them, and on the subjects of this node, for a call that
// names it.
func NewNATSAgentRPCServer(bus messaging.MessagingClient, nodeID string) *NATSAgentRPCServer {
	return &NATSAgentRPCServer{bus: bus, nodeID: nodeID}
}

// ServeMCPTool answers tool requests in the agent-workers queue group and on the
// tool subject of this node. The handler runs on the delivery goroutine and one
// tool call runs at a time, as it always did, and on context.Background because
// the handler sets its own budget: a call in flight when the worker is told to
// stop runs to that budget.
func (s *NATSAgentRPCServer) ServeMCPTool(h mcpremote.ToolHandler) error {
	handler := func(data []byte, reply func([]byte)) {
		var req mcpremote.MCPToolRequest
		if err := json.Unmarshal(data, &req); err != nil {
			sendAgentReply(reply, mcpremote.MCPToolResponse{Error: fmt.Sprintf("unmarshal error: %v", err)})
			return
		}
		s.tool.Lock()
		defer s.tool.Unlock()
		sendAgentReply(reply, h(context.Background(), req))
	}
	if _, err := s.bus.QueueSubscribeReply(messaging.SubjectMCPToolExecute, messaging.QueueAgentWorkers, handler); err != nil {
		return fmt.Errorf("serving mcp tool on %s: %w", messaging.SubjectMCPToolExecute, err)
	}
	subject := messaging.SubjectNodeMCPToolExecute(s.nodeID)
	if _, err := s.bus.SubscribeReply(subject, handler); err != nil {
		return fmt.Errorf("serving mcp tool on %s: %w", subject, err)
	}
	return nil
}

// ServeMCPDiscovery answers discovery requests like ServeMCPTool answers tool
// requests. Its own subscriptions let a discovery overlap a tool call.
func (s *NATSAgentRPCServer) ServeMCPDiscovery(h mcpremote.DiscoveryHandler) error {
	handler := func(data []byte, reply func([]byte)) {
		var req mcpremote.MCPDiscoveryRequest
		if err := json.Unmarshal(data, &req); err != nil {
			sendAgentReply(reply, mcpremote.MCPDiscoveryResponse{Error: fmt.Sprintf("unmarshal error: %v", err)})
			return
		}
		s.discovery.Lock()
		defer s.discovery.Unlock()
		sendAgentReply(reply, h(context.Background(), req))
	}
	if _, err := s.bus.QueueSubscribeReply(messaging.SubjectMCPDiscovery, messaging.QueueAgentWorkers, handler); err != nil {
		return fmt.Errorf("serving mcp discovery on %s: %w", messaging.SubjectMCPDiscovery, err)
	}
	subject := messaging.SubjectNodeMCPDiscovery(s.nodeID)
	if _, err := s.bus.SubscribeReply(subject, handler); err != nil {
		return fmt.Errorf("serving mcp discovery on %s: %w", subject, err)
	}
	return nil
}

// ServeBackendStop calls h with the backend named by each stop request sent to
// this node. It never replies: the frontend sends the stop as a request and
// reads the timeout from a node without a backend supervisor as success. A body
// it cannot decode is dropped, as nothing waits for an answer.
func (s *NATSAgentRPCServer) ServeBackendStop(h func(backend string)) error {
	subject := messaging.SubjectNodeBackendStop(s.nodeID)
	_, err := s.bus.Subscribe(subject, func(data []byte) {
		// Only the backend name is read, so a change to the other fields of
		// the stop request cannot make this listener drop it.
		var req struct {
			Backend string `json:"backend"`
		}
		if json.Unmarshal(data, &req) != nil {
			return
		}
		h(req.Backend)
	})
	if err != nil {
		return fmt.Errorf("serving backend stop on %s: %w", subject, err)
	}
	return nil
}

// sendAgentReply ignores the encoding error, as the agent worker always has:
// the response types hold only JSON-safe data.
func sendAgentReply(reply func([]byte), resp any) {
	data, _ := json.Marshal(resp)
	reply(data)
}
