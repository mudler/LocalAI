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
		subAllow = []string{
			"agent.execute",
			"jobs.*.cancel",
			"jobs.*.progress",
			"jobs.*.result",
			"mcp.tools.execute",
			"mcp.discovery",
			"_INBOX.>",
		}
		pubAllow = []string{
			"agent.>",
			"jobs.>",
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