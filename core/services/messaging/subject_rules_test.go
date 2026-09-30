package messaging_test

import (
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/messaging"
)

var _ = Describe("Subject rules", func() {
	DescribeTable("accepts served subjects",
		func(subject string) {
			Expect(messaging.ValidateSubject(subject)).To(Succeed())
		},
		Entry("job queue", "jobs.new"),
		Entry("single-token wildcard", "jobs.*.cancel"),
		Entry("two wildcards", "agent.*.events.*"),
		Entry("control subject", "nodes.abc.backend.install"),
		Entry("mcp request", "mcp.tools.execute"),
		Entry("finetune progress", "finetune.job1.progress"),
	)

	DescribeTable("refuses a root that is not served",
		func(subject string) {
			err := messaging.ValidateSubject(subject)
			Expect(errors.Is(err, messaging.ErrUnservedSubject)).To(BeTrue(), "got %v", err)
			Expect(err.Error()).To(ContainSubstring(subject))
		},
		Entry("unknown root", "bogus.thing"),
		Entry("empty token", "jobs..x"),
		Entry("bare root that is unknown", "telemetry"),
	)

	It("refuses the empty subject", func() {
		Expect(errors.Is(messaging.ValidateSubject(""), messaging.ErrUnservedSubject)).To(BeTrue())
	})

	DescribeTable("refuses wildcards other than a whole single token",
		func(subject string) {
			Expect(errors.Is(messaging.ValidateSubject(subject), messaging.ErrUnsupportedWildcard)).To(BeTrue())
		},
		Entry("full wildcard", "jobs.>"),
		Entry("partial token", "jobs.a*.cancel"),
		Entry("wildcard root", "*.new"),
	)

	DescribeTable("matches like a NATS single-token wildcard",
		func(filter, subject string, want bool) {
			Expect(messaging.MatchSubject(filter, subject)).To(Equal(want))
		},
		Entry("exact", "jobs.new", "jobs.new", true),
		Entry("wildcard hit", "jobs.*.cancel", "jobs.abc.cancel", true),
		Entry("wildcard wrong tail", "jobs.*.cancel", "jobs.abc.result", false),
		Entry("wildcard does not span tokens", "jobs.*", "jobs.a.b", false),
		Entry("length mismatch", "jobs.new", "jobs.new.extra", false),
	)

	It("lists the broadcast roots sorted", func() {
		Expect(messaging.BroadcastRoots()).To(Equal([]string{
			"agent", "cache", "finetune", "gallery", "jobs",
			"prefixcache", "responses", "staging", "state",
		}))
	})

	It("lists the control roots sorted", func() {
		Expect(messaging.ControlRoots()).To(Equal([]string{"mcp", "nodes"}))
	})

	DescribeTable("takes the first token as the subject root",
		func(subject, want string) {
			Expect(messaging.SubjectRoot(subject)).To(Equal(want))
		},
		Entry("multi-token subject", "jobs.new", "jobs"),
		Entry("bare root", "jobs", "jobs"),
		Entry("empty subject", "", ""),
	)

	It("serves every subject the constructors in subjects.go build", func() {
		const id = "11111111-2222-3333-4444-555555555555"
		subjects := []string{
			messaging.SubjectJobsNew, messaging.SubjectMCPCIJobsNew, messaging.SubjectAgentExecute,
			messaging.SubjectMCPToolExecute, messaging.SubjectMCPDiscovery,
			messaging.SubjectGalleryOpStart, messaging.SubjectGalleryOpEnd,
			messaging.SubjectCacheInvalidateSkills, messaging.SubjectCacheInvalidateModels,
			messaging.SubjectCacheInvalidateBackends,
			messaging.SubjectPrefixCacheObserve, messaging.SubjectPrefixCacheInvalidate,
			messaging.SubjectPrefixCachePressure, messaging.SubjectPrefixCacheResidency,
			messaging.SubjectJobCancelWildcard, messaging.SubjectJobResultWildcard,
			messaging.SubjectJobProgressWildcard, messaging.SubjectAgentCancelWildcard,
			messaging.SubjectGalleryCancelWildcard, messaging.SubjectGalleryProgressWildcard,
			messaging.SubjectResponseCancelWildcard,
			messaging.SubjectAgentEvents("agent1", "user1"),
			messaging.SubjectJobProgress(id), messaging.SubjectJobResult(id),
			messaging.SubjectFineTuneProgress(id), messaging.SubjectGalleryProgress(id),
			messaging.SubjectStagingProgress(id),
			messaging.SubjectJobCancel(id), messaging.SubjectAgentCancel(id),
			messaging.SubjectFineTuneCancel(id), messaging.SubjectGalleryCancel(id),
			messaging.SubjectResponseCancel(id),
			messaging.SubjectCacheInvalidateCollection("c1"), messaging.SubjectSyncStateDelta("s1"),
			messaging.SubjectNodeBackendInstall(id), messaging.SubjectNodeBackendUpgrade(id),
			messaging.SubjectNodeBackendList(id), messaging.SubjectNodeBackendStop(id),
			messaging.SubjectNodeModelStop(id), messaging.SubjectNodeBackendDelete(id),
			messaging.SubjectNodeModelUnload(id), messaging.SubjectNodeModelDelete(id),
			messaging.SubjectNodeModelsRunning(id), messaging.SubjectNodeStop(id),
			messaging.SubjectNodeFilesEnsure(id), messaging.SubjectNodeFilesStage(id),
			messaging.SubjectNodeFilesRelease(id), messaging.SubjectNodeFilesTemp(id),
			messaging.SubjectNodeFilesListDir(id),
		}
		for _, s := range subjects {
			Expect(messaging.ValidateSubject(s)).To(Succeed(), "subject %q", s)
		}
	})
})
