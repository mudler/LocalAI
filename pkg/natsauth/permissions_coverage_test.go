package natsauth_test

import (
	"os"
	"regexp"
	"strings"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/pkg/natsauth"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// subjectMatches implements NATS subject-token matching: "*" matches exactly one
// token and ">" matches one or more trailing tokens. It lets these tests assert
// that a permission allow-list (which uses wildcards) actually covers a concrete
// subject a component publishes/subscribes — the same check the NATS server makes.
func subjectMatches(pattern, subject string) bool {
	p := strings.Split(pattern, ".")
	s := strings.Split(subject, ".")
	for i, tok := range p {
		if tok == ">" {
			return i < len(s) // ">" must match at least one remaining token
		}
		if i >= len(s) {
			return false
		}
		if tok != "*" && tok != s[i] {
			return false
		}
	}
	return len(p) == len(s)
}

func anyAllows(allow []string, subject string) bool {
	for _, p := range allow {
		if subjectMatches(p, subject) {
			return true
		}
	}
	return false
}

var _ = Describe("WorkerPermissions subject coverage", func() {
	// A node ID containing NATS-reserved characters exercises the (duplicated)
	// sanitizer in pkg/natsauth against the canonical one in core/services/messaging.
	// If the two ever diverge, the minted prefix stops matching the real subject
	// and these assertions fail — guarding the copy noted in the review.
	const nodeID = "host.a 1*b"

	Context("backend worker", func() {
		pub, sub := natsauth.WorkerPermissions(nodeID, "backend")

		// Every subject core/services/worker/{lifecycle,file_staging}.go subscribes to.
		subscribed := []string{
			messaging.SubjectNodeBackendInstall(nodeID),
			messaging.SubjectNodeBackendUpgrade(nodeID),
			messaging.SubjectNodeBackendStop(nodeID),
			messaging.SubjectNodeBackendDelete(nodeID),
			messaging.SubjectNodeBackendList(nodeID),
			messaging.SubjectNodeModelUnload(nodeID),
			messaging.SubjectNodeModelDelete(nodeID),
			messaging.SubjectNodeStop(nodeID),
			messaging.SubjectNodeFilesEnsure(nodeID),
			messaging.SubjectNodeFilesStage(nodeID),
			messaging.SubjectNodeFilesTemp(nodeID),
			messaging.SubjectNodeFilesListDir(nodeID),
		}
		for _, subject := range subscribed {
			It("allows subscribing to "+subject, func() {
				Expect(anyAllows(sub, subject)).To(BeTrue(),
					"backend JWT sub allow-list %v does not cover %s", sub, subject)
			})
		}

		It("allows publishing backend.install progress", func() {
			subject := messaging.SubjectNodeBackendInstallProgress(nodeID, "op-123")
			Expect(anyAllows(pub, subject)).To(BeTrue(),
				"backend JWT pub allow-list %v does not cover %s", pub, subject)
		})
	})

	Context("agent worker", func() {
		// node_type "agent"; subjects from core/cli/agent_worker.go.
		pub, sub := natsauth.WorkerPermissions(nodeID, "agent")
		_ = pub

		subscribed := []string{
			messaging.SubjectAgentExecute,            // dispatcher (default --agent-subject)
			messaging.SubjectMCPToolExecute,          // QueueSubscribeReply
			messaging.SubjectMCPDiscovery,            // QueueSubscribeReply
			messaging.SubjectMCPCIJobsNew,            // QueueSubscribe — jobs.mcp-ci.new
			messaging.SubjectNodeBackendStop(nodeID), // Subscribe — MCP session cleanup
		}
		for _, subject := range subscribed {
			It("allows subscribing to "+subject, func() {
				Expect(anyAllows(sub, subject)).To(BeTrue(),
					"agent JWT sub allow-list %v does not cover %s — the agent worker subscribes to it", sub, subject)
			})
		}
	})
})

var allowPubRe = regexp.MustCompile(`--allow-pub "([^"]*)"`)

var _ = Describe("Documented NATS service-user permissions", func() {
	// scripts/nats-auth-setup.sh ships the recommended service (frontend) JWT
	// permissions. They must cover every subject the frontend actually publishes,
	// or prefix-cache sync (and friends) break once LOCALAI_NATS_REQUIRE_AUTH is on.
	const scriptPath = "../../scripts/nats-auth-setup.sh"

	// Representative subjects the frontend publishes on the control plane.
	// prefixcache.* is emitted by prefixcache.Sync in core/application/distributed.go.
	frontendPublishes := []string{
		messaging.SubjectPrefixCacheObserve,
		messaging.SubjectPrefixCacheInvalidate,
		messaging.SubjectNodeBackendInstall("node-1"),
		messaging.SubjectGalleryProgress("op-1"),
	}

	It("cover every subject the frontend publishes", func() {
		raw, err := os.ReadFile(scriptPath)
		Expect(err).ToNot(HaveOccurred(), "cannot read %s", scriptPath)
		m := allowPubRe.FindStringSubmatch(string(raw))
		Expect(m).To(HaveLen(2), "no --allow-pub list found in %s", scriptPath)
		allow := strings.Split(m[1], ",")

		for _, subject := range frontendPublishes {
			Expect(anyAllows(allow, subject)).To(BeTrue(),
				"service-user --allow-pub %v does not cover %s (frontend publishes it)", allow, subject)
		}
	})
})
