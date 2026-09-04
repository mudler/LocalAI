// SPDX-License-Identifier: MIT

package nodes

import (
	"context"
	"errors"
	"fmt"

	mcpremote "github.com/mudler/LocalAI/core/services/mcp"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/workerctl"
	"github.com/mudler/xlog"
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

// ErrAgentCancelUndelivered reports that a cancel may have reached nobody: at
// least one agent worker that could be running the execution was not asked, or
// did not answer.
//
// It is the answer this whole family was held back for. A cancel that could not
// be delivered is NOT a cancel that was refused and NOT a run that does not
// exist, and a caller told any of those three in place of another acts on
// something that did not happen. It says nothing about any particular worker
// either, which is why it is its own sentinel and carries neither
// ErrWorkerUnroutable nor anything cluster.IsWorkerAnswer accepts: no node may
// be reaped, demoted or evicted because a cancel went undelivered.
var ErrAgentCancelUndelivered = errors.New("nodes: an agent cancel could not be delivered to every agent worker that might be running it")

// ErrAgentRunNotOnAnyWorker reports that every agent worker this deployment
// could reach answered that it is not running the named execution.
//
// It is assembled ONLY from workers' own answers, and only when every worker
// was reached; the moment one was not, ErrAgentCancelUndelivered is the answer
// instead. It still does not say the run does not exist: it says no agent
// worker is running it, which is the largest claim the evidence supports.
var ErrAgentRunNotOnAnyWorker = errors.New("nodes: no agent worker of this deployment is running that execution")

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

// CancelAgentRun asks the agent workers of this deployment to stop one
// execution, and reports which of three different things happened.
//
// A FAN-OUT and not a pick, which is what makes it the one agent verb that does
// not go through agentVerb. A message id names exactly one execution, running
// on exactly one worker, and no row in this deployment records which: the claim
// that dispatched it names the claiming REPLICA, not the worker, and it is
// deleted when the run ends. So the cancel is offered to every worker a live
// replica can reach, exactly as the broadcast it replaces was, and each worker
// answers only for itself.
//
// The three answers, and why none may stand in for another:
//
//   - nil. A worker answered that it cancelled the run. That is a worker's own
//     answer and it is conclusive, even if another worker could not be reached:
//     the execution has been cancelled, and there is only one of it.
//   - ErrAgentCancelUndelivered. Some worker that might have been running it
//     was not reached. Nothing was learned, and the caller may not report the
//     run as missing or the cancel as declined.
//   - ErrAgentRunNotOnAnyWorker. Every worker was reached and every one of them
//     answered that it is not running that execution.
//
// A worker that is RECONNECTING is counted undelivered, and this is the
// decision rather than an omission: it is not retried here and it is not
// queued. Retrying would hold the caller for the length of the reconnect grace
// with no bound it chose, and queueing would need durable state whose only
// consumer is a run whose control stream died with the tunnel. Reporting it,
// once, as a cancel that may not have arrived is the only thing this frontend
// actually knows, and it leaves the retry where the budget lives: with the
// caller.
func (a *AgentControlClient) CancelAgentRun(ctx context.Context, req messaging.AgentCancelRequest) error {
	if a == nil || a.sel == nil || a.cc == nil {
		return fmt.Errorf("control rpc %s: %w", workerctl.PathAgentCancel, ErrNoAgentControl)
	}
	reach, err := a.sel.Reachable(ctx)
	if err != nil {
		return err
	}

	if len(reach.Connected) == 0 && len(reach.Absent) == 0 {
		// Nothing was asked of anyone and nothing was learned. This must not
		// become ErrAgentRunNotOnAnyWorker, which is assembled from workers'
		// own answers: a deployment with no agent worker has produced no
		// answers at all, and reporting one would tell a caller that a run it
		// can still see is not running anywhere.
		return fmt.Errorf("cancelling message %q of agent %q: %w", req.MessageID, req.AgentName, ErrNoAgentWorker)
	}

	cancelled := false
	// Seeded with the workers nobody could ask at all: a tunnel lost inside the
	// reconnect grace, or a presence this replica could not read. They are part
	// of the fleet this cancel did not finish asking, and dropping them here is
	// what would turn an unfinished fan-out into "no worker is running it".
	undelivered := append([]string(nil), reach.Absent...)
	for _, nodeID := range reach.Connected {
		var reply messaging.AgentCancelReply
		if err := a.cc.Call(ctx, nodeID, workerctl.PathAgentCancel, req, &reply); err != nil {
			// An unreachable peer, a refused stream, a tunnel that died between
			// the connection read and the dial, or a worker too old to serve
			// the verb. None of them is an answer about this execution.
			xlog.Warn("An agent worker could not be asked to cancel an execution",
				"nodeID", nodeID, "agent", req.AgentName, "messageID", req.MessageID, "error", err)
			undelivered = append(undelivered, nodeID)
			continue
		}
		if reply.Cancelled {
			cancelled = true
		}
	}

	switch {
	case cancelled:
		return nil
	case len(undelivered) > 0:
		return fmt.Errorf("cancelling message %q of agent %q: %d of %d agent workers could not be asked (%v): %w",
			req.MessageID, req.AgentName, len(undelivered), len(reach.Connected)+len(reach.Absent), undelivered, ErrAgentCancelUndelivered)
	default:
		return fmt.Errorf("cancelling message %q of agent %q: all %d reachable agent workers answered that they are not running it: %w",
			req.MessageID, req.AgentName, len(reach.Connected), ErrAgentRunNotOnAnyWorker)
	}
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
