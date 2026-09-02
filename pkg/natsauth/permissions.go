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
		// Backend worker: file staging on this node only.
		//
		// The backend and model lifecycle verbs left the bus: they are HTTP
		// routes under workerctl.Prefix, served on the worker's own server and
		// reached through its tunnel, so no subject is minted for them and none
		// is allowed here. The wildcard stays because the file-staging subjects
		// are still under this node's prefix; narrowing it to nodes.<id>.files.>
		// is a separate change that would break a worker mid-upgrade.
		subAllow = []string{
			prefix + ".>",
			"_INBOX.>",
		}
		// backend.install.*.progress is gone with the subject: install progress
		// is written into the install response the frontend is already reading.
		pubAllow = []string{
			prefix + ".files.>",
			"_INBOX.>",
		}
	}
	return pubAllow, subAllow
}
