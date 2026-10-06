package cli

import (
	"errors"

	"github.com/mudler/LocalAI/core/services/messaging"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("validateAgentSubject", func() {
	It("accepts the default agent execution subject", func() {
		Expect(validateAgentSubject("agent.execute")).To(Succeed())
	})

	It("refuses a subject whose root no carrier serves and names the env var", func() {
		err := validateAgentSubject("tenant-a.agent.execute")
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, messaging.ErrUnservedSubject)).To(BeTrue())
		Expect(err.Error()).To(ContainSubstring("LOCALAI_AGENT_SUBJECT"))
		Expect(err.Error()).To(ContainSubstring("tenant-a.agent.execute"))
		Expect(err.Error()).To(ContainSubstring("agent.execute"))
	})

	It("refuses a multi-token wildcard", func() {
		err := validateAgentSubject("agent.>")
		Expect(errors.Is(err, messaging.ErrUnsupportedWildcard)).To(BeTrue())
		Expect(err.Error()).To(ContainSubstring("LOCALAI_AGENT_SUBJECT"))
	})
})
