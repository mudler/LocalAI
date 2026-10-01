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

	DescribeTable("serves every root",
		func(root string) {
			Expect(messaging.ValidateSubject(root + ".x")).To(Succeed())
		},
		Entry("jobs", "jobs"), Entry("agent", "agent"), Entry("gallery", "gallery"),
		Entry("cache", "cache"), Entry("staging", "staging"), Entry("prefixcache", "prefixcache"),
		Entry("responses", "responses"), Entry("state", "state"), Entry("finetune", "finetune"),
		Entry("nodes", "nodes"), Entry("mcp", "mcp"),
	)

	It("refuses an unknown root", func() {
		Expect(errors.Is(messaging.ValidateSubject("bogus.x"), messaging.ErrUnservedSubject)).To(BeTrue())
	})

	It("serves every subject the constructors in subjects.go build", func() {
		const id = "11111111-2222-3333-4444-555555555555"
		// When you add a subject constant or constructor, add it here too.
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
			messaging.SubjectNodeBackendInstallProgress(id, "op1"),
		}
		for _, s := range subjects {
			Expect(messaging.ValidateSubject(s)).To(Succeed(), "subject %q", s)
		}
	})
})
