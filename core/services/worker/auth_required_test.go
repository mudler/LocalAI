package worker

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The umbrella switch used to imply NATS auth as well. A backend worker no
// longer authenticates to a bus, so registration auth is all it still implies,
// and this is the only helper left to keep honest.
var _ = Describe("Worker auth-required helpers", func() {
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
