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
		Entry("exact", "gallery.new", "gallery.new", true),
		Entry("wildcard hit", "gallery.*.cancel", "gallery.abc.cancel", true),
		Entry("wildcard wrong tail", "gallery.*.cancel", "gallery.abc.result", false),
		Entry("wildcard does not span tokens", "gallery.*", "gallery.a.b", false),
		Entry("length mismatch", "gallery.new", "jobs.new.extra", false),
	)
})
