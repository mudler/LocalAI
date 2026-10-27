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
		// Agent workers serve the MCP tool and discovery RPC; they must not
		// handle backend.install. Keep this list in sync with the subscriptions
		// in core/cli/agent_worker.go.
		subAllow = []string{
			"gallery.*.cancel",
			"gallery.*.progress",
			"mcp.tools.execute",
			"mcp.discovery",
			prefix + ".backend.stop", // stop events drive MCP session cleanup
			"staging.*.progress",
			"_INBOX.>",
		}
		pubAllow = []string{
			"_INBOX.>",
		}
	default:
		// Backend worker: lifecycle + file staging on this node only.
		subAllow = []string{
			prefix + ".>",
			"_INBOX.>",
		}
		pubAllow = []string{
			prefix + ".backend.install.*.progress",
			prefix + ".files.>",
			"_INBOX.>",
		}
	}
	return pubAllow, subAllow
}
