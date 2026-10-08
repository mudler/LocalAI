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

var _ = Describe("agentCarrier", func() {
	It("attaches to NATS when the frontend names it or names nothing, and the URL is there", func() {
		for _, named := range []string{"nats", ""} {
			onTunnel, err := agentCarrier(named, "nats://n:4222", "")
			Expect(err).ToNot(HaveOccurred(), named)
			Expect(onTunnel).To(BeFalse(), named)
		}
	})

	It("asks for the URL when the cluster runs on NATS and there is none", func() {
		_, err := agentCarrier("nats", "", "")
		Expect(err).To(MatchError(ContainSubstring("LOCALAI_NATS_URL")))
		_, err = agentCarrier("", "", "")
		Expect(err).To(MatchError(ContainSubstring("LOCALAI_NATS_URL")))
	})

	It("takes the URL the frontend hands over when the worker has none", func() {
		onTunnel, err := agentCarrier("nats", "", "nats://handed:4222")
		Expect(err).ToNot(HaveOccurred())
		Expect(onTunnel).To(BeFalse())
	})

	It("attaches to the tunnel when the frontend names it, with a URL or without one", func() {
		for _, url := range []string{"", "nats://n:4222"} {
			onTunnel, err := agentCarrier("tunnel", url, "")
			Expect(err).ToNot(HaveOccurred())
			Expect(onTunnel).To(BeTrue())
		}
	})

	It("asks for an upgrade when the frontend names a carrier it does not know", func() {
		_, err := agentCarrier("smoke-signals", "nats://n:4222", "")
		Expect(err).To(MatchError(ContainSubstring("upgrade")))
	})
})
