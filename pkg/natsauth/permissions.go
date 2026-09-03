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
func WorkerPermissions(nodeID, nodeType string) (pubAllow, subAllow []string) {
	tok := workerSubjectToken(nodeID)
	prefix := "nodes." + tok

	switch nodeType {
	case "agent":
		// Agent workers consume queue workloads; they must not handle backend.install.
		// Keep this list in sync with the subscriptions in core/cli/agent_worker.go.
		subAllow = []string{
			"agent.execute",
			"agent.*.cancel",
			"gallery.*.cancel",
			"gallery.*.progress",
			"jobs.*.cancel",
			"jobs.*.progress",
			"jobs.*.result",
			"jobs.mcp-ci.new", // MCP CI jobs dispatched to agent workers
			"mcp.tools.execute",
			"mcp.discovery",
			prefix + ".backend.stop", // stop events drive MCP session cleanup
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
