// SPDX-License-Identifier: MIT

package nodes

import (
	"context"
	"errors"
	"fmt"

	mcpremote "github.com/mudler/LocalAI/core/services/mcp"
	"github.com/mudler/LocalAI/core/services/workerctl"
)

// maxAgentPicks bounds how many agent workers one verb is offered to before it
// gives up.
//
// Three rather than "every agent in the fleet": a caller is waiting, and a
// deployment whose first three connected agents all fail to answer has a
// problem that trying a fourth does not fix. It is also not one: a worker whose
// tunnel died between the connection read and the dial is ordinary, and giving
// up on the first of those would make every re-homing agent an outage.
const maxAgentPicks = 3

// ErrNoAgentControl reports that this deployment has no agent control client
// wired at all.
//
// Like ErrNoAgentWorker it is neither ErrWorkerUnroutable nor anything
// cluster.IsWorkerAnswer accepts: it is a fact about this process's own wiring
// and says nothing about any worker.
var ErrNoAgentControl = errors.New("nodes: this deployment has no agent control client")

// AgentControlClient issues the frontend's control RPCs to whichever agent
// worker the selector picks.
//
// It is the caller the agent worker's control plane has been waiting for. What
// used to be a NATS request onto a queue group is now two ordinary things: a
// SELECTION, which is a query against the connection rows, and a control RPC
// over the picked worker's own tunnel, relayed by the peer mesh when another
// replica holds it.
type AgentControlClient struct {
	sel *AgentSelector
	cc  *ControlClient
}

// NewAgentControlClient returns the client that carries the frontend's agent
// verbs to the workers sel picks, over cc.
func NewAgentControlClient(sel *AgentSelector, cc *ControlClient) *AgentControlClient {
	return &AgentControlClient{sel: sel, cc: cc}
}

// ExecuteMCPTool runs one MCP tool on an agent worker and returns its answer.
//
// A reply carrying an Error is the WORKER'S OWN ANSWER and comes back as an
// error naming it, never as a retry: see agentVerb.
func (a *AgentControlClient) ExecuteMCPTool(ctx context.Context, req mcpremote.MCPToolRequest) (*mcpremote.MCPToolResponse, error) {
	return agentVerb(ctx, a, workerctl.PathMCPToolExecute, req,
		func(r *mcpremote.MCPToolResponse) string { return r.Error })
}

// DiscoverMCPTools asks an agent worker which MCP servers and tools a model's
// configuration reaches.
func (a *AgentControlClient) DiscoverMCPTools(ctx context.Context, req mcpremote.MCPDiscoveryRequest) (*mcpremote.MCPDiscoveryResponse, error) {
	return agentVerb(ctx, a, workerctl.PathMCPDiscovery, req,
		func(r *mcpremote.MCPDiscoveryResponse) string { return r.Error })
}

// agentVerb is the ONE place the select-call-retry rule lives, and the one
// place the line between a retryable failure and an answer is drawn.
//
// The rule, stated as the code enforces it:
//
//   - A DECODED REPLY whose Error field is set is the worker's own answer to
//     this verb. It is returned as an error naming what the worker said and is
//     NEVER offered to another worker: retrying it would turn "this MCP server
//     rejected your arguments" into "the fleet is broken", and could run a tool
//     twice.
//   - Everything the RPC returns as a Go ERROR is a failure to obtain an
//     answer. The request never reached a handler (a refused stream, an
//     unreachable peer, a lost tunnel, a 404 from an older build) or its answer
//     could not be read, so nothing ran to completion here and another worker
//     may be offered the verb.
//
// This is deliberately NOT keyed on cluster.IsWorkerAnswer, and the reason is
// worth stating because the plan for this task said it should be.
// IsWorkerAnswer accepts the tunnel's stream-refusal vocabulary, which a worker
// writes BEFORE any request body reaches its control server: ErrStreamTagUnknown
// and ErrStreamTargetUnavailable both mean "I could not carry this to my own
// control plane", not "I ran your tool and here is what happened". Returning
// those unchanged without trying another agent would make one agent worker with
// a dead control server take down MCP for the whole deployment, and it is the
// case the plan's own spec list requires to be retried.
//
// The taxonomy is preserved either way: whatever error is returned is returned
// UNWRAPPED, so cluster.IsWorkerAnswer and ErrWorkerUnroutable still see
// exactly what ControlClient produced.
func agentVerb[Req any, Rep any](ctx context.Context, a *AgentControlClient, path string, req Req,
	answerError func(*Rep) string) (*Rep, error) {
	if a == nil || a.sel == nil || a.cc == nil {
		// Guarded on the nil RECEIVER too, because the interface a caller holds
		// this through is satisfied by a typed nil pointer, which is not an
		// untyped nil and would otherwise panic inside a request.
		return nil, fmt.Errorf("control rpc %s: %w", path, ErrNoAgentControl)
	}
	tried := make(map[string]bool, maxAgentPicks)
	var lastErr error
	for range maxAgentPicks {
		nodeID, _, err := a.sel.pickConnectedExcluding(ctx, tried)
		if err != nil {
			if lastErr != nil && errors.Is(err, ErrNoAgentWorker) {
				// Every worker that held a tunnel has now failed to answer. The
				// evidence is what they said, not that the candidate list ran
				// out, and reporting an empty fleet here would hide a fleet
				// that is refusing.
				return nil, lastErr
			}
			return nil, err
		}
		tried[nodeID] = true

		var reply Rep
		if err := a.cc.Call(ctx, nodeID, path, req, &reply); err != nil {
			lastErr = err
			if ctx.Err() != nil {
				// The caller's own budget is spent. Another pick would fail the
				// same way immediately, and the error already says whose fault
				// the expiry was.
				return nil, lastErr
			}
			continue
		}
		if msg := answerError(&reply); msg != "" {
			return nil, fmt.Errorf("agent worker %q answered %s with an error: %s", nodeID, path, msg)
		}
		return &reply, nil
	}
	return nil, lastErr
}
