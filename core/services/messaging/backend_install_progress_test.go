package messaging_test

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/messaging"
)

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
})
