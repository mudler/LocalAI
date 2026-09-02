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

// workerSubjectTokenForTest mirrors the sanitizer both packages implement, so
// the negative assertion below names the exact prefix without reaching into
// either package's unexported copy.
func workerSubjectTokenForTest(nodeID string) string {
	return strings.NewReplacer(".", "-", "*", "-", ">", "-", " ", "-", "\t", "-", "\n", "-").Replace(nodeID)
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

		// A backend worker subscribes to no subject of its own on this build.
		// Every verb a frontend gives it — the backend and model lifecycle ten,
		// and now the four file-staging verbs — is an HTTP route on its
		// tunnelled control plane, so there is no subject left to cover. See
		// core/services/workerctl.
		//
		// The subscribe wildcard is asserted rather than removed because the
		// grant is still minted and a worker mid-upgrade still uses it.
		It("still grants a backend worker its own node subtree to subscribe on", func() {
			Expect(sub).To(ConsistOf(
				"nodes."+workerSubjectTokenForTest(nodeID)+".>",
				"_INBOX.>",
			))
		})

		// The negative half, and it is the one that would catch a verb quietly
		// coming back to the bus: a backend worker is granted nothing to
		// publish at all beyond its own inbox. File staging used to be the one
		// exception and is not any more.
		It("grants a backend worker no publish rights outside its inbox", func() {
			Expect(pub).To(ConsistOf("_INBOX.>"))
		})

		It("no longer grants a backend worker the file-staging publish subtree", func() {
			Expect(anyAllows(pub, "nodes."+workerSubjectTokenForTest(nodeID)+".files.stage")).To(BeFalse(),
				"backend JWT pub allow-list %v still covers file staging", pub)
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
		messaging.SubjectNodeBackendStop("node-1"),
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
