package worker

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Worker auth-required helpers", func() {
	DescribeTable("NatsAuthRequired",
		func(nats, umbrella, want bool) {
			cfg := &Config{NatsRequireAuth: nats, DistributedRequireAuth: umbrella}
			Expect(cfg.NatsAuthRequired()).To(Equal(want))
		},
		Entry("neither", false, false, false),
		Entry("granular only", true, false, true),
		Entry("umbrella only", false, true, true),
		Entry("both", true, true, true),
	)

	DescribeTable("RegistrationAuthRequired",
		func(reg, umbrella, want bool) {
			cfg := &Config{RegistrationRequireAuth: reg, DistributedRequireAuth: umbrella}
			Expect(cfg.RegistrationAuthRequired()).To(Equal(want))
		},
		Entry("neither", false, false, false),
		Entry("granular only", true, false, true),
		Entry("umbrella only", false, true, true),
		Entry("both", true, true, true),
	)
})
