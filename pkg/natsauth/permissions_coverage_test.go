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

		// A backend worker opens no connection at all on this build. Every verb
		// a frontend gives it, the backend and model lifecycle ten plus the
		// four file-staging verbs, is an HTTP route on its tunnelled control
		// plane, so there is no subject left to cover. See
		// core/services/workerctl.
		It("no longer grants a backend worker its own node subtree to subscribe on", func() {
			Expect(sub).ToNot(ContainElement("nodes."+workerSubjectTokenForTest(nodeID)+".>"),
				"the node subtree went with the connection the worker no longer opens")
		})

		// The grant must be a grant of NOTHING and not an ABSENT grant: NATS
		// treats an empty allow list as no restriction, so a branch that
		// returned nil would silently widen every backend JWT the frontend
		// still mints to the whole account. ConsistOf, not BeEmpty, is what
		// tells those two apart.
		It("grants a backend worker its own inbox and nothing else", func() {
			Expect(sub).To(ConsistOf("_INBOX.>"))
			Expect(sub).ToNot(BeEmpty(),
				"an empty allow list is unrestricted in NATS, not restrictive")
		})

		// The negative half, and it is the one that would catch a verb quietly
		// coming back to the bus: a backend worker is granted nothing to
		// publish at all beyond its own inbox. File staging used to be the one
		// exception and is not any more.
		It("grants a backend worker no publish rights outside its inbox", func() {
			Expect(pub).To(ConsistOf("_INBOX.>"))
			Expect(pub).ToNot(BeEmpty(),
				"an empty allow list is unrestricted in NATS, not restrictive")
		})

		It("no longer grants a backend worker the file-staging publish subtree", func() {
			Expect(anyAllows(pub, "nodes."+workerSubjectTokenForTest(nodeID)+".files.stage")).To(BeFalse(),
				"backend JWT pub allow-list %v still covers file staging", pub)
		})
	})

	Context("agent worker", func() {
		// node_type "agent"; subjects from core/cli/agent_worker.go.
		pub, sub := natsauth.WorkerPermissions(nodeID, "agent")

		// The subjects an agent worker still subscribes to. Both queue-group
		// workloads have left this list: agent execution and MCP CI runs are
		// streaming control verbs on the tunnel now, driven by a claim a
		// frontend replica took off the job store.
		subscribed := []string{
			messaging.SubjectAgentCancelWildcard,
		}

		// The half that catches a narrowing going too far. NATS reads an EMPTY
		// allow list as NO restriction, so a branch trimmed to nothing does not
		// lock an agent worker down, it opens the whole account to it.
		It("keeps the agent worker's allow lists non-empty", func() {
			Expect(sub).ToNot(BeEmpty(),
				"an empty allow list is unrestricted in NATS, not restrictive")
			Expect(pub).ToNot(BeEmpty(),
				"an empty allow list is unrestricted in NATS, not restrictive")
		})

		// MCP execution and discovery, backend.stop, and now agent execution
		// and MCP CI runs, are all control RPCs on the worker's tunnel,
		// addressed by the frontend rather than by a subject. An agent worker
		// subscribes to none of them, so a JWT that still granted one would be
		// granting a subscription nothing serves. Every entry is spelled out by
		// hand rather than built from a deleted constant, which is the only way
		// this can still name the subject that used to be granted.
		for _, subject := range []string{
			"mcp.tools.execute",
			"mcp.discovery",
			"nodes." + workerSubjectTokenForTest(nodeID) + ".backend.stop",
			"agent.execute",
			"jobs.mcp-ci.new",
		} {
			It("no longer grants an agent worker "+subject, func() {
				Expect(anyAllows(sub, subject)).To(BeFalse(),
					"agent JWT sub allow-list %v still covers %s", sub, subject)
			})
		}

		// The other half of the narrowing, and the one the phase's own trap
		// makes necessary: an allow list trimmed to nothing is UNRESTRICTED in
		// NATS. Naming what must survive is what tells a narrowing from an
		// escalation, so the queue workloads an agent worker still consumes are
		// asserted present just above, and the list is asserted non-empty here.
		It("still grants the agent worker the broadcast subjects it lives on", func() {
			Expect(sub).To(ContainElement("agent.*.cancel"))
			Expect(sub).To(ContainElement("jobs.*.progress"))
			Expect(sub).To(ContainElement("_INBOX.>"))
		})
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
