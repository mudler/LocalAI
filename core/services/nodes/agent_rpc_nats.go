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

// NATSAgentRPCServer is the agent worker's end of NATSAgentControl, plus the
// node's backend stop listener. It keeps the subscriptions it makes because
// they live as long as the process and nobody unsubscribes them.
type NATSAgentRPCServer struct {
	bus    messaging.MessagingClient
	nodeID string

	mu   sync.Mutex
	subs []messaging.Subscription
}

func NewNATSAgentRPCServer(bus messaging.MessagingClient, nodeID string) *NATSAgentRPCServer {
	return &NATSAgentRPCServer{bus: bus, nodeID: nodeID}
}

// ServeMCPTool answers tool requests in the agent-workers queue group. The
// handler runs inline on the delivery goroutine, so a worker serves one tool
// call at a time, and on context.Background because the handler sets its own
// budget: a call in flight when the worker is told to stop runs to that budget.
func (s *NATSAgentRPCServer) ServeMCPTool(h mcpremote.ToolHandler) error {
	sub, err := s.bus.QueueSubscribeReply(messaging.SubjectMCPToolExecute, messaging.QueueAgentWorkers, func(data []byte, reply func([]byte)) {
		var req mcpremote.MCPToolRequest
		if err := json.Unmarshal(data, &req); err != nil {
			sendAgentReply(reply, mcpremote.MCPToolResponse{Error: fmt.Sprintf("unmarshal error: %v", err)})
			return
		}
		sendAgentReply(reply, h(context.Background(), req))
	})
	if err != nil {
		return fmt.Errorf("serving mcp tool on %s: %w", messaging.SubjectMCPToolExecute, err)
	}
	s.keep(sub)
	return nil
}

// ServeMCPDiscovery answers discovery requests like ServeMCPTool answers tool
// requests. Its own subscription lets a discovery overlap a tool call.
func (s *NATSAgentRPCServer) ServeMCPDiscovery(h mcpremote.DiscoveryHandler) error {
	sub, err := s.bus.QueueSubscribeReply(messaging.SubjectMCPDiscovery, messaging.QueueAgentWorkers, func(data []byte, reply func([]byte)) {
		var req mcpremote.MCPDiscoveryRequest
		if err := json.Unmarshal(data, &req); err != nil {
			sendAgentReply(reply, mcpremote.MCPDiscoveryResponse{Error: fmt.Sprintf("unmarshal error: %v", err)})
			return
		}
		sendAgentReply(reply, h(context.Background(), req))
	})
	if err != nil {
		return fmt.Errorf("serving mcp discovery on %s: %w", messaging.SubjectMCPDiscovery, err)
	}
	s.keep(sub)
	return nil
}

// ServeBackendStop calls h with the backend named by each stop request sent to
// this node. It never replies: the frontend sends the stop as a request and
// reads the timeout from a node without a backend supervisor as success. A body
// it cannot decode is dropped, as nothing waits for an answer.
func (s *NATSAgentRPCServer) ServeBackendStop(h func(backend string)) error {
	subject := messaging.SubjectNodeBackendStop(s.nodeID)
	sub, err := s.bus.Subscribe(subject, func(data []byte) {
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
	s.keep(sub)
	return nil
}

func (s *NATSAgentRPCServer) keep(sub messaging.Subscription) {
	s.mu.Lock()
	s.subs = append(s.subs, sub)
	s.mu.Unlock()
}

// sendAgentReply ignores the encoding error, as the agent worker always has:
// the response types hold only JSON-safe data.
func sendAgentReply(reply func([]byte), resp any) {
	data, _ := json.Marshal(resp)
	reply(data)
}
