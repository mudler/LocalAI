package natsauth

import "strings"

// workerSubjectToken mirrors messaging.sanitizeSubjectToken without importing unexported logic.
func workerSubjectToken(nodeID string) string {
	r := strings.NewReplacer(".", "-", "*", "-", ">", "-", " ", "-", "\t", "-", "\n", "-")
	return r.Replace(nodeID)
}

// WorkerPermissions returns NATS pub/sub allow lists for a registered node.
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
		// Backend worker. Every verb a frontend gives it left the bus: the
		// backend and model lifecycle verbs and now file staging too are HTTP
		// routes under workerctl.Prefix, served on the worker's own server and
		// reached through its tunnel, so no subject is minted for them and none
		// is allowed here.
		//
		// The subscribe wildcard stays for now. A worker subscribes to nothing
		// under it on this build, but narrowing it is a change a worker
		// mid-upgrade would feel, and the connection itself is what the next
		// step of this removal deletes.
		subAllow = []string{
			prefix + ".>",
			"_INBOX.>",
		}
		// Nothing left to publish. backend.install.*.progress went with the
		// install subject, and the file-staging replies went with theirs.
		pubAllow = []string{
			"_INBOX.>",
		}
	}
	return pubAllow, subAllow
}
