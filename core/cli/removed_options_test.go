package cli

import (
	"github.com/alecthomas/kong"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Every option of the old agent pool, jobs and skills is still accepted, as a
// flag or as an environment variable, and is named in the one warning.
var _ = Describe("RemovedAgentOptions", func() {
	parse := func(args ...string) RemovedAgentOptions {
		var o RemovedAgentOptions
		k, err := kong.New(&o)
		Expect(err).ToNot(HaveOccurred())
		_, err = k.Parse(args)
		Expect(err).ToNot(HaveOccurred())
		return o
	}

	DescribeTable("accepts a removed flag and names it",
		func(flag, env string) {
			Expect(parse(flag + "=1").set()).To(ConsistOf(env))
		},
		Entry("multimodal model", "--agent-pool-multimodal-model", "LOCALAI_AGENT_POOL_MULTIMODAL_MODEL"),
		Entry("transcription model", "--agent-pool-transcription-model", "LOCALAI_AGENT_POOL_TRANSCRIPTION_MODEL"),
		Entry("transcription language", "--agent-pool-transcription-language", "LOCALAI_AGENT_POOL_TRANSCRIPTION_LANGUAGE"),
		Entry("tts model", "--agent-pool-tts-model", "LOCALAI_AGENT_POOL_TTS_MODEL"),
		Entry("timeout", "--agent-pool-timeout", "LOCALAI_AGENT_POOL_TIMEOUT"),
		Entry("custom actions", "--agent-pool-custom-actions-dir", "LOCALAI_AGENT_POOL_CUSTOM_ACTIONS_DIR"),
		Entry("hub url", "--agent-hub-url", "LOCALAI_AGENT_HUB_URL"),
	)

	It("accepts the switches without a value", func() {
		Expect(parse("--agent-pool-enable-skills", "--agent-pool-enable-logs").set()).To(ConsistOf("LOCALAI_AGENT_POOL_ENABLE_SKILLS", "LOCALAI_AGENT_POOL_ENABLE_LOGS"))
	})

	It("reads the environment", func() {
		GinkgoT().Setenv("LOCALAI_AGENT_POOL_TIMEOUT", "10m")
		GinkgoT().Setenv("LOCALAI_AGENT_POOL_TTS_MODEL", "tts")
		Expect(parse().set()).To(ConsistOf("LOCALAI_AGENT_POOL_TIMEOUT", "LOCALAI_AGENT_POOL_TTS_MODEL"))
	})

	It("names nothing when nothing is set", func() {
		Expect(parse().set()).To(BeEmpty())
	})
})

// The agent worker no longer runs agents or jobs. Its old options are accepted
// and, when set, named in the same single warning.
var _ = Describe("RemovedAgentWorkerOptions", func() {
	parse := func(args ...string) AgentWorkerCMD {
		var cmd AgentWorkerCMD
		k, err := kong.New(&cmd)
		Expect(err).ToNot(HaveOccurred())
		_, err = k.Parse(append([]string{"--nats-url=nats://x", "--register-to=http://x"}, args...))
		Expect(err).ToNot(HaveOccurred())
		return cmd
	}

	It("names only the removed options that are set", func() {
		GinkgoT().Setenv("LOCALAI_API_TOKEN", "t")
		GinkgoT().Setenv("LOCALAI_AGENT_QUEUE", "q")
		Expect(parse().RemovedAgentWorkerOptions.set()).To(ConsistOf("LOCALAI_API_TOKEN", "LOCALAI_AGENT_QUEUE"))
	})

	It("names nothing when none is set", func() {
		Expect(parse().RemovedAgentWorkerOptions.set()).To(BeEmpty())
	})
})
