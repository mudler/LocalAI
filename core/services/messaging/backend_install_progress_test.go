package messaging_test

import (
	"encoding/json"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/messaging"
)

var _ = Describe("Phase constants", func() {
	// Pin the wire-format string values. A future refactor that renames
	// a constant must NOT silently change the JSON value the master
	// receives or break consumers that switch on Phase.
	DescribeTable("phase constant",
		func(actual, expected string) {
			Expect(actual).To(Equal(expected))
		},
		Entry("resolving", messaging.PhaseResolving, "resolving"),
		Entry("downloading", messaging.PhaseDownloading, "downloading"),
		Entry("extracting", messaging.PhaseExtracting, "extracting"),
		Entry("starting", messaging.PhaseStarting, "starting"),
	)
})

var _ = Describe("BackendInstallProgress", func() {
	Context("SubjectNodeBackendInstallProgress", func() {
		It("composes the per-op progress subject", func() {
			Expect(messaging.SubjectNodeBackendInstallProgress("node-abc", "op-123")).
				To(Equal("nodes.node-abc.backend.install.op-123.progress"))
		})

		It("sanitizes NATS-reserved characters in node and op tokens", func() {
			// '.' is the NATS hierarchy delimiter, '*' and '>' are wildcards,
			// and whitespace must be stripped - sanitizeSubjectToken replaces
			// all of them with '-'. The resulting subject must still parse as
			// exactly six hierarchy segments: nodes/<node>/backend/install/<op>/progress.
			subj := messaging.SubjectNodeBackendInstallProgress("a.b c", "x.y z")
			Expect(subj).ToNot(ContainSubstring(" "))
			Expect(strings.Count(subj, ".")).To(Equal(5))
		})
	})

	Context("BackendInstallProgressEvent", func() {
		It("JSON round-trips with all known fields", func() {
			ev := messaging.BackendInstallProgressEvent{
				OpID:       "op-123",
				NodeID:     "node-abc",
				Backend:    "vllm",
				FileName:   "vllm-cpu.tar.zst",
				Current:    "412 MB",
				Total:      "2.1 GB",
				Percentage: 19.6,
				Phase:      "downloading",
			}
			raw, err := json.Marshal(ev)
			Expect(err).ToNot(HaveOccurred())

			var got messaging.BackendInstallProgressEvent
			Expect(json.Unmarshal(raw, &got)).To(Succeed())
			Expect(got).To(Equal(ev))
		})
	})
})
