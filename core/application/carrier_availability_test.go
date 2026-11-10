package application

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the report of what a replica can build", func() {
	It("is refreshed more often than a change of carrier calls it stale", func() {
		// A preflight that did not ask the replicas first reads the last report
		// that each one wrote by itself. A report that is refreshed less often
		// than it may be old would mark every replica stale between two
		// refreshes.
		Expect(availabilityMaxAge).To(BeNumerically(">", availabilityEvery))
	})
})
