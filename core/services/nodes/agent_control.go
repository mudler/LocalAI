package nodes

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/mudler/LocalAI/core/config"
	mcpremote "github.com/mudler/LocalAI/core/services/mcp"
	"github.com/mudler/LocalAI/core/services/workerctl"
)

// maxAgentPicks bounds how many agent workers one request is offered to. A
// caller is waiting, and a fleet whose first three workers cannot take the
// request has a problem that a fourth try does not fix. A worker whose tunnel
// broke between the connection read and the dial is ordinary, and giving up on
// the first of those would turn every worker that moves between replicas into an
// outage.
const maxAgentPicks = 3

// ErrNoAgentControl reports that this process was built without an agent control
// client. It says nothing about any worker.
var ErrNoAgentControl = errors.New("nodes: this deployment has no agent control client")

// AgentControlClient is the AgentControl of the tunnel carrier. It sends the MCP
// requests of the frontend to the agent worker that the selector picks, over the
// tunnel of that worker.
//
// The contract is the one of the NATS implementation. A decoded reply comes back
// with a nil error, also when its Error field is set, because that is the answer
// of the worker. A request that no agent worker could be offered wraps
// ErrNoRoute. A timeout, a broken stream and a reply that cannot be read are
// ordinary errors.
type AgentControlClient struct {
	sel *AgentSelector
	cc  *ControlClient
}

// NewAgentControlClient returns the client that sends the agent requests that
// sel picks a worker for over cc.
func NewAgentControlClient(sel *AgentSelector, cc *ControlClient) *AgentControlClient {
	return &AgentControlClient{sel: sel, cc: cc}
}

// ExecuteMCPTool runs one MCP tool on an agent worker.
func (a *AgentControlClient) ExecuteMCPTool(ctx context.Context, req mcpremote.MCPToolRequest) (*mcpremote.MCPToolResponse, error) {
	return agentVerb[mcpremote.MCPToolRequest, mcpremote.MCPToolResponse](ctx, a, workerctl.VerbMCPToolExecute, config.DefaultMCPToolTimeout, req)
}

// DiscoverMCPTools lists the MCP servers and tools that the configuration of a
// model reaches.
func (a *AgentControlClient) DiscoverMCPTools(ctx context.Context, req mcpremote.MCPDiscoveryRequest) (*mcpremote.MCPDiscoveryResponse, error) {
	return agentVerb[mcpremote.MCPDiscoveryRequest, mcpremote.MCPDiscoveryResponse](ctx, a, workerctl.VerbMCPDiscovery, config.DefaultMCPDiscoveryTimeout, req)
}

// agentVerb sends one request to up to maxAgentPicks workers, one after the
// other, and stops at the first answer.
//
// What counts as no answer, and may go to the next worker, is only what proves
// that the request did not start: no route to the worker, a worker too old to
// serve the verb, a worker with no free slot. A stream that broke, a reply that
// cannot be read and a timeout are not on the list, because the tool may have run
// and a second run could do it twice.
//
// Only the deadline of ctx applies, never its cancellation. A chat client that
// disconnects must not abort a tool call that is already running on a worker.
func agentVerb[Req, Rep any](ctx context.Context, a *AgentControlClient, verb string, fallback time.Duration, req Req) (*Rep, error) {
	if a == nil || a.sel == nil || a.cc == nil {
		// A typed nil pointer is not a nil interface, so the receiver is checked
		// as well as its parts.
		return nil, fmt.Errorf("control request %s: %w", verb, ErrNoAgentControl)
	}
	callCtx, cancel, err := agentCallContext(ctx, fallback)
	if err != nil {
		return nil, err
	}
	defer cancel()

	tried := make(map[string]bool, maxAgentPicks)
	var last error
	for range maxAgentPicks {
		nodeID, _, err := a.sel.PickConnectedExcluding(callCtx, tried)
		if err != nil {
			if last != nil && errors.Is(err, ErrNoAgentWorker) {
				// Every worker that holds a tunnel has failed to take the request.
				// What they said is the evidence; the end of the list is not.
				return nil, last
			}
			return nil, err
		}
		tried[nodeID] = true

		var reply Rep
		if err := a.cc.Call(callCtx, nodeID, verb, req, &reply); err != nil {
			last = err
			if callerRanOut(callCtx) != nil || !requestNeverStarted(err) {
				return nil, err
			}
			continue
		}
		return &reply, nil
	}
	return nil, last
}

// requestNeverStarted reports whether the failure of a call proves that the
// worker did not start the request.
func requestNeverStarted(err error) bool {
	return errors.Is(err, ErrNoRoute) || errors.Is(err, errVerbNotServed) || errors.Is(err, ErrWorkerBusy)
}

// agentCallContext keeps the deadline of ctx and drops its cancellation. Without
// a deadline the call gets the default of its verb. A budget that is spent
// already is refused here, and is the budget of the caller running out.
func agentCallContext(ctx context.Context, fallback time.Duration) (context.Context, context.CancelFunc, error) {
	budget := fallback
	if deadline, ok := ctx.Deadline(); ok {
		budget = time.Until(deadline)
		if budget <= 0 {
			return nil, nil, fmt.Errorf("agent request budget spent: %w", context.DeadlineExceeded)
		}
	}
	callCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), budget)
	return callCtx, cancel, nil
}
