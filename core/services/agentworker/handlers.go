// Package agentworker serves an agent worker's control plane over the tunnel it
// holds to the frontend.
//
// An agent worker runs no backend processes and stages no files. What it does
// serve is MCP: it creates the sessions a frontend cannot (stdio servers under
// docker), executes tools against them, and answers discovery. Those verbs
// travelled on NATS request-reply, which meant the frontend picked a worker by
// letting the bus's queue group pick one, and neither side could say which
// worker had answered.
//
// This package is the worker half of moving them onto the same HTTP control
// plane a backend worker already serves. The frontend half, which chooses WHICH
// agent worker to ask, is not here: this package answers, it does not select.
// That selection is nodes.AgentSelector, and it is a query against the
// connection rows rather than anything a broker does.
package agentworker

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/workerctl"
)

// UnaryHandler answers a verb with exactly one reply and no progress. The
// handler's bytes become the whole response body.
//
// An error returned here is this worker FAILING TO SERVE the verb, never the
// verb's own bad news. The distinction is the one the whole phase is built on:
// a body on a 200 is the WORKER'S OWN ANSWER, which a reap guard may act on,
// and everything else is a failure the frontend must not read as evidence
// about anything. An MCP tool that ran and errored is an answer, and it travels
// as bytes with an error field set, exactly as it did over the bus.
type UnaryHandler func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error)

// StreamHandler answers a verb that emits progress lines BEFORE its reply.
//
// It exists from this task even though nothing sets one yet, because the
// alternative found in review was a later task discovering that a Config of
// UnaryHandlers cannot express a verb that streams, and inventing a second
// handler shape under time pressure.
//
// pub is how a handler writes those lines. It is messaging.Publisher and not
// *agents.StreamPublisher so that this package does not import
// core/services/agents: the concrete writer is constructed by the caller that
// owns the response body, which is the server in this package, and the handler
// only ever needs Publish(subject string, data any) error.
type StreamHandler func(ctx context.Context, raw json.RawMessage, pub messaging.Publisher) (json.RawMessage, error)

// Config is what the agent worker's control plane needs to serve its verbs.
//
// A nil field means this worker does not serve that verb, and Register mounts
// nothing for it, so the catch-all under workerctl.Prefix answers 404. That is
// the same 404 an older build gives, which is what the frontend already reads
// as "this worker does not serve that verb" (nodes.ErrWorkerControlUnsupported)
// rather than as absence. It is why a later task can add two handlers without
// any other file changing shape.
type Config struct {
	// MCPTool answers workerctl.PathMCPToolExecute.
	MCPTool UnaryHandler
	// MCPDiscovery answers workerctl.PathMCPDiscovery.
	MCPDiscovery UnaryHandler
	// BackendStop drops the MCP sessions cached for a backend that is going
	// away. It answers workerctl.PathBackendStop, the SAME path a backend
	// worker serves with a completely different implementation: that one kills
	// the process and recycles its port. One path, two implementations, one
	// caller, which is what lets the frontend stop branching on node type to
	// pick a carrier.
	BackendStop func(ctx context.Context, req messaging.BackendStopRequest) error

	// AgentCancel answers workerctl.PathAgentCancel. It is the LAST family a
	// worker needed a message bus for: the cancel used to be a broadcast this
	// process had to dial a bus to hear, and is now an ordinary control RPC on
	// the tunnel it already holds.
	//
	// Unary and not streaming: a cancel has one answer and no progress. That
	// answer is messaging.AgentCancelReply and it speaks only for this worker.
	AgentCancel UnaryHandler

	// AgentExecute answers workerctl.PathAgentExecute and MCPCIRun answers
	// workerctl.PathMCPCIRun. Both are nil in this task and both are set later,
	// which is where the queue subjects they replace become claim rows. They are
	// declared here so the streaming shape is decided once, with the rest of the
	// control plane, rather than twice.
	AgentExecute StreamHandler
	MCPCIRun     StreamHandler
}

// Register mounts every agent control verb whose handler is non-nil on mux.
//
// It is the Register half of a nodes.AuthenticatedRoutes, so the mux it is
// handed is private and reachable only through that route set's bearer check.
// Nothing here does its own authentication, and nothing here may: a second
// check beside the first is the one that gets forgotten.
func (c Config) Register(mux *http.ServeMux) {
	if c.MCPTool != nil {
		mux.HandleFunc(workerctl.PathMCPToolExecute, serveUnary(workerctl.PathMCPToolExecute, c.MCPTool))
	}
	if c.MCPDiscovery != nil {
		mux.HandleFunc(workerctl.PathMCPDiscovery, serveUnary(workerctl.PathMCPDiscovery, c.MCPDiscovery))
	}
	if c.BackendStop != nil {
		mux.HandleFunc(workerctl.PathBackendStop, serveBackendStop(c.BackendStop))
	}
	if c.AgentCancel != nil {
		mux.HandleFunc(workerctl.PathAgentCancel, serveUnary(workerctl.PathAgentCancel, c.AgentCancel))
	}
	if c.AgentExecute != nil {
		mux.HandleFunc(workerctl.PathAgentExecute, serveStream(workerctl.PathAgentExecute, c.AgentExecute))
	}
	if c.MCPCIRun != nil {
		mux.HandleFunc(workerctl.PathMCPCIRun, serveStream(workerctl.PathMCPCIRun, c.MCPCIRun))
	}

	// The catch-all, mounted unconditionally and last. A path under the control
	// prefix that no verb claims is a frontend newer than this worker, or a
	// verb this worker does not implement, and both are the same answer.
	mux.HandleFunc(workerctl.Prefix, workerctl.WriteUnknownPath)
}
