package nibharness

import (
	"github.com/mudler/nib/types"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("agent options", func() {
	It("apply agent options overrides only what is set", func() {
		cfg := types.Config{AgentOptions: types.AgentOptions{Iterations: 10, MaxAttempts: 2, MaxRetries: 5}}

		applyAgentOptions(&cfg, AgentOptions{})
		got := cfg.AgentOptions
		Expect(got.Iterations).To(Equal(10), "zero options changed the config: %+v", got)
		Expect(got.MaxAttempts).To(Equal(2), "zero options changed the config: %+v", got)
		Expect(got.MaxRetries).To(Equal(5), "zero options changed the config: %+v", got)
		Expect(got.ForceReasoning).To(BeFalse(), "zero options changed the config: %+v", got)

		applyAgentOptions(&cfg, AgentOptions{Iterations: 4, MaxAttempts: 7, ForceReasoning: true})
		got = cfg.AgentOptions
		Expect(got.Iterations).To(Equal(4), "options not applied: %+v", got)
		Expect(got.MaxAttempts).To(Equal(7), "options not applied: %+v", got)
		Expect(got.ForceReasoning).To(BeTrue(), "options not applied: %+v", got)
		Expect(got.MaxRetries).To(Equal(5), "options not applied: %+v", got)
	})
})
