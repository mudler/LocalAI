package natsauth

import "strings"

// workerSubjectToken mirrors messaging.sanitizeSubjectToken without importing unexported logic.
func workerSubjectToken(nodeID string) string {
	r := strings.NewReplacer(".", "-", "*", "-", ">", "-", " ", "-", "\t", "-", "\n", "-")
	return r.Replace(nodeID)
}

// WorkerPermissions returns NATS pub/sub allow lists for a registered node.
//
// It serves AGENT nodes. They are the only workers left that connect to the
// bus: an agent worker subscribes to the queue subjects listed below, while a
// backend worker connects to no bus at all, because every verb a frontend gives
// it is an HTTP route on its own server reached through its outbound tunnel
// (core/services/workerctl).
//
// The non-agent branch is therefore a grant of nothing, and it has to be
// spelled that way rather than deleted. NATS reads an EMPTY allow list as no
// restriction, so a function that returned nil here would upgrade every JWT the
// frontend still mints for a backend node from "its own inbox" to "the entire
// account". The inbox is self-scoped and reaches no cluster subject.
//
// nodeID no longer narrows anything: no allow list below is per-node, because
// the only per-node subject an agent worker ever subscribed to was its
// backend.stop, which is a control RPC on its tunnel now. It stays in the
// signature so a future per-node grant has somewhere to come from, and because
// mint.go names the JWT user after it.
func WorkerPermissions(nodeID, nodeType string) (pubAllow, subAllow []string) {
	switch nodeType {
	case "agent":
		// Agent workers consume queue workloads; they must not handle backend.install.
		// Keep this list in sync with the subscriptions in core/cli/agent_worker.go.
		//
		// MCP tool execution and discovery are NOT here, and neither is the
		// per-node backend.stop: all three are control RPCs on the tunnel the
		// worker holds, addressed by the frontend rather than by a subject.
		// Removing them narrowed this list; it must never be narrowed to
		// nothing, because NATS reads an EMPTY allow list as no restriction at
		// all, which would widen an agent JWT to the whole account.
		subAllow = []string{
			"agent.execute",
			"agent.*.cancel",
			"gallery.*.cancel",
			"gallery.*.progress",
			"jobs.*.cancel",
			"jobs.*.progress",
			"jobs.*.result",
			"jobs.mcp-ci.new", // MCP CI jobs dispatched to agent workers
			"staging.*.progress",
			"_INBOX.>",
		}
		pubAllow = []string{
			"agent.>",
			"jobs.>",
			"_INBOX.>",
		}
	default:
		// Backend worker: nothing, held open at its own inbox for the reason in
		// the doc comment. The node subtree it used to subscribe on went with
		// the connection itself, which this worker no longer opens.
		subAllow = []string{"_INBOX.>"}
		pubAllow = []string{"_INBOX.>"}
	}
	return pubAllow, subAllow
}
