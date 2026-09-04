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

// Every subject this package still mints, pinned to the exact literal it must
// produce.
//
// The table exists because of what is being deleted around it. Four builders
// went in this commit: fine-tune progress and cancel, whose families moved off
// this carrier, and skills and collection cache invalidation, which had no
// production publisher or subscriber to move. The only thing that had ever
// exercised any of the four was an e2e spec that published on a subject and
// subscribed to the same subject, which passes for any literal at all. A
// builder taken out by mistake alongside them would have had no unit failure
// anywhere: its call sites publish, the publish succeeds, and the symptom is a
// subscriber that never fires.
//
// So the pin is the LITERAL, not a round trip through the builder, and it
// covers every survivor rather than the ones a reviewer thought were at risk.
// A subject is also a wire format: two releases in one deployment agree on
// these strings and on nothing else, so a rename that looks internal is a
// rolling upgrade in which half the fleet stops hearing the other half.
var _ = Describe("the subjects this package mints", func() {
	DescribeTable("builds the exact subject its subscribers filter on",
		func(got, want string) { Expect(got).To(Equal(want)) },

		Entry("agent events", SubjectAgentEvents("a1", "u1"), "agent.a1.events.u1"),
		Entry("agent events, anonymous user", SubjectAgentEvents("a1", ""), "agent.a1.events.anonymous"),
		Entry("agent events wildcard", SubjectAgentEventsWildcard, "agent.*.events.*"),
		Entry("agent cancel", SubjectAgentCancel("a1"), "agent.a1.cancel"),
		Entry("agent cancel wildcard", SubjectAgentCancelWildcard, "agent.*.cancel"),

		Entry("job progress", SubjectJobProgress("j1"), "jobs.j1.progress"),
		Entry("job progress wildcard", SubjectJobProgressWildcard, "jobs.*.progress"),
		Entry("job result", SubjectJobResult("j1"), "jobs.j1.result"),
		Entry("job result wildcard", SubjectJobResultWildcard, "jobs.*.result"),
		Entry("job cancel", SubjectJobCancel("j1"), "jobs.j1.cancel"),
		Entry("job cancel wildcard", SubjectJobCancelWildcard, "jobs.*.cancel"),

		Entry("gallery progress", SubjectGalleryProgress("op1"), "gallery.op1.progress"),
		Entry("gallery progress wildcard", SubjectGalleryProgressWildcard, "gallery.*.progress"),
		Entry("gallery cancel", SubjectGalleryCancel("op1"), "gallery.op1.cancel"),
		Entry("gallery cancel wildcard", SubjectGalleryCancelWildcard, "gallery.*.cancel"),
		Entry("gallery opcache start", SubjectGalleryOpStart, "gallery.opcache.start"),
		Entry("gallery opcache end", SubjectGalleryOpEnd, "gallery.opcache.end"),

		Entry("staging progress", SubjectStagingProgress("m1"), "staging.m1.progress"),
		Entry("staging progress wildcard", SubjectStagingProgressWildcard, "staging.*.progress"),

		Entry("response cancel", SubjectResponseCancel("r1"), "responses.r1.cancel"),
		Entry("response cancel wildcard", SubjectResponseCancelWildcard, "responses.*.cancel"),

		Entry("model cache invalidation", SubjectCacheInvalidateModels, "cache.invalidate.models"),
		Entry("backend cache invalidation", SubjectCacheInvalidateBackends, "cache.invalidate.backends"),

		Entry("syncstate delta", SubjectSyncStateDelta("agent.tasks"), "state.agent-tasks.delta"),
		Entry("syncstate tenant delta", SubjectSyncStateTenantDelta("agent.tasks", "u1"), "state.agent-tasks.u1.delta"),
		Entry("syncstate tenant wildcard", SubjectSyncStateTenantWildcard("agent.tasks"), "state.agent-tasks.*.delta"),

		Entry("prefix-cache observe", SubjectPrefixCacheObserve, "prefixcache.observe"),
		Entry("prefix-cache invalidate", SubjectPrefixCacheInvalidate, "prefixcache.invalidate"),
	)

	It("mints nothing for the families that left this carrier", func() {
		// Stated as an absence of PREFIXES rather than of identifiers, because
		// an identifier that is gone cannot be named here at all: the file
		// would not compile. What can be asserted is that no survivor mints
		// into the retired namespaces, which is what a half-finished deletion
		// would leave behind.
		//
		// finetune.* went when fine-tune progress and cancel moved to the
		// broadcast carrier's own subjects; cache.invalidate.skills and
		// cache.invalidate.collections went with the skills and collection
		// caches.
		for _, subject := range []string{
			SubjectAgentEvents("a1", "u1"), SubjectAgentCancel("a1"),
			SubjectJobProgress("j1"), SubjectJobResult("j1"), SubjectJobCancel("j1"),
			SubjectGalleryProgress("op1"), SubjectGalleryCancel("op1"),
			SubjectStagingProgress("m1"), SubjectResponseCancel("r1"),
			SubjectCacheInvalidateModels, SubjectCacheInvalidateBackends,
			SubjectSyncStateDelta("agent.tasks"),
		} {
			Expect(subject).ToNot(HavePrefix("finetune."))
			Expect(subject).ToNot(HavePrefix("cache.invalidate.skills"))
			Expect(subject).ToNot(HavePrefix("cache.invalidate.collections"))
		}
	})
})
