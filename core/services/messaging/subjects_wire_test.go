package messaging

import (
	"encoding/json"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// BackendInstallReply.WorkerLocalAddress was called Address until workers
// stopped advertising. The Go field was renamed so no reader takes it for a
// dial target; the wire key was deliberately NOT renamed, because a worker and
// a frontend from different releases have to keep understanding each other
// across a rolling upgrade.
//
// That is a cross-version compatibility property resting on one struct tag, and
// a struct tag nobody asserts is a property nobody has. Renaming just the tags
// left the whole suite green when this was written.
var _ = Describe("backend.install reply wire format", func() {
	It("writes the address under the key an older frontend reads", func() {
		out, err := json.Marshal(BackendInstallReply{Success: true, WorkerLocalAddress: "127.0.0.1:50052"})
		Expect(err).ToNot(HaveOccurred())

		var raw map[string]any
		Expect(json.Unmarshal(out, &raw)).To(Succeed())
		Expect(raw).To(HaveKeyWithValue("address", "127.0.0.1:50052"))
		Expect(raw).ToNot(HaveKey("worker_local_address"),
			"renaming the wire key would make every install reply unreadable to a frontend of another release")
	})

	It("reads the address an older worker sends", func() {
		// An older worker puts its ADVERTISED host here. Only the port is used,
		// and the port is the same, so accepting it is both harmless and the
		// thing that keeps a mixed fleet working.
		var reply BackendInstallReply
		Expect(json.Unmarshal([]byte(`{"success":true,"address":"worker-1:50052"}`), &reply)).To(Succeed())
		Expect(reply.Success).To(BeTrue())
		Expect(reply.WorkerLocalAddress).To(Equal("worker-1:50052"))
	})

	It("omits the address when the install failed", func() {
		out, err := json.Marshal(BackendInstallReply{Success: false, Error: "boom"})
		Expect(err).ToNot(HaveOccurred())
		Expect(string(out)).ToNot(ContainSubstring("address"))
	})
})

// The per-tenant SyncedMap subject is a data-boundary, not a naming
// convenience: one subject shared by N tenants means tenant A's delta is
// applied into tenant B's in-memory map on every replica. These pins hold the
// SHAPE that makes cross-matching impossible, because the shape is the whole
// mechanism.
var _ = Describe("per-tenant SyncedMap subjects", func() {
	It("puts the tenant in its own token", func() {
		Expect(SubjectSyncStateTenantDelta("agent.tasks", "u1")).To(Equal("state.agent-tasks.u1.delta"))
	})

	It("builds the every-tenant filter from the same name", func() {
		Expect(SubjectSyncStateTenantWildcard("agent.tasks")).To(Equal("state.agent-tasks.*.delta"))
	})

	It("sanitizes the tenant so an id cannot inject a token", func() {
		// A tenant id is user-controlled in the sense that it comes from the
		// auth store rather than from this package. If it were interpolated
		// raw, an id containing '.' would mint extra tokens and an id
		// containing '*' would mint a wildcard that spans every other tenant.
		Expect(SubjectSyncStateTenantDelta("agent.tasks", "a.b*c>d")).To(Equal("state.agent-tasks.a-b-c-d.delta"))
	})

	It("cannot cross-match the cluster-wide subject", func() {
		// SubjectMatches compares token COUNT first, so three tokens and four
		// tokens are unrelated no matter what the tokens say. This is the
		// assertion the four-token choice exists for.
		Expect(SubjectMatches(SubjectSyncStateTenantWildcard("agent.tasks"), SubjectSyncStateDelta("agent.tasks"))).To(BeFalse(),
			"the every-tenant filter must not swallow the cluster-wide subject")
		Expect(SubjectMatches(SubjectSyncStateDelta("agent.tasks"), SubjectSyncStateTenantDelta("agent.tasks", "u1"))).To(BeFalse(),
			"the cluster-wide subject must not double as a tenant filter")
	})

	It("matches every tenant through the wildcard", func() {
		// The complement of the token-count assertion, and the half the
		// cluster-wide administrative view depends on: four tokens are only
		// the right shape if the wildcard actually reaches them.
		Expect(SubjectMatches(
			SubjectSyncStateTenantWildcard("agent.tasks"),
			SubjectSyncStateTenantDelta("agent.tasks", "u1"),
		)).To(BeTrue())
		Expect(SubjectMatches(
			SubjectSyncStateTenantWildcard("agent.tasks"),
			SubjectSyncStateTenantDelta("agent.tasks", "u2"),
		)).To(BeTrue())
	})

	It("cannot cross-match another tenant", func() {
		Expect(SubjectMatches(
			SubjectSyncStateTenantDelta("agent.tasks", "u1"),
			SubjectSyncStateTenantDelta("agent.tasks", "u2"),
		)).To(BeFalse(), "tenant u1's filter must never match tenant u2's subject")
	})

	It("cannot cross-match a different state family", func() {
		Expect(SubjectMatches(
			SubjectSyncStateTenantWildcard("agent.tasks"),
			SubjectSyncStateTenantDelta("finetune.jobs", "u1"),
		)).To(BeFalse())
	})

	It("refuses an empty tenant at subscribe time rather than silently spanning", func() {
		// Nothing in the tree calls the builder with an empty tenant - the
		// SyncedMap routes the empty case to the cluster-wide subject instead.
		// Pin what happens anyway, because the failure mode of a subject with
		// an empty token is a subscription that never fires, and a caller must
		// meet that as a refusal rather than as silence.
		Expect(ValidFilter(SubjectSyncStateTenantDelta("agent.tasks", ""))).To(MatchError(ErrUnsupportedFilter))
	})
})

// The agent-events wildcard used to be a hand-written literal at the one place
// that subscribes to it, three files away from the builder it has to match.
// These pin the pairing itself, not the string: the filter is only useful if
// every subject SubjectAgentEvents can produce matches it, and if the near
// misses that a shorter filter would swallow do not.
var _ = Describe("the agent-events wildcard", func() {
	It("matches what SubjectAgentEvents builds, for any agent and any user", func() {
		for _, agent := range []string{"a", "my-agent", "agent.with.dots"} {
			for _, user := range []string{"u", "", "user id"} {
				subject := SubjectAgentEvents(agent, user)
				Expect(SubjectMatches(SubjectAgentEventsWildcard, subject)).To(BeTrue(),
					"filter %q must match %q", SubjectAgentEventsWildcard, subject)
			}
		}
	})

	It("has the same token count as the subjects it matches", func() {
		// SubjectMatches compares token counts before anything else, so a
		// filter one token short matches NOTHING and the persister goes silent
		// with no error anywhere. Stated as a count so a builder that grows a
		// token reddens here rather than in a suite that only checks delivery.
		Expect(strings.Count(SubjectAgentEventsWildcard, ".")).
			To(Equal(strings.Count(SubjectAgentEvents("a", "u"), ".")))
	})

	It("does not match a subject with the events token in another position", func() {
		Expect(SubjectMatches(SubjectAgentEventsWildcard, "agent.a.events")).To(BeFalse())
		Expect(SubjectMatches(SubjectAgentEventsWildcard, "agent.a.b.events.u")).To(BeFalse())
		Expect(SubjectMatches(SubjectAgentEventsWildcard, "agent.a.cancel")).To(BeFalse())
	})
})
