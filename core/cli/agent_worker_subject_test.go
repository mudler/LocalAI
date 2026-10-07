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

var _ = Describe("checkAgentCarrier", func() {
	It("accepts NATS with a URL, named or not named", func() {
		Expect(checkAgentCarrier("nats", "nats://n:4222")).To(Succeed())
		Expect(checkAgentCarrier("", "nats://n:4222")).To(Succeed())
	})

	It("asks for the URL when the cluster runs on NATS and there is none", func() {
		Expect(checkAgentCarrier("nats", "")).To(MatchError(ContainSubstring("LOCALAI_NATS_URL")))
		Expect(checkAgentCarrier("", "")).To(MatchError(ContainSubstring("LOCALAI_NATS_URL")))
	})

	It("says that it cannot use the tunnel, with or without a URL", func() {
		Expect(checkAgentCarrier("tunnel", "")).To(MatchError(ContainSubstring("cannot use")))
		Expect(checkAgentCarrier("tunnel", "nats://n:4222")).To(MatchError(ContainSubstring("cannot use")))
	})
})
