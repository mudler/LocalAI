package testutil_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/testutil"
)

var _ = Describe("FakeBus subject matching", func() {
	DescribeTable("matches like a NATS single-token wildcard",
		func(filter, subject string, want bool) {
			Expect(testutil.SubjectMatches(filter, subject)).To(Equal(want))
		},
		Entry("exact", "jobs.new", "jobs.new", true),
		Entry("wildcard hit", "jobs.*.cancel", "jobs.abc.cancel", true),
		Entry("wildcard wrong tail", "jobs.*.cancel", "jobs.abc.result", false),
		Entry("wildcard does not span tokens", "jobs.*", "jobs.a.b", false),
		Entry("length mismatch", "jobs.new", "jobs.new.extra", false),
	)
})
